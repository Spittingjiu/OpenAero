package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"openaero-core/pkg/config"
	"openaero-core/pkg/diagnose"
	"openaero-core/pkg/nodes"
	"openaero-core/pkg/prewarm"
	"openaero-core/pkg/subscription"
	"openaero-core/pkg/traffic"
	"openaero-core/pkg/updater"
)

var apiMutex sync.Mutex

type AccessRecord struct {
	Time          string `json:"time"`
	Duration      string `json:"duration"`
	Target        string `json:"target"`
	Outbound      string `json:"outbound"`
	OutboundLabel string `json:"outbound_label"`
	Action        string `json:"action"`
}

type Server struct {
	addr           string
	logHub         *LogHub
	trafficMonitor *traffic.TrafficMonitor
}

func NewServer(addr string) *Server {
	return &Server{
		addr:           addr,
		logHub:         NewLogHub(),
		trafficMonitor: traffic.NewTrafficMonitor("openaero0", 9090),
	}
}

func (s *Server) Start() {
	_, _ = EnsureApiToken()
	s.logHub.Start()
	mux := http.NewServeMux()

	wrap := func(h http.HandlerFunc) http.HandlerFunc {
		return s.authMiddleware(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			h(w, r)
		})
	}

	streamWrap := func(h http.HandlerFunc) http.HandlerFunc {
		return s.authMiddleware(func(w http.ResponseWriter, r *http.Request) {
			h(w, r)
		})
	}

	mux.HandleFunc("/api/stream", streamWrap(s.handleStream))
	mux.HandleFunc("/api/status", wrap(s.handleStatus))
	mux.HandleFunc("/api/logs", wrap(s.handleLogs))
	mux.HandleFunc("/api/access_logs", wrap(s.handleAccessLogs))
	mux.HandleFunc("/api/config", wrap(s.handleConfig))
	mux.HandleFunc("/api/nodes", wrap(s.handleNodes))
	mux.HandleFunc("/api/nodes/test", wrap(s.handleNodesTest))
	mux.HandleFunc("/api/nodes/ortt_test", wrap(s.handleNodesOrttTest))
	mux.HandleFunc("/api/nodes/select", wrap(s.handleNodeHotSelect))
	mux.HandleFunc("/api/node/hot_select", wrap(s.handleNodeHotSelect)) // 兼容前端调用
	mux.HandleFunc("/api/groups", wrap(s.handleGroups))
	mux.HandleFunc("/api/groups/set", wrap(s.handleGroupSet))
	mux.HandleFunc("/api/rules/add", wrap(s.handleRuleAdd))
	mux.HandleFunc("/api/rules/update", wrap(s.handleRulesUpdate))
	mux.HandleFunc("/api/traffic", wrap(s.handleTraffic))
	mux.HandleFunc("/api/diagnose", wrap(s.handleDiagnoseRoute))
	mux.HandleFunc("/api/diagnose_route", wrap(s.handleDiagnoseRoute)) // 兼容前端调用
	mux.HandleFunc("/api/sub/update", wrap(s.handleSubAsyncUpdate))
	mux.HandleFunc("/api/sub/async_update", wrap(s.handleSubAsyncUpdate)) // 兼容前端调用
	mux.HandleFunc("/api/sub/progress", wrap(s.handleSubProgress))
	mux.HandleFunc("/api/sub/cron", wrap(s.handleSubCron))
	mux.HandleFunc("/api/core/update", wrap(s.handleCoreUpdate))
	mux.HandleFunc("/api/service", wrap(s.handleServiceAction))
	mux.HandleFunc("/api/service/action", wrap(s.handleServiceAction)) // 兼容前端调用
	mux.HandleFunc("/api/lan/devices", wrap(s.handleLanDevices))
	mux.HandleFunc("/api/acl/update", wrap(s.handleAclUpdate))
	mux.HandleFunc("/api/connections", wrap(s.handleConnections))
	mux.HandleFunc("/api/connections/close", wrap(s.handleConnectionClose))
	mux.HandleFunc("/api/connection/close", wrap(s.handleConnectionClose)) // 兼容前端调用
	mux.HandleFunc("/api/connections/close_all", wrap(s.handleConnectionsCloseAll))
	mux.HandleFunc("/api/bypass/status", wrap(s.handleBypassStatus))
	mux.HandleFunc("/api/bypass/toggle", wrap(s.handleBypassToggle))

	rootMux := http.NewServeMux()
	rootMux.Handle("/api/", mux)
	rootMux.Handle("/openaero-api/", http.StripPrefix("/openaero-api", mux))

	log.Printf("[api] 🌐 核心控制 API (双轨鉴权已加固) 正在监听: %s", s.addr)
	go func() {
		if err := http.ListenAndServe(s.addr, rootMux); err != nil {
			log.Printf("[api] 监听退出: %v", err)
		}
	}()
}

func (s *Server) getStatusData() map[string]interface{} {
	cfg, _ := config.LoadConfig()
	nodeList, _ := config.LoadNodes()

	sbPid := config.GetSingBoxPid()
	corePid := os.Getpid()
	sbMem := getProcessMemory(sbPid)

	var activeNode *config.Node
	activeNodeId := ""
	if cfg != nil {
		activeNodeId = cfg.ActiveNodeId
		if activeNodeId == "auto_select" {
			bestLat := 0
			for _, n := range nodeList {
				if !config.IsInfoNode(n) && n.Latency > 0 && (bestLat == 0 || n.Latency < bestLat) {
					bestLat = n.Latency
				}
			}
			activeNode = &config.Node{
				Id:      "auto_select",
				Remark:  "⚡ 自动测速优选 (URL-Test)",
				Type:    "urltest",
				Latency: bestLat,
			}
		} else {
			for _, grp := range cfg.StrategyGroups {
				if grp.Id == "proxy" && grp.TargetNodeId != "" && grp.TargetNodeId != "DIRECT" && grp.TargetNodeId != "REJECT" {
					activeNode = config.FindNode(nodeList, grp.TargetNodeId)
					break
				}
			}
			if activeNode == nil {
				activeNode = config.FindNode(nodeList, cfg.ActiveNodeId)
			}
		}
	}
	if activeNode == nil && len(nodeList) > 0 {
		for i := range nodeList {
			if !config.IsInfoNode(nodeList[i]) {
				activeNode = &nodeList[i]
				break
			}
		}
		if activeNode == nil {
			activeNode = &nodeList[0]
		}
	}
	if activeNode != nil && activeNode.Id != "auto_select" && activeNode.Latency <= 0 {
		if delay, err := nodes.TestNodeViaClashAPI(activeNode.Id, 9090, 1200*time.Millisecond); err == nil && delay > 0 {
			activeNode.Latency = delay
			actId := activeNode.Id
			go func(id string, d int) {
				apiMutex.Lock()
				defer apiMutex.Unlock()
				if nl, err := config.LoadNodes(); err == nil {
					for i := range nl {
						if nl[i].Id == id {
							nl[i].Latency = d
							break
						}
					}
					_ = config.SaveNodes(nl)
				}
			}(actId, delay)
		}
	}

	orttTarget := "待命中"
	orttMode := "follow_node"
	ipv6Enabled := false
	if cfg != nil {
		ipv6Enabled = cfg.Ipv6Enabled
		orttMode = cfg.GetOrttMode()
		port := cfg.GetOrttPort()
		if orttMode == "disabled" {
			orttTarget = "已关闭"
		} else {
			targets := []string{}
			if prewarm.GlobalRelay != nil {
				targets = prewarm.GlobalRelay.GetTargets()
			}
			if len(targets) == 0 {
				targets = config.ResolveActiveOrttTargets(cfg, nodeList)
			}
			if len(targets) > 0 {
				var targetStrs []string
				for _, t := range targets {
					targetStrs = append(targetStrs, fmt.Sprintf("%s:%d", t, port))
				}
				orttTarget = strings.Join(targetStrs, ", ")
			}
		}
	}

	status := map[string]interface{}{
		"success":               true,
		"singbox_running":       sbPid > 0,
		"singbox_pid":           sbPid,
		"singbox_mem":           sbMem,
		"daemon_pid":            corePid,
		"singbox_version":       updater.GetCurrentSingBoxVersion(),
		"mode":                  "rule",
		"active_node":           ToPublicNode(activeNode),
		"active_node_id":        activeNodeId,
		"nodes_count":           len(nodeList),
		"strategy_groups_count": 0,
		"ortt_mode":             orttMode,
		"ortt_target":           orttTarget,
		"sing_matrix_mode":      orttMode,
		"sing_matrix_target":    orttTarget,
		"ipv6_enabled":          ipv6Enabled,
		"watchdog_status":       "running",
	}

	if cfg != nil {
		status["mode"] = cfg.Mode
		status["strategy_groups_count"] = len(cfg.StrategyGroups)
	}
	return status
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	_ = json.NewEncoder(w).Encode(s.getStatusData())
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"logs":    s.logHub.GetLogs(),
	})
}

func (s *Server) handleAccessLogs(w http.ResponseWriter, r *http.Request) {
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"logs":    s.logHub.GetAccessLogs(),
	})
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	apiMutex.Lock()
	defer apiMutex.Unlock()

	if r.Method == "GET" {
		cfg, err := config.LoadConfig()
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
			return
		}
		_ = json.NewEncoder(w).Encode(cfg)
		return
	}

	if r.Method == "POST" {
		var newCfg config.AppConfig
		if err := json.NewDecoder(r.Body).Decode(&newCfg); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 400)
			return
		}

		oldCfg, _ := config.LoadConfig()
		if oldCfg != nil {
			if len(newCfg.StrategyGroups) == 0 {
				newCfg.StrategyGroups = oldCfg.StrategyGroups
			}
			if len(newCfg.RuleProviders) == 0 && len(oldCfg.RuleProviders) > 0 {
				newCfg.RuleProviders = oldCfg.RuleProviders
			}
			if len(newCfg.Subscriptions) == 0 {
				newCfg.Subscriptions = oldCfg.Subscriptions
			}
			if newCfg.ActiveNodeId == "" {
				newCfg.ActiveNodeId = oldCfg.ActiveNodeId
			}
			if len(newCfg.CustomDirectDomains) == 0 && len(oldCfg.CustomDirectDomains) > 0 {
				newCfg.CustomDirectDomains = oldCfg.CustomDirectDomains
			}
			if len(newCfg.CustomProxyDomains) == 0 && len(oldCfg.CustomProxyDomains) > 0 {
				newCfg.CustomProxyDomains = oldCfg.CustomProxyDomains
			}
			if newCfg.DnsMode == "" {
				newCfg.DnsMode = oldCfg.DnsMode
			}
			if newCfg.FakeIpRange == "" {
				newCfg.FakeIpRange = oldCfg.FakeIpRange
			}
			if !newCfg.AccessControl.Enabled && len(newCfg.AccessControl.BypassIPs) == 0 && len(oldCfg.AccessControl.BypassIPs) > 0 {
				newCfg.AccessControl = oldCfg.AccessControl
			}
			// Preserve OrttSecret if not passed in post request
			if strings.TrimSpace(newCfg.OrttSecret) == "" && oldCfg.OrttSecret != "" {
				newCfg.OrttSecret = oldCfg.OrttSecret
			}
		}

		if newCfg.DnsMode == "" {
			newCfg.DnsMode = "fake-ip"
		}
		if newCfg.FakeIpRange == "" {
			newCfg.FakeIpRange = "198.18.0.0/15"
		} else {
			cleanPfx, err := ValidatePrefix(newCfg.FakeIpRange)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"invalid fake_ip_range: %v"}`, err), 400)
				return
			}
			newCfg.FakeIpRange = cleanPfx
		}

		if newCfg.Mode == "" {
			newCfg.Mode = "rule"
		} else if err := ValidateMode(newCfg.Mode); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), 400)
			return
		}

		newCfg.NormalizeOrtt()
		if prewarm.GlobalRelay != nil {
			if newCfg.OrttSecret != "" {
				prewarm.GlobalRelay.UpdateSecret([]byte(newCfg.OrttSecret))
			}
			nl, _ := config.LoadNodes()
			targets := config.ResolveActiveOrttTargets(&newCfg, nl)
			prewarm.GlobalRelay.UpdateTargets(targets, newCfg.GetOrttPort())
		}

		if err := config.SaveConfig(&newCfg); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
			return
		}

		// Sync UCI configuration without process fork
		go func(m string, en bool) {
			_ = config.SyncUciOpenAero(en, m)
		}(newCfg.Mode, newCfg.Enabled)

		forceRestart := false
		if oldCfg != nil && oldCfg.Ipv6Enabled != newCfg.Ipv6Enabled {
			forceRestart = true
		}
		if err := ReloadSingBoxEx(&newCfg, forceRestart); err != nil {
			if strings.HasPrefix(err.Error(), "ROLLED_BACK:") {
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"success":     false,
					"rolled_back": true,
					"error":       err.Error(),
				})
				return
			}
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": "配置应用成功并通过健康校验"})
		return
	}

	http.Error(w, `{"error":"method not allowed"}`, 405)
}

func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request) {
	nodeList, err := config.LoadNodes()
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
		return
	}
	cfg, _ := config.LoadConfig()
	activeId := ""
	if cfg != nil {
		activeId = cfg.ActiveNodeId
	}
	for i := range nodeList {
		nodeList[i].IsActive = (nodeList[i].Id == activeId)
	}

	_ = json.NewEncoder(w).Encode(ToPublicNodes(nodeList))
}

func (s *Server) getOrttSecret() []byte {
	cfg, err := config.LoadConfig()
	var secret []byte
	if err == nil && cfg != nil && strings.TrimSpace(cfg.OrttSecret) != "" {
		secret = []byte(strings.TrimSpace(cfg.OrttSecret))
	}
	if len(secret) == 0 && prewarm.GlobalRelay != nil {
		secret = prewarm.GlobalRelay.GetSecret()
	}
	if len(secret) == 0 {
		if envSec := os.Getenv("ORTT_SECRET"); envSec != "" {
			secret = []byte(envSec)
		}
	}
	if len(secret) == 0 {
		secret = []byte("ortt-default-psk-2026")
	}
	return secret
}

func (s *Server) handleNodesTest(w http.ResponseWriter, r *http.Request) {
	nodeList, _ := config.LoadNodes()
	if len(nodeList) == 0 {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "nodes": []PublicNode{}})
		return
	}

	testedNodes := nodes.TestNodesConcurrently(nodeList, 10, 3500*time.Millisecond)

	// Concurrently update ORTT status on tested nodes
	secret := s.getOrttSecret()
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	for i := range testedNodes {
		if config.IsInfoNode(testedNodes[i]) {
			continue
		}
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			res := prewarm.ProbeNodeOrttFullLink(&testedNodes[idx], secret, 9090)
			testedNodes[idx].OrttStatus = res.Status
			testedNodes[idx].OrttLabel = res.Label
			testedNodes[idx].OrttRttMs = res.RttMs
			testedNodes[idx].OrttWarmConns = res.WarmConns
			testedNodes[idx].OrttPoolHits = res.PoolHits
			testedNodes[idx].OrttPoolMisses = res.PoolMisses
			testedNodes[idx].OrttHitRate = res.HitRate
			testedNodes[idx].OrttCheckedAt = res.CheckedAt
		}(i)
	}
	wg.Wait()

	apiMutex.Lock()
	_ = config.SaveNodes(testedNodes)
	apiMutex.Unlock()

	// Dynamically refresh prewarm relay targets if low-latency nodes changed
	if prewarm.GlobalRelay != nil {
		if cfg, err := config.LoadConfig(); err == nil && cfg != nil {
			port := cfg.GetOrttPort()
			targets := config.ResolveActiveOrttTargets(cfg, testedNodes)
			if len(targets) > 0 {
				prewarm.GlobalRelay.UpdateTargets(targets, port)
			}
		}
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "全节点并发测速与 ORTT 全链路检测已完成",
		"nodes":   ToPublicNodes(testedNodes),
	})
}

func (s *Server) handleNodesOrttTest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Id string `json:"id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	nodeList, _ := config.LoadNodes()
	if len(nodeList) == 0 {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "nodes": []PublicNode{}})
		return
	}

	secret := s.getOrttSecret()
	clashPort := 9090

	// Single node test
	if req.Id != "" {
		targetIdx := -1
		for i := range nodeList {
			if nodeList[i].Id == req.Id {
				targetIdx = i
				break
			}
		}
		if targetIdx >= 0 {
			res := prewarm.ProbeNodeOrttFullLink(&nodeList[targetIdx], secret, clashPort)
			nodeList[targetIdx].OrttStatus = res.Status
			nodeList[targetIdx].OrttLabel = res.Label
			nodeList[targetIdx].OrttRttMs = res.RttMs
			nodeList[targetIdx].OrttWarmConns = res.WarmConns
			nodeList[targetIdx].OrttPoolHits = res.PoolHits
			nodeList[targetIdx].OrttPoolMisses = res.PoolMisses
			nodeList[targetIdx].OrttHitRate = res.HitRate
			nodeList[targetIdx].OrttCheckedAt = res.CheckedAt

			apiMutex.Lock()
			_ = config.SaveNodes(nodeList)
			apiMutex.Unlock()

			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"message": "单节点 ORTT 全链路检测完成",
				"node":    ToPublicNode(&nodeList[targetIdx]),
				"nodes":   ToPublicNodes(nodeList),
			})
			return
		}
	}

	// Concurrent test for all nodes
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)

	for i := range nodeList {
		if config.IsInfoNode(nodeList[i]) {
			continue
		}
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			n := &nodeList[idx]
			res := prewarm.ProbeNodeOrttFullLink(n, secret, clashPort)
			n.OrttStatus = res.Status
			n.OrttLabel = res.Label
			n.OrttRttMs = res.RttMs
			n.OrttWarmConns = res.WarmConns
			n.OrttPoolHits = res.PoolHits
			n.OrttPoolMisses = res.PoolMisses
			n.OrttHitRate = res.HitRate
			n.OrttCheckedAt = res.CheckedAt
		}(i)
	}
	wg.Wait()

	apiMutex.Lock()
	_ = config.SaveNodes(nodeList)
	apiMutex.Unlock()

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "全节点 ORTT 全链路检测已完成",
		"nodes":   ToPublicNodes(nodeList),
	})
}

func (s *Server) handleNodeHotSelect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		NodeId string `json:"node_id"`
		Group  string `json:"group"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, 400)
		return
	}
	if req.Group == "" {
		req.Group = "GLOBAL"
	}

	nodeList, _ := config.LoadNodes()
	targetNode := config.FindNode(nodeList, req.NodeId)
	if targetNode == nil && req.NodeId != "direct" && req.NodeId != "block" && req.NodeId != "DIRECT" && req.NodeId != "REJECT" && req.NodeId != "GLOBAL" && req.NodeId != "auto_select" {
		http.Error(w, `{"error":"node not found"}`, 404)
		return
	}

	targetTag := req.NodeId
	if targetNode != nil {
		targetTag = targetNode.Id
	} else if strings.EqualFold(req.NodeId, "DIRECT") {
		targetTag = "direct"
	} else if strings.EqualFold(req.NodeId, "REJECT") || strings.EqualFold(req.NodeId, "BLOCK") {
		targetTag = "block"
	} else if strings.EqualFold(req.NodeId, "GLOBAL") {
		targetTag = "GLOBAL"
	} else if strings.EqualFold(req.NodeId, "auto_select") {
		targetTag = "auto_select"
	}

	cfg, _ := config.LoadConfig()
	clashPort := cfg.ClashAPIPort
	if clashPort <= 0 {
		clashPort = 9090
	}

	prevNodeId := cfg.ActiveNodeId
	t0 := time.Now()
	selectorName := req.Group
	if selectorName == "match" {
		selectorName = "GLOBAL"
	}
	hotSuccess := config.HotSwitchClashSelector(selectorName, targetTag, clashPort)
	elapsedMs := time.Since(t0).Milliseconds()

	apiMutex.Lock()
	c, _ := config.LoadConfig()
	if c == nil {
		c = cfg
	}
	var updatedGroupIds []string
	if req.Group == "GLOBAL" || req.Group == "match" {
		c.ActiveNodeId = targetTag
		updatedGroupIds = append(updatedGroupIds, "GLOBAL")
		for i := range c.StrategyGroups {
			if c.StrategyGroups[i].Id == "match" {
				c.StrategyGroups[i].TargetNodeId = targetTag
			}
		}
		if prewarm.GlobalRelay != nil {
			targets := config.ResolveActiveOrttTargets(c, nodeList)
			prewarm.GlobalRelay.UpdateTargets(targets, c.GetOrttPort())
		}
	} else {
		updatedGroupIds = append(updatedGroupIds, req.Group)
		for i := range c.StrategyGroups {
			if c.StrategyGroups[i].Id == req.Group {
				c.StrategyGroups[i].TargetNodeId = targetTag
			}
		}
	}
	_ = config.SaveConfig(c)
	if jsonStr, err := config.CompileSingBoxConfig(c, nodeList); err == nil {
		_ = config.AtomicWriteFile(config.RunConfigFilePath, []byte(jsonStr), 0644)
	}
	apiMutex.Unlock()

	if !hotSuccess {
		apiMutex.Lock()
		_ = ReloadSingBox(c)
		apiMutex.Unlock()
	} else if (req.Group == "GLOBAL" || req.Group == "match") && targetNode != nil && prevNodeId != "" && prevNodeId != targetTag {
		socksPort := c.SocksPort
		if socksPort <= 0 {
			socksPort = 10808
		}
		mode := c.Mode
		go func(prevId, newId, newRemark string, groups []string) {
			if !probeCanaryHealth(mode, socksPort, 3*time.Second) {
				log.Printf("[canary-hot] ❌ 节点 [%s (%s)] 金丝雀探活失败，正在通过 Clash API 自动回滚至 [%s]", newRemark, newId, prevId)
				for _, g := range groups {
					_ = config.HotSwitchClashSelector(g, prevId, clashPort)
				}
				apiMutex.Lock()
				defer apiMutex.Unlock()
				rc, _ := config.LoadConfig()
				if rc != nil && rc.ActiveNodeId == newId {
					rc.ActiveNodeId = prevId
					for i := range rc.StrategyGroups {
						if rc.StrategyGroups[i].Id == "match" {
							rc.StrategyGroups[i].TargetNodeId = prevId
						}
					}
					_ = config.SaveConfig(rc)
					if nl, err := config.LoadNodes(); err == nil {
						if prewarm.GlobalRelay != nil {
							targets := config.ResolveActiveOrttTargets(rc, nl)
							prewarm.GlobalRelay.UpdateTargets(targets, rc.GetOrttPort())
						}
						if js, err := config.CompileSingBoxConfig(rc, nl); err == nil {
							_ = config.AtomicWriteFile(config.RunConfigFilePath, []byte(js), 0644)
						}
					}
				}
			} else {
				config.BackupGoodConfig()
			}
		}(prevNodeId, targetTag, targetNode.Remark, updatedGroupIds)
	}

	remark := targetTag
	if targetNode != nil {
		remark = targetNode.Remark
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":      true,
		"hot_switched": hotSuccess,
		"group":        req.Group,
		"active_node":  remark,
		"time_ms":      elapsedMs,
	})
}

func (s *Server) handleGroups(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.LoadConfig()
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
		return
	}
	_ = json.NewEncoder(w).Encode(cfg.StrategyGroups)
}

func (s *Server) handleGroupSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Id           string `json:"id"`
		TargetNodeId string `json:"target_node_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, 400)
		return
	}

	cfg, _ := config.LoadConfig()
	clashPort := cfg.ClashAPIPort
	if clashPort <= 0 {
		clashPort = 9090
	}

	targetTag := req.TargetNodeId
	if strings.EqualFold(req.TargetNodeId, "DIRECT") {
		targetTag = "direct"
	} else if strings.EqualFold(req.TargetNodeId, "REJECT") || strings.EqualFold(req.TargetNodeId, "BLOCK") {
		targetTag = "block"
	} else if req.TargetNodeId == "" || strings.EqualFold(req.TargetNodeId, "GLOBAL") {
		targetTag = "GLOBAL"
	} else if strings.EqualFold(req.TargetNodeId, "auto_select") {
		targetTag = "auto_select"
	}

	selectorName := req.Id
	if selectorName == "match" {
		selectorName = "GLOBAL"
	}
	t0 := time.Now()
	if config.HotSwitchClashSelector(selectorName, targetTag, clashPort) {
		elapsedMs := time.Since(t0).Milliseconds()
		apiMutex.Lock()
		c, _ := config.LoadConfig()
		if c == nil {
			c = cfg
		}
		for i := range c.StrategyGroups {
			if c.StrategyGroups[i].Id == req.Id {
				c.StrategyGroups[i].TargetNodeId = req.TargetNodeId
				if req.Id == "match" && req.TargetNodeId != "" && req.TargetNodeId != "DIRECT" && req.TargetNodeId != "REJECT" {
					c.ActiveNodeId = targetTag
				}
				break
			}
		}
		_ = config.SaveConfig(c)
		if nl, err := config.LoadNodes(); err == nil {
			if prewarm.GlobalRelay != nil {
				targets := config.ResolveActiveOrttTargets(c, nl)
				prewarm.GlobalRelay.UpdateTargets(targets, c.GetOrttPort())
			}
			if js, err := config.CompileSingBoxConfig(c, nl); err == nil {
				_ = config.AtomicWriteFile(config.RunConfigFilePath, []byte(js), 0644)
			}
		}
		apiMutex.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success":      true,
			"hot_switched": true,
			"time_ms":      elapsedMs,
		})
		return
	}

	apiMutex.Lock()
	defer apiMutex.Unlock()

	found := false
	for i := range cfg.StrategyGroups {
		if cfg.StrategyGroups[i].Id == req.Id {
			cfg.StrategyGroups[i].TargetNodeId = req.TargetNodeId
			found = true
			if req.Id == "match" && req.TargetNodeId != "" && req.TargetNodeId != "DIRECT" && req.TargetNodeId != "REJECT" {
				cfg.ActiveNodeId = targetTag
			}
			break
		}
	}
	if !found {
		http.Error(w, `{"error":"group not found"}`, 404)
		return
	}

	_ = config.SaveConfig(cfg)
	if prewarm.GlobalRelay != nil {
		nl, _ := config.LoadNodes()
		targets := config.ResolveActiveOrttTargets(cfg, nl)
		prewarm.GlobalRelay.UpdateTargets(targets, cfg.GetOrttPort())
	}
	if err := ReloadSingBox(cfg); err != nil {
		if strings.HasPrefix(err.Error(), "ROLLED_BACK:") {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success":     false,
				"rolled_back": true,
				"error":       "策略组目标节点无法连通外网，系统已自动回滚！",
			})
			return
		}
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "hot_switched": false})
}

func (s *Server) handleRuleAdd(w http.ResponseWriter, r *http.Request) {
	apiMutex.Lock()
	defer apiMutex.Unlock()

	var req struct {
		Domain string `json:"domain"`
		Target string `json:"target"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Domain) == "" {
		http.Error(w, `{"error":"domain cannot be empty"}`, 400)
		return
	}

	domain := strings.TrimSpace(strings.ToLower(req.Domain))
	cfg, _ := config.LoadConfig()

	if req.Target == "direct" {
		for _, d := range cfg.CustomDirectDomains {
			if d == domain {
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
				return
			}
		}
		cfg.CustomDirectDomains = append(cfg.CustomDirectDomains, domain)
	} else if req.Target == "proxy" {
		for _, d := range cfg.CustomProxyDomains {
			if d == domain {
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
				return
			}
		}
		cfg.CustomProxyDomains = append(cfg.CustomProxyDomains, domain)
	} else {
		for i := range cfg.StrategyGroups {
			if cfg.StrategyGroups[i].Id == req.Target {
				for _, d := range cfg.StrategyGroups[i].Rules {
					if d == domain {
						_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
						return
					}
				}
				cfg.StrategyGroups[i].Rules = append(cfg.StrategyGroups[i].Rules, domain)
				break
			}
		}
	}

	_ = config.SaveConfig(cfg)
	if err := ReloadSingBox(cfg); err != nil {
		if strings.HasPrefix(err.Error(), "ROLLED_BACK:") {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success":     false,
				"rolled_back": true,
				"error":       "新规则导致网络分流异常，系统已自动回滚！",
			})
			return
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "domain": domain})
}

func (s *Server) handleTraffic(w http.ResponseWriter, r *http.Request) {
	stats := s.trafficMonitor.Update()
	_ = json.NewEncoder(w).Encode(stats)
}

func (s *Server) handleDiagnoseRoute(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Target   string `json:"target"`
		ClientIP string `json:"client_ip"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Target == "" {
		http.Error(w, `{"error":"target is required"}`, 400)
		return
	}

	cfg, _ := config.LoadConfig()
	nodeList, _ := config.LoadNodes()

	clientIP := req.ClientIP
	if clientIP == "" {
		if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil && host != "127.0.0.1" && host != "::1" {
			clientIP = host
		}
	}

	report := diagnose.DiagnoseRouteWithClient(req.Target, clientIP, cfg, nodeList)
	_ = json.NewEncoder(w).Encode(report)
}

func (s *Server) handleSubAsyncUpdate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Url string `json:"url"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	cfg, _ := config.LoadConfig()
	subUrl := req.Url
	if subUrl == "" && len(cfg.Subscriptions) > 0 {
		subUrl = cfg.Subscriptions[0].Url
	}
	if subUrl == "" {
		http.Error(w, `{"error":"subscription url is empty"}`, 400)
		return
	}

	go func() {
		sm := subscription.GlobalSubManager
		sm.SetProgress("FETCHING", 20, "正在连接并下载机场订阅数据...", "")

		fetchResult, err := subscription.FetchSubscriptionFull(subUrl, "clash-verge/v2.4.5")
		if err != nil {
			sm.SetProgress("FAILED", 20, "下载订阅失败", err.Error())
			return
		}

		if len(fetchResult.Nodes) == 0 {
			sm.SetProgress("FAILED", 30, "订阅未解析出任何可用节点", "empty nodes")
			return
		}

		sm.SetProgress("PARSING", 50, fmt.Sprintf("已成功清洗并解析出 %d 个有效节点 (已过滤广告与乱码)", len(fetchResult.Nodes)), "")

		apiMutex.Lock()
		defer apiMutex.Unlock()

		sm.SetProgress("COMPILING", 75, "正在编译最新 sing-box 路由与 Clash API 规则...", "")
		c, _ := config.LoadConfig()
		if c != nil {
			if req.Url != "" && len(c.Subscriptions) > 0 {
				c.Subscriptions[0].Url = req.Url
			}
			subscription.ApplySubscriptionResult(c, fetchResult, subUrl)
			_ = config.SaveConfig(c)
		}

		sm.SetProgress("RELOADING", 90, "正在热载内核配置并执行金丝雀连通性校验...", "")
		if err := ReloadSingBox(c); err != nil {
			sm.SetProgress("FAILED", 90, "内核重载或自愈回滚校验失败", err.Error())
			return
		}

		sm.SetProgress("SUCCESS", 100, fmt.Sprintf("订阅更新完毕！已激活 %d 个节点，分流服务正常运行", len(fetchResult.Nodes)), "")
	}()

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"status":  "started",
		"message": "订阅异步更新任务已启动",
	})
}

func (s *Server) handleSubProgress(w http.ResponseWriter, r *http.Request) {
	progress := subscription.GlobalSubManager.GetProgress()
	_ = json.NewEncoder(w).Encode(progress)
}

func (s *Server) handleSubCron(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Interval int `json:"interval"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, 400)
		return
	}

	if err := subscription.SetupCrontab(req.Interval); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
		return
	}

	apiMutex.Lock()
	cfg, _ := config.LoadConfig()
	cfg.AutoUpdateInterval = req.Interval
	_ = config.SaveConfig(cfg)
	apiMutex.Unlock()

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"interval": req.Interval,
	})
}

func (s *Server) handleCoreUpdate(w http.ResponseWriter, r *http.Request) {
	apiMutex.Lock()
	defer apiMutex.Unlock()

	ver, err := updater.UpdateSingBoxCore()
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"version": ver,
	})
}

func (s *Server) handleRulesUpdate(w http.ResponseWriter, r *http.Request) {
	apiMutex.Lock()
	defer apiMutex.Unlock()

	cfg, _ := config.LoadConfig()
	if cfg != nil {
		subUrl := ""
		if len(cfg.Subscriptions) > 0 {
			subUrl = cfg.Subscriptions[0].Url
		}
		if subUrl != "" {
			res, err := subscription.FetchSubscriptionFull(subUrl, "clash-verge/v2.4.5")
			if err == nil && len(res.Providers) > 0 {
				cfg.RuleProviders = res.Providers
				_ = config.SaveConfig(cfg)
				_ = ReloadSingBox(cfg)
			}
		}
	}

	_ = updater.UpdateRuleSets()
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

func (s *Server) handleServiceAction(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, 400)
		return
	}

	switch req.Action {
	case "stop":
		apiMutex.Lock()
		cfg, _ := config.LoadConfig()
		if cfg != nil {
			cfg.Enabled = false
			_ = config.SaveConfig(cfg)
		}
		apiMutex.Unlock()
		_ = config.SyncUciOpenAero(false, "")
		CleanupAllOpenAeroFirewallAndRoutes()
		_ = config.SignalSingBox(syscall.SIGTERM)
		time.Sleep(300 * time.Millisecond)
		_ = config.SignalSingBox(syscall.SIGKILL)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": "核心代理已停止，防火墙与 DNS 已恢复直通"})
	case "start", "restart":
		apiMutex.Lock()
		cfg, _ := config.LoadConfig()
		if cfg != nil {
			cfg.Enabled = true
			_ = config.SaveConfig(cfg)
		}
		_ = config.SyncUciOpenAero(true, "")
		if cfg != nil {
			_ = ReloadSingBoxEx(cfg, true)
		}
		apiMutex.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": "核心服务已启动并恢复透明分流"})
	default:
		http.Error(w, `{"error":"unknown action"}`, 400)
	}
}

type LanDevice struct {
	IP          string `json:"ip"`
	MAC         string `json:"mac"`
	Name        string `json:"name"`
	IsBypassed  bool   `json:"is_bypassed"`
	IsProxyOnly bool   `json:"is_proxy_only"`
}

func (s *Server) handleLanDevices(w http.ResponseWriter, r *http.Request) {
	cfg, _ := config.LoadConfig()
	bypassedIPMap := make(map[string]bool)
	bypassedMACMap := make(map[string]bool)
	proxyOnlyIPMap := make(map[string]bool)

	if cfg != nil {
		for _, ip := range cfg.AccessControl.BypassIPs {
			bypassedIPMap[strings.TrimSpace(ip)] = true
		}
		for _, mac := range cfg.AccessControl.BypassMACs {
			bypassedMACMap[strings.ToLower(strings.TrimSpace(mac))] = true
		}
		for _, ip := range cfg.AccessControl.ProxyOnlyIPs {
			proxyOnlyIPMap[strings.TrimSpace(ip)] = true
		}
	}

	devices := make([]LanDevice, 0)
	seenIP := make(map[string]bool)
	ipToMac, ipToName := config.ReadLanHosts()
	for ip, mac := range ipToMac {
		if net.ParseIP(ip) == nil || seenIP[ip] {
			continue
		}
		seenIP[ip] = true
		name := ipToName[ip]
		if name == "" {
			name = "局域网终端 (" + ip + ")"
		}
		devices = append(devices, LanDevice{
			IP:          ip,
			MAC:         mac,
			Name:        name,
			IsBypassed:  bypassedIPMap[ip] || bypassedMACMap[mac],
			IsProxyOnly: proxyOnlyIPMap[ip],
		})
	}
	if cfg != nil {
		for _, ip := range cfg.AccessControl.BypassIPs {
			ip = strings.TrimSpace(ip)
			if ip != "" && !seenIP[ip] {
				seenIP[ip] = true
				devices = append(devices, LanDevice{
					IP:          ip,
					MAC:         "",
					Name:        "指定直连设备 (" + ip + ")",
					IsBypassed:  true,
					IsProxyOnly: false,
				})
			}
		}
	}

	mode := "blacklist"
	enabled := true
	bypassIPs := []string{}
	bypassMACs := []string{}
	if cfg != nil {
		mode = cfg.AccessControl.Mode
		enabled = cfg.AccessControl.Enabled
		bypassIPs = cfg.AccessControl.BypassIPs
		bypassMACs = cfg.AccessControl.BypassMACs
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":     true,
		"enabled":     enabled,
		"mode":        mode,
		"bypass_ips":  bypassIPs,
		"bypass_macs": bypassMACs,
		"devices":     devices,
	})
}

func (s *Server) handleAclUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, `{"error":"POST only"}`, 405)
		return
	}

	var req config.AccessControlConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, 400)
		return
	}

	// Strict strong-type parameter validation for ACL inputs
	validBypassIPs, err := ValidateIPList(req.BypassIPs)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"invalid bypass_ips: %v"}`, err), 400)
		return
	}
	validProxyIPs, err := ValidateIPList(req.ProxyOnlyIPs)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"invalid proxy_only_ips: %v"}`, err), 400)
		return
	}
	validMACs, err := ValidateMACList(req.BypassMACs)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"invalid bypass_macs: %v"}`, err), 400)
		return
	}

	req.BypassIPs = validBypassIPs
	req.ProxyOnlyIPs = validProxyIPs
	req.BypassMACs = validMACs

	apiMutex.Lock()
	defer apiMutex.Unlock()

	cfg, _ := config.LoadConfig()
	if cfg == nil {
		cfg = config.DefaultConfig()
	}

	cfg.AccessControl = req
	_ = config.SaveConfig(cfg)

	applyACLNetwork(cfg)

	nodeList, _ := config.LoadNodes()
	jsonStr, err := config.CompileSingBoxConfig(cfg, nodeList)
	if err == nil {
		_ = config.AtomicWriteFile(config.RunConfigFilePath, []byte(jsonStr), 0644)
		_ = config.SignalSingBox(syscall.SIGHUP)
		config.EnsureTunRoutesAfterReload(cfg)
		config.BackupGoodConfig()
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "访问控制策略已热加载生效",
	})
}
