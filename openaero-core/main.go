package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"openaero-core/pkg/api"
	"openaero-core/pkg/config"
	"openaero-core/pkg/nodes"
	"openaero-core/pkg/prewarm"
	"openaero-core/pkg/subscription"
	"openaero-core/pkg/updater"
	"openaero-core/pkg/watchdog"
)

var (
	Version = "v2.6.3"
	Commit  = "dev"
)

func init() {
	time.Local = time.FixedZone("CST", 8*3600)
}

func main() {
	versionFlag := flag.Bool("version", false, "Print version info and exit")
	configPath := flag.String("c", config.RunConfigFilePath, "sing-box config json path")
	apiPort := flag.String("api", "127.0.0.1:2026", "Local REST API address")
	updateSubs := flag.Bool("update-subs", false, "Execute silent subscription update and exit")
	silent := flag.Bool("silent", false, "Suppress output during update")
	applyNet := flag.Bool("apply-net", false, "Apply firewall rules, routing tables, and sysctl settings, then exit")
	cleanupNet := flag.Bool("cleanup-net", false, "Clean up firewall rules, routing tables, and terminate sing-box, then exit")
	flag.Parse()

	if *versionFlag {
		fmt.Printf("%s (%s)\n", Version, Commit)
		return
	}

	if *updateSubs {
		executeSilentSubUpdate(*silent)
		return
	}

	if *applyNet {
		cfg, err := config.LoadConfig()
		if err != nil || cfg == nil {
			cfg = config.DefaultConfig()
		}
		api.ApplyNetworkSettings(cfg)
		config.EnsureTunRoutes(cfg)
		return
	}

	if *cleanupNet {
		api.CleanupAllOpenAeroFirewallAndRoutes()
		_ = config.SignalSingBox(syscall.SIGTERM)
		time.Sleep(200 * time.Millisecond)
		_ = config.SignalSingBox(syscall.SIGKILL)
		return
	}

	if flag.NArg() > 0 && !isFlag(flag.Arg(0)) {
		handleCliCommand(flag.Args())
		return
	}

	log.Printf("[openaero-core] 🚀 守护与分流管理引擎启动中... (配置: %s)", *configPath)

	// 1. Ensure config and run_config exist
	cfg, err := config.LoadConfig()
	if err != nil || cfg == nil {
		log.Printf("[openaero-core] 初始化默认配置: %v", err)
		cfg = config.DefaultConfig()
		_ = config.SaveConfig(cfg)
	}

	// 2. Start local REST API immediately (zero-wait for LuCI)
	server := api.NewServer(*apiPort)
	server.Start()

	nodeList, _ := config.LoadNodes()
	hasValidSub := false
	for _, sub := range cfg.Subscriptions {
		if strings.TrimSpace(sub.Url) != "" {
			hasValidSub = true
			break
		}
	}
	if !hasValidSub || len(nodeList) == 0 {
		log.Println("[openaero-core] 请在管理面板配置订阅链接以导入节点")
	}
	_ = api.ReloadSingBox(cfg)

	// TASK_12: Start periodic silent node health check (3s startup, 12s cold-start compensation, and 5-min intervals)
	startPeriodicNodeHealthCheck(cfg)

	// 2. Start Anti-deadlock Watchdog
	var wd *watchdog.Watchdog
	if cfg.WatchdogEnabled {
		interval := cfg.WatchdogInterval
		if interval <= 0 {
			interval = 15
		}
		target := cfg.WatchdogTarget
		if target == "" {
			target = "223.5.5.5:53"
		}
		wd = watchdog.New(target, interval, 3)
		wd.Start()
		log.Printf("[openaero-core] 🛡️ 防断网自愈看门狗已激活 (目标: %s, 周期: %ds)", target, interval)
	}

	// 3. Start ORTT 0-RTT Relay (Multi-Target Prewarming)
	_ = os.Remove("/etc/openaero/cache.db")
	cfg.NormalizeOrtt()
	port := cfg.GetOrttPort()
	targets := config.ResolveActiveOrttTargets(cfg, nodeList)

	secret := []byte(strings.TrimSpace(cfg.OrttSecret))
	if len(secret) == 0 {
		if envSec := os.Getenv("ORTT_SECRET"); envSec != "" {
			secret = []byte(envSec)
		}
	}

	relay := prewarm.NewRelayWithSecret("", port, secret, 1)
	prewarm.GlobalRelay = relay
	if len(targets) > 0 && len(secret) > 0 {
		relay.UpdateTargets(targets, port)
		log.Printf("[openaero-core] ⚡ ORTT 0-RTT 极速预热已激活 (目标: %s, 端口: %d)", strings.Join(targets, ", "), port)
	} else if len(secret) == 0 {
		log.Println("[openaero-core] [ortt] ⚠️ 未配置 ortt_secret 鉴权密钥，ORTT 预热模块拒绝启动")
	} else {
		log.Printf("[openaero-core] ⚡ ORTT 0-RTT 预热待命中 (目标节点为空或已禁用)")
	}

	log.Printf("[openaero-core] ✅ 系统就绪，已托管 %d 个节点与 %d 个策略组", len(nodeList), len(cfg.StrategyGroups))

	// Wait for exit
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	log.Println("[openaero-core] 正在安全停止守护进程...")
	if wd != nil {
		wd.Stop()
	}
	if relay != nil {
		relay.Stop()
	}
	log.Println("[openaero-core] 已退出")
}

func isFlag(s string) bool {
	return len(s) > 0 && s[0] == '-'
}

func handleCliCommand(args []string) {
	cmd := args[0]
	switch cmd {
	case "test":
		nodeList, err := config.LoadNodes()
		if err != nil || len(nodeList) == 0 {
			fmt.Println("未找到任何可用节点")
			return
		}
		fmt.Printf("开始对 %d 个节点进行并发测速...\n", len(nodeList))
		res := nodes.TestNodesConcurrently(nodeList, 10, 2500*time.Millisecond)
		for _, n := range res {
			lat := fmt.Sprintf("%dms", n.Latency)
			if n.Latency < 0 {
				lat = "超时"
			}
			fmt.Printf("- [%s] %s (%s): %s\n", n.Type, n.Remark, n.Server, lat)
		}
	case "sub":
		if len(args) < 2 {
			fmt.Println("用法: openaero-core sub <订阅URL>")
			return
		}
		url := args[1]
		fmt.Printf("正在从 %s 获取订阅...\n", url)
		res, err := subscription.FetchSubscriptionFull(url, "clash-verge/v2.4.5")
		if err != nil {
			fmt.Printf("获取失败: %v\n", err)
			return
		}
		_ = config.SaveNodes(res.Nodes)
		fmt.Printf("成功导入 %d 个节点！\n", len(res.Nodes))
	case "update-core":
		fmt.Println("正在检查并更新 sing-box 内核...")
		ver, err := updater.UpdateSingBoxCore()
		if err != nil {
			fmt.Printf("更新失败: %v\n", err)
			return
		}
		fmt.Printf("更新成功，当前内核版本: %s\n", ver)
	case "status":
		cfg, _ := config.LoadConfig()
		nodes, _ := config.LoadNodes()
		b, _ := json.MarshalIndent(map[string]interface{}{
			"active_node_id": cfg.ActiveNodeId,
			"node_count":     len(nodes),
			"mode":           cfg.Mode,
		}, "", "  ")
		fmt.Println(string(b))
	case "version", "-version", "--version":
		fmt.Printf("%s (%s)\n", Version, Commit)
	default:
		fmt.Printf("未知指令: %s\n", cmd)
	}
}

func executeSilentSubUpdate(silent bool) {
	if !silent {
		log.Println("[openaero-core] 开始执行订阅静默自动更新与健康校验...")
	}
	cfg, err := config.LoadConfig()
	if err != nil || len(cfg.Subscriptions) == 0 {
		if !silent {
			log.Println("[openaero-core] 错误: 未配置有效订阅")
		}
		os.Exit(1)
	}

	sub := cfg.Subscriptions[0]
	if strings.TrimSpace(sub.Url) == "" {
		if !silent {
			log.Println("[openaero-core] 错误: 订阅链接为空")
		}
		os.Exit(1)
	}

	fetchResult, err := subscription.FetchSubscriptionFull(sub.Url, "clash-verge/v2.4.5")
	if err != nil || len(fetchResult.Nodes) == 0 {
		if !silent {
			log.Printf("[openaero-core] 错误: 获取订阅失败: %v", err)
		}
		os.Exit(1)
	}

	oldNodes, _ := config.LoadNodes()

	subscription.ApplySubscriptionResult(cfg, fetchResult, cfg.Subscriptions[0].Url)
	_ = config.SaveConfig(cfg)

	if err := api.ReloadSingBox(cfg); err != nil {
		if !silent {
			log.Printf("[openaero-core] ⚠️ 新配置未通过金丝雀探针校验 (%v)，正在执行安全回滚！", err)
		}
		_ = config.SaveNodes(oldNodes)
		_ = api.ReloadSingBox(cfg)
		os.Exit(1)
	}

	if !silent {
		log.Printf("[openaero-core] ✅ 订阅自动更新成功！已加载 %d 个节点并通过金丝雀探针验证", len(fetchResult.Nodes))
	}
	os.Exit(0)
}

var healthCheckMutex sync.Mutex

func runNodeHealthCheck(cfg *config.AppConfig) {
	if !healthCheckMutex.TryLock() {
		return // Skip if another health check is in progress
	}
	defer healthCheckMutex.Unlock()

	nl, err := config.LoadNodes()
	if err != nil || len(nl) == 0 {
		return
	}

	tested := nodes.TestNodesConcurrently(nl, 10, 3500*time.Millisecond)

	orttSecret := []byte(strings.TrimSpace(cfg.OrttSecret))
	if len(orttSecret) == 0 && prewarm.GlobalRelay != nil {
		orttSecret = prewarm.GlobalRelay.GetSecret()
	}
	if len(orttSecret) == 0 {
		if envSec := os.Getenv("ORTT_SECRET"); envSec != "" {
			orttSecret = []byte(envSec)
		}
	}
	if len(orttSecret) == 0 {
		orttSecret = []byte("ortt-default-psk-2026")
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	for i := range tested {
		if config.IsInfoNode(tested[i]) {
			continue
		}
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			res := prewarm.ProbeNodeOrttFullLink(&tested[idx], orttSecret, 9090)
			tested[idx].OrttStatus = res.Status
			tested[idx].OrttLabel = res.Label
			tested[idx].OrttRttMs = res.RttMs
			tested[idx].OrttWarmConns = res.WarmConns
			tested[idx].OrttPoolHits = res.PoolHits
			tested[idx].OrttPoolMisses = res.PoolMisses
			tested[idx].OrttHitRate = res.HitRate
			tested[idx].OrttCheckedAt = res.CheckedAt
		}(i)
	}
	wg.Wait()

	_ = config.SaveNodes(tested)

	// Dynamically refresh prewarm relay targets if low-latency nodes changed
	if prewarm.GlobalRelay != nil {
		port := cfg.GetOrttPort()
		targets := config.ResolveActiveOrttTargets(cfg, tested)
		if len(targets) > 0 {
			prewarm.GlobalRelay.UpdateTargets(targets, port)
		}
	}
}

func startPeriodicNodeHealthCheck(cfg *config.AppConfig) {
	go func() {
		// 1. Initial check 3s after startup
		time.Sleep(3 * time.Second)
		runNodeHealthCheck(cfg)
		log.Println("[openaero-core] ⚡ 启动自动测速与 ORTT 全链路检测完成")

		// 2. Cold-start compensation 12s after startup (check for unverified/failed nodes)
		time.Sleep(9 * time.Second) // 3s + 9s = 12s
		nl, _ := config.LoadNodes()
		needsComp := false
		for _, n := range nl {
			if !config.IsInfoNode(n) && (n.Latency <= 0 || n.OrttStatus == "" || n.OrttStatus == "untested" || n.OrttStatus == "broken" || n.OrttStatus == "degraded") {
				needsComp = true
				break
			}
		}
		if needsComp {
			log.Println("[openaero-core] 🔍 启动冷启动补偿巡检，补测未通过/未检测节点...")
			runNodeHealthCheck(cfg)
		}

		// 3. Periodic silent check every 5 minutes
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			runNodeHealthCheck(cfg)
			log.Println("[openaero-core] 🔄 5分钟周期静默巡检完成，节点延迟与热池已刷新")
		}
	}()
}
