package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCompilerRuleProviderDNSAndPriority(t *testing.T) {
	cfg := DefaultConfig()
	cfg.RuleProviders = []RuleProvider{
		{
			Id:       "my_whitelist",
			Name:     "Custom Whitelist",
			Target:   "DIRECT",
			Domains:  []string{"my-direct-domain.com"},
			Suffixes: []string{"direct-suffix.cn"},
		},
		{
			Id:       "my_proxy",
			Name:     "Custom Proxy",
			Target:   "proxy",
			Domains:  []string{"my-proxy-domain.com"},
			Suffixes: []string{"proxy-suffix.org"},
		},
	}
	cfg.StrategyGroups = []StrategyGroup{
		{
			Id:           "youtube",
			Name:         "YouTube",
			Enabled:      true,
			TargetNodeId: "node1",
			Rules:        []string{"youtube.com", "googlevideo.com"},
		},
	}

	testNodes := []Node{
		{
			Id:     "node1",
			Remark: "Test Node",
			Type:   "shadowsocks",
			Server: "1.2.3.4",
			Port:   8388,
		},
	}

	jsonStr, err := CompileSingBoxConfig(cfg, testNodes)
	if err != nil {
		t.Fatalf("CompileSingBoxConfig failed: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		t.Fatalf("Failed to parse compiled json: %v", err)
	}

	// 1. Verify NET-02: Custom RuleProviders must appear in dns.rules
	dnsMap, ok := parsed["dns"].(map[string]interface{})
	if !ok {
		t.Fatalf("dns section not found in compiled config")
	}
	dnsRules, ok := dnsMap["rules"].([]interface{})
	if !ok {
		t.Fatalf("dns.rules not found or invalid format")
	}

	foundDirectDns := false
	foundRemoteDns := false
	for _, r := range dnsRules {
		rm, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		if domains, ok := rm["domain"].([]interface{}); ok {
			for _, d := range domains {
				if d == "my-direct-domain.com" {
					foundDirectDns = true
				}
				if d == "my-proxy-domain.com" {
					foundRemoteDns = true
				}
			}
		}
	}

	if !foundDirectDns {
		t.Errorf("NET-02 BUG: 'my-direct-domain.com' was not found in dns.rules (RuleProvider dropped)")
	}
	if !foundRemoteDns {
		t.Errorf("NET-02 BUG: 'my-proxy-domain.com' was not found in dns.rules (RuleProvider dropped)")
	}

	// 2. Verify NET-03: Strategy Groups must precede geosite-cn / geoip-cn in route.rules
	routeMap, ok := parsed["route"].(map[string]interface{})
	if !ok {
		t.Fatalf("route section not found in compiled config")
	}
	routeRules, ok := routeMap["rules"].([]interface{})
	if !ok {
		t.Fatalf("route.rules not found")
	}

	youtubeIndex := -1
	cnRulesIndex := -1

	for idx, r := range routeRules {
		rm, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		if suffixes, ok := rm["domain_suffix"].([]interface{}); ok {
			for _, s := range suffixes {
				if s == "youtube.com" {
					youtubeIndex = idx
				}
			}
		}
		if ruleSets, ok := rm["rule_set"].([]interface{}); ok {
			for _, rs := range ruleSets {
				if rsStr, ok := rs.(string); ok && strings.Contains(rsStr, "cn") {
					if cnRulesIndex == -1 {
						cnRulesIndex = idx
					}
				}
			}
		}
	}

	if youtubeIndex == -1 {
		t.Errorf("NET-03: Strategy group rule for youtube was not found in route.rules")
	}
	if cnRulesIndex != -1 && youtubeIndex > cnRulesIndex {
		t.Errorf("NET-03 BUG: Rule priority inverted! CN rules (index %d) precede Strategy group rules (index %d)", cnRulesIndex, youtubeIndex)
	}
}

func TestCompilerAuditSpecificDefects(t *testing.T) {
	cfg := DefaultConfig()
	cfg.StrategyGroups = []StrategyGroup{
		{
			Id:        "auto_select",
			Name:      "自动选择",
			Type:      "urltest",
			Enabled:   true,
			URL:       "https://cp.cloudflare.com/generate_204",
			Interval:  300,
			Tolerance: 50,
		},
		{
			Id:           "youtube",
			Name:         "YouTube分流",
			Type:         "selector",
			Enabled:      true,
			TargetNodeId: "GLOBAL", // Follow GLOBAL
			Rules:        []string{"youtube.com"},
		},
	}

	testNodes := []Node{
		{
			Id:     "node-hk",
			Remark: "HK",
			Type:   "vless",
			Server: "1.2.3.4",
			Port:   443,
			Uuid:   "11111111-2222-3333-4444-555555555555",
		},
		{
			Id:     "node-sg",
			Remark: "SG",
			Type:   "vless",
			Server: "5.6.7.8",
			Port:   443,
			Uuid:   "66666666-7777-8888-9999-000000000000",
		},
	}

	jsonStr, err := CompileSingBoxConfig(cfg, testNodes)
	if err != nil {
		t.Fatalf("CompileSingBoxConfig failed: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		t.Fatalf("Failed to parse compiled json: %v", err)
	}

	outbounds, ok := parsed["outbounds"].([]interface{})
	if !ok {
		t.Fatalf("outbounds not found in compiled config")
	}

	// 1. Defect 1: urltest outbound must exist and be registered in GLOBAL
	foundUrlTest := false
	var globalSelector map[string]interface{}
	var youtubeSelector map[string]interface{}

	for _, ob := range outbounds {
		obMap, ok := ob.(map[string]interface{})
		if !ok {
			continue
		}
		tag := obMap["tag"]
		typ := obMap["type"]
		if tag == "auto_select" && typ == "urltest" {
			foundUrlTest = true
			obs := obMap["outbounds"].([]interface{})
			if len(obs) != 2 {
				t.Errorf("urltest outbounds count = %d; want 2", len(obs))
			}
		}
		if tag == "GLOBAL" && typ == "selector" {
			globalSelector = obMap
		}
		if tag == "youtube" && typ == "selector" {
			youtubeSelector = obMap
		}
	}

	if !foundUrlTest {
		t.Errorf("Defect 1: urltest outbound with tag 'auto_select' not found")
	}

	if globalSelector == nil {
		t.Fatalf("GLOBAL selector outbound not found")
	}
	globalObs := globalSelector["outbounds"].([]interface{})
	foundAutoInGlobal := false
	for _, o := range globalObs {
		if o == "auto_select" {
			foundAutoInGlobal = true
			break
		}
	}
	if !foundAutoInGlobal {
		t.Errorf("Defect 1: 'auto_select' not included in GLOBAL selector outbounds")
	}

	// 2. Defect 2: Strategy group selector must have GLOBAL as first choice and default to GLOBAL
	if youtubeSelector == nil {
		t.Fatalf("youtube selector outbound not found")
	}
	ytObs := youtubeSelector["outbounds"].([]interface{})
	if len(ytObs) == 0 || ytObs[0] != "GLOBAL" {
		t.Errorf("Defect 2: youtube selector first outbound = %v; want 'GLOBAL'", ytObs[0])
	}
	if youtubeSelector["default"] != "GLOBAL" {
		t.Errorf("Defect 2: youtube selector default = %v; want 'GLOBAL'", youtubeSelector["default"])
	}

	// 3. Defect 3: Domestic CN services (apple, microsoft, steam) must route to direct
	routeMap := parsed["route"].(map[string]interface{})
	routeRules := routeMap["rules"].([]interface{})
	foundAppleDirect := false
	foundSteamDirect := false
	foundAdBlock := false

	for _, r := range routeRules {
		rm, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		out := rm["outbound"]
		if suffixes, ok := rm["domain_suffix"].([]interface{}); ok {
			for _, s := range suffixes {
				if s == "apple.com" && out == "direct" {
					foundAppleDirect = true
				}
				if s == "steamcontent.com" && out == "direct" {
					foundSteamDirect = true
				}
				if s == "stats.g.doubleclick.net" && out == "block" {
					foundAdBlock = true
				}
			}
		}
	}

	if !foundAppleDirect {
		t.Errorf("Defect 3: apple.com not routed to direct")
	}
	if !foundSteamDirect {
		t.Errorf("Defect 3: steamcontent.com not routed to direct")
	}
	if !foundAdBlock {
		t.Errorf("Defect 4: ad blocking for stats.g.doubleclick.net not routed to block")
	}
}

func TestCompilerACLAndFakeIP(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DnsMode = "fake-ip"
	cfg.AccessControl.Enabled = true
	cfg.AccessControl.Mode = "blacklist"
	cfg.AccessControl.BypassIPs = []string{"192.168.1.150", "192.168.1.151"}

	nodes := []Node{
		{Id: "node-1", Remark: "HK", Type: "shadowsocks", Server: "1.2.3.4", Port: 8388, Password: "pwd", Method: "aes-256-gcm"},
	}

	jsonStr, err := CompileSingBoxConfig(cfg, nodes)
	if err != nil {
		t.Fatalf("CompileSingBoxConfig failed: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		t.Fatalf("Failed to parse json: %v", err)
	}

	// 1. Verify Fake-IP in dns.servers
	dnsMap := parsed["dns"].(map[string]interface{})
	servers := dnsMap["servers"].([]interface{})
	foundFakeIPServer := false
	for _, s := range servers {
		sm := s.(map[string]interface{})
		if sm["tag"] == "fakeip-dns" && sm["type"] == "fakeip" {
			foundFakeIPServer = true
		}
	}
	if !foundFakeIPServer {
		t.Errorf("Fake-IP DNS server not found in dns.servers")
	}

	// 2. Verify ACL in route.rules (first rule)
	routeMap := parsed["route"].(map[string]interface{})
	routeRules := routeMap["rules"].([]interface{})
	firstRule := routeRules[0].(map[string]interface{})
	if firstRule["outbound"] != "direct" {
		t.Errorf("First route rule outbound = %v; want 'direct'", firstRule["outbound"])
	}
	srcCidrs, ok := firstRule["source_ip_cidr"].([]interface{})
	if !ok || len(srcCidrs) != 2 || srcCidrs[0] != "192.168.1.150/32" {
		t.Errorf("First route rule source_ip_cidr = %v; want ['192.168.1.150/32', '192.168.1.151/32']", srcCidrs)
	}

	// 3. Verify ACL in dns.rules (first rule)
	dnsRules := dnsMap["rules"].([]interface{})
	firstDnsRule := dnsRules[0].(map[string]interface{})
	if firstDnsRule["server"] != "direct-dns" {
		t.Errorf("First dns rule server = %v; want 'direct-dns'", firstDnsRule["server"])
	}

	// 4. Verify Fake-IP filter rules & rewrite_ttl = 1 (Defect 3 & Defect 4)
	foundLocalDnsServer := false
	for _, s := range servers {
		sm := s.(map[string]interface{})
		if sm["tag"] == "local-dns" && sm["server"] == "127.0.0.1" {
			foundLocalDnsServer = true
		}
	}
	if !foundLocalDnsServer {
		t.Errorf("Defect 3: local-dns (127.0.0.1:53) not found in dns.servers")
	}

	foundRewriteTTL := false
	for _, r := range dnsRules {
		rm := r.(map[string]interface{})
		if rm["server"] == "fakeip-dns" {
			if ttl, ok := rm["rewrite_ttl"].(float64); ok && ttl == 1 {
				foundRewriteTTL = true
			}
		}
	}
	if !foundRewriteTTL {
		t.Errorf("Defect 4: rewrite_ttl: 1 not set for fakeip-dns rule")
	}

	// 5. Verify experimental.cache_file with store_fakeip: true
	expMap, ok := parsed["experimental"].(map[string]interface{})
	if !ok {
		t.Fatalf("experimental section missing")
	}
	cacheFile, ok := expMap["cache_file"].(map[string]interface{})
	if !ok || cacheFile["store_fakeip"] != true {
		t.Errorf("Defect 4: experimental.cache_file.store_fakeip != true")
	}
}

func TestCompiler23AuditMutualExclusionAndFactoryDefaults(t *testing.T) {
	// 1. Requirement 3: Factory defaults must have empty bypass_macs and bypass_ips
	defCfg := DefaultConfig()
	if len(defCfg.AccessControl.BypassMACs) != 0 {
		t.Errorf("23-Audit Req 3: DefaultConfig().AccessControl.BypassMACs must be empty, got %v", defCfg.AccessControl.BypassMACs)
	}
	if len(defCfg.AccessControl.BypassIPs) != 0 {
		t.Errorf("23-Audit Req 3: DefaultConfig().AccessControl.BypassIPs must be empty, got %v", defCfg.AccessControl.BypassIPs)
	}

	// 2. Requirement 1 & 2: Use isolated RFC 5737 TEST-NET IP (192.0.2.100) and verify Fake-IP mutual exclusion
	cfg := DefaultConfig()
	cfg.DnsMode = "fake-ip"
	cfg.AccessControl.Enabled = true
	cfg.AccessControl.Mode = "blacklist"
	cfg.AccessControl.BypassIPs = []string{"192.0.2.100"}

	nodes := []Node{
		{Id: "node-1", Remark: "HK", Type: "shadowsocks", Server: "1.2.3.4", Port: 8388, Password: "pwd", Method: "aes-256-gcm"},
	}

	jsonStr, err := CompileSingBoxConfig(cfg, nodes)
	if err != nil {
		t.Fatalf("CompileSingBoxConfig failed: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		t.Fatalf("Failed to parse json: %v", err)
	}

	dnsMap := parsed["dns"].(map[string]interface{})
	dnsRules := dnsMap["rules"].([]interface{})

	foundInvertedFakeIP := false
	for _, r := range dnsRules {
		rm := r.(map[string]interface{})
		if rm["server"] == "fakeip-dns" {
			if rm["type"] == "logical" && rm["mode"] == "and" {
				if subRules, ok := rm["rules"].([]interface{}); ok {
					for _, sr := range subRules {
						srm, ok := sr.(map[string]interface{})
						if ok && srm["invert"] == true {
							if cidrs, ok := srm["source_ip_cidr"].([]interface{}); ok && len(cidrs) == 1 && cidrs[0] == "192.0.2.100/32" {
								foundInvertedFakeIP = true
							}
						}
					}
				}
			}
		}
	}
	if !foundInvertedFakeIP {
		t.Errorf("23-Audit Req 2: fakeip-dns rule must have hard mutual exclusion (logical AND with source_ip_cidr + invert: true) for bypassed devices")
	}
}

func TestCompiler25AuditFakeIPQueryTypeAndDomesticDirect(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DnsMode = "fake-ip"
	cfg.Ipv6Enabled = false
	cfg.RuleProviders = []RuleProvider{
		{
			Id:       "my_proxylist",
			Target:   "match",
			Suffixes: []string{"youtube.com", "ytimg.com"},
		},
	}

	nodes := []Node{
		{Id: "node-1", Remark: "US", Type: "shadowsocks", Server: "1.2.3.4", Port: 8388, Password: "pwd", Method: "aes-256-gcm"},
	}

	jsonStr, err := CompileSingBoxConfig(cfg, nodes)
	if err != nil {
		t.Fatalf("CompileSingBoxConfig failed: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		t.Fatalf("Failed to parse json: %v", err)
	}

	dnsMap := parsed["dns"].(map[string]interface{})
	dnsRules := dnsMap["rules"].([]interface{})

	// 1. Defect 1: Verify HTTPS/SVCB predefined NOERROR rule and strict query_type: ["A"] on fakeip-dns
	foundHttpsPredefined := false
	for _, r := range dnsRules {
		rm := r.(map[string]interface{})
		if rm["action"] == "predefined" && rm["rcode"] == "NOERROR" {
			if qts, ok := rm["query_type"].([]interface{}); ok && len(qts) >= 2 {
				foundHttpsPredefined = true
			}
		}
		if rm["server"] == "fakeip-dns" {
			qts, ok := rm["query_type"].([]interface{})
			if !ok || len(qts) == 0 {
				t.Errorf("25-Audit Defect 1: fakeip-dns rule missing query_type filter: %v", rm)
			}
		}
		if rm["server"] == "remote-dns" {
			t.Errorf("25-Audit Defect 2: my_proxylist must not use remote-dns when Fake-IP is enabled: %v", rm)
		}
	}
	if !foundHttpsPredefined {
		t.Errorf("25-Audit Defect 1: missing predefined NOERROR rule for HTTPS/SVCB queries")
	}

	// 2. Defect 2: Verify .cn, effirst.com, zstaticcdn.com in direct route rules and ICMP direct rule
	routeMap := parsed["route"].(map[string]interface{})
	routeRules := routeMap["rules"].([]interface{})
	foundCnDirect := false
	foundEffirstDirect := false
	foundZstaticDirect := false
	foundIcmpDirect := false

	for _, r := range routeRules {
		rm := r.(map[string]interface{})
		if rm["protocol"] == "icmp" && rm["outbound"] == "direct" {
			foundIcmpDirect = true
		}
		if rm["outbound"] == "direct" {
			if suffixes, ok := rm["domain_suffix"].([]interface{}); ok {
				for _, s := range suffixes {
					switch s {
					case "cn":
						foundCnDirect = true
					case "effirst.com":
						foundEffirstDirect = true
					case "zstaticcdn.com":
						foundZstaticDirect = true
					}
				}
			}
		}
	}

	if !foundCnDirect || !foundEffirstDirect || !foundZstaticDirect {
		t.Errorf("25-Audit Defect 2: missing direct route suffixes (cn=%v, effirst.com=%v, zstaticcdn.com=%v)", foundCnDirect, foundEffirstDirect, foundZstaticDirect)
	}
	if !foundIcmpDirect {
		t.Errorf("25-Audit Defect 2: missing protocol=icmp -> direct route rule")
	}
}

func TestCompiler27AuditFullLinkShuntingAndStability(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DnsMode = "fake-ip"
	cfg.ActiveNodeId = "auto_select"
	cfg.CustomProxyDomains = []string{"custom-proxy-site.cn"}
	cfg.RuleProviders = []RuleProvider{
		{
			Id:       "my_proxylist",
			Name:     "My Proxy List",
			Target:   "match",
			Suffixes: []string{"example-proxy.org"},
		},
		{
			Id:       "telegram",
			Name:     "Telegram",
			Target:   "telegram",
			Suffixes: []string{"t.me", "telegram.org"},
			Ips:      []string{"149.154.160.0/20", "91.108.0.0/16"},
		},
	}
	cfg.StrategyGroups = []StrategyGroup{
		{
			Id:           "match",
			Name:         "漏网之鱼 (MATCH)",
			TargetNodeId: "auto_select",
			Enabled:      true,
		},
		{
			Id:           "telegram",
			Name:         "Telegram",
			TargetNodeId: "node-hk",
			Enabled:      true,
		},
	}

	nodes := []Node{
		{Id: "node-info", Remark: "剩余流量：88.8 GB", Type: "vless", Server: "127.0.0.1", Port: 443},
		{Id: "node-hk", Remark: "HK", Type: "vless", Server: "1.2.3.4", Port: 443, Uuid: "11111111-2222-3333-4444-555555555555"},
	}

	jsonStr, err := CompileSingBoxConfig(cfg, nodes)
	if err != nil {
		t.Fatalf("CompileSingBoxConfig failed: %v", err)
	}

	// Verify Defect 3: ActiveNodeId was NOT overwritten when set to auto_select
	if cfg.ActiveNodeId != "auto_select" {
		t.Errorf("27-Audit Defect 3: cfg.ActiveNodeId was overwritten to %q; want 'auto_select'", cfg.ActiveNodeId)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		t.Fatalf("Failed to parse json: %v", err)
	}

	// Verify Defect 3: GLOBAL selector default == "auto_select" and auto_select excludes info node
	outbounds := parsed["outbounds"].([]interface{})
	for _, ob := range outbounds {
		obm := ob.(map[string]interface{})
		if obm["tag"] == "GLOBAL" {
			if obm["default"] != "auto_select" {
				t.Errorf("27-Audit Defect 3: GLOBAL selector default = %v; want 'auto_select'", obm["default"])
			}
		}
		if obm["tag"] == "auto_select" {
			obs := obm["outbounds"].([]interface{})
			if len(obs) != 1 || obs[0] != "node-hk" {
				t.Errorf("27-Audit Defect 6: auto_select outbounds = %v; want ['node-hk'] (excluding info node)", obs)
			}
		}
	}

	// Verify Defect 4: remote-dns.detour == "GLOBAL"
	dnsMap := parsed["dns"].(map[string]interface{})
	for _, s := range dnsMap["servers"].([]interface{}) {
		sm := s.(map[string]interface{})
		if sm["tag"] == "remote-dns" && sm["detour"] != "GLOBAL" {
			t.Errorf("27-Audit Defect 4: remote-dns.detour = %v; want 'GLOBAL'", sm["detour"])
		}
	}

	// Verify Defect 8: cache_file.path == "/tmp/openaero_cache.db"
	expMap := parsed["experimental"].(map[string]interface{})
	cacheFile := expMap["cache_file"].(map[string]interface{})
	if cacheFile["path"] != "/tmp/openaero_cache.db" {
		t.Errorf("27-Audit Defect 8: cache_file.path = %v; want '/tmp/openaero_cache.db'", cacheFile["path"])
	}

	// Verify Defect 1 & 2: route.rules contains telegram ip_cidr, my_proxylist & CustomProxyDomains precede DomesticDirectSuffixes ("cn"), and steamcommunity.com is removed
	routeMap := parsed["route"].(map[string]interface{})
	routeRules := routeMap["rules"].([]interface{})
	foundTelegramIpCidr := false
	myProxyRouteIdx := -1
	customProxyRouteIdx := -1
	cnDirectRouteIdx := -1

	for idx, r := range routeRules {
		rm := r.(map[string]interface{})
		if rm["outbound"] == "telegram" {
			if cidrs, ok := rm["ip_cidr"].([]interface{}); ok && len(cidrs) >= 2 {
				foundTelegramIpCidr = true
			}
		}
		if suffixes, ok := rm["domain_suffix"].([]interface{}); ok {
			for _, s := range suffixes {
				switch s {
				case "example-proxy.org":
					myProxyRouteIdx = idx
					if rm["outbound"] != "GLOBAL" {
						t.Errorf("27-Audit Defect 4: my_proxylist outbound = %v; want 'GLOBAL'", rm["outbound"])
					}
				case "custom-proxy-site.cn":
					customProxyRouteIdx = idx
					if rm["outbound"] != "GLOBAL" {
						t.Errorf("27-Audit Defect 4: CustomProxyDomains outbound = %v; want 'GLOBAL'", rm["outbound"])
					}
				case "cn":
					if rm["outbound"] == "direct" {
						cnDirectRouteIdx = idx
					}
				case "steamcommunity.com":
					if rm["outbound"] == "direct" {
						t.Errorf("27-Audit Defect 2: steamcommunity.com must NOT be in direct domestic suffixes")
					}
				}
			}
		}
	}

	if !foundTelegramIpCidr {
		t.Errorf("27-Audit Defect 1: telegram RuleProvider ip_cidr was dropped from route.rules")
	}
	if myProxyRouteIdx == -1 || customProxyRouteIdx == -1 || cnDirectRouteIdx == -1 {
		t.Fatalf("27-Audit Defect 2: missing expected route rules (myProxy=%d, customProxy=%d, cnDirect=%d)", myProxyRouteIdx, customProxyRouteIdx, cnDirectRouteIdx)
	}
	if myProxyRouteIdx > cnDirectRouteIdx || customProxyRouteIdx > cnDirectRouteIdx {
		t.Errorf("27-Audit Defect 2: route priority inverted! myProxy(%d) / customProxy(%d) must precede cnDirect(%d)", myProxyRouteIdx, customProxyRouteIdx, cnDirectRouteIdx)
	}

	// Verify Defect 2: dns.rules priority (my_proxylist & CustomProxyDomains precede cn direct-dns)
	dnsRules := dnsMap["rules"].([]interface{})
	myProxyDnsIdx := -1
	customProxyDnsIdx := -1
	cnDirectDnsIdx := -1
	for idx, r := range dnsRules {
		rm := r.(map[string]interface{})
		if suffixes, ok := rm["domain_suffix"].([]interface{}); ok {
			for _, s := range suffixes {
				switch s {
				case "example-proxy.org":
					myProxyDnsIdx = idx
				case "custom-proxy-site.cn":
					customProxyDnsIdx = idx
				case "cn":
					if rm["server"] == "direct-dns" {
						cnDirectDnsIdx = idx
					}
				}
			}
		}
	}
	if myProxyDnsIdx == -1 || customProxyDnsIdx == -1 || cnDirectDnsIdx == -1 {
		t.Fatalf("27-Audit Defect 2: missing expected dns rules (myProxy=%d, customProxy=%d, cnDirect=%d)", myProxyDnsIdx, customProxyDnsIdx, cnDirectDnsIdx)
	}
	if myProxyDnsIdx > cnDirectDnsIdx || customProxyDnsIdx > cnDirectDnsIdx {
		t.Errorf("27-Audit Defect 2: DNS priority inverted! myProxy(%d) / customProxy(%d) must precede cnDirect(%d)", myProxyDnsIdx, customProxyDnsIdx, cnDirectDnsIdx)
	}
}



func TestTask07QuicOrderingAndZzaoDe(t *testing.T) {
	// 1. Verify DomesticDirectSuffixes contains zzao.de
	foundZzao := false
	for _, s := range DomesticDirectSuffixes {
		if s == "zzao.de" {
			foundZzao = true
			break
		}
	}
	if !foundZzao {
		t.Fatalf("DomesticDirectSuffixes must contain zzao.de")
	}

	// 2. Verify compiled route rules ordering
	cfg := DefaultConfig()
	cfg.Mode = "rule"
	testNodes := []Node{
		{Id: "n1", Server: "23.80.81.106", Port: 443, Type: "vless"},
	}
	jsonStr, err := CompileSingBoxConfig(cfg, testNodes)
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	routeMap := parsed["route"].(map[string]interface{})
	rules := routeMap["rules"].([]interface{})

	// First rule must be sniff
	firstRule := rules[0].(map[string]interface{})
	if firstRule["action"] != "sniff" {
		t.Errorf("First route rule must have action sniff, got %v", firstRule["action"])
	}

	// Find domestic UDP 443 rule and overseas UDP 443 reject
	domesticUdpIdx := -1
	overseasUdpRejectIdx := -1
	for idx, r := range rules {
		rm := r.(map[string]interface{})
		action, _ := rm["action"].(string)
		outbound, _ := rm["outbound"].(string)
		ports, _ := rm["port"].([]interface{})
		has443 := false
		for _, p := range ports {
			if num, ok := p.(float64); ok && int(num) == 443 {
				has443 = true
			}
		}

		if has443 {
			if outbound == "direct" {
				domesticUdpIdx = idx
			} else if action == "reject" && overseasUdpRejectIdx == -1 {
				overseasUdpRejectIdx = idx
			}
		}
	}

	if domesticUdpIdx == -1 {
		t.Errorf("Domestic UDP 443 direct rule missing in route.rules")
	}
	if overseasUdpRejectIdx == -1 {
		t.Errorf("Overseas UDP 443 reject rule missing in route.rules")
	}
	if domesticUdpIdx >= overseasUdpRejectIdx {
		t.Errorf("Domestic UDP 443 must precede overseas UDP 443 reject: domestic=%d, reject=%d", domesticUdpIdx, overseasUdpRejectIdx)
	}
}

func TestTask07ResolveActiveOrttTargets(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.OrttMode = "follow_node"
	cfg.ActiveNodeId = "node1"
	cfg.StrategyGroups = []StrategyGroup{
		{
			Id:           "google",
			Enabled:      true,
			TargetNodeId: "node2",
		},
		{
			Id:           "youtube",
			Enabled:      true,
			TargetNodeId: "node2",
		},
		{
			Id:           "bilibili",
			Enabled:      true,
			TargetNodeId: "DIRECT",
		},
	}

	nodes := []Node{
		{Id: "node1", Server: "23.80.81.106"},
		{Id: "node2", Server: "38.105.30.170"},
	}

	targets := ResolveActiveOrttTargets(cfg, nodes)
	if len(targets) != 2 {
		t.Fatalf("Expected 2 unique targets, got %d: %v", len(targets), targets)
	}

	expected := map[string]bool{"23.80.81.106": true, "38.105.30.170": true}
	for _, tg := range targets {
		if !expected[tg] {
			t.Errorf("Unexpected target %s", tg)
		}
	}
}
