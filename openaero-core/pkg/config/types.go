package config

import (
	"encoding/json"
	"os"
)

type RuleProvider struct {
	Id       string   `json:"id"`
	Name     string   `json:"name"`
	Url      string   `json:"url"`
	Type     string   `json:"type"`     // "http", "local"
	Behavior string   `json:"behavior"` // "classical", "domain", "ipcidr"
	Format   string   `json:"format"`   // "yaml", "text"
	Target   string   `json:"target"`   // e.g. "DIRECT", "REJECT", "match", "google"
	Domains  []string `json:"domains"`  // Full domains
	Suffixes []string `json:"suffixes"` // Domain suffixes
	Keywords []string `json:"keywords"` // Domain keywords
	Ips      []string `json:"ips"`      // IP-CIDRs
	Count    int      `json:"count"`
	Updated  string   `json:"updated"`
}

type StrategyGroup struct {
	Id           string   `json:"id"`
	Name         string   `json:"name"`
	Type         string   `json:"type,omitempty"` // "selector" or "urltest"
	Icon         string   `json:"icon"`
	TargetNodeId string   `json:"target_node_id"` // Node ID or "GLOBAL" or "DIRECT" or "REJECT" or "auto_select" or "" (follow active)
	RuleSetTags  []string `json:"rule_set_tags"`
	Rules        []string `json:"rules"` // Domain rules
	Keywords     []string `json:"keywords,omitempty"` // Domain keyword rules (e.g. DOMAIN-KEYWORD,antigravity,Gemini)
	Enabled      bool     `json:"enabled"`
	URL          string   `json:"url,omitempty"`
	Interval     int      `json:"interval,omitempty"`
	Tolerance    int      `json:"tolerance,omitempty"`
}

type Node struct {
	Id          string `json:"id"`
	Remark      string `json:"remark"`
	Type        string `json:"type"` // vless, hysteria2, vmess, shadowsocks, anytls
	Server      string `json:"server"`
	Port        int    `json:"port"`
	Password    string `json:"password,omitempty"`
	Method      string `json:"method,omitempty"`
	Uuid        string `json:"uuid,omitempty"`
	Flow        string `json:"flow,omitempty"`
	Sni         string `json:"sni,omitempty"`
	PublicKey   string `json:"public_key,omitempty"`
	ShortId     string `json:"short_id,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Insecure    bool   `json:"insecure"`
	Latency     int    `json:"latency"` // ms, -1 for timeout
	IsActive    bool   `json:"is_active"`
	OrttStatus     string `json:"ortt_status"`      // "healthy" | "broken" | "degraded" | "untested"
	OrttLabel      string `json:"ortt_label"`       // 直观中文诊断文案，如 "🟢 ORTT 正常 (热池:152 · 命中:94%)" 或 "❌ ORTT 失效 (39999 鉴权无响应)"
	OrttRttMs      int    `json:"ortt_rtt_ms"`      // UDP 39999 CmdPing->CmdPong 真实往返延迟 (ms)
	OrttWarmConns  int    `json:"ortt_warm_conns"`  // 远端当前活跃预热连接数
	OrttPoolHits   uint32 `json:"ortt_pool_hits"`   // 远端真实热池命中次数
	OrttPoolMisses uint32 `json:"ortt_pool_misses"` // 远端热池未命中次数
	OrttHitRate    string `json:"ortt_hit_rate"`    // 命中率百分比字符串，如 "93.7%"
	OrttCheckedAt  string `json:"ortt_checked_at"`  // 最近一次检测时间 "15:04:05"
}

type Subscription struct {
	Id          string `json:"id"`
	Name        string `json:"name"`
	Url         string `json:"url"`
	Enabled     bool   `json:"enabled"`
	LastUpdated string `json:"last_updated"`
	NodeCount   int    `json:"node_count"`
}

type AccessControlConfig struct {
	Enabled      bool     `json:"enabled"`
	Mode         string   `json:"mode"`           // "blacklist" (bypass list) or "whitelist" (proxy-only list)
	BypassIPs    []string `json:"bypass_ips"`     // Source IPs bypassing proxy directly
	BypassMACs   []string `json:"bypass_macs"`    // Source MACs bypassing proxy directly
	ProxyOnlyIPs []string `json:"proxy_only_ips"` // Source IPs for proxy only (whitelist mode)
}

type AppConfig struct {
	Enabled             bool                `json:"enabled"`
	Mode                string              `json:"mode"` // rule, global, direct
	DnsMode             string              `json:"dns_mode"` // "fake-ip", "redir-host"
	FakeIpRange         string              `json:"fake_ip_range"` // "198.18.0.0/15"
	SocksPort           int                 `json:"socks_port"`
	HttpPort            int                 `json:"http_port"`
	DnsPort             int                 `json:"dns_port"`
	ActiveNodeId        string              `json:"active_node_id"`
	Ipv6Enabled         bool                `json:"ipv6_enabled"`
	Ipv6Mode            string              `json:"ipv6_mode"` // "drop", "proxy", "disable"
	AutoUpdateInterval  int                 `json:"auto_update_interval"`
	ClashAPIPort        int                 `json:"clash_api_port"`
	AccessControl       AccessControlConfig `json:"access_control"`
	Subscriptions       []Subscription      `json:"subscriptions"`
	StrategyGroups      []StrategyGroup     `json:"strategy_groups"`
	RuleProviders       []RuleProvider      `json:"rule_providers"`
	DirectRuleSetTags   []string            `json:"direct_rule_set_tags,omitempty"`
	CustomDirectDomains []string            `json:"custom_direct_domains"`
	CustomProxyDomains  []string            `json:"custom_proxy_domains"`
	SingMatrixMode      string              `json:"sing_matrix_mode"` // "follow_node", "custom", "disabled"
	SingMatrixVPS       string              `json:"sing_matrix_vps"`
	SingMatrixPort      int                 `json:"sing_matrix_port"`
	OrttMode            string              `json:"ortt_mode"` // "follow_node", "custom", "disabled"
	OrttVPS             string              `json:"ortt_vps"`
	OrttPort            int                 `json:"ortt_port"`
	OrttSecret          string              `json:"ortt_secret,omitempty"`
	WatchdogEnabled     bool                `json:"watchdog_enabled"`
	WatchdogInterval    int                 `json:"watchdog_interval"`
	WatchdogTarget      string              `json:"watchdog_target"`
	BypassMode          bool                `json:"bypass_mode"`
}

func (c *AppConfig) GetOrttMode() string {
	if c.OrttMode != "" {
		return c.OrttMode
	}
	if c.SingMatrixMode != "" {
		return c.SingMatrixMode
	}
	return "follow_node"
}

func (c *AppConfig) GetOrttVPS() string {
	if c.OrttVPS != "" {
		return c.OrttVPS
	}
	return c.SingMatrixVPS
}

func (c *AppConfig) GetOrttPort() int {
	if c.OrttPort > 0 {
		return c.OrttPort
	}
	if c.SingMatrixPort > 0 {
		return c.SingMatrixPort
	}
	return 39999
}

func (c *AppConfig) NormalizeOrtt() {
	mode := c.GetOrttMode()
	vps := c.GetOrttVPS()
	port := c.GetOrttPort()

	c.OrttMode = mode
	c.OrttVPS = vps
	c.OrttPort = port

	c.SingMatrixMode = mode
	c.SingMatrixVPS = vps
	c.SingMatrixPort = port
}

// DefaultGroupDomains defines fallback domains for well-known strategy groups (SSOT)
var DefaultGroupDomains = map[string][]string{
	"gemini": {
		"antigravity.google", "gemini.google.com", "generativelanguage.googleapis.com",
		"makersuite.google.com", "deepmind.google", "aistudio.google.com", "bard.google.com",
		"alkalimakersuite-pa.clients6.google.com",
	},
	"google": {
		"google.com", "google.com.hk", "google.com.tw", "google.co.jp",
		"googleapis.com", "gstatic.com", "googleusercontent.com",
		"googlevideo.com", "1e100.net", "ggpht.com", "android.com", "g.co",
		"googleplay.com", "gvt1.com",
	},
	"chatgpt": {
		"openai.com", "chatgpt.com", "oaistatic.com", "oaiusercontent.com", "ai.com",
	},
	"openai": {
		"openai.com", "chatgpt.com", "oaistatic.com", "oaiusercontent.com", "ai.com",
	},
	"youtube": {
		"youtube.com", "googlevideo.com", "ytimg.com", "youtu.be", "yt.be",
	},
	"telegram": {
		"telegram.org", "t.me", "telegra.ph", "telegram.me", "telesco.pe",
	},
}

func DefaultConfig() *AppConfig {
	return &AppConfig{
		Enabled:            true,
		Mode:               "rule",
		DnsMode:            "fake-ip",
		FakeIpRange:        "198.18.0.0/15",
		SocksPort:          10808,
		HttpPort:           10809,
		DnsPort:            1053,
		Ipv6Enabled:        false,
		Ipv6Mode:           "drop",
		AutoUpdateInterval: 1,
		ClashAPIPort:       9090,
		OrttMode:           "follow_node",
		OrttPort:           39999,
		SingMatrixMode:     "follow_node",
		SingMatrixPort:     39999,
		AccessControl: AccessControlConfig{
			Enabled:      true,
			Mode:         "blacklist",
			BypassIPs:    []string{},
			BypassMACs:   []string{},
			ProxyOnlyIPs: []string{},
		},
		Subscriptions: []Subscription{
			{
				Id:          "sub1",
				Name:        "默认订阅",
				Url:         "",
				Enabled:     true,
				LastUpdated: "",
				NodeCount:   0,
			},
		},
		StrategyGroups: []StrategyGroup{
			{
				Id:           "gemini",
				Name:         "Gemini",
				Icon:         "♊",
				TargetNodeId: "",
				RuleSetTags:  []string{"geosite-google-gemini", "geosite-openai"},
				Rules:        DefaultGroupDomains["gemini"],
				Enabled:      true,
			},
			{
				Id:           "chatgpt",
				Name:         "ChatGPT",
				Icon:         "🤖",
				TargetNodeId: "",
				RuleSetTags:  []string{"geosite-openai"},
				Rules:        DefaultGroupDomains["chatgpt"],
				Enabled:      true,
			},
			{
				Id:           "youtube",
				Name:         "YouTube分流",
				Icon:         "📺",
				TargetNodeId: "",
				RuleSetTags:  []string{"geosite-youtube"},
				Rules:        DefaultGroupDomains["youtube"],
				Enabled:      true,
			},
			{
				Id:           "telegram",
				Name:         "Telegram分流",
				Icon:         "✈️",
				TargetNodeId: "",
				RuleSetTags:  []string{"geosite-telegram"},
				Rules:        DefaultGroupDomains["telegram"],
				Enabled:      true,
			},
			{
				Id:           "google",
				Name:         "Google",
				Icon:         "🔍",
				TargetNodeId: "",
				RuleSetTags:  []string{"geosite-google"},
				Rules:        DefaultGroupDomains["google"],
				Enabled:      true,
			},
			{Id: "match", Name: "节点选择 (MATCH 兜底)", Icon: "🌐", TargetNodeId: "", RuleSetTags: []string{}, Enabled: true},
		},
		RuleProviders:       loadDefaultRuleProviders(),
		CustomDirectDomains: []string{"baidu.com", "qq.com", "tencent.com", "aliyun.com", "taobao.com", "bilibili.com", "zhihu.com"},
		CustomProxyDomains:  []string{},
		OrttVPS:             "",
		SingMatrixVPS:       "",
		OrttSecret:          "",
		WatchdogEnabled:     true,
		WatchdogInterval:    15,
		WatchdogTarget:      "223.5.5.5:53",
		BypassMode:          false,
	}
}

func loadDefaultRuleProviders() []RuleProvider {
	if data, err := os.ReadFile("/etc/openaero/default_providers.json"); err == nil {
		var list []RuleProvider
		if json.Unmarshal(data, &list) == nil {
			return list
		}
	}
	return []RuleProvider{}
}
