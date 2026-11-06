package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const (
	ConfigFilePath        = "/etc/openaero/config.json"
	ConfigGoodFilePath    = "/etc/openaero/config.json.good"
	NodesFilePath         = "/etc/openaero/nodes.json"
	RunConfigFilePath     = "/etc/openaero/run_config.json"
	RunConfigGoodFilePath = "/etc/openaero/run_config.json.good"
	RulesDir              = "/etc/openaero/rules"
)

// DomesticDirectSuffixes holds built-in domestic CN TLDs, portals, and CDNs (excluding blocked domains like steamcommunity.com) (27-Audit Defect 2)
var DomesticDirectSuffixes = []string{
	"cn", "com.cn", "net.cn", "org.cn", "gov.cn", "edu.cn",
	"apple.com", "icloud.com", "aaplimg.com", "mzstatic.com",
	"microsoft.com", "windowsupdate.com", "live.com", "msftconnecttest.com", "msftncsi.com",
	"steamcontent.com", "steampowered.com", "steamserver.net",
	"qq.com", "tencent.com", "qpic.cn", "gtimg.cn", "gtimg.com", "weiyun.com", "myqcloud.com",
	"bilibili.com", "bilivideo.com", "hdslb.com", "biliapi.net", "biliapi.com", "b23.tv",
	"alidns.com", "dns.alidns.com", "aliyun.com", "aliyuncs.com", "alicdn.com", "taobao.com", "tmall.com", "tbcache.com",
	"baidu.com", "bdstatic.com", "bdimg.com", "baidupcs.com",
	"weibo.com", "weibocdn.com", "sina.com", "sinaimg.cn", "effirst.com",
	"douyin.com", "snssdk.com", "byteimg.com", "pstatp.com", "bytedance.com", "ibytedtos.com", "toutiao.com",
	"xhscdn.com", "xiaohongshu.com", "kuaishou.com", "yximgs.com",
	"jd.com", "360buyimg.com", "163.com", "126.net", "126.com", "netease.com",
	"zhihu.com", "zhimg.com", "meituan.com", "dpfile.com", "dianping.com",
	"mi.com", "xiaomi.com", "miui.com", "huawei.com", "vmall.com", "hicloud.com",
	"zstaticcdn.com", "ksyuncdn.com", "ourdvsss.com", "cdn20.com", "chinanetcenter.com", "zzao.de",
}

// IsInfoNode detects non-proxy informational subscription entries (traffic quota, expiry, website notice) (27-Audit Defect 6)
func IsInfoNode(n Node) bool {
	srv := strings.TrimSpace(strings.ToLower(n.Server))
	if srv == "" || srv == "127.0.0.1" || srv == "0.0.0.0" || srv == "localhost" || n.Port <= 0 {
		return true
	}
	rem := strings.ToLower(n.Remark)
	infoKeywords := []string{
		"剩余流量", "套餐到期", "距离下次重置", "到期时间", "流量重置",
		"官网", "网址", "导航", "群组", "频道", "客服", "购买", "续费", "过期", "失联", "备用网址",
	}
	for _, kw := range infoKeywords {
		if strings.Contains(rem, kw) {
			return true
		}
	}
	return false
}

// AtomicWriteFile writes data to a temporary file, fsyncs to disk, and atomically renames over path (27-Audit Defect 8)
func AtomicWriteFile(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmpPath := path + ".tmp"
	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	_ = f.Sync()
	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, path)
}

// ReadLanHosts reads /tmp/dhcp.leases, /var/dhcp.leases, /etc/dhcp.leases and /proc/net/arp.
// Returns (ipToMac, ipToName) mappings (SSOT).
func ReadLanHosts() (map[string]string, map[string]string) {
	ipToMac := make(map[string]string)
	ipToName := make(map[string]string)

	for _, leaseFile := range []string{"/tmp/dhcp.leases", "/var/dhcp.leases", "/etc/dhcp.leases"} {
		data, err := os.ReadFile(leaseFile)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 3 {
				mac := strings.ToLower(fields[1])
				ip := fields[2]
				if ip != "" && mac != "" {
					ipToMac[ip] = mac
				}
				if len(fields) >= 4 {
					name := fields[3]
					if name != "" && name != "*" {
						ipToName[ip] = name
					}
				}
			}
		}
	}

	if arpData, err := os.ReadFile("/proc/net/arp"); err == nil {
		for _, line := range strings.Split(string(arpData), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 4 {
				ip := fields[0]
				mac := strings.ToLower(fields[3])
				if ip != "IP" && mac != "00:00:00:00:00:00" && mac != "" {
					if _, exists := ipToMac[ip]; !exists {
						ipToMac[ip] = mac
					}
				}
			}
		}
	}

	return ipToMac, ipToName
}

// ResolveMacToIPs resolves a MAC address to its currently bound IPv4 addresses from DHCP leases and ARP table.
func ResolveMacToIPs(mac string) []string {
	targetMac := strings.ToLower(strings.TrimSpace(mac))
	if targetMac == "" {
		return nil
	}
	ipToMac, _ := ReadLanHosts()
	ips := make([]string, 0)
	for ip, m := range ipToMac {
		if m == targetMac {
			ips = append(ips, ip)
		}
	}
	sort.Strings(ips)
	return ips
}

var lastSingBoxPid int32

// checkSingBoxComm returns true if /proc/<pid>/comm is "sing-box"
func checkSingBoxComm(pid int) bool {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(data)) == "sing-box"
}

// GetSingBoxPid returns the PID of the sing-box process, or 0 if not running.
// Uses a zero-fork Pure-Go /proc scanner with atomic last-known PID caching (< 0.01ms hit time).
func GetSingBoxPid() int {
	oldPid := int(atomic.LoadInt32(&lastSingBoxPid))
	if oldPid > 0 && checkSingBoxComm(oldPid) {
		return oldPid
	}

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if len(name) == 0 || name[0] < '0' || name[0] > '9' {
			continue
		}
		pid, err := strconv.Atoi(name)
		if err != nil {
			continue
		}
		if checkSingBoxComm(pid) {
			atomic.StoreInt32(&lastSingBoxPid, int32(pid))
			return pid
		}
	}
	atomic.StoreInt32(&lastSingBoxPid, 0)
	return 0
}

// HotSwitchClashSelector switches a selector group to targetTag via Clash REST API.
func HotSwitchClashSelector(group string, targetTag string, clashPort int) bool {
	if clashPort <= 0 {
		clashPort = 9090
	}
	apiURL := fmt.Sprintf("http://127.0.0.1:%d/proxies/%s", clashPort, url.PathEscape(group))
	reqBody, _ := json.Marshal(map[string]string{"name": targetTag})
	clashReq, err := http.NewRequest(http.MethodPut, apiURL, bytes.NewBuffer(reqBody))
	if err != nil {
		return false
	}
	clashReq.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 800 * time.Millisecond}
	resp, err := client.Do(clashReq)
	if err == nil && (resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent) {
		_ = resp.Body.Close()
		return true
	}
	if resp != nil {
		_ = resp.Body.Close()
	}
	return false
}

// LoadConfig loads the app config or initializes default (with automatic power-loss recovery from .good checkpoint) (27-Audit Defect 8)
func LoadConfig() (*AppConfig, error) {
	var cfg AppConfig
	data, err := os.ReadFile(ConfigFilePath)
	if err != nil || len(data) == 0 || json.Unmarshal(data, &cfg) != nil {
		// Try automatic recovery from ConfigGoodFilePath
		if goodData, gErr := os.ReadFile(ConfigGoodFilePath); gErr == nil && len(goodData) > 0 {
			if json.Unmarshal(goodData, &cfg) == nil {
				_ = AtomicWriteFile(ConfigFilePath, goodData, 0644)
				goto loadedOK
			}
		}
		defCfg := DefaultConfig()
		_ = SaveConfig(defCfg)
		return defCfg, nil
	}
loadedOK:

	// Filter out deprecated 'proxy' group and ensure clean strategy groups
	cleanGroups := make([]StrategyGroup, 0)
	hasMatch := false
	for _, grp := range cfg.StrategyGroups {
		if grp.Id == "proxy" {
			continue
		}
		if grp.Id == "match" {
			hasMatch = true
		}
		cleanGroups = append(cleanGroups, grp)
	}
	if !hasMatch {
		cleanGroups = append(cleanGroups, StrategyGroup{
			Id:           "match",
			Name:         "漏网之鱼 (MATCH)",
			Icon:         "🌐",
			TargetNodeId: cfg.ActiveNodeId,
			RuleSetTags:  []string{},
			Enabled:      true,
		})
	}
	cfg.StrategyGroups = cleanGroups

	// Ensure built-in groups have default rules populated if empty
	changed := false
	if cfg.AccessControl.BypassIPs == nil {
		cfg.AccessControl.BypassIPs = []string{}
		changed = true
	}
	if cfg.AccessControl.BypassMACs == nil {
		cfg.AccessControl.BypassMACs = []string{}
		changed = true
	}
	if cfg.AccessControl.ProxyOnlyIPs == nil {
		cfg.AccessControl.ProxyOnlyIPs = []string{}
		changed = true
	}
	if cfg.AccessControl.Mode == "" || (cfg.AccessControl.Mode == "whitelist" && len(cfg.AccessControl.ProxyOnlyIPs) == 0) {
		cfg.AccessControl.Mode = "blacklist"
		changed = true
	}
	if cfg.DnsMode == "" {
		cfg.DnsMode = "fake-ip"
		changed = true
	}
	if cfg.FakeIpRange == "" {
		cfg.FakeIpRange = "198.18.0.0/15"
		changed = true
	}
	for i := range cfg.StrategyGroups {
		grp := &cfg.StrategyGroups[i]
		if len(grp.Rules) == 0 {
			if defs, ok := DefaultGroupDomains[grp.Id]; ok {
				grp.Rules = defs
				changed = true
			}
		}
	}
	if changed {
		_ = SaveConfig(&cfg)
	}

	return &cfg, nil
}

// SaveConfig saves the app config to disk atomically (27-Audit Defect 8)
func SaveConfig(cfg *AppConfig) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return AtomicWriteFile(ConfigFilePath, data, 0644)
}

// BackupGoodConfig saves current working configs as safe checkpoints
func BackupGoodConfig() {
	if data, err := os.ReadFile(ConfigFilePath); err == nil && len(data) > 0 {
		_ = AtomicWriteFile(ConfigGoodFilePath, data, 0644)
	}
	if data, err := os.ReadFile(RunConfigFilePath); err == nil && len(data) > 0 {
		_ = AtomicWriteFile(RunConfigGoodFilePath, data, 0644)
	}
}

// RollbackGoodConfig restores the last known good configuration
func RollbackGoodConfig() error {
	var err error
	if _, statErr := os.Stat(ConfigGoodFilePath); statErr == nil {
		if data, readErr := os.ReadFile(ConfigGoodFilePath); readErr == nil && len(data) > 0 {
			err = AtomicWriteFile(ConfigFilePath, data, 0644)
		}
	}
	if _, statErr := os.Stat(RunConfigGoodFilePath); statErr == nil {
		if data, readErr := os.ReadFile(RunConfigGoodFilePath); readErr == nil && len(data) > 0 {
			_ = AtomicWriteFile(RunConfigFilePath, data, 0644)
		}
	}
	return err
}

// HasGoodCheckpoint checks if a valid rollback checkpoint exists
func HasGoodCheckpoint() bool {
	s1, err1 := os.Stat(ConfigGoodFilePath)
	s2, err2 := os.Stat(RunConfigGoodFilePath)
	return err1 == nil && err2 == nil && s1.Size() > 0 && s2.Size() > 0
}

// LoadNodes loads all parsed nodes
func LoadNodes() ([]Node, error) {
	if _, err := os.Stat(NodesFilePath); os.IsNotExist(err) {
		return []Node{}, nil
	}
	data, err := os.ReadFile(NodesFilePath)
	if err != nil {
		return nil, err
	}
	var nodes []Node
	if err := json.Unmarshal(data, &nodes); err != nil {
		return nil, err
	}
	return nodes, nil
}

// SaveNodes saves nodes to disk atomically (27-Audit Defect 8)
func SaveNodes(nodes []Node) error {
	data, err := json.MarshalIndent(nodes, "", "  ")
	if err != nil {
		return err
	}
	return AtomicWriteFile(NodesFilePath, data, 0644)
}

// FindNode finds node by ID or remark
func FindNode(nodes []Node, idOrRemark string) *Node {
	for i := range nodes {
		if nodes[i].Id == idOrRemark || nodes[i].Remark == idOrRemark {
			return &nodes[i]
		}
	}
	return nil
}

// ResolveActiveOrttTargets discovers all VPS server IPs/domains that should receive ORTT prewarm packets (Multi-Target Relay)
func ResolveActiveOrttTargets(cfg *AppConfig, nodes []Node) []string {
	if cfg == nil || !cfg.Enabled || cfg.GetOrttMode() == "disabled" {
		return nil
	}
	if cfg.GetOrttMode() == "custom" && cfg.GetOrttVPS() != "" {
		return []string{cfg.GetOrttVPS()}
	}

	// Mode is "follow_node" (default)
	var targets []string
	seen := make(map[string]bool)

	addTarget := func(host string) {
		host = strings.TrimSpace(host)
		if host != "" && !seen[host] {
			seen[host] = true
			targets = append(targets, host)
		}
	}

	// Helper to find best healthy non-info node (or first non-info)
	findBestAutoNode := func() *Node {
		var bestNode *Node
		for i := range nodes {
			n := &nodes[i]
			if !IsInfoNode(*n) && n.Latency > 0 {
				if bestNode == nil || n.Latency < bestNode.Latency {
					bestNode = n
				}
			}
		}
		if bestNode == nil {
			for i := range nodes {
				if !IsInfoNode(nodes[i]) {
					bestNode = &nodes[i]
					break
				}
			}
		}
		return bestNode
	}

	// 1. Identify primary active node target
	if cfg.ActiveNodeId == "auto_select" {
		if bn := findBestAutoNode(); bn != nil {
			addTarget(bn.Server)
		}
	} else if cfg.ActiveNodeId != "" && cfg.ActiveNodeId != "DIRECT" && cfg.ActiveNodeId != "direct" {
		if n := FindNode(nodes, cfg.ActiveNodeId); n != nil {
			addTarget(n.Server)
		}
	}

	// 2. Traverse Strategy Groups for explicitly bound nodes
	for _, grp := range cfg.StrategyGroups {
		if !grp.Enabled || grp.TargetNodeId == "" {
			continue
		}
		target := grp.TargetNodeId
		if target == "GLOBAL" || target == "DIRECT" || target == "direct" || target == "REJECT" || target == "block" {
			continue
		}
		if target == "auto_select" {
			if bn := findBestAutoNode(); bn != nil {
				addTarget(bn.Server)
			}
		} else {
			if n := FindNode(nodes, target); n != nil {
				addTarget(n.Server)
			}
		}
	}

	// 3. Up to 10 active proxy nodes as prewarm standby targets (TASK_13: full 6-node coverage)
	type candNode struct {
		server  string
		latency int
	}
	var candNodes []candNode
	for i := range nodes {
		n := &nodes[i]
		if !IsInfoNode(*n) && strings.TrimSpace(n.Server) != "" {
			candNodes = append(candNodes, candNode{
				server:  strings.TrimSpace(n.Server),
				latency: n.Latency,
			})
		}
	}
	sort.Slice(candNodes, func(i, j int) bool {
		// Prefer nodes with valid positive latency, sorted lowest first
		if candNodes[i].latency > 0 && candNodes[j].latency > 0 {
			return candNodes[i].latency < candNodes[j].latency
		}
		if candNodes[i].latency > 0 {
			return true
		}
		if candNodes[j].latency > 0 {
			return false
		}
		return candNodes[i].server < candNodes[j].server
	})
	limit := 10
	if len(candNodes) < limit {
		limit = len(candNodes)
	}
	for i := 0; i < limit; i++ {
		addTarget(candNodes[i].server)
	}

	return targets
}

// CompileSingBoxConfig compiles AppConfig and Nodes into sing-box 1.13+ run_config.json
func CompileSingBoxConfig(cfg *AppConfig, nodes []Node) (string, error) {
	// 1. Identify active fallback Match node ("漏网之鱼") (27-Audit Defect 3: preserve auto_select and DIRECT)
	isAutoSelect := (cfg.ActiveNodeId == "auto_select")
	isMatchDirect := (cfg.ActiveNodeId == "DIRECT" || cfg.ActiveNodeId == "direct")
	var matchNode *Node
	for _, grp := range cfg.StrategyGroups {
		if grp.Id == "match" && grp.TargetNodeId != "" && grp.TargetNodeId != "REJECT" {
			if grp.TargetNodeId == "auto_select" {
				isAutoSelect = true
				isMatchDirect = false
				cfg.ActiveNodeId = "auto_select"
				break
			}
			if grp.TargetNodeId == "DIRECT" || grp.TargetNodeId == "direct" {
				isMatchDirect = true
				isAutoSelect = false
				break
			}
			matchNode = FindNode(nodes, grp.TargetNodeId)
			if matchNode != nil {
				isAutoSelect = false
				isMatchDirect = false
				cfg.ActiveNodeId = matchNode.Id
				break
			}
		}
	}
	if !isAutoSelect && !isMatchDirect && matchNode == nil {
		matchNode = FindNode(nodes, cfg.ActiveNodeId)
	}
	if !isAutoSelect && !isMatchDirect && matchNode == nil && len(nodes) > 0 {
		for i := range nodes {
			if !IsInfoNode(nodes[i]) {
				matchNode = &nodes[i]
				break
			}
		}
		if matchNode == nil {
			matchNode = &nodes[0]
		}
		cfg.ActiveNodeId = matchNode.Id
	}

	// 2. Build Outbounds
	outbounds := make([]map[string]interface{}, 0)
	outboundTags := make(map[string]bool)

	// Helper to add node outbound
	addNodeOutbound := func(node *Node, customTag string) string {
		tag := customTag
		if tag == "" {
			tag = node.Id
		}
		if outboundTags[tag] {
			return tag
		}

		ob := map[string]interface{}{
			"type":        node.Type,
			"tag":         tag,
			"server":      node.Server,
			"server_port": node.Port,
		}

		if node.Type != "anytls" && node.Type != "hysteria2" {
			ob["tcp_fast_open"] = true
		}
		if node.Type != "hysteria2" {
			ob["udp_fragment"] = true
		}

		switch node.Type {
		case "vless":
			ob["uuid"] = node.Uuid
			if node.Flow != "" {
				ob["flow"] = node.Flow
			} else {
				ob["flow"] = "xtls-rprx-vision"
			}
			tls := map[string]interface{}{
				"enabled":     true,
				"server_name": node.Sni,
				"utls": map[string]interface{}{
					"enabled":     true,
					"fingerprint": "chrome",
				},
			}
			if node.PublicKey != "" {
				tls["reality"] = map[string]interface{}{
					"enabled":    true,
					"public_key": node.PublicKey,
					"short_id":   node.ShortId,
				}
			}
			ob["tls"] = tls

		case "hysteria2":
			ob["password"] = node.Password
			ob["tls"] = map[string]interface{}{
				"enabled":     true,
				"server_name": node.Sni,
				"insecure":    node.Insecure,
			}

		case "anytls", "trojan":
			ob["password"] = node.Password
			tls := map[string]interface{}{
				"enabled": true,
			}
			if node.Sni != "" {
				tls["server_name"] = node.Sni
			}
			if node.Insecure {
				tls["insecure"] = true
			}
			ob["tls"] = tls

		case "vmess":
			ob["uuid"] = node.Uuid
			ob["alter_id"] = 0
			ob["security"] = "auto"
			if node.Sni != "" {
				ob["tls"] = map[string]interface{}{
					"enabled":     true,
					"server_name": node.Sni,
				}
			}

		case "shadowsocks":
			ob["method"] = node.Method
			ob["password"] = node.Password
		}

		outbounds = append(outbounds, ob)
		outboundTags[tag] = true
		return tag
	}

	// Register ALL parsed subscription nodes into outbounds so Clash API can probe/switch any node
	allNodeTags := make([]string, 0)
	validAutoSelectTags := make([]string, 0)
	for i := range nodes {
		n := &nodes[i]
		tag := addNodeOutbound(n, n.Id)
		allNodeTags = append(allNodeTags, tag)
		if !IsInfoNode(*n) {
			validAutoSelectTags = append(validAutoSelectTags, tag)
		}
	}
	if len(validAutoSelectTags) == 0 {
		validAutoSelectTags = allNodeTags
	}

	// Always ensure fallback match outbound is registered
	matchOutboundTag := "direct"
	if matchNode != nil {
		matchOutboundTag = addNodeOutbound(matchNode, matchNode.Id)
	}

	// Direct & Block outbounds
	outbounds = append(outbounds, map[string]interface{}{
		"type": "direct",
		"tag":  "direct",
	})
	outbounds = append(outbounds, map[string]interface{}{
		"type": "block",
		"tag":  "block",
	})

	// URL-Test (auto_select) outbound (Defect 1 & 27-Audit Defect 6: exclude info nodes)
	var autoSelectGroup *StrategyGroup
	for i := range cfg.StrategyGroups {
		if cfg.StrategyGroups[i].Type == "urltest" || cfg.StrategyGroups[i].Id == "auto_select" {
			autoSelectGroup = &cfg.StrategyGroups[i]
			break
		}
	}

	hasAutoSelect := len(validAutoSelectTags) > 0
	if hasAutoSelect {
		testURL := "https://cp.cloudflare.com/generate_204"
		interval := 300
		tolerance := 50
		if autoSelectGroup != nil {
			if autoSelectGroup.URL != "" {
				testURL = autoSelectGroup.URL
			}
			if autoSelectGroup.Interval > 0 {
				interval = autoSelectGroup.Interval
			}
			if autoSelectGroup.Tolerance > 0 {
				tolerance = autoSelectGroup.Tolerance
			}
		}
		outbounds = append(outbounds, map[string]interface{}{
			"type":      "urltest",
			"tag":       "auto_select",
			"outbounds": validAutoSelectTags,
			"url":       testURL,
			"interval":  fmt.Sprintf("%ds", interval),
			"tolerance": tolerance,
		})
	}

	// Selector: GLOBAL (27-Audit Defect 3)
	globalOutboundList := make([]string, 0)
	if hasAutoSelect {
		globalOutboundList = append(globalOutboundList, "auto_select")
	}
	globalOutboundList = append(globalOutboundList, allNodeTags...)
	globalOutboundList = append(globalOutboundList, "direct")

	defaultGlobal := matchOutboundTag
	if isAutoSelect || cfg.ActiveNodeId == "auto_select" {
		if hasAutoSelect {
			defaultGlobal = "auto_select"
		}
	} else if isMatchDirect {
		defaultGlobal = "direct"
	} else if defaultGlobal == "" || defaultGlobal == "direct" {
		if len(validAutoSelectTags) > 0 {
			defaultGlobal = validAutoSelectTags[0]
		} else if len(allNodeTags) > 0 {
			defaultGlobal = allNodeTags[0]
		} else {
			defaultGlobal = "direct"
		}
	}
	outbounds = append(outbounds, map[string]interface{}{
		"type":      "selector",
		"tag":       "GLOBAL",
		"outbounds": globalOutboundList,
		"default":   defaultGlobal,
	})

	// Map strategy groups to outbounds with selector support and GLOBAL follow nesting (Defect 2)
	groupOutbounds := make(map[string]string) // group.Id -> outbound tag
	for _, grp := range cfg.StrategyGroups {
		if grp.Id == "proxy" || grp.Id == "auto_select" {
			continue // skip legacy proxy group and urltest group
		}
		if grp.Id == "match" {
			groupOutbounds[grp.Id] = "GLOBAL"
			continue
		}

		grpOutboundList := []string{"GLOBAL"}
		if hasAutoSelect {
			grpOutboundList = append(grpOutboundList, "auto_select")
		}
		grpOutboundList = append(grpOutboundList, "direct", "block")
		grpOutboundList = append(grpOutboundList, allNodeTags...)

		defaultTarget := "GLOBAL"
		if grp.TargetNodeId == "DIRECT" {
			defaultTarget = "direct"
		} else if grp.TargetNodeId == "REJECT" || grp.TargetNodeId == "BLOCK" {
			defaultTarget = "block"
		} else if grp.TargetNodeId == "auto_select" {
			defaultTarget = "auto_select"
		} else if grp.TargetNodeId != "" && grp.TargetNodeId != "GLOBAL" {
			target := FindNode(nodes, grp.TargetNodeId)
			if target != nil {
				defaultTarget = target.Id
			}
		}

		outbounds = append(outbounds, map[string]interface{}{
			"type":      "selector",
			"tag":       grp.Id,
			"outbounds": grpOutboundList,
			"default":   defaultTarget,
		})
		groupOutbounds[grp.Id] = grp.Id
	}

	// 3. Collect all Server IPs, Subscription Domains, and Node Domains to prevent ANY routing loops or DNS bootstrap deadlocks
	serverIps := make([]string, 0)
	nodeDomains := make([]string, 0)
	seenDomain := make(map[string]bool)
	for _, n := range nodes {
		srv := strings.TrimSpace(n.Server)
		if srv == "" {
			continue
		}
		if net.ParseIP(srv) != nil {
			if !strings.Contains(srv, ":") {
				serverIps = append(serverIps, srv+"/32")
			}
		} else {
			if !seenDomain[srv] {
				seenDomain[srv] = true
				nodeDomains = append(nodeDomains, srv)
			}
		}
	}
	// Extract subscription hostnames to ensure subscription refresh never deadlocks or routes to Fake-IP
	for _, sub := range cfg.Subscriptions {
		u := strings.TrimSpace(sub.Url)
		if u == "" {
			continue
		}
		if parsed, err := url.Parse(u); err == nil && parsed.Hostname() != "" {
			host := strings.ToLower(parsed.Hostname())
			if net.ParseIP(host) == nil {
				if !seenDomain[host] {
					seenDomain[host] = true
					nodeDomains = append(nodeDomains, host)
				}
			} else {
				if !strings.Contains(host, ":") {
					serverIps = append(serverIps, host+"/32")
				}
			}
		}
	}
	// Extract domain suffixes from node/subscription domains (e.g. 2000319.xyz)
	nodeDomainSuffixes := make([]string, 0)
	seenSuffix := make(map[string]bool)
	for _, d := range nodeDomains {
		parts := strings.Split(d, ".")
		if len(parts) >= 2 {
			suffix := strings.Join(parts[len(parts)-2:], ".")
			if !seenSuffix[suffix] && len(suffix) > 3 {
				seenSuffix[suffix] = true
				nodeDomainSuffixes = append(nodeDomainSuffixes, suffix)
			}
		}
	}

	uniqueIps := make([]string, 0)
	seenIp := make(map[string]bool)
	for _, ip := range serverIps {
		if !seenIp[ip] {
			seenIp[ip] = true
			uniqueIps = append(uniqueIps, ip)
		}
	}

	// 4. Inbounds (with IPv6 switch support)
	socksPort := cfg.SocksPort
	if socksPort <= 0 {
		socksPort = 10808
	}
	httpPort := cfg.HttpPort
	if httpPort <= 0 {
		httpPort = 10809
	}
	dnsPort := cfg.DnsPort
	if dnsPort <= 0 {
		dnsPort = 1053
	}

	ipv6Mode := strings.ToLower(strings.TrimSpace(cfg.Ipv6Mode))
	if ipv6Mode == "" {
		if cfg.Ipv6Enabled {
			ipv6Mode = "proxy"
		} else {
			ipv6Mode = "drop"
		}
	}

	var tunAddress []string
	if ipv6Mode == "proxy" {
		tunAddress = []string{"172.19.0.1/30", "fdfe:dcba:9876::1/126"}
	} else {
		tunAddress = []string{"172.19.0.1/30"}
	}

	inbounds := []map[string]interface{}{
		{
			"type":                     "tun",
			"tag":                      "tun-in",
			"interface_name":           "openaero0",
			"mtu":                      1400,
			"auto_route":               true,
			"strict_route":             false,
			"stack":                    "mixed",
			"address":                  tunAddress,
			"endpoint_independent_nat": true,
			"route_exclude_address": []string{
				"192.168.0.0/16",
				"10.0.0.0/8",
				"172.16.0.0/12",
				"100.64.0.0/10",
				"223.5.5.5/32",
				"119.29.29.29/32",
			},
		},
		{
			"type":        "mixed",
			"tag":         "socks-in",
			"listen":      "0.0.0.0",
			"listen_port": socksPort,
		},
		{
			"type":        "mixed",
			"tag":         "http-in",
			"listen":      "0.0.0.0",
			"listen_port": httpPort,
		},
		{
			"type":        "direct",
			"tag":         "dns-in",
			"listen":      "::",
			"listen_port": dnsPort,
		},
	}

	// 5. Rule sets list (dynamically loaded from local binary .srs files in RulesDir)
	existingRuleSets := make([]map[string]interface{}, 0)
	availableRuleSetTags := make(map[string]bool)

	if entries, err := os.ReadDir(RulesDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".srs") {
				continue
			}
			tag := strings.TrimSuffix(entry.Name(), ".srs")
			p := filepath.Join(RulesDir, entry.Name())
			if fi, err := entry.Info(); err == nil && fi.Size() > 0 {
				existingRuleSets = append(existingRuleSets, map[string]interface{}{
					"tag":    tag,
					"type":   "local",
					"format": "binary",
					"path":   p,
				})
				availableRuleSetTags[tag] = true
			}
		}
	}
	sort.Slice(existingRuleSets, func(i, j int) bool {
		return existingRuleSets[i]["tag"].(string) < existingRuleSets[j]["tag"].(string)
	})

	// Collect all direct tags from built-in domestic sets and user subscription DirectRuleSetTags
	directGeositeTags := make([]string, 0)
	directGeoipTags := make([]string, 0)
	seenDirectTag := make(map[string]bool)

	allCandidateDirect := []string{
		"geosite-category-games@cn",
		"geosite-steam@cn",
		"geosite-tencent",
		"geosite-microsoft@cn",
		"geosite-apple@cn",
		"geosite-cn",
	}
	for _, tag := range cfg.DirectRuleSetTags {
		allCandidateDirect = append(allCandidateDirect, tag)
	}
	for _, tag := range allCandidateDirect {
		tag = strings.TrimSpace(tag)
		if tag == "" || seenDirectTag[tag] {
			continue
		}
		if availableRuleSetTags[tag] {
			seenDirectTag[tag] = true
			if strings.HasPrefix(tag, "geoip-") {
				directGeoipTags = append(directGeoipTags, tag)
			} else {
			directGeositeTags = append(directGeositeTags, tag)
			}
		}
	}
	if availableRuleSetTags["geoip-cn"] && !seenDirectTag["geoip-cn"] {
		seenDirectTag["geoip-cn"] = true
		directGeoipTags = append(directGeoipTags, "geoip-cn")
	}

	dnsStrategy := "ipv4_only"
	if ipv6Mode == "proxy" {
		dnsStrategy = "prefer_ipv4"
	}

	dnsMode := strings.ToLower(strings.TrimSpace(cfg.DnsMode))
	if dnsMode == "" {
		dnsMode = "fake-ip"
	}
	isFakeIP := (dnsMode == "fake-ip")

	noDataTypes := []string{"HTTPS", "SVCB"}
	fakeipQueryTypes := []string{"A", "AAAA"}
	if !cfg.Ipv6Enabled || ipv6Mode != "proxy" {
		noDataTypes = []string{"HTTPS", "SVCB", "AAAA"}
		fakeipQueryTypes = []string{"A"}
	}

	makeProxyDnsRule := func(domains, suffixes, keywords, ruleSets []string) map[string]interface{} {
		var dm map[string]interface{}
		if isFakeIP {
			dm = map[string]interface{}{
				"query_type":  fakeipQueryTypes,
				"server":      "fakeip-dns",
				"rewrite_ttl": 1,
			}
		} else {
			dm = map[string]interface{}{
				"server": "remote-dns",
			}
		}
		if len(domains) > 0 {
			dm["domain"] = domains
		}
		if len(suffixes) > 0 {
			dm["domain_suffix"] = suffixes
		}
		if len(keywords) > 0 {
			dm["domain_keyword"] = keywords
		}
		if len(ruleSets) > 0 {
			dm["rule_set"] = ruleSets
		}
		return dm
	}

	// 6 & 7. Build DNS Rules and Route Rules in strict top-to-bottom priority order (27-Audit Defects 1, 2, 4, 5)
	dnsRules := make([]map[string]interface{}, 0)
	routeRules := make([]map[string]interface{}, 0)

	// [Priority 1] Base Protocol Hijack & System Direct Rules
	routeRules = append(routeRules,
		map[string]interface{}{"action": "sniff"},
		map[string]interface{}{"protocol": "dns", "action": "hijack-dns"},
		map[string]interface{}{"inbound": []string{"dns-in"}, "action": "hijack-dns"},
		map[string]interface{}{"ip_is_private": true, "outbound": "direct"},
		map[string]interface{}{"protocol": "icmp", "outbound": "direct"},
	)

	// Local LAN & NTP / Connectivity Probe Direct Rules (Never allocate Fake-IP)
	localLanSuffixes := []string{
		"lan", "local", "arpa", "localdomain", "internal", "localhost", "home.arpa",
	}
	localLanDomains := []string{
		"router.lan", "openwrt.lan", "istoreos.lan", "openaero.lan", "localhost",
	}
	ntpAndProbeSuffixes := []string{
		"ntp.org", "pool.ntp.org",
		"time.windows.com", "time.apple.com", "time.google.com", "time.cloudflare.com", "time.nist.gov", "time.asia.apple.com",
		"msftncsi.com", "msftconnecttest.com", "connectivitycheck.gstatic.com", "detectportal.firefox.com", "captive.apple.com",
	}

	routeRules = append(routeRules,
		map[string]interface{}{"domain_suffix": localLanSuffixes, "outbound": "direct"},
		map[string]interface{}{"domain": localLanDomains, "outbound": "direct"},
		map[string]interface{}{"domain_suffix": ntpAndProbeSuffixes, "outbound": "direct"},
	)
	dnsRules = append(dnsRules,
		map[string]interface{}{"domain_suffix": localLanSuffixes, "server": "local-dns"},
		map[string]interface{}{"domain": localLanDomains, "server": "local-dns"},
		map[string]interface{}{"domain_suffix": ntpAndProbeSuffixes, "server": "direct-dns"},
	)

	// Node Server IPs & Domains Direct Bootstrap (prevent routing loops)
	if len(uniqueIps) > 0 {
		routeRules = append(routeRules, map[string]interface{}{
			"ip_cidr":  uniqueIps,
			"outbound": "direct",
		})
	}
	if len(nodeDomains) > 0 || len(nodeDomainSuffixes) > 0 {
		ruleMap := map[string]interface{}{"outbound": "direct"}
		dnsMap := map[string]interface{}{"server": "direct-dns"}
		if len(nodeDomains) > 0 {
			ruleMap["domain"] = nodeDomains
			dnsMap["domain"] = nodeDomains
		}
		if len(nodeDomainSuffixes) > 0 {
			ruleMap["domain_suffix"] = nodeDomainSuffixes
			dnsMap["domain_suffix"] = nodeDomainSuffixes
		}
		routeRules = append(routeRules, ruleMap)
		dnsRules = append(dnsRules, dnsMap)
	}

	// Predefined NOERROR for HTTPS (Type 65) / SVCB (Type 64) and AAAA (when IPv6 disabled) (25-Audit Defect 1)
	if isFakeIP || !cfg.Ipv6Enabled || ipv6Mode != "proxy" {
		dnsRules = append(dnsRules, map[string]interface{}{
			"query_type": noDataTypes,
			"action":     "predefined",
			"rcode":      "NOERROR",
		})
	}

	// Ad Blocking / Reject Rules
	adSuffixes := []string{
		"stats.g.doubleclick.net",
		"adservice.google.com",
		"pagead2.googlesyndication.com",
		"adguard.com",
		"analytics.google.com",
		"telemetry.microsoft.com",
	}
	routeRules = append(routeRules, map[string]interface{}{
		"domain_suffix": adSuffixes,
		"outbound":      "block",
	})
	if availableRuleSetTags["geosite-category-ads-all"] {
		routeRules = append(routeRules, map[string]interface{}{
			"rule_set": []string{"geosite-category-ads-all"},
			"outbound": "block",
		})
	}
	for _, rp := range cfg.RuleProviders {
		if rp.Target == "REJECT" || rp.Id == "reject" {
			ruleMap := map[string]interface{}{"outbound": "block"}
			hasRule := false
			if len(rp.Domains) > 0 { ruleMap["domain"] = rp.Domains; hasRule = true }
			if len(rp.Suffixes) > 0 { ruleMap["domain_suffix"] = rp.Suffixes; hasRule = true }
			if len(rp.Keywords) > 0 { ruleMap["domain_keyword"] = rp.Keywords; hasRule = true }
			if len(rp.Ips) > 0 { ruleMap["ip_cidr"] = rp.Ips; hasRule = true }
			if hasRule {
				routeRules = append(routeRules, ruleMap)
			}
		}
	}

	// [Priority 2] User Custom Forced Direct (my_whitelist + CustomDirectDomains)
	if len(cfg.CustomDirectDomains) > 0 {
		routeRules = append(routeRules, map[string]interface{}{
			"domain_suffix": cfg.CustomDirectDomains,
			"outbound":      "direct",
		})
		dnsRules = append(dnsRules, map[string]interface{}{
			"domain_suffix": cfg.CustomDirectDomains,
			"server":        "direct-dns",
		})
	}
	for _, rp := range cfg.RuleProviders {
		if rp.Target == "DIRECT" {
			ruleMap := map[string]interface{}{"outbound": "direct"}
			hasRule := false
			if len(rp.Domains) > 0 { ruleMap["domain"] = rp.Domains; hasRule = true }
			if len(rp.Suffixes) > 0 { ruleMap["domain_suffix"] = rp.Suffixes; hasRule = true }
			if len(rp.Keywords) > 0 { ruleMap["domain_keyword"] = rp.Keywords; hasRule = true }
			if len(rp.Ips) > 0 {
				filteredIps := make([]string, 0, len(rp.Ips))
				for _, cidr := range rp.Ips {
					// Never allow DIRECT rule-providers (e.g. 'private') to hijack Fake-IP pool 198.18.0.0/15 or fc00::/18
					if strings.HasPrefix(cidr, "198.18.") || strings.HasPrefix(cidr, "198.19.") || strings.HasPrefix(strings.ToLower(cidr), "fc00:") {
						continue
					}
					filteredIps = append(filteredIps, cidr)
				}
				if len(filteredIps) > 0 {
					ruleMap["ip_cidr"] = filteredIps
					hasRule = true
				}
			}
			if hasRule {
				routeRules = append(routeRules, ruleMap)
				if len(rp.Domains) > 0 || len(rp.Suffixes) > 0 || len(rp.Keywords) > 0 {
					dnsMap := map[string]interface{}{"server": "direct-dns"}
					if len(rp.Domains) > 0 { dnsMap["domain"] = rp.Domains }
					if len(rp.Suffixes) > 0 { dnsMap["domain_suffix"] = rp.Suffixes }
					if len(rp.Keywords) > 0 { dnsMap["domain_keyword"] = rp.Keywords }
					dnsRules = append(dnsRules, dnsMap)
				}
			}
		}
	}

	// Explicitly allow Domestic UDP 443 (QUIC/HTTP3 for Douyin/Bilibili/Taobao/CN/Games) to go direct before rejecting overseas QUIC
	routeRules = append(routeRules, map[string]interface{}{
		"network":       []string{"udp"},
		"port":          []int{443},
		"domain_suffix": DomesticDirectSuffixes,
		"outbound":      "direct",
	})
	if len(directGeositeTags) > 0 {
		routeRules = append(routeRules, map[string]interface{}{
			"network":  []string{"udp"},
			"port":     []int{443},
			"rule_set": directGeositeTags,
			"outbound": "direct",
		})
	}
	if len(directGeoipTags) > 0 {
		routeRules = append(routeRules, map[string]interface{}{
			"network":  []string{"udp"},
			"port":     []int{443},
			"rule_set": directGeoipTags,
			"outbound": "direct",
		})
	}
	// Now reject all remaining (overseas) UDP 443 so YouTube/Google/ChatGPT/GLOBAL fall back to TCP 443 + ORTT in <1ms!
	if cfg.Mode != "direct" {
		routeRules = append(routeRules, map[string]interface{}{
			"network": []string{"udp"},
			"port":    []int{443},
			"action":  "reject",
		})
	}

	// [Priority 3] User Custom Forced Proxy (my_proxylist + CustomProxyDomains) -> Strictly BEFORE any built-in .cn / geosite-cn rules! (27-Audit Defect 2 & 4)
	if len(cfg.CustomProxyDomains) > 0 {
		routeRules = append(routeRules, map[string]interface{}{
			"domain_suffix": cfg.CustomProxyDomains,
			"outbound":      "GLOBAL",
		})
		dnsRules = append(dnsRules, makeProxyDnsRule(nil, cfg.CustomProxyDomains, nil, nil))
	}
	for _, rp := range cfg.RuleProviders {
		if rp.Target == "match" || rp.Target == "proxy" {
			ruleMap := map[string]interface{}{"outbound": "GLOBAL"}
			hasRule := false
			if len(rp.Domains) > 0 { ruleMap["domain"] = rp.Domains; hasRule = true }
			if len(rp.Suffixes) > 0 { ruleMap["domain_suffix"] = rp.Suffixes; hasRule = true }
			if len(rp.Keywords) > 0 { ruleMap["domain_keyword"] = rp.Keywords; hasRule = true }
			if len(rp.Ips) > 0 { ruleMap["ip_cidr"] = rp.Ips; hasRule = true }
			if hasRule {
				routeRules = append(routeRules, ruleMap)
				if len(rp.Domains) > 0 || len(rp.Suffixes) > 0 || len(rp.Keywords) > 0 {
					dnsRules = append(dnsRules, makeProxyDnsRule(rp.Domains, rp.Suffixes, rp.Keywords, nil))
				}
			}
		}
	}

	// [Priority 4] Specific Strategy Groups & RuleProviders (including rp.Ips CIDR rules!) (27-Audit Defect 1 & 5)
	for _, rp := range cfg.RuleProviders {
		if rp.Target != "DIRECT" && rp.Target != "REJECT" && rp.Target != "match" && rp.Target != "proxy" && rp.Target != "" {
			outTag := groupOutbounds[rp.Target]
			if outTag == "" {
				if rp.Target == "GLOBAL" || rp.Target == "auto_select" {
					outTag = rp.Target
				} else {
					outTag = "GLOBAL"
				}
			}
			ruleMap := map[string]interface{}{"outbound": outTag}
			hasRule := false
			if len(rp.Domains) > 0 { ruleMap["domain"] = rp.Domains; hasRule = true }
			if len(rp.Suffixes) > 0 { ruleMap["domain_suffix"] = rp.Suffixes; hasRule = true }
			if len(rp.Keywords) > 0 { ruleMap["domain_keyword"] = rp.Keywords; hasRule = true }
			if len(rp.Ips) > 0 { ruleMap["ip_cidr"] = rp.Ips; hasRule = true }
			if hasRule {
				routeRules = append(routeRules, ruleMap)
			}
			if len(rp.Domains) > 0 || len(rp.Suffixes) > 0 || len(rp.Keywords) > 0 {
				isDirectGroup := false
				for _, g := range cfg.StrategyGroups {
					if g.Id == rp.Target && (g.TargetNodeId == "DIRECT" || g.TargetNodeId == "direct") {
						isDirectGroup = true
						break
					}
				}
				if isDirectGroup {
					dnsMap := map[string]interface{}{"server": "direct-dns"}
					if len(rp.Domains) > 0 { dnsMap["domain"] = rp.Domains }
					if len(rp.Suffixes) > 0 { dnsMap["domain_suffix"] = rp.Suffixes }
					if len(rp.Keywords) > 0 { dnsMap["domain_keyword"] = rp.Keywords }
					dnsRules = append(dnsRules, dnsMap)
				} else {
					dnsRules = append(dnsRules, makeProxyDnsRule(rp.Domains, rp.Suffixes, rp.Keywords, nil))
				}
			}
		}
	}

	matchTarget := groupOutbounds["match"]
	if matchTarget == "" {
		matchTarget = "GLOBAL"
	}

	if cfg.Mode == "global" {
		routeRules = append(routeRules, map[string]interface{}{
			"network": []string{"udp"},
			"port":    []int{443},
			"action":  "reject",
		})
		routeRules = append(routeRules, map[string]interface{}{
			"network":  []string{"tcp", "udp"},
			"outbound": "GLOBAL",
		})
	} else if cfg.Mode == "direct" {
		routeRules = append(routeRules, map[string]interface{}{
			"network":  []string{"tcp", "udp"},
			"outbound": "direct",
		})
	} else {
		// Rule Mode (Default)
		boundRuleSets := make(map[string]bool)
		for _, grp := range cfg.StrategyGroups {
			if !grp.Enabled || grp.Id == "proxy" || grp.Id == "match" || grp.Id == "auto_select" {
				continue
			}
			targetOutbound := groupOutbounds[grp.Id]
			if targetOutbound == "" {
				targetOutbound = "GLOBAL"
			}
			isDirectGroup := (grp.TargetNodeId == "DIRECT" || grp.TargetNodeId == "direct")

			// A. Domain suffix rules
			rulesList := grp.Rules
			if len(rulesList) == 0 {
				if defs, ok := DefaultGroupDomains[grp.Id]; ok {
					rulesList = defs
				}
			}
			if len(rulesList) > 0 {
				routeRules = append(routeRules, map[string]interface{}{
					"domain_suffix": rulesList,
					"outbound":      targetOutbound,
				})
				if isDirectGroup {
					dnsRules = append(dnsRules, map[string]interface{}{
						"domain_suffix": rulesList,
						"server":        "direct-dns",
					})
				} else {
					dnsRules = append(dnsRules, makeProxyDnsRule(nil, rulesList, nil, nil))
				}
			}

			// A2. Domain keyword rules from strategy group
			if len(grp.Keywords) > 0 {
				routeRules = append(routeRules, map[string]interface{}{
					"domain_keyword": grp.Keywords,
					"outbound":       targetOutbound,
				})
				if isDirectGroup {
					dnsRules = append(dnsRules, map[string]interface{}{
						"domain_keyword": grp.Keywords,
						"server":         "direct-dns",
					})
				} else {
					dnsRules = append(dnsRules, makeProxyDnsRule(nil, nil, grp.Keywords, nil))
				}
			}

			// B. Binary rule_sets from strategy group (e.g. geosite-google, geosite-youtube, geosite-openai, geosite-telegram)
			validRuleSets := make([]string, 0)
			dnsRuleSets := make([]string, 0)
			for _, rset := range grp.RuleSetTags {
				if availableRuleSetTags[rset] {
					validRuleSets = append(validRuleSets, rset)
					boundRuleSets[rset] = true
					if strings.HasPrefix(rset, "geosite-") {
						dnsRuleSets = append(dnsRuleSets, rset)
					}
				}
			}
			if grp.Id == "telegram" && availableRuleSetTags["geoip-telegram"] && !boundRuleSets["geoip-telegram"] {
				validRuleSets = append(validRuleSets, "geoip-telegram")
				boundRuleSets["geoip-telegram"] = true
			}
			if len(validRuleSets) > 0 {
				routeRules = append(routeRules, map[string]interface{}{
					"rule_set": validRuleSets,
					"outbound": targetOutbound,
				})
			}
			if len(dnsRuleSets) > 0 {
				if isDirectGroup {
					dnsRules = append(dnsRules, map[string]interface{}{
						"rule_set": dnsRuleSets,
						"server":   "direct-dns",
					})
				} else {
					dnsRules = append(dnsRules, makeProxyDnsRule(nil, nil, nil, dnsRuleSets))
				}
			}
		}

		// C. Mount additional overseas .srs rule sets (geosite-github, geosite-twitter, geosite-netflix, etc.) (27-Audit Defect 5)
		extraOverseasRuleSets := make([]string, 0)
		for _, tag := range []string{"geosite-google", "geosite-google-gemini", "geosite-youtube", "geosite-openai", "geosite-telegram", "geosite-github", "geosite-twitter", "geosite-netflix"} {
			if availableRuleSetTags[tag] && !boundRuleSets[tag] {
				extraOverseasRuleSets = append(extraOverseasRuleSets, tag)
				boundRuleSets[tag] = true
			}
		}
		if len(extraOverseasRuleSets) > 0 {
			routeRules = append(routeRules, map[string]interface{}{
				"rule_set": extraOverseasRuleSets,
				"outbound": "GLOBAL",
			})
			dnsRules = append(dnsRules, makeProxyDnsRule(nil, nil, nil, extraOverseasRuleSets))
		}

		// [Priority 5] Built-in & Subscription Domestic Direct Rules (DomesticDirectSuffixes + directGeositeTags + directGeoipTags)
		routeRules = append(routeRules, map[string]interface{}{
			"domain_suffix": DomesticDirectSuffixes,
			"outbound":      "direct",
		})
		dnsRules = append(dnsRules, map[string]interface{}{
			"domain_suffix": DomesticDirectSuffixes,
			"server":        "direct-dns",
		})

		if len(directGeositeTags) > 0 {
			routeRules = append(routeRules, map[string]interface{}{
				"rule_set": directGeositeTags,
				"outbound": "direct",
			})
			dnsRules = append(dnsRules, map[string]interface{}{
				"rule_set": directGeositeTags,
				"server":   "direct-dns",
			})
		}
		if len(directGeoipTags) > 0 {
			routeRules = append(routeRules, map[string]interface{}{
				"rule_set": directGeoipTags,
				"outbound": "direct",
			})
		}
	}

	// Fallback Fake-IP DNS rule at the end of dnsRules
	if isFakeIP {
		dnsRules = append(dnsRules, map[string]interface{}{
			"query_type":  fakeipQueryTypes,
			"server":      "fakeip-dns",
			"rewrite_ttl": 1,
		})
	}

	// [Priority 0] Access Control (ACL) Source IP Bypass Rules (OA-SPEC-ACL-01)
	if cfg.AccessControl.Enabled {
		bypassCidrs := make([]string, 0)
		for _, ip := range cfg.AccessControl.BypassIPs {
			ip = strings.TrimSpace(ip)
			if ip != "" {
				if !strings.Contains(ip, "/") {
					bypassCidrs = append(bypassCidrs, ip+"/32")
				} else {
					bypassCidrs = append(bypassCidrs, ip)
				}
			}
		}
		// Resolve any BypassMACs to their active IPs from DHCP/ARP (Defect 1)
		for _, mac := range cfg.AccessControl.BypassMACs {
			mac = strings.ToLower(strings.TrimSpace(mac))
			if mac != "" {
				for _, ip := range ResolveMacToIPs(mac) {
					if !strings.Contains(ip, "/") {
						bypassCidrs = append(bypassCidrs, ip+"/32")
					} else {
						bypassCidrs = append(bypassCidrs, ip)
					}
				}
			}
		}
		if len(bypassCidrs) > 0 {
			routeRules = append([]map[string]interface{}{
				{
					"inbound":        []string{"tun-in", "socks-in", "http-in"},
					"source_ip_cidr": bypassCidrs,
					"outbound":       "direct",
				},
			}, routeRules...)

			// 23-Audit Requirement 2 & 25-Audit Defect 1: Mutual exclusion guard using logical AND so
			// invert: true only negates source_ip_cidr without inverting query_type: ["A", "AAAA"]
			for i := range dnsRules {
				if dnsRules[i]["server"] == "fakeip-dns" && dnsRules[i]["domain"] == nil && dnsRules[i]["domain_suffix"] == nil && dnsRules[i]["domain_keyword"] == nil && dnsRules[i]["rule_set"] == nil {
					dnsRules[i] = map[string]interface{}{
						"type": "logical",
						"mode": "and",
						"rules": []map[string]interface{}{
							{"query_type": fakeipQueryTypes},
							{"source_ip_cidr": bypassCidrs, "invert": true},
						},
						"server":      "fakeip-dns",
						"rewrite_ttl": 1,
					}
				}
			}

			dnsRules = append([]map[string]interface{}{
				{
					"source_ip_cidr": bypassCidrs,
					"server":         "direct-dns",
				},
			}, dnsRules...)
		}

		if cfg.AccessControl.Mode == "whitelist" && len(cfg.AccessControl.ProxyOnlyIPs) > 0 {
			proxyOnlyCidrs := make([]string, 0)
			for _, ip := range cfg.AccessControl.ProxyOnlyIPs {
				ip = strings.TrimSpace(ip)
				if ip != "" {
					if !strings.Contains(ip, "/") {
						proxyOnlyCidrs = append(proxyOnlyCidrs, ip+"/32")
					} else {
						proxyOnlyCidrs = append(proxyOnlyCidrs, ip)
					}
				}
			}
			if len(proxyOnlyCidrs) > 0 {
				routeRules = append([]map[string]interface{}{
					{
						"inbound":        []string{"tun-in", "socks-in", "http-in"},
						"source_ip_cidr": proxyOnlyCidrs,
						"invert":         true,
						"outbound":       "direct",
					},
				}, routeRules...)

				for i := range dnsRules {
					if dnsRules[i]["server"] == "fakeip-dns" {
						dnsRules[i]["source_ip_cidr"] = proxyOnlyCidrs
					}
				}

				dnsRules = append([]map[string]interface{}{
					{
						"source_ip_cidr": proxyOnlyCidrs,
						"invert":         true,
						"server":         "direct-dns",
					},
				}, dnsRules...)
			}
		}
	}

	route := map[string]interface{}{
		"auto_detect_interface":   true,
		"default_domain_resolver": "direct-dns",
		"final":                   matchTarget,
		"rule_set":                existingRuleSets,
		"rules":                   routeRules,
	}

	remoteDnsServer := map[string]interface{}{
		"tag":    "remote-dns",
		"type":   "https",
		"server": "1.1.1.1",
	}
	if len(allNodeTags) > 0 {
		remoteDnsServer["detour"] = "GLOBAL"
	}
	directDnsServer := map[string]interface{}{
		"tag":    "direct-dns",
		"type":   "udp",
		"server": "223.5.5.5",
	}
	localDnsServer := map[string]interface{}{
		"tag":         "local-dns",
		"type":        "udp",
		"server":      "127.0.0.1",
		"server_port": 53,
	}

	dnsServers := []map[string]interface{}{
		remoteDnsServer,
		directDnsServer,
		localDnsServer,
	}
	if isFakeIP {
		fakeRange := cfg.FakeIpRange
		if fakeRange == "" {
			fakeRange = "198.18.0.0/15"
		}
		fakeServer := map[string]interface{}{
			"tag":         "fakeip-dns",
			"type":        "fakeip",
			"inet4_range": fakeRange,
		}
		if cfg.Ipv6Enabled && ipv6Mode == "proxy" {
			fakeServer["inet6_range"] = "fc00::/18"
		}
		dnsServers = append(dnsServers, fakeServer)
	}

	dns := map[string]interface{}{
		"servers":           dnsServers,
		"rules":             dnsRules,
		"final":             "remote-dns",
		"strategy":          dnsStrategy,
		"independent_cache": true,
		"cache_capacity":    8192,
	}

	clashPort := cfg.ClashAPIPort
	if clashPort <= 0 {
		clashPort = 9090
	}

	// 27-Audit Defect 8: Place bbolt cache.db on RAM tmpfs (/tmp/openaero_cache.db) to eliminate eMMC wear & power-loss corruption
	fullConfig := map[string]interface{}{
		"log": map[string]interface{}{
			"level": "info",
		},
		"experimental": map[string]interface{}{
			"clash_api": map[string]interface{}{
				"external_controller": fmt.Sprintf("127.0.0.1:%d", clashPort),
				"default_mode":        "rule",
			},
			"cache_file": map[string]interface{}{
				"enabled":      true,
				"path":         "/tmp/openaero_cache.db",
				"cache_id":     "openaero",
				"store_fakeip": true,
				"store_rdrc":   true,
			},
		},
		"dns":       dns,
		"inbounds":  inbounds,
		"outbounds": outbounds,
		"route":     route,
	}

	bytes, err := json.MarshalIndent(fullConfig, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal sing-box config failed: %w", err)
	}

	return string(bytes), nil
}

var routeReloadRunning atomic.Bool

// EnsureTunRoutes performs a lightweight check-first verification of table 2022 routes and kernel policy rules (8987/8988/8989).
// When routes and rules are already intact, it returns immediately with 0 writes, 0 sleeps, and 0 background goroutines (27-Audit Defect 7).
func EnsureTunRoutes(cfg *AppConfig) {
	if _, err := os.Stat("/sys/class/net/openaero0"); err != nil {
		return
	}
	if cfg == nil {
		cfg, _ = LoadConfig()
	}
	fakeRange := "198.18.0.0/15"
	isFakeIP := true
	ipv6Enabled := false
	if cfg != nil {
		if strings.TrimSpace(cfg.FakeIpRange) != "" {
			fakeRange = strings.TrimSpace(cfg.FakeIpRange)
		}
		dm := strings.ToLower(strings.TrimSpace(cfg.DnsMode))
		if dm != "" && dm != "fake-ip" {
			isFakeIP = false
		}
		ipv6Enabled = cfg.Ipv6Enabled
	}

	// 1. Check kernel policy rules first; only add if missing
	ruleOut, _ := exec.Command("ip", "rule", "show").Output()
	ruleStr := string(ruleOut)
	if !strings.Contains(ruleStr, "8987:") {
		_ = exec.Command("ip", "rule", "add", "to", "223.5.5.5", "ipproto", "tcp", "lookup", "main", "priority", "8987").Run()
		_ = exec.Command("ip", "rule", "add", "to", "119.29.29.29", "ipproto", "tcp", "lookup", "main", "priority", "8987").Run()
	}
	if !strings.Contains(ruleStr, "39999") {
		_ = exec.Command("ip", "rule", "add", "ipproto", "udp", "dport", "39999", "lookup", "main", "priority", "8987").Run()
	}
	if isFakeIP && !strings.Contains(ruleStr, "8988:") {
		_ = exec.Command("ip", "rule", "add", "to", fakeRange, "lookup", "2022", "priority", "8988").Run()
	}
	if !strings.Contains(ruleStr, "8989:") {
		_ = exec.Command("ip", "rule", "add", "ipproto", "icmp", "lookup", "main", "priority", "8989").Run()
	}

	// 2. Check table 2022 routes first; if already intact, return immediately (0 write forks!)
	routeOut, _ := exec.Command("ip", "route", "show", "table", "2022").Output()
	routeStr := string(routeOut)
	hasDefault := strings.Contains(routeStr, "default") && strings.Contains(routeStr, "openaero0")
	hasFakeRoute := !isFakeIP || strings.Contains(routeStr, fakeRange)

	if hasDefault && hasFakeRoute {
		return
	}

	_ = exec.Command("ip", "route", "replace", "default", "dev", "openaero0", "table", "2022").Run()
	if isFakeIP {
		_ = exec.Command("ip", "route", "replace", fakeRange, "dev", "openaero0", "table", "2022").Run()
		_ = exec.Command("ip", "route", "replace", fakeRange, "dev", "openaero0").Run()
	}
	if ipv6Enabled {
		_ = exec.Command("ip", "-6", "route", "replace", "default", "dev", "openaero0", "table", "2022").Run()
	}
}

// EnsureTunRoutesAfterReload reinforces TUN routes right after a SIGHUP reload or service restart when openaero0 is recreated (27-Audit Defect 7).
func EnsureTunRoutesAfterReload(cfg *AppConfig) {
	EnsureTunRoutes(cfg)
	if !routeReloadRunning.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer routeReloadRunning.Store(false)
		for _, delayMs := range []int{300, 600, 1000} {
			time.Sleep(time.Duration(delayMs) * time.Millisecond)
			EnsureTunRoutes(cfg)
		}
	}()
}

