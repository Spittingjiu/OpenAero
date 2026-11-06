package subscription

import (
	"net"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"unicode/utf8"
	"strconv"
	"strings"
	"sync"
	"time"

	"openaero-core/pkg/config"
	"gopkg.in/yaml.v3"
)

type ClashProxy struct {
	Name           string                 `yaml:"name"`
	Type           string                 `yaml:"type"`
	Server         string                 `yaml:"server"`
	Port           int                    `yaml:"port"`
	UUID           string                 `yaml:"uuid,omitempty"`
	Password       string                 `yaml:"password,omitempty"`
	Cipher         string                 `yaml:"cipher,omitempty"`
	AlterId        int                    `yaml:"alterId,omitempty"`
	TLS            bool                   `yaml:"tls,omitempty"`
	Servername     string                 `yaml:"servername,omitempty"`
	SNI            string                 `yaml:"sni,omitempty"`
	SkipCertVerify bool                   `yaml:"skip-cert-verify,omitempty"`
	Flow           string                 `yaml:"flow,omitempty"`
	ClientFP       string                 `yaml:"client-fingerprint,omitempty"`
	RealityOpts    map[string]interface{} `yaml:"reality-opts,omitempty"`
}

type ClashProxyGroup struct {
	Name      string   `yaml:"name"`
	Type      string   `yaml:"type"`
	Proxies   []string `yaml:"proxies"`
	URL       string   `yaml:"url,omitempty"`
	Interval  int      `yaml:"interval,omitempty"`
	Tolerance int      `yaml:"tolerance,omitempty"`
}

type ClashRuleProvider struct {
	Type     string `yaml:"type"`
	Behavior string `yaml:"behavior"`
	Path     string `yaml:"path"`
	Url      string `yaml:"url"`
	Interval int    `yaml:"interval"`
	Format   string `yaml:"format"`
}

type ClashConfig struct {
	Proxies       []ClashProxy                 `yaml:"proxies"`
	ProxyGroups   []ClashProxyGroup            `yaml:"proxy-groups"`
	RuleProviders map[string]ClashRuleProvider `yaml:"rule-providers"`
	Rules         []string                     `yaml:"rules"`
	Dns           ClashDnsConfig               `yaml:"dns"`
}

type ClashDnsConfig struct {
	Enable                      bool                   `yaml:"enable"`
	FakeIpFilter                []string               `yaml:"fake-ip-filter"`
	ProxyServerNameserverPolicy map[string]interface{} `yaml:"proxy-server-nameserver-policy"`
	NameserverPolicy            map[string]interface{} `yaml:"nameserver-policy"`
}

type FetchResult struct {
	Nodes               []config.Node          `json:"nodes"`
	Groups              []config.StrategyGroup `json:"groups"`
	Providers           []config.RuleProvider  `json:"providers"`
	DirectRuleSetTags   []string               `json:"direct_rule_set_tags"`
	CustomDirectDomains []string               `json:"custom_direct_domains"`
}

// ApplySubscriptionResult applies a FetchResult to the given AppConfig and persists nodes (SSOT).
// Preserves existing latency, ORTT status, and pinned TargetNodeId of StrategyGroups.
func ApplySubscriptionResult(cfg *config.AppConfig, res *FetchResult, subUrl string) {
	if cfg == nil || res == nil {
		return
	}
	// Preserve existing node latency and ORTT metrics across subscription refreshes
	if existingNodes, err := config.LoadNodes(); err == nil && len(existingNodes) > 0 {
		metricsMap := make(map[string]config.Node)
		for _, en := range existingNodes {
			key := fmt.Sprintf("%s:%d", en.Server, en.Port)
			metricsMap[key] = en
			if en.Id != "" {
				metricsMap[en.Id] = en
			}
		}
		for i := range res.Nodes {
			key := fmt.Sprintf("%s:%d", res.Nodes[i].Server, res.Nodes[i].Port)
			old, found := metricsMap[key]
			if !found && res.Nodes[i].Id != "" {
				old, found = metricsMap[res.Nodes[i].Id]
			}
			if found {
				res.Nodes[i].Latency = old.Latency
				res.Nodes[i].OrttStatus = old.OrttStatus
				res.Nodes[i].OrttLabel = old.OrttLabel
				res.Nodes[i].OrttRttMs = old.OrttRttMs
				res.Nodes[i].OrttWarmConns = old.OrttWarmConns
				res.Nodes[i].OrttPoolHits = old.OrttPoolHits
				res.Nodes[i].OrttPoolMisses = old.OrttPoolMisses
				res.Nodes[i].OrttHitRate = old.OrttHitRate
				res.Nodes[i].OrttCheckedAt = old.OrttCheckedAt
			}
		}
	}
	_ = config.SaveNodes(res.Nodes)

	nowStr := time.Now().Format("2006-01-02 15:04:05")
	for i := range cfg.Subscriptions {
		if subUrl == "" || cfg.Subscriptions[i].Url == subUrl {
			cfg.Subscriptions[i].LastUpdated = nowStr
			cfg.Subscriptions[i].NodeCount = len(res.Nodes)
			break
		}
	}
	if len(cfg.Subscriptions) > 0 && cfg.Subscriptions[0].LastUpdated != nowStr {
		cfg.Subscriptions[0].LastUpdated = nowStr
		cfg.Subscriptions[0].NodeCount = len(res.Nodes)
	}

	if len(res.Groups) > 0 {
		existingTargets := make(map[string]string)
		for _, eg := range cfg.StrategyGroups {
			if eg.TargetNodeId != "" {
				existingTargets[eg.Id] = eg.TargetNodeId
				existingTargets[eg.Name] = eg.TargetNodeId
			}
		}
		for i := range res.Groups {
			gid := res.Groups[i].Id
			gname := res.Groups[i].Name
			if t, ok := existingTargets[gid]; ok {
				res.Groups[i].TargetNodeId = t
			} else if t, ok := existingTargets[gname]; ok {
				res.Groups[i].TargetNodeId = t
			}
		}
		cfg.StrategyGroups = res.Groups
	}

	if len(res.Providers) > 0 {
		cfg.RuleProviders = res.Providers
	}

	if len(res.DirectRuleSetTags) > 0 {
		seen := make(map[string]bool)
		for _, tag := range cfg.DirectRuleSetTags {
			seen[tag] = true
		}
		for _, tag := range res.DirectRuleSetTags {
			if !seen[tag] {
				seen[tag] = true
				cfg.DirectRuleSetTags = append(cfg.DirectRuleSetTags, tag)
			}
		}
	}

	if len(res.CustomDirectDomains) > 0 {
		seen := make(map[string]bool)
		for _, d := range cfg.CustomDirectDomains {
			seen[d] = true
		}
		for _, d := range res.CustomDirectDomains {
			if !seen[d] {
				seen[d] = true
				cfg.CustomDirectDomains = append(cfg.CustomDirectDomains, d)
			}
		}
	}
}

func fetchHttpWithTimeout(reqUrl string, userAgent string, timeout time.Duration, proxyStr string) ([]byte, error) {
	tr := &http.Transport{
		DisableKeepAlives: true,
	}
	if proxyStr != "" {
		if pUrl, err := url.Parse(proxyStr); err == nil {
			tr.Proxy = http.ProxyURL(pUrl)
		}
	}
	client := &http.Client{
		Transport: tr,
		Timeout:   timeout,
	}
	req, err := http.NewRequest("GET", reqUrl, nil)
	if err != nil {
		return nil, err
	}
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func FetchSubscriptionFull(subUrl string, userAgent string) (*FetchResult, error) {
	if userAgent == "" {
		userAgent = "clash-verge/v2.4.5"
	}

	// 1. Try local proxy (127.0.0.1:10809) with 6s timeout
	body, err := fetchHttpWithTimeout(subUrl, userAgent, 6*time.Second, "http://127.0.0.1:10809")
	if err != nil {
		// 2. Fallback to direct client with 8s timeout
		body, err = fetchHttpWithTimeout(subUrl, userAgent, 8*time.Second, "")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to fetch subscription: %w", err)
	}

	content := string(body)

	// Check if Clash YAML
	if strings.Contains(content, "proxies:") {
		return parseClashYAML(body)
	}

	// Try base64 decoding
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(content))
	if err == nil {
		nodes, err := parseV2RayLinks(string(decoded))
		return &FetchResult{Nodes: nodes}, err
	}
	// Try URL-safe base64
	decoded, err = base64.URLEncoding.DecodeString(strings.TrimSpace(content))
	if err == nil {
		nodes, err := parseV2RayLinks(string(decoded))
		return &FetchResult{Nodes: nodes}, err
	}

	// Maybe plain text links
	nodes, err := parseV2RayLinks(content)
	return &FetchResult{Nodes: nodes}, err
}

func parseClashYAML(data []byte) (*FetchResult, error) {
	var cc ClashConfig
	if err := yaml.Unmarshal(data, &cc); err != nil {
		return nil, fmt.Errorf("yaml unmarshal failed: %w", err)
	}

	nodes := make([]config.Node, 0, len(cc.Proxies))
	for _, p := range cc.Proxies {
		nodeType := strings.ToLower(p.Type)
		sni := p.Servername
		if sni == "" {
			sni = p.SNI
		}

		node := config.Node{
			Id:          makeNodeId(p.Name, p.Server, p.Port),
			Remark:      SanitizeRemark(p.Name),
			Type:        nodeType,
			Server:      p.Server,
			Port:        p.Port,
			Password:    p.Password,
			Method:      p.Cipher,
			Uuid:        p.UUID,
			Flow:        p.Flow,
			Sni:         sni,
			Fingerprint: p.ClientFP,
			Insecure:    p.SkipCertVerify,
			Latency:     -1,
		}

		if p.RealityOpts != nil {
			if pbk, ok := p.RealityOpts["public-key"].(string); ok {
				node.PublicKey = pbk
			}
			if sid, ok := p.RealityOpts["short-id"].(string); ok {
				node.ShortId = sid
			}
		}

		nodes = append(nodes, node)
	}

	// 1. Identify MATCH fallback group name from rules
	matchGroupName := "节点选择"
	for _, rule := range cc.Rules {
		parts := strings.Split(rule, ",")
		if len(parts) >= 2 && strings.TrimSpace(parts[0]) == "MATCH" {
			matchGroupName = strings.TrimSpace(parts[1])
			break
		}
	}

	// 2. Extract rules for each group and DIRECT from rules list
	groupRules := make(map[string][]string)
	groupKeywords := make(map[string][]string)
	groupRuleSets := make(map[string][]string)
	directRuleSetTags := make([]string, 0)
	seenDirectTags := make(map[string]bool)
	customDirectDomains := make([]string, 0)
	seenDirectDomain := make(map[string]bool)

	for _, rule := range cc.Rules {
		parts := strings.Split(rule, ",")
		if len(parts) < 3 {
			continue
		}
		ruleType := strings.ToUpper(strings.TrimSpace(parts[0]))
		val := strings.TrimSpace(parts[1])
		target := strings.TrimSpace(parts[2])

		if target == "DIRECT" {
			if ruleType == "GEOSITE" {
				tag := "geosite-" + strings.TrimPrefix(val, "geosite-")
				if !seenDirectTags[tag] {
					seenDirectTags[tag] = true
					directRuleSetTags = append(directRuleSetTags, tag)
				}
			} else if ruleType == "GEOIP" {
				tag := "geoip-" + strings.ToLower(strings.TrimPrefix(val, "geoip-"))
				if !seenDirectTags[tag] {
					seenDirectTags[tag] = true
					directRuleSetTags = append(directRuleSetTags, tag)
				}
			} else if ruleType == "DOMAIN" || ruleType == "DOMAIN-SUFFIX" {
				cleanVal := strings.TrimPrefix(strings.TrimPrefix(val, "+."), ".")
				if !seenDirectDomain[cleanVal] && cleanVal != "" {
					seenDirectDomain[cleanVal] = true
					customDirectDomains = append(customDirectDomains, cleanVal)
				}
			}
		} else {
			if ruleType == "DOMAIN" || ruleType == "DOMAIN-SUFFIX" {
				groupRules[target] = append(groupRules[target], val)
			} else if ruleType == "DOMAIN-KEYWORD" {
				groupKeywords[target] = append(groupKeywords[target], val)
			} else if ruleType == "GEOSITE" {
				tag := "geosite-" + strings.TrimPrefix(val, "geosite-")
				groupRuleSets[target] = append(groupRuleSets[target], tag)
			} else if ruleType == "GEOIP" {
				tag := "geoip-" + strings.ToLower(strings.TrimPrefix(val, "geoip-"))
				groupRuleSets[target] = append(groupRuleSets[target], tag)
			} else if ruleType == "RULE-SET" {
				tag := "geosite-" + strings.TrimPrefix(val, "geosite-")
				groupRuleSets[target] = append(groupRuleSets[target], tag)
			}
		}
	}

	// Also parse cc.Dns fake-ip-filter and proxy-server-nameserver-policy into direct domains
	for _, f := range cc.Dns.FakeIpFilter {
		f = strings.TrimSpace(f)
		if f == "" || strings.Contains(f, "*") {
			continue
		}
		clean := strings.TrimPrefix(strings.TrimPrefix(f, "+."), ".")
		if clean != "" && !seenDirectDomain[clean] {
			seenDirectDomain[clean] = true
			customDirectDomains = append(customDirectDomains, clean)
		}
	}
	for host := range cc.Dns.ProxyServerNameserverPolicy {
		host = strings.TrimSpace(host)
		if host != "" && !strings.Contains(host, "geosite:") && !strings.Contains(host, "*") {
			clean := strings.TrimPrefix(strings.TrimPrefix(host, "+."), ".")
			if clean != "" && !seenDirectDomain[clean] {
				seenDirectDomain[clean] = true
				customDirectDomains = append(customDirectDomains, clean)
			}
		}
	}

	// 3. Construct strategy groups dynamically from cc.ProxyGroups
	groups := make([]config.StrategyGroup, 0)
	var matchGroup *config.StrategyGroup

	for _, pg := range cc.ProxyGroups {
		sanitizedName := SanitizeRemark(pg.Name)
		if sanitizedName == "手动选择" {
			continue
		}

		lowerName := strings.ToLower(sanitizedName)
		isMatch := (sanitizedName == matchGroupName || sanitizedName == "节点选择" || sanitizedName == "MATCH" || sanitizedName == "漏网之鱼")
		isAutoSelect := (pg.Type == "url-test" || sanitizedName == "自动选择")

		var id, icon string
		groupType := "selector"
		if isAutoSelect {
			id = "auto_select"
			icon = "⚡"
			groupType = "urltest"
		} else if isMatch {
			id = "match"
			icon = "🌐"
		} else if strings.Contains(lowerName, "gemini") {
			id = "gemini"
			icon = "♊"
		} else if strings.Contains(lowerName, "google") {
			id = "google"
			icon = "🔍"
		} else if strings.Contains(lowerName, "chatgpt") || strings.Contains(lowerName, "openai") {
			id = "chatgpt"
			icon = "🤖"
		} else if strings.Contains(lowerName, "youtube") {
			id = "youtube"
			icon = "📺"
		} else if strings.Contains(lowerName, "telegram") {
			id = "telegram"
			icon = "✈️"
		} else {
			id = strings.ReplaceAll(lowerName, " ", "_")
			icon = "🔀"
		}

		rulesList := groupRules[pg.Name]
		if len(rulesList) == 0 && sanitizedName != pg.Name {
			rulesList = groupRules[sanitizedName]
		}
		for k, defs := range config.DefaultGroupDomains {
			if strings.Contains(lowerName, k) {
				rulesList = append(rulesList, defs...)
				break
			}
		}

		seen := make(map[string]bool)
		uniqueRules := make([]string, 0)
		for _, r := range rulesList {
			if !seen[r] {
				seen[r] = true
				uniqueRules = append(uniqueRules, r)
			}
		}

		rsTags := groupRuleSets[pg.Name]
		if len(rsTags) == 0 && sanitizedName != pg.Name {
			rsTags = groupRuleSets[sanitizedName]
		}
		if len(rsTags) == 0 {
			if strings.Contains(lowerName, "gemini") {
				rsTags = []string{"geosite-google-gemini", "geosite-openai"}
			} else if strings.Contains(lowerName, "google") {
				rsTags = []string{"geosite-google"}
			} else if strings.Contains(lowerName, "chatgpt") || strings.Contains(lowerName, "openai") {
				rsTags = []string{"geosite-openai"}
			} else if strings.Contains(lowerName, "youtube") {
				rsTags = []string{"geosite-youtube"}
			} else if strings.Contains(lowerName, "telegram") {
				rsTags = []string{"geosite-telegram"}
			}
		}

		grpName := sanitizedName
		if isMatch && !strings.Contains(grpName, "MATCH") {
			grpName = grpName + " (MATCH 兜底)"
		}

		url := pg.URL
		if isAutoSelect && url == "" {
			url = "https://cp.cloudflare.com/generate_204"
		}
		interval := pg.Interval
		if isAutoSelect && interval <= 0 {
			interval = 300
		}
		tolerance := pg.Tolerance
		if isAutoSelect && tolerance <= 0 {
			tolerance = 50
		}

		grpKeywords := groupKeywords[pg.Name]
		if len(grpKeywords) == 0 && sanitizedName != pg.Name {
			grpKeywords = groupKeywords[sanitizedName]
		}

		grp := config.StrategyGroup{
			Id:           id,
			Name:         grpName,
			Type:         groupType,
			Icon:         icon,
			TargetNodeId: "",
			RuleSetTags:  rsTags,
			Rules:        uniqueRules,
			Keywords:     grpKeywords,
			Enabled:      true,
			URL:          url,
			Interval:     interval,
			Tolerance:    tolerance,
		}

		if isMatch {
			matchGroup = &grp
		} else {
			groups = append(groups, grp)
		}
	}

	if matchGroup != nil {
		groups = append(groups, *matchGroup)
	} else {
		groups = append(groups, config.StrategyGroup{
			Id:           "match",
			Name:         "节点选择 (MATCH 兜底)",
			Icon:         "🌐",
			TargetNodeId: "",
			Enabled:      true,
		})
	}

	// 5. Parse and fetch rule-providers (e.g. my_proxylist, my_whitelist, google_play, gvt1)
	providers := make([]config.RuleProvider, 0)
	ruleTargets := make(map[string]string)
	for _, rule := range cc.Rules {
		parts := strings.Split(rule, ",")
		if len(parts) >= 3 && strings.ToUpper(strings.TrimSpace(parts[0])) == "RULE-SET" {
			rName := strings.TrimSpace(parts[1])
			rTarget := strings.TrimSpace(parts[2])
			ruleTargets[rName] = rTarget
		}
	}

	// Cache lookup from existing config for fallback
	cachedMap := make(map[string]config.RuleProvider)
	if existingCfg, err := config.LoadConfig(); err == nil && existingCfg != nil {
		for _, p := range existingCfg.RuleProviders {
			cachedMap[p.Id] = p
		}
	}

	for name, rp := range cc.RuleProviders {
		targetGroup := ruleTargets[name]
		targetId := "match"
		if targetGroup == "DIRECT" {
			targetId = "DIRECT"
		} else if targetGroup == "REJECT" {
			targetId = "REJECT"
		} else if strings.Contains(targetGroup, "Gemini") {
			targetId = "gemini"
		} else if strings.Contains(targetGroup, "ChatGPT") || strings.Contains(targetGroup, "OpenAI") {
			targetId = "chatgpt"
		} else if strings.Contains(targetGroup, "YouTube") {
			targetId = "youtube"
		} else if strings.Contains(targetGroup, "Telegram") {
			targetId = "telegram"
		} else if strings.Contains(targetGroup, "Google") {
			targetId = "google"
		} else if strings.Contains(targetGroup, "自动选择") {
			targetId = "auto_select"
		} else if targetGroup == "节点选择" || targetGroup == matchGroupName {
			targetId = "match"
		}

		prov := config.RuleProvider{
			Id:       name,
			Name:     name,
			Url:      rp.Url,
			Type:     rp.Type,
			Behavior: rp.Behavior,
			Format:   rp.Format,
			Target:   targetId,
			Domains:  make([]string, 0),
			Suffixes: make([]string, 0),
			Keywords: make([]string, 0),
			Ips:      make([]string, 0),
			Updated:  time.Now().Format("2006-01-02 15:04"),
		}

		providers = append(providers, prov)
	}

	// Concurrent Rule-Providers fetch with worker pool (max concurrency 6, timeout 8s)
	type ruleTask struct {
		idx int
		url string
	}
	var tasks []ruleTask
	for i := range providers {
		if providers[i].Url != "" {
			tasks = append(tasks, ruleTask{idx: i, url: providers[i].Url})
		}
	}

	if len(tasks) > 0 {
		var wg sync.WaitGroup
		sem := make(chan struct{}, 6)
		for _, t := range tasks {
			wg.Add(1)
			sem <- struct{}{}
			go func(task ruleTask) {
				defer func() {
					<-sem
					wg.Done()
				}()
				ok := fetchRuleProviderPayload(&providers[task.idx], task.url)
				if !ok {
					// Fallback to cached rule-provider if available
					if cached, found := cachedMap[providers[task.idx].Id]; found && cached.Count > 0 {
						providers[task.idx].Domains = cached.Domains
						providers[task.idx].Suffixes = cached.Suffixes
						providers[task.idx].Keywords = cached.Keywords
						providers[task.idx].Ips = cached.Ips
						providers[task.idx].Count = cached.Count
						providers[task.idx].Updated = cached.Updated
						if cached.Format != "" {
							providers[task.idx].Format = cached.Format
						}
					}
				}
			}(t)
		}
		wg.Wait()
	}

	return &FetchResult{
		Nodes:               nodes,
		Groups:              groups,
		Providers:           providers,
		DirectRuleSetTags:   directRuleSetTags,
		CustomDirectDomains: customDirectDomains,
	}, nil
}

func fetchRuleProviderPayload(prov *config.RuleProvider, url string) bool {
	// Try proxy first (6s timeout), fallback to direct (6s timeout)
	bodyBytes, err := fetchHttpWithTimeout(url, "Mozilla/5.0", 6*time.Second, "http://127.0.0.1:10809")
	if err != nil {
		bodyBytes, err = fetchHttpWithTimeout(url, "Mozilla/5.0", 6*time.Second, "")
	}
	if err != nil {
		return false
	}

	var yObj struct {
		Payload []string `yaml:"payload"`
	}
	if err := yaml.Unmarshal(bodyBytes, &yObj); err == nil && len(yObj.Payload) > 0 {
		if prov.Id == "reject" && len(yObj.Payload) > 2000 {
			prov.Count = len(yObj.Payload)
			prov.Format = "srs (内置二进制加速)"
			return true
		}
		parseRuleItems(prov, yObj.Payload)
		prov.Count = len(yObj.Payload)
		return true
	}

	lines := strings.Split(string(bodyBytes), "\n")
	var rawItems []string
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l != "" && !strings.HasPrefix(l, "#") && !strings.HasPrefix(l, "//") {
			rawItems = append(rawItems, l)
		}
	}
	if len(rawItems) > 0 {
		if prov.Id == "reject" && len(rawItems) > 2000 {
			prov.Count = len(rawItems)
			prov.Format = "srs (内置二进制加速)"
			return true
		}
		parseRuleItems(prov, rawItems)
		prov.Count = len(rawItems)
		return true
	}
	return false
}

func parseRuleItems(prov *config.RuleProvider, items []string) {
	for _, it := range items {
		it = strings.TrimSpace(it)
		parts := strings.Split(it, ",")
		if len(parts) >= 2 {
			rtype := strings.ToUpper(strings.TrimSpace(parts[0]))
			val := strings.TrimSpace(parts[1])
			if rtype == "DOMAIN" {
				prov.Domains = append(prov.Domains, val)
			} else if rtype == "DOMAIN-SUFFIX" {
				prov.Suffixes = append(prov.Suffixes, val)
			} else if rtype == "DOMAIN-KEYWORD" {
				prov.Keywords = append(prov.Keywords, val)
			} else if rtype == "IP-CIDR" || rtype == "IP-CIDR6" {
				prov.Ips = append(prov.Ips, val)
			}
		} else if strings.HasPrefix(it, "+.") {
			prov.Suffixes = append(prov.Suffixes, strings.TrimPrefix(it, "+."))
		} else if strings.HasPrefix(it, ".") {
			prov.Suffixes = append(prov.Suffixes, strings.TrimPrefix(it, "."))
		} else if strings.Contains(it, "/") {
			prov.Ips = append(prov.Ips, it)
		} else if it != "" {
			prov.Domains = append(prov.Domains, it)
		}
	}
}

func parseV2RayLinks(content string) ([]config.Node, error) {
	lines := strings.Split(content, "\n")
	nodes := make([]config.Node, 0)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "vless://") {
			node, err := parseVlessLink(line)
			if err == nil {
				nodes = append(nodes, *node)
			}
		} else if strings.HasPrefix(line, "hysteria2://") || strings.HasPrefix(line, "hy2://") {
			node, err := parseHy2Link(line)
			if err == nil {
				nodes = append(nodes, *node)
			}
		} else if strings.HasPrefix(line, "vmess://") {
			node, err := parseVmessLink(line)
			if err == nil {
				nodes = append(nodes, *node)
			}
		} else if strings.HasPrefix(line, "ss://") {
			if node, err := parseSsLink(line); err == nil {
				nodes = append(nodes, *node)
			}
		} else if strings.HasPrefix(line, "trojan://") {
			if node, err := parseTrojanLink(line); err == nil {
				nodes = append(nodes, *node)
			}
		}
	}

	return nodes, nil
}

func parseVlessLink(raw string) (*config.Node, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}

	port, _ := strconv.Atoi(u.Port())
	q := u.Query()
	remark := SanitizeRemark(u.Fragment)
	if remark == "" || remark == "Default Node" {
		remark = u.Hostname()
	}

	return &config.Node{
		Id:          makeNodeId(remark, u.Hostname(), port),
		Remark:      remark,
		Type:        "vless",
		Server:      u.Hostname(),
		Port:        port,
		Uuid:        u.User.Username(),
		Flow:        q.Get("flow"),
		Sni:         q.Get("sni"),
		PublicKey:   q.Get("pbk"),
		ShortId:     q.Get("sid"),
		Fingerprint: q.Get("fp"),
		Latency:     -1,
	}, nil
}

func parseHy2Link(raw string) (*config.Node, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	port, _ := strconv.Atoi(u.Port())
	pwd := u.User.Username()
	if p, ok := u.User.Password(); ok {
		pwd = p
	}
	remark := SanitizeRemark(u.Fragment)
	if remark == "" || remark == "Default Node" {
		remark = u.Hostname()
	}

	return &config.Node{
		Id:       makeNodeId(remark, u.Hostname(), port),
		Remark:   remark,
		Type:     "hysteria2",
		Server:   u.Hostname(),
		Port:     port,
		Password: pwd,
		Sni:      u.Query().Get("sni"),
		Insecure: u.Query().Get("insecure") == "1",
		Latency:  -1,
	}, nil
}

func parseVmessLink(raw string) (*config.Node, error) {
	b64 := strings.TrimPrefix(raw, "vmess://")
	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, err
	}
	var data map[string]interface{}
	if err := json.Unmarshal(decoded, &data); err != nil {
		return nil, err
	}

	remark := SanitizeRemark(fmt.Sprintf("%v", data["ps"]))
	add := fmt.Sprintf("%v", data["add"])
	portStr := fmt.Sprintf("%v", data["port"])
	port, _ := strconv.Atoi(portStr)

	return &config.Node{
		Id:       makeNodeId(remark, add, port),
		Remark:   remark,
		Type:     "vmess",
		Server:   add,
		Port:     port,
		Uuid:     fmt.Sprintf("%v", data["id"]),
		Sni:      fmt.Sprintf("%v", data["sni"]),
		Insecure: fmt.Sprintf("%v", data["tls"]) == "tls",
		Latency:  -1,
	}, nil
}


var adSuffixRegex = regexp.MustCompile(`(?i)\s*[|｜-]\s*(剩余流量|官网|到期时间|TG群|通知|群组).*$`)

// SanitizeRemark cleans up mojibake, unescapes url characters, strips invalid utf8, and cleans ads
func SanitizeRemark(raw string) string {
	if raw == "" {
		return "Default Node"
	}

	// 1. URL Query Unescape for percentage encodings (%E9%A6%99%E6%B8%AF)
	decoded, err := url.QueryUnescape(raw)
	if err == nil {
		raw = decoded
	}

	// 2. Trim whitespace
	raw = strings.TrimSpace(raw)

	// 3. Ensure valid UTF-8, replace or drop corrupt byte sequences
	if !utf8.ValidString(raw) {
		raw = strings.ToValidUTF8(raw, "")
	}

	// 4. Drop non-printable control characters
	raw = strings.Map(func(r rune) rune {
		if r < 32 && r != ' ' {
			return -1
		}
		return r
	}, raw)

	// 5. Trim common airport promotional suffixes
	raw = adSuffixRegex.ReplaceAllString(raw, "")

	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "Node"
	}
	return raw
}

func makeNodeId(remark, server string, port int) string {
	h := md5.New()
	h.Write([]byte(fmt.Sprintf("%s-%s-%d", remark, server, port)))
	return hex.EncodeToString(h.Sum(nil))[:16]
}


func parseSsLink(raw string) (*config.Node, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	remark := SanitizeRemark(u.Fragment)
	if remark == "" || remark == "Default Node" {
		remark = u.Hostname()
	}

	var method, password string
	if u.User != nil {
		// SIP002 format: ss://BASE64(method:password)@server:port
		userInfo := u.User.Username()
		decoded, err := base64.RawURLEncoding.DecodeString(userInfo)
		if err != nil {
			decoded, err = base64.StdEncoding.DecodeString(userInfo)
		}
		if err == nil && strings.Contains(string(decoded), ":") {
			parts := strings.SplitN(string(decoded), ":", 2)
			method, password = parts[0], parts[1]
		} else if p, ok := u.User.Password(); ok {
			method, password = userInfo, p
		}
	} else {
		// Legacy format: ss://BASE64(method:password@server:port)
		b64 := strings.Split(strings.TrimPrefix(raw, "ss://"), "#")[0]
		decoded, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			decoded, err = base64.RawURLEncoding.DecodeString(b64)
		}
		if err == nil {
			parts := strings.SplitN(string(decoded), "@", 2)
			if len(parts) == 2 {
				authParts := strings.SplitN(parts[0], ":", 2)
				if len(authParts) == 2 {
					method, password = authParts[0], authParts[1]
				}
				h, p, err := net.SplitHostPort(parts[1])
				if err == nil {
					port, _ := strconv.Atoi(p)
					return &config.Node{
						Id:       makeNodeId(remark, h, port),
						Remark:   remark,
						Type:     "shadowsocks",
						Server:   h,
						Port:     port,
						Method:   method,
						Password: password,
						Latency:  -1,
					}, nil
				}
			}
		}
	}

	port, _ := strconv.Atoi(u.Port())
	return &config.Node{
		Id:       makeNodeId(remark, u.Hostname(), port),
		Remark:   remark,
		Type:     "shadowsocks",
		Server:   u.Hostname(),
		Port:     port,
		Method:   method,
		Password: password,
		Latency:  -1,
	}, nil
}

func parseTrojanLink(raw string) (*config.Node, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	port, _ := strconv.Atoi(u.Port())
	remark := SanitizeRemark(u.Fragment)
	if remark == "" || remark == "Default Node" {
		remark = u.Hostname()
	}
	sni := u.Query().Get("sni")
	if sni == "" {
		sni = u.Query().Get("peer")
	}
	if sni == "" {
		sni = u.Hostname()
	}

	return &config.Node{
		Id:       makeNodeId(remark, u.Hostname(), port),
		Remark:   remark,
		Type:     "trojan",
		Server:   u.Hostname(),
		Port:     port,
		Password: u.User.Username(),
		Sni:      sni,
		Insecure: u.Query().Get("allowInsecure") == "1",
		Latency:  -1,
	}, nil
}
