package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"openaero-core/pkg/config"
	"openaero-core/pkg/traffic"
)

var (
	connIdRegex = regexp.MustCompile(`^[a-zA-Z0-9-]+$`)
)

type clashConnectionsResp struct {
	DownloadTotal int64             `json:"downloadTotal"`
	UploadTotal   int64             `json:"uploadTotal"`
	Connections   []clashConnection `json:"connections"`
}

type clashConnection struct {
	ID          string            `json:"id"`
	Metadata    clashConnMetadata `json:"metadata"`
	Upload      int64             `json:"upload"`
	Download    int64             `json:"download"`
	Start       string            `json:"start"`
	Chains      []string          `json:"chains"`
	Rule        string            `json:"rule"`
	RulePayload string            `json:"rulePayload"`
	CurUpload   int64             `json:"curUpload"`
	CurDownload int64             `json:"curDownload"`
}

type clashConnMetadata struct {
	Network         string `json:"network"`
	Type            string `json:"type"`
	SourceIP        string `json:"sourceIP"`
	SourcePort      string `json:"sourcePort"`
	DestinationIP   string `json:"destinationIP"`
	DestinationPort string `json:"destinationPort"`
	Host            string `json:"host"`
	DnsMode         string `json:"dnsMode"`
}

type FormattedConnection struct {
	ID            string   `json:"id"`
	ClientIP      string   `json:"client_ip"`
	ClientName    string   `json:"client_name"`
	Network       string   `json:"network"`
	Host          string   `json:"host"`
	Destination   string   `json:"destination"`
	Rule          string   `json:"rule"`
	Chains        []string `json:"chains"`
	UploadSpeed   string   `json:"upload_speed"`
	DownloadSpeed string   `json:"download_speed"`
	UploadTotal   string   `json:"upload_total"`
	DownloadTotal string   `json:"download_total"`
	StartTime     string   `json:"start_time"`
	Duration      string   `json:"duration"`
}

type connTrafficSample struct {
	upload        int64
	download      int64
	ts            time.Time
	lastUpSpeed   int64
	lastDownSpeed int64
}

var (
	connTrafficMu   sync.Mutex
	lastConnSamples = make(map[string]connTrafficSample)
)

func (s *Server) handleConnections(w http.ResponseWriter, r *http.Request) {
	cfg, _ := config.LoadConfig()
	clashPort := 9090
	if cfg != nil && cfg.ClashAPIPort > 0 {
		clashPort = cfg.ClashAPIPort
	}

	client := http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/connections", clashPort))
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"failed to connect to Clash API: %v"}`, err), 502)
		return
	}
	defer resp.Body.Close()

	var cResp clashConnectionsResp
	if err := json.NewDecoder(resp.Body).Decode(&cResp); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"failed to parse Clash API connections: %v"}`, err), 500)
		return
	}

	ipToName := getIpToNameMap()
	now := time.Now()

	filteredConns := make([]clashConnection, 0, len(cResp.Connections))
	activeIDs := make(map[string]struct{}, len(cResp.Connections))

	connTrafficMu.Lock()
	for _, c := range cResp.Connections {
		if traffic.ShouldFilterConnection(c.Metadata.SourceIP, c.Metadata.DestinationPort, c.Metadata.Network) {
			continue
		}
		activeIDs[c.ID] = struct{}{}

		var upSpeed, downSpeed int64
		if prev, ok := lastConnSamples[c.ID]; ok {
			dt := now.Sub(prev.ts).Seconds()
			if dt >= 0.2 {
				dUp := c.Upload - prev.upload
				dDown := c.Download - prev.download
				if dUp < 0 {
					dUp = 0
				}
				if dDown < 0 {
					dDown = 0
				}
				upSpeed = int64(float64(dUp) / dt)
				downSpeed = int64(float64(dDown) / dt)
				lastConnSamples[c.ID] = connTrafficSample{
					upload:        c.Upload,
					download:      c.Download,
					ts:            now,
					lastUpSpeed:   upSpeed,
					lastDownSpeed: downSpeed,
				}
			} else {
				upSpeed = prev.lastUpSpeed
				downSpeed = prev.lastDownSpeed
			}
		} else {
			if st, err := time.Parse(time.RFC3339Nano, c.Start); err == nil {
				ageSec := now.Sub(st).Seconds()
				if ageSec < 0.5 {
					ageSec = 0.5
				}
				if ageSec <= 30.0 {
					upSpeed = int64(float64(c.Upload) / ageSec)
					downSpeed = int64(float64(c.Download) / ageSec)
				}
			}
			lastConnSamples[c.ID] = connTrafficSample{
				upload:        c.Upload,
				download:      c.Download,
				ts:            now,
				lastUpSpeed:   upSpeed,
				lastDownSpeed: downSpeed,
			}
		}
		c.CurUpload = upSpeed
		c.CurDownload = downSpeed
		filteredConns = append(filteredConns, c)
	}

	for id := range lastConnSamples {
		if _, alive := activeIDs[id]; !alive {
			delete(lastConnSamples, id)
		}
	}
	connTrafficMu.Unlock()

	conns := filteredConns
	sort.Slice(conns, func(i, j int) bool {
		if conns[i].CurDownload != conns[j].CurDownload {
			return conns[i].CurDownload > conns[j].CurDownload
		}
		if conns[i].CurUpload != conns[j].CurUpload {
			return conns[i].CurUpload > conns[j].CurUpload
		}
		return conns[i].Download > conns[j].Download
	})

	totalCount := len(conns)
	if len(conns) > 50 {
		conns = conns[:50]
	}

	formatted := make([]FormattedConnection, 0, len(conns))
	for _, c := range conns {
		clientIP := c.Metadata.SourceIP
		clientName := ipToName[clientIP]
		if clientIP == "172.19.0.1" {
			clientName = "172.19.0.1 (本机服务)"
		} else if clientName == "" {
			clientName = clientIP
		}

		host := c.Metadata.Host
		if host == "" {
			host = c.Metadata.DestinationIP
		}
		dest := host
		if c.Metadata.DestinationPort != "" {
			dest = fmt.Sprintf("%s:%s", host, c.Metadata.DestinationPort)
		}

		durationStr := "刚刚"
		st, err := time.Parse(time.RFC3339Nano, c.Start)
		if err == nil {
			dur := now.Sub(st)
			if dur > time.Hour {
				durationStr = fmt.Sprintf("%dh %dm", int(dur.Hours()), int(dur.Minutes())%60)
			} else if dur > time.Minute {
				durationStr = fmt.Sprintf("%dm %ds", int(dur.Minutes()), int(dur.Seconds())%60)
			} else {
				durationStr = fmt.Sprintf("%ds", int(dur.Seconds()))
			}
		}

		upSpdStr := traffic.FormatSpeed(c.CurUpload)
		downSpdStr := traffic.FormatSpeed(c.CurDownload)
		upTotStr := traffic.FormatBytes(uint64(c.Upload))
		downTotStr := traffic.FormatBytes(uint64(c.Download))

		formatted = append(formatted, FormattedConnection{
			ID:            c.ID,
			ClientIP:      clientIP,
			ClientName:    clientName,
			Network:       strings.ToUpper(c.Metadata.Network),
			Host:          host,
			Destination:   dest,
			Rule:          c.Rule,
			Chains:        c.Chains,
			UploadSpeed:   upSpdStr,
			DownloadSpeed: downSpdStr,
			UploadTotal:   upTotStr,
			DownloadTotal: downTotStr,
			StartTime:     c.Start,
			Duration:      durationStr,
		})
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":           true,
		"connections_count": totalCount,
		"total":             totalCount,
		"total_download":    traffic.FormatBytes(uint64(cResp.DownloadTotal)),
		"total_upload":      traffic.FormatBytes(uint64(cResp.UploadTotal)),
		"connections":       formatted,
	})
}

func (s *Server) handleConnectionClose(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, `{"error":"POST only"}`, 405)
		return
	}

	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		http.Error(w, `{"error":"invalid connection id"}`, 400)
		return
	}

	// Validate req.ID format: letters, numbers and hyphens only
	if !connIdRegex.MatchString(req.ID) {
		http.Error(w, `{"error":"invalid connection id format"}`, 400)
		return
	}

	cfg, _ := config.LoadConfig()
	clashPort := 9090
	if cfg != nil && cfg.ClashAPIPort > 0 {
		clashPort = cfg.ClashAPIPort
	}

	client := http.Client{Timeout: 1500 * time.Millisecond}
	httpReq, err := http.NewRequest("DELETE", fmt.Sprintf("http://127.0.0.1:%d/connections/%s", clashPort, req.ID), nil)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), 500)
		return
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"failed to kill connection: %v"}`, err), 502)
		return
	}
	defer resp.Body.Close()

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "连接已切断",
	})
}

func (s *Server) handleConnectionsCloseAll(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, `{"error":"POST only"}`, 405)
		return
	}

	cfg, _ := config.LoadConfig()
	clashPort := 9090
	if cfg != nil && cfg.ClashAPIPort > 0 {
		clashPort = cfg.ClashAPIPort
	}

	client := http.Client{Timeout: 2000 * time.Millisecond}
	httpReq, err := http.NewRequest("DELETE", fmt.Sprintf("http://127.0.0.1:%d/connections", clashPort), nil)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), 500)
		return
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"failed to close all connections: %v"}`, err), 502)
		return
	}
	defer resp.Body.Close()

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "已关闭全部网络连接",
	})
}

var (
	cachedIpToName     map[string]string
	cachedIpToNameTime time.Time
	cachedIpToNameMu   sync.RWMutex
)

func getIpToNameMap() map[string]string {
	cachedIpToNameMu.RLock()
	if cachedIpToName != nil && time.Since(cachedIpToNameTime) < 10*time.Second {
		res := cachedIpToName
		cachedIpToNameMu.RUnlock()
		return res
	}
	cachedIpToNameMu.RUnlock()

	cachedIpToNameMu.Lock()
	defer cachedIpToNameMu.Unlock()
	if cachedIpToName != nil && time.Since(cachedIpToNameTime) < 10*time.Second {
		return cachedIpToName
	}

	ipToMac, rawIpToName := config.ReadLanHosts()
	ipToName := make(map[string]string, len(ipToMac))
	for ip := range ipToMac {
		if name, ok := rawIpToName[ip]; ok && name != "" {
			ipToName[ip] = fmt.Sprintf("%s (%s)", ip, name)
		} else {
			ipToName[ip] = ip
		}
	}
	cachedIpToName = ipToName
	cachedIpToNameTime = time.Now()
	return ipToName
}

func (s *Server) handleBypassStatus(w http.ResponseWriter, r *http.Request) {
	routerIP := getRouterLanIP()
	hasOption3 := false
	hasOption6 := false
	opt3Val := ""
	opt6Val := ""

	if data, err := os.ReadFile("/etc/config/dhcp"); err == nil {
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "list dhcp_option") {
				clean := strings.Trim(strings.TrimPrefix(line, "list dhcp_option"), " '\t\"")
				parts := strings.SplitN(clean, ",", 2)
				if len(parts) == 2 {
					code := strings.TrimSpace(parts[0])
					val := strings.TrimSpace(parts[1])
					if code == "3" {
						hasOption3 = true
						opt3Val = val
					} else if code == "6" {
						hasOption6 = true
						opt6Val = val
					}
				}
			}
		}
	}

	enabled := hasOption3 && hasOption6 && opt3Val == routerIP && opt6Val == routerIP
	healthy := enabled
	var warning string
	if !enabled {
		if hasOption3 || hasOption6 {
			warning = fmt.Sprintf("⚠️ 旁路网关配置不匹配: 当前 Option 3=[%s], Option 6=[%s], 本机 IP=[%s]", opt3Val, opt6Val, routerIP)
		} else {
			warning = "⚠️ 旁路网关模式未开启：当前局域网终端默认网关由主路由分配。若终端仅将 DNS 指向本路由而网关未指向本路由，将导致 Fake-IP 黑洞断网。建议点击【一键接管 DHCP 网关与 DNS】形成拓扑闭环。"
		}
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":        true,
		"enabled":        enabled,
		"router_ip":      routerIP,
		"gateway_option": opt3Val,
		"dns_option":     opt6Val,
		"healthy":        healthy,
		"warning":        warning,
	})
}

func (s *Server) handleBypassToggle(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Enable bool `json:"enable"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	routerIP := getRouterLanIP()
	if err := config.SetUciDhcpBypass(req.Enable, routerIP); err != nil {
		log.Printf("[bypass] 设置 DHCP bypass 失败: %v", err)
	} else {
		_ = runCmdLogged("/etc/init.d/dnsmasq", "reload")
		if req.Enable {
			log.Printf("[bypass] 🛡️ 成功启用旁路网关模式，注入 Option 3/6 -> %s", routerIP)
		} else {
			log.Printf("[bypass] 🔄 成功恢复默认 DHCP 设置，移除 Option 3/6")
		}
	}

	cfg, _ := config.LoadConfig()
	if cfg != nil {
		cfg.BypassMode = req.Enable
		_ = config.SaveConfig(cfg)
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   true,
		"enabled":   req.Enable,
		"router_ip": routerIP,
		"message":   "旁路网关模式设置已生效",
	})
}
