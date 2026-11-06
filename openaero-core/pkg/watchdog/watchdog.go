package watchdog

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"openaero-core/pkg/config"
	"openaero-core/pkg/nodes"
	"openaero-core/pkg/prewarm"
)

const (
	StateNormal         int32 = 0
	StateWanDown        int32 = 1
	StateProxyFail      int32 = 2
	StateDirectFallback int32 = 3
)

type Watchdog struct {
	targetIP       string
	interval       time.Duration
	failThreshold  int
	wanFailCount   int32
	proxyFailCount int32
	state          int32
	stopChan       chan struct{}

	restartMu      sync.Mutex
	lastRestart    time.Time
	restartCount   int
}

func New(targetIP string, intervalSec int, failThreshold int) *Watchdog {
	if targetIP == "" {
		targetIP = "223.5.5.5:53"
	}
	if intervalSec <= 0 {
		intervalSec = 5
	}
	if failThreshold <= 0 {
		failThreshold = 3
	}

	return &Watchdog{
		targetIP:      targetIP,
		interval:      time.Duration(intervalSec) * time.Second,
		failThreshold: failThreshold,
		stopChan:      make(chan struct{}),
	}
}

func (w *Watchdog) Start() {
	go func() {
		ticker := time.NewTicker(w.interval)
		defer ticker.Stop()

		for {
			select {
			case <-w.stopChan:
				return
			case <-ticker.C:
				w.check()
			}
		}
	}()
}

func (w *Watchdog) Stop() {
	select {
	case <-w.stopChan:
		return
	default:
		close(w.stopChan)
	}
}

func (w *Watchdog) check() {
	cfg, err := config.LoadConfig()
	if err != nil || cfg == nil || !cfg.Enabled || cfg.Mode == "direct" {
		return
	}

	// 1. Process Liveness Self-Healing (27-Audit Defect 6.1):
	// If sing-box crashed or was killed (OOM / SIGKILL), immediately respawn it and restore TUN routes.
	if config.GetSingBoxPid() <= 0 {
		log.Println("[watchdog] 🚨 检测到 sing-box 核心进程异常退出 (PID=0)，立即触发进程级受控自愈重启！")
		w.SafeRestartWithBackoff(cfg)
		return
	}

	// 2. Lightweight Table 2022 & Fake-IP Route Check-First Self-Healing (27-Audit Defect 7)
	config.EnsureTunRoutes(cfg)

	// 3. Stage 1: WAN / Gateway Physical Connectivity Probe (26-Audit Defect 4: TCP handshake, bypassing sing-box UDP DNS cache)
	wanAlive := w.probeDomesticDNS()
	if !wanAlive {
		fails := atomic.AddInt32(&w.wanFailCount, 1)
		if int(fails) >= w.failThreshold {
			if atomic.CompareAndSwapInt32(&w.state, StateNormal, StateWanDown) ||
				atomic.CompareAndSwapInt32(&w.state, StateProxyFail, StateWanDown) {
				log.Println("[watchdog] ⚠️ WAN 离线，挂起自愈动作（严禁重启核心服务，等待网络物理层恢复）")
			}
		}
		// In WAN down state, reset proxy failure counter to prevent false alarms
		atomic.StoreInt32(&w.proxyFailCount, 0)
		return
	}

	// WAN is alive
	if atomic.LoadInt32(&w.wanFailCount) > 0 {
		atomic.StoreInt32(&w.wanFailCount, 0)
	}
	if atomic.CompareAndSwapInt32(&w.state, StateWanDown, StateNormal) {
		log.Println("[watchdog] ✅ WAN 连接已恢复，解除离线挂起，恢复代理监控")
	}

	// 4. Non-Disruptive StateDirectFallback Background Recovery Probe (27-Audit Defect 6.4):
	// Never switch GLOBAL back to the dead node before confirming node recovery!
	// Probe candidate nodes directly via Clash API /proxies/{id}/delay while GLOBAL stays on "direct".
	if atomic.LoadInt32(&w.state) == StateDirectFallback {
		w.probeRecoveryFromDirectFallback(cfg)
		return
	}

	// 5. Stage 2: Proxy Health Check (Only when WAN is confirmed healthy)
	socksPort := cfg.SocksPort
	if socksPort <= 0 {
		socksPort = 10808
	}

	proxyAlive := w.probeProxyCanary(socksPort)
	if proxyAlive {
		if atomic.LoadInt32(&w.proxyFailCount) > 0 {
			atomic.StoreInt32(&w.proxyFailCount, 0)
		}
		if atomic.CompareAndSwapInt32(&w.state, StateProxyFail, StateNormal) {
			log.Println("[watchdog] ✅ 代理链路健康检查恢复正常")
		}
		return
	}

	// Proxy probe failed
	fails := atomic.AddInt32(&w.proxyFailCount, 1)
	log.Printf("[watchdog] 代理链路探测失败 (%d/%d)", fails, w.failThreshold)
	if int(fails) >= w.failThreshold {
		atomic.StoreInt32(&w.proxyFailCount, 0)
		w.handleProxyFailure(cfg)
	}
}

func (w *Watchdog) probeDomesticDNS() bool {
	// 26-Audit Defect 4: Perform genuine TCP 3-way handshake against domestic DNS endpoints (routed via priority 8987 main table)
	// so sing-box's in-memory UDP DNS cache never causes 0ms false-positive WAN alive results during physical WAN outages.
	targets := []string{
		w.targetIP,
		"223.5.5.5:53",
		"119.29.29.29:53",
	}
	for _, t := range targets {
		if t == "" {
			continue
		}
		conn, err := net.DialTimeout("tcp", t, 1500*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return true
		}
	}
	return false
}

func (w *Watchdog) probeProxyCanary(socksPort int) bool {
	// 27-Audit Defect 6.1 & 6.2: First probe GLOBAL selector directly via Clash API
	// so pinned sub-strategy groups (e.g. 'google' group) never mask a dead GLOBAL/main node!
	if delay, err := nodes.TestNodeViaClashAPI("GLOBAL", 9090, 2000*time.Millisecond); err == nil && delay > 0 {
		return true
	}

	proxyURL, err := url.Parse(fmt.Sprintf("socks5://127.0.0.1:%d", socksPort))
	if err != nil {
		return false
	}

	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
			DialContext: (&net.Dialer{
				Timeout: 2 * time.Second,
			}).DialContext,
			DisableKeepAlives: true,
		},
		Timeout: 2 * time.Second,
	}

	targets := []string{
		"http://cp.cloudflare.com/generate_204",
	}

	for _, t := range targets {
		resp, err := client.Get(t)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 || resp.StatusCode == 204 {
				return true
			}
		}
	}
	return false
}

// findBestHealthyNode concurrently probes candidate nodes via Clash API and returns the lowest-latency healthy node.
func (w *Watchdog) findBestHealthyNode(candidates []config.Node, clashPort int) *config.Node {
	if len(candidates) == 0 {
		return nil
	}
	if clashPort <= 0 {
		clashPort = 9090
	}

	type probeResult struct {
		node  config.Node
		delay int
	}
	resChan := make(chan probeResult, len(candidates))
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup

	for _, n := range candidates {
		wg.Add(1)
		go func(cand config.Node) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			delay, err := nodes.TestNodeViaClashAPI(cand.Id, clashPort, 2500*time.Millisecond)
			if err == nil && delay > 0 {
				cand.Latency = delay
				resChan <- probeResult{node: cand, delay: delay}
			}
		}(n)
	}
	wg.Wait()
	close(resChan)

	var best *config.Node
	bestDelay := int(^uint(0) >> 1)
	for r := range resChan {
		if r.delay < bestDelay {
			bestDelay = r.delay
			nCopy := r.node
			best = &nCopy
		}
	}
	return best
}

func (w *Watchdog) probeRecoveryFromDirectFallback(cfg *config.AppConfig) {
	nodeList, err := config.LoadNodes()
	if err != nil || len(nodeList) == 0 {
		return
	}
	candidates := make([]config.Node, 0, len(nodeList))
	for _, n := range nodeList {
		if !config.IsInfoNode(n) {
			candidates = append(candidates, n)
		}
	}
	clashPort := cfg.ClashAPIPort
	if clashPort <= 0 {
		clashPort = 9090
	}
	bestNode := w.findBestHealthyNode(candidates, clashPort)
	if bestNode == nil {
		// Still no healthy nodes; keep GLOBAL on "direct" without disturbing LAN traffic
		return
	}

	log.Printf("[watchdog] 🎉 后台探活发现可用节点 [%s (%dms)]，自动退出直连保护态并恢复代理！", bestNode.Remark, bestNode.Latency)
	w.applyFailoverNode(cfg, nodeList, bestNode, clashPort)
	atomic.StoreInt32(&w.proxyFailCount, 0)
	atomic.StoreInt32(&w.state, StateNormal)
}

func (w *Watchdog) applyFailoverNode(cfg *config.AppConfig, nodeList []config.Node, bestNode *config.Node, clashPort int) {
	cfg.ActiveNodeId = bestNode.Id
	// 27-Audit Defect 4.2: Only update the "match" group; never overwrite user-pinned sub-strategy groups!
	for i := range cfg.StrategyGroups {
		if cfg.StrategyGroups[i].Id == "match" {
			cfg.StrategyGroups[i].TargetNodeId = bestNode.Id
		}
	}
	_ = config.SaveConfig(cfg)

	// Synchronously update 0-RTT prewarm targets (Multi-Target Relay)
	if prewarm.GlobalRelay != nil {
		targets := config.ResolveActiveOrttTargets(cfg, nodeList)
		prewarm.GlobalRelay.UpdateTargets(targets, cfg.GetOrttPort())
	}

	// Recompile run_config.json atomically
	if jsonStr, err := config.CompileSingBoxConfig(cfg, nodeList); err == nil {
		_ = config.AtomicWriteFile(config.RunConfigFilePath, []byte(jsonStr), 0644)
		log.Printf("[watchdog] 🔄 成功重编并原子落盘 run_config.json (ActiveNodeId=%s)", bestNode.Id)
	} else {
		log.Printf("[watchdog] 编译配置失败: %v", err)
	}

	// 25-Audit Defect 3 & 27-Audit Defect 3: Immediately call Clash API PUT /proxies/GLOBAL for <10ms zero-downtime failover
	if config.HotSwitchClashSelector("GLOBAL", bestNode.Id, clashPort) {
		log.Printf("[watchdog] ⚡ 已通过 Clash API 完成内存秒级热切换 -> [%s (%s, %dms)]", bestNode.Remark, bestNode.Id, bestNode.Latency)
	} else {
		_ = config.SignalSingBox(syscall.SIGHUP)
		config.EnsureTunRoutesAfterReload(cfg)
		log.Printf("[watchdog] Clash API 不可用，已向 sing-box 发送 HUP 热重载信号")
	}
}

// handleProxyFailure performs smart failover without thrashing process restarts
func (w *Watchdog) handleProxyFailure(cfg *config.AppConfig) {
	atomic.StoreInt32(&w.state, StateProxyFail)
	log.Println("[watchdog] ⚠️ 探测到活动节点失效，启动无感自愈与智能故障转移 (Smart Failover)...")

	nodeList, err := config.LoadNodes()
	if err != nil || len(nodeList) == 0 {
		log.Println("[watchdog] 无节点列表可用，自动降级为 DIRECT 直连模式以保障局域网畅通")
		w.fallbackToDirect(cfg)
		return
	}

	// 27-Audit Defect 6.2: Filter out non-proxy info nodes (剩余流量/套餐到期/127.0.0.1) and the currently failing node
	candidates := make([]config.Node, 0, len(nodeList))
	for _, n := range nodeList {
		if config.IsInfoNode(n) {
			continue
		}
		if n.Id == cfg.ActiveNodeId {
			continue
		}
		candidates = append(candidates, n)
	}

	clashPort := cfg.ClashAPIPort
	if clashPort <= 0 {
		clashPort = 9090
	}

	// Concurrently probe all candidate nodes and pick the lowest-latency reachable node
	bestNode := w.findBestHealthyNode(candidates, clashPort)
	if bestNode == nil {
		// 27-Audit Defect 6.3: All candidate nodes are unreachable (regardless of nodeList length) -> fallback to DIRECT
		log.Println("[watchdog] ⚠️ 所有候选代理节点均不可达，自动进入内存级 DIRECT 直连保护态以保障局域网畅通")
		w.fallbackToDirect(cfg)
		return
	}

	log.Printf("[watchdog] 正在将活动节点由 [%s] 智能切换至最优可用节点 [%s (%s, %dms)]...",
		cfg.ActiveNodeId, bestNode.Remark, bestNode.Server, bestNode.Latency)
	w.applyFailoverNode(cfg, nodeList, bestNode, clashPort)
	atomic.StoreInt32(&w.state, StateNormal)
}

func (w *Watchdog) fallbackToDirect(cfg *config.AppConfig) {
	// 26-Audit Defect 4 & 27-Audit Defect 6.4: Never overwrite cfg.Mode = "direct" on disk (which permanently disables Watchdog recovery).
	// Instead, transition in-memory state to StateDirectFallback and temporarily switch Clash API GLOBAL to "direct".
	atomic.StoreInt32(&w.state, StateDirectFallback)
	clashPort := 9090
	if cfg != nil && cfg.ClashAPIPort > 0 {
		clashPort = cfg.ClashAPIPort
	}
	_ = config.HotSwitchClashSelector("GLOBAL", "direct", clashPort)
	log.Println("[watchdog] 🛡️ 已进入内存级临时直连保护态 (StateDirectFallback)，后台将继续静默探测节点并在恢复后自动切回代理")
}

// SafeRestartWithBackoff throttles service restarts (max 4 restarts per 15 minutes) and respawns sing-box without killing openaero-core.
func (w *Watchdog) SafeRestartWithBackoff(cfg *config.AppConfig) bool {
	w.restartMu.Lock()
	defer w.restartMu.Unlock()

	now := time.Now()
	if now.Sub(w.lastRestart) > 15*time.Minute {
		w.restartCount = 0
	}

	if w.restartCount >= 4 {
		log.Printf("[watchdog] 🛑 15分钟内重启已达上限 (%d 次)，触发退避防护，阻止重复重启风暴", w.restartCount)
		return false
	}

	w.restartCount++
	w.lastRestart = now
	log.Printf("[watchdog] 🚀 执行受控 sing-box 内核自愈拉起 (第 %d/4 次)...", w.restartCount)

	// Ensure run_config.json exists and is valid before starting sing-box
	if _, err := os.Stat(config.RunConfigFilePath); err != nil && cfg != nil {
		if nl, err := config.LoadNodes(); err == nil {
			if js, err := config.CompileSingBoxConfig(cfg, nl); err == nil {
				_ = config.AtomicWriteFile(config.RunConfigFilePath, []byte(js), 0644)
			}
		}
	}

	// Wait briefly to see if procd already respawned sing-box to avoid duplicate instances
	time.Sleep(1200 * time.Millisecond)
	if config.GetSingBoxPid() <= 0 {
		cmd := exec.Command("/usr/bin/sing-box", "run", "-c", config.RunConfigFilePath)
		if err := cmd.Start(); err == nil {
			go func() { _ = cmd.Wait() }()
		}
	}
	config.EnsureTunRoutesAfterReload(cfg)
	return true
}