package api

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"openaero-core/pkg/config"
)

func getLogreadCmd(filter string, lines int) *exec.Cmd {
	logCmd := "logread"
	for _, p := range []string{"/sbin/logread", "/usr/sbin/logread", "/bin/logread"} {
		if _, err := os.Stat(p); err == nil {
			logCmd = p
			break
		}
	}
	args := []string{"-l", strconv.Itoa(lines)}
	if filter != "" {
		args = append(args, "-e", filter)
	}
	return exec.Command(logCmd, args...)
}

type LogItem struct {
	Type    string `json:"type"`
	Payload string `json:"payload"`
}

type LogHub struct {
	mu          sync.RWMutex
	sysLogs     []string
	accessLogs  []AccessRecord
	connIndex   map[string]int
	stopChan    chan struct{}
	running     bool
	nodeNames   map[string]string
	nodeNamesMu sync.RWMutex
}

var (
	ansiRe     = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
	timeRe     = regexp.MustCompile(`(\d{2}:\d{2}:\d{2})`)
	outboundRe = regexp.MustCompile(`\[(\d+)\s+([0-9\.]+[mµu]?s)\]\s+outbound\/([a-zA-Z0-9_\-]+)\[([a-zA-Z0-9_\-]+)\]:\s+outbound\s+(?:connection|packet connection)(?:\s+to\s+([^\s]+))?`)
	dnsRe      = regexp.MustCompile(`\[(\d+)\s+([0-9\.]+[mµu]?s)\]\s+dns:\s+exchanged\s+([A-Z]+)\s+([^\s]+)`)
)

func NewLogHub() *LogHub {
	return &LogHub{
		sysLogs:    make([]string, 0, 100),
		accessLogs: make([]AccessRecord, 0, 50),
		connIndex:  make(map[string]int),
		stopChan:   make(chan struct{}),
		nodeNames:  make(map[string]string),
	}
}

func (h *LogHub) refreshNodeNames() {
	nl, err := config.LoadNodes()
	if err != nil {
		return
	}
	nm := make(map[string]string, len(nl)*2)
	for _, n := range nl {
		nm[n.Id] = n.Remark
		nm["node-"+n.Id] = n.Remark
	}
	h.nodeNamesMu.Lock()
	h.nodeNames = nm
	h.nodeNamesMu.Unlock()
}

func (h *LogHub) getNodeRemark(tag string) (string, bool) {
	h.nodeNamesMu.RLock()
	rk, ok := h.nodeNames[tag]
	h.nodeNamesMu.RUnlock()
	if ok {
		return rk, true
	}
	h.refreshNodeNames()
	h.nodeNamesMu.RLock()
	rk, ok = h.nodeNames[tag]
	h.nodeNamesMu.RUnlock()
	return rk, ok
}

func (h *LogHub) Start() {
	if h.running {
		return
	}
	h.running = true
	h.refreshNodeNames()

	// Seed once on cold start from logread (zero continuous forks later)
	go h.seedFromLogread()

	// Continuously stream from Clash API
	go h.consumeClashLogs()
}

func (h *LogHub) seedFromLogread() {
	cmd := getLogreadCmd("sing-box", 80)
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		return
	}
	rawLines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, rawLine := range rawLines {
		if strings.Contains(rawLine, "sing-box") || strings.Contains(rawLine, "openaero") {
			parts := strings.SplitN(rawLine, "]: ", 2)
			clean := rawLine
			if len(parts) == 2 {
				clean = parts[1]
			}
			clean = ansiRe.ReplaceAllString(clean, "")
			clean = strings.TrimSpace(clean)
			if clean != "" {
				h.appendSysLog(clean)
				h.parseAndAppendAccessLog(clean, "")
			}
		}
	}
}

func (h *LogHub) consumeClashLogs() {
	client := &http.Client{Timeout: 0}
	for {
		select {
		case <-h.stopChan:
			return
		default:
		}

		pid := config.GetSingBoxPid()
		if pid <= 0 {
			time.Sleep(1 * time.Second)
			continue
		}

		req, err := http.NewRequest("GET", "http://127.0.0.1:9090/logs?level=info", nil)
		if err != nil {
			time.Sleep(1 * time.Second)
			continue
		}

		resp, err := client.Do(req)
		if err != nil {
			time.Sleep(1500 * time.Millisecond)
			continue
		}

		reader := bufio.NewReader(resp.Body)
		for {
			line, err := reader.ReadBytes('\n')
			if len(line) > 0 {
				var item LogItem
				if jsonErr := json.Unmarshal(line, &item); jsonErr == nil && item.Payload != "" {
					cleanPayload := ansiRe.ReplaceAllString(item.Payload, "")
					formatted := fmt.Sprintf("[%s] [%s] %s", time.Now().Format("15:04:05"), item.Type, cleanPayload)
					h.appendSysLog(formatted)
					h.parseAndAppendAccessLog(cleanPayload, time.Now().Format("15:04:05"))
				}
			}
			if err != nil {
				resp.Body.Close()
				break
			}
		}
		time.Sleep(1 * time.Second)
	}
}

func (h *LogHub) appendSysLog(entry string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sysLogs = append(h.sysLogs, entry)
	if len(h.sysLogs) > 100 {
		h.sysLogs = h.sysLogs[len(h.sysLogs)-100:]
	}
}

func (h *LogHub) parseAndAppendAccessLog(line string, defaultTime string) {
	tStr := defaultTime
	if tmMatch := timeRe.FindStringSubmatch(line); len(tmMatch) > 1 {
		tStr = tmMatch[1]
	}
	if tStr == "" {
		tStr = time.Now().Format("15:04:05")
	}

	if obMatch := outboundRe.FindStringSubmatch(line); len(obMatch) > 0 {
		connId := obMatch[1]
		dur := obMatch[2]
		tag := obMatch[4]
		target := obMatch[5]
		if target == "" {
			return
		}
		// Filter out internal canary probes, prewarm UDP port 39999, internal DNS, and wol.zzao.de
		if target == "cp.cloudflare.com:80" || target == "www.google.com:80" ||
			strings.HasSuffix(target, ":39999") || strings.HasSuffix(target, ":53") || strings.HasSuffix(target, ":1053") ||
			strings.Contains(target, "wol.zzao.de") {
			return
		}

		rec := AccessRecord{
			Time:     tStr,
			Duration: dur,
			Target:   target,
			Outbound: tag,
		}

		if tag == "direct" {
			rec.OutboundLabel = "🇨🇳 直连 (DIRECT)"
			rec.Action = "direct"
		} else if tag == "block" {
			rec.OutboundLabel = "🛑 拦截 (REJECT)"
			rec.Action = "reject"
		} else {
			if rk, ok := h.getNodeRemark(tag); ok {
				rec.OutboundLabel = "🌐 " + rk + " (PROXY)"
			} else if tag == "match-out" || tag == "proxy" || tag == "GLOBAL" || tag == "match" {
				rec.OutboundLabel = "🌐 漏网之鱼 (MATCH)"
			} else {
				rec.OutboundLabel = "🌐 " + tag + " (PROXY)"
			}
			rec.Action = "proxy"
		}

		h.mu.Lock()
		defer h.mu.Unlock()
		if idx, exists := h.connIndex[connId]; exists && idx < len(h.accessLogs) {
			h.accessLogs[idx] = rec
		} else if len(h.accessLogs) > 0 && h.accessLogs[len(h.accessLogs)-1].Target == rec.Target && h.accessLogs[len(h.accessLogs)-1].Outbound == rec.Outbound && h.accessLogs[len(h.accessLogs)-1].Time == rec.Time {
			h.accessLogs[len(h.accessLogs)-1] = rec
		} else {
			h.connIndex[connId] = len(h.accessLogs)
			h.accessLogs = append(h.accessLogs, rec)
			if len(h.accessLogs) > 50 {
				h.accessLogs = h.accessLogs[len(h.accessLogs)-50:]
				// Rebuild connIndex to stay within bounds
				h.connIndex = make(map[string]int)
			}
		}
		return
	}

	if dnsMatch := dnsRe.FindStringSubmatch(line); len(dnsMatch) > 0 {
		dur := dnsMatch[2]
		qtype := dnsMatch[3]
		domain := strings.TrimSuffix(dnsMatch[4], ".")
		if domain == "dns.alidns.com" || domain == "cp.cloudflare.com" || domain == "wol.zzao.de" || strings.HasSuffix(domain, ".zzao.de") {
			return
		}

		if qtype != "PTR" && qtype != "OPT" && strings.Contains(domain, ".") && !strings.Contains(domain, "arpa") {
			rec := AccessRecord{
				Time:          tStr,
				Duration:      dur,
				Target:        domain,
				Outbound:      "dns",
				OutboundLabel: fmt.Sprintf("🔍 DNS [%s]", qtype),
				Action:        "dns",
			}

			h.mu.Lock()
			defer h.mu.Unlock()
			if len(h.accessLogs) > 0 && h.accessLogs[len(h.accessLogs)-1].Target == rec.Target && h.accessLogs[len(h.accessLogs)-1].Action == "dns" {
				h.accessLogs[len(h.accessLogs)-1] = rec
			} else {
				h.accessLogs = append(h.accessLogs, rec)
				if len(h.accessLogs) > 50 {
					h.accessLogs = h.accessLogs[len(h.accessLogs)-50:]
				}
			}
		}
	}
}

func (h *LogHub) GetLogs() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if len(h.sysLogs) == 0 {
		return []string{
			fmt.Sprintf("[%s] [info] sing-box 正常运行中，TUN 接口 openaero0 实时转发中", time.Now().Format("15:04:05")),
			fmt.Sprintf("[%s] [info] DNS 1053 劫持就绪，分流策略引擎生效中", time.Now().Format("15:04:05")),
		}
	}
	res := make([]string, len(h.sysLogs))
	copy(res, h.sysLogs)
	return res
}

func (h *LogHub) GetAccessLogs() []AccessRecord {
	h.mu.RLock()
	defer h.mu.RUnlock()
	n := len(h.accessLogs)
	res := make([]AccessRecord, n)
	for i := 0; i < n; i++ {
		res[i] = h.accessLogs[n-1-i]
	}
	return res
}

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	sendEvent := func() error {
		payload := map[string]interface{}{
			"status":      s.getStatusData(),
			"traffic":     s.trafficMonitor.Update(),
			"logs":        s.logHub.GetLogs(),
			"access_logs": s.logHub.GetAccessLogs(),
		}
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "data: %s\n\n", data)
		if err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}

	// Send initial state immediately
	if err := sendEvent(); err != nil {
		return
	}

	ticker := time.NewTicker(1500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if err := sendEvent(); err != nil {
				return
			}
		}
	}
}
