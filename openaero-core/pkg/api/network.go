package api

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"openaero-core/pkg/config"
)

// ValidateIP validates and returns a clean normalized IPv4/IPv6 string
func ValidateIP(ipStr string) (string, error) {
	ipStr = strings.TrimSpace(ipStr)
	addr, err := netip.ParseAddr(ipStr)
	if err != nil {
		return "", fmt.Errorf("invalid IP %q: %w", ipStr, err)
	}
	return addr.String(), nil
}

// ValidateIPList validates a list of IP strings, eliminating duplicates and invalids
func ValidateIPList(ips []string) ([]string, error) {
	valid := make([]string, 0, len(ips))
	seen := make(map[string]bool)
	for _, ip := range ips {
		ip = strings.TrimSpace(ip)
		if ip == "" {
			continue
		}
		cleanIP, err := ValidateIP(ip)
		if err != nil {
			return nil, err
		}
		if !seen[cleanIP] {
			seen[cleanIP] = true
			valid = append(valid, cleanIP)
		}
	}
	return valid, nil
}

// ValidateMAC validates and normalizes a MAC address string
func ValidateMAC(macStr string) (string, error) {
	macStr = strings.TrimSpace(macStr)
	hw, err := net.ParseMAC(macStr)
	if err != nil {
		return "", fmt.Errorf("invalid MAC %q: %w", macStr, err)
	}
	return hw.String(), nil
}

// ValidateMACList validates a list of MAC strings
func ValidateMACList(macs []string) ([]string, error) {
	valid := make([]string, 0, len(macs))
	seen := make(map[string]bool)
	for _, m := range macs {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		cleanMAC, err := ValidateMAC(m)
		if err != nil {
			return nil, err
		}
		if !seen[cleanMAC] {
			seen[cleanMAC] = true
			valid = append(valid, cleanMAC)
		}
	}
	return valid, nil
}

// ValidatePrefix validates CIDR prefix strings (e.g. "198.18.0.0/15")
func ValidatePrefix(prefixStr string) (string, error) {
	prefixStr = strings.TrimSpace(prefixStr)
	pfx, err := netip.ParsePrefix(prefixStr)
	if err != nil {
		return "", fmt.Errorf("invalid CIDR prefix %q: %w", prefixStr, err)
	}
	return pfx.String(), nil
}

// ValidateMode validates proxy modes
func ValidateMode(mode string) error {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode != "rule" && mode != "global" && mode != "direct" {
		return fmt.Errorf("invalid mode %q: must be 'rule', 'global', or 'direct'", mode)
	}
	return nil
}

// runCmdLogged executes a command directly without a subshell and logs errors with stderr output
func runCmdLogged(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("[network] command failed: %s %s: %v (output: %s)", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		return fmt.Errorf("%s %s failed: %w (%s)", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// cleanIpRulesByPriority removes IP rules matching a given priority in a safe Go loop
func cleanIpRulesByPriority(prio string) {
	for i := 0; i < 15; i++ {
		if err := exec.Command("ip", "rule", "del", "priority", prio).Run(); err != nil {
			break
		}
	}
}

// runNftTransaction executes an atomic batch of nftables instructions via stdin to nft -f -
func runNftTransaction(rules string) error {
	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(rules)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("[network] nft transaction failed: %v (output: %s)", err, strings.TrimSpace(string(out)))
		return fmt.Errorf("nft transaction error: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// deleteNftRulesByComment removes rules matching a specific comment from a chain without shell piping
func deleteNftRulesByComment(chain, matchComment string) {
	out, err := exec.Command("nft", "-a", "list", "chain", "inet", "fw4", chain).Output()
	if err != nil {
		return
	}
	lines := strings.Split(string(out), "\n")
	handles := make([]string, 0)
	for _, line := range lines {
		if strings.Contains(line, matchComment) {
			idx := strings.LastIndex(line, "handle ")
			if idx != -1 {
				h := strings.TrimSpace(line[idx+7:])
				if h != "" {
					handles = append(handles, h)
				}
			}
		}
	}
	for _, h := range handles {
		_ = exec.Command("nft", "delete", "rule", "inet", "fw4", chain, "handle", h).Run()
	}
}

// getRouterLanIP discovers the current router LAN IPv4 dynamically without hardcoded addresses (Zero-Fork Pure Go)
func getRouterLanIP() string {
	if iface, err := net.InterfaceByName("br-lan"); err == nil {
		if addrs, err := iface.Addrs(); err == nil {
			for _, a := range addrs {
				if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
					return ipnet.IP.String()
				}
			}
		}
	}
	// Secondary: Read /etc/config/network in pure Go without uci fork
	if data, err := os.ReadFile("/etc/config/network"); err == nil {
		lines := strings.Split(string(data), "\n")
		inLan := false
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "config interface 'lan'") || strings.HasPrefix(trimmed, "config interface lan") {
				inLan = true
				continue
			}
			if inLan && strings.HasPrefix(trimmed, "config interface") {
				inLan = false
			}
			if inLan && strings.HasPrefix(trimmed, "option ipaddr") {
				parts := strings.Fields(trimmed)
				if len(parts) >= 3 {
					rawIP := strings.Trim(parts[2], "'\"")
					if net.ParseIP(rawIP) != nil {
						return rawIP
					}
				}
			}
		}
	}
	if ifaces, err := net.Interfaces(); err == nil {
		for _, iface := range ifaces {
			if (iface.Flags&net.FlagUp) != 0 && (iface.Flags&net.FlagLoopback) == 0 {
				if addrs, err := iface.Addrs(); err == nil {
					for _, a := range addrs {
						if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
							return ipnet.IP.String()
						}
					}
				}
			}
		}
	}
	return "127.0.0.1"
}

// ensureRouterHosts updates /etc/hosts with router.lan mapping dynamically
func ensureRouterHosts() {
	routerIP := getRouterLanIP()
	if routerIP == "" || routerIP == "127.0.0.1" {
		return
	}
	hostsData, err := os.ReadFile("/etc/hosts")
	if err != nil {
		return
	}
	content := string(hostsData)
	expectedLine := fmt.Sprintf("%s router.lan openwrt.lan istoreos.lan", routerIP)

	lines := strings.Split(content, "\n")
	newLines := make([]string, 0, len(lines)+1)
	found := false
	changed := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "router.lan") {
			found = true
			if trimmed != expectedLine {
				newLines = append(newLines, expectedLine)
				changed = true
			} else {
				newLines = append(newLines, line)
			}
		} else {
			newLines = append(newLines, line)
		}
	}

	if !found {
		newLines = append(newLines, expectedLine)
		changed = true
	}

	if changed {
		_ = os.WriteFile("/etc/hosts", []byte(strings.Join(newLines, "\n")+"\n"), 0644)
		_ = exec.Command("/etc/init.d/dnsmasq", "reload").Run()
	}
}

func ensureSysctlNoRedirects() {
	_ = config.WriteSysctl("net.ipv4.conf.all.send_redirects", "0")
	_ = config.WriteSysctl("net.ipv4.conf.default.send_redirects", "0")
	_ = config.WriteSysctl("net.ipv4.conf.br-lan.send_redirects", "0")

	sysctlPath := "/etc/sysctl.conf"
	data, err := os.ReadFile(sysctlPath)
	if err == nil {
		content := string(data)
		if !strings.Contains(content, "net.ipv4.conf.br-lan.send_redirects") {
			f, err := os.OpenFile(sysctlPath, os.O_APPEND|os.O_WRONLY, 0644)
			if err == nil {
				_, _ = f.WriteString("\nnet.ipv4.conf.all.send_redirects = 0\nnet.ipv4.conf.default.send_redirects = 0\nnet.ipv4.conf.br-lan.send_redirects = 0\n")
				_ = f.Close()
			}
		}
	}
}

// applyHighSpeedSysctl tunes kernel network stack for ultra-low latency & maximum throughput (Zero-Fork Pure Go)
func applyHighSpeedSysctl() {
	_ = config.WriteSysctl("net.ipv4.tcp_congestion_control", "bbr")
	_ = config.WriteSysctl("net.ipv4.tcp_fastopen", "3")
	_ = config.WriteSysctl("net.core.rmem_max", "33554432")
	_ = config.WriteSysctl("net.core.wmem_max", "33554432")
	_ = config.WriteSysctl("net.ipv4.tcp_rmem", "4096 131072 33554432")
	_ = config.WriteSysctl("net.ipv4.tcp_wmem", "4096 65536 33554432")
	_ = config.WriteSysctl("net.ipv4.tcp_mtu_probing", "1")
	_ = config.WriteSysctl("net.ipv4.tcp_tw_reuse", "1")
	_ = config.WriteSysctl("net.ipv4.tcp_slow_start_after_idle", "0")
	_ = config.WriteSysctl("net.core.netdev_max_backlog", "16384")

	sysctlPath := "/etc/sysctl.conf"
	data, err := os.ReadFile(sysctlPath)
	if err == nil {
		content := string(data)
		if !strings.Contains(content, "net.ipv4.tcp_slow_start_after_idle") {
			f, err := os.OpenFile(sysctlPath, os.O_APPEND|os.O_WRONLY, 0644)
			if err == nil {
				_, _ = f.WriteString("\n# OpenAero High-Speed Network Tuning\nnet.ipv4.tcp_congestion_control = bbr\nnet.ipv4.tcp_fastopen = 3\nnet.core.rmem_max = 33554432\nnet.core.wmem_max = 33554432\nnet.ipv4.tcp_rmem = 4096 131072 33554432\nnet.ipv4.tcp_wmem = 4096 65536 33554432\nnet.ipv4.tcp_mtu_probing = 1\nnet.ipv4.tcp_tw_reuse = 1\nnet.ipv4.tcp_slow_start_after_idle = 0\nnet.core.netdev_max_backlog = 16384\n")
				_ = f.Close()
			}
		}
	}
}

// CleanupAllOpenAeroFirewallAndRoutes purges all openaero firewall hooks, sets, and routes
func CleanupAllOpenAeroFirewallAndRoutes() {
	_ = os.Remove("/usr/share/nftables.d/chain-post/dstnat/30-openaero.nft")
	_ = os.Remove("/usr/share/nftables.d/chain-post/dstnat/20-openaero-acl.nft")
	_ = os.Remove("/usr/share/nftables.d/table-post/10-openaero-acl.nft")
	_ = os.Remove("/usr/share/nftables.d/chain-pre/srcnat/10-openaero-srcnat.nft")

	if entries, err := os.ReadDir("/usr/share/nftables.d"); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				subDir := "/usr/share/nftables.d/" + e.Name()
				if subEntries, err := os.ReadDir(subDir); err == nil {
					for _, se := range subEntries {
						if strings.Contains(se.Name(), "openaero") {
							_ = os.Remove(subDir + "/" + se.Name())
						}
					}
				}
			}
		}
	}

	if _, err := os.Stat("/etc/init.d/firewall"); err == nil {
		_ = exec.Command("/etc/init.d/firewall", "reload").Run()
	} else {
		_ = exec.Command("fw4", "reload").Run()
	}

	for _, chain := range []string{"dstnat", "forward", "srcnat"} {
		deleteNftRulesByComment(chain, "openaero")
	}

	flushSets := "flush set inet fw4 openaero_bypass\n" +
		"delete set inet fw4 openaero_bypass\n" +
		"flush set inet fw4 openaero_bypass_macs\n" +
		"delete set inet fw4 openaero_bypass_macs\n"
	_ = runNftTransaction(flushSets)

	_ = exec.Command("ip", "route", "flush", "table", "2022").Run()
	_ = exec.Command("ip", "route", "del", "198.18.0.0/15", "dev", "openaero0").Run()

	for i := 0; i < 15; i++ {
		if err := exec.Command("ip", "rule", "del", "fwmark", "1", "table", "2022").Run(); err != nil {
			break
		}
	}
	for _, prio := range []string{"8987", "8988", "8989", "8990"} {
		cleanIpRulesByPriority(prio)
	}
}

func applyACLNetwork(cfg *config.AppConfig) {
	if cfg == nil {
		return
	}

	// 1. Clear old ip rule with priority 8990
	cleanIpRulesByPriority("8990")

	// 2. Ensure directories for nftables fw4 persistence exist
	_ = os.MkdirAll("/usr/share/nftables.d/table-post", 0755)
	_ = os.MkdirAll("/usr/share/nftables.d/chain-post/dstnat", 0755)

	tablePostFile := "/usr/share/nftables.d/table-post/10-openaero-acl.nft"
	dstnatFile := "/usr/share/nftables.d/chain-post/dstnat/20-openaero-acl.nft"

	if cfg.AccessControl.Enabled {
		// Collect and strictly validate all bypass IPs
		rawBypassIPs, _ := ValidateIPList(cfg.AccessControl.BypassIPs)
		allBypassIPs := make([]string, 0, len(rawBypassIPs))
		seenIPs := make(map[string]bool)
		for _, ip := range rawBypassIPs {
			if !seenIPs[ip] {
				seenIPs[ip] = true
				allBypassIPs = append(allBypassIPs, ip)
			}
		}

		// Collect and strictly validate all bypass MACs
		rawBypassMACs, _ := ValidateMACList(cfg.AccessControl.BypassMACs)
		allBypassMACs := make([]string, 0, len(rawBypassMACs))
		seenMACs := make(map[string]bool)
		for _, mac := range rawBypassMACs {
			if !seenMACs[mac] {
				seenMACs[mac] = true
				allBypassMACs = append(allBypassMACs, mac)

				// Dynamically resolve MAC to bound IP addresses
				for _, rip := range config.ResolveMacToIPs(mac) {
					if cleanRip, err := ValidateIP(rip); err == nil && !seenIPs[cleanRip] {
						seenIPs[cleanRip] = true
						allBypassIPs = append(allBypassIPs, cleanRip)
					}
				}
			}
		}

		// Apply Linux kernel policy routing Priority 8990 (table main -> direct WAN)
		for _, ip := range allBypassIPs {
			if !strings.Contains(ip, "/") {
				_ = exec.Command("ip", "rule", "add", "from", ip, "lookup", "main", "priority", "8990").Run()
			}
		}

		// Atomic nftables transaction to define and populate sets
		nftTx := "add set inet fw4 openaero_bypass { type ipv4_addr; }\n" +
			"flush set inet fw4 openaero_bypass\n" +
			"add set inet fw4 openaero_bypass_macs { type ether_addr; }\n" +
			"flush set inet fw4 openaero_bypass_macs\n"

		if len(allBypassIPs) > 0 {
			nftTx += fmt.Sprintf("add element inet fw4 openaero_bypass { %s }\n", strings.Join(allBypassIPs, ", "))
		}
		if len(allBypassMACs) > 0 {
			nftTx += fmt.Sprintf("add element inet fw4 openaero_bypass_macs { %s }\n", strings.Join(allBypassMACs, ", "))
		}
		_ = runNftTransaction(nftTx)

		// Insert immediate in-memory bypass rules in dstnat via atomic nft transaction
		deleteNftRulesByComment("dstnat", "openaero_acl_bypass")

		var aclRules strings.Builder
		if len(allBypassIPs) > 0 {
			aclRules.WriteString("insert rule inet fw4 dstnat ip saddr @openaero_bypass return comment \"openaero_acl_bypass\"\n")
			aclRules.WriteString("insert rule inet fw4 dstnat ip saddr @openaero_bypass meta l4proto { tcp, udp } th dport 53 dnat ip to 223.5.5.5:53 comment \"openaero_acl_bypass_dns\"\n")
		}
		if len(allBypassMACs) > 0 {
			aclRules.WriteString("insert rule inet fw4 dstnat ether saddr @openaero_bypass_macs return comment \"openaero_acl_bypass_mac\"\n")
			aclRules.WriteString("insert rule inet fw4 dstnat ether saddr @openaero_bypass_macs meta l4proto { tcp, udp } th dport 53 dnat ip to 223.5.5.5:53 comment \"openaero_acl_bypass_mac_dns\"\n")
		}
		if aclRules.Len() > 0 {
			if err := runNftTransaction(aclRules.String()); err != nil {
				log.Printf("[network] failed to apply ACL bypass rules: %v", err)
			}
		}

		// Write persistent fw4 drop-in config files
		tablePostContent := "set openaero_bypass {\n\ttype ipv4_addr\n"
		if len(allBypassIPs) > 0 {
			tablePostContent += fmt.Sprintf("\telements = { %s }\n", strings.Join(allBypassIPs, ", "))
		}
		tablePostContent += "}\nset openaero_bypass_macs {\n\ttype ether_addr\n"
		if len(allBypassMACs) > 0 {
			tablePostContent += fmt.Sprintf("\telements = { %s }\n", strings.Join(allBypassMACs, ", "))
		}
		tablePostContent += "}\n"
		_ = os.WriteFile(tablePostFile, []byte(tablePostContent), 0644)

		dstnatContent := "ip saddr @openaero_bypass meta l4proto { tcp, udp } th dport 53 dnat ip to 223.5.5.5:53 comment \"openaero_acl_bypass_dns\";\nether saddr @openaero_bypass_macs meta l4proto { tcp, udp } th dport 53 dnat ip to 223.5.5.5:53 comment \"openaero_acl_bypass_mac_dns\";\nip saddr @openaero_bypass return comment \"openaero_acl_bypass\";\nether saddr @openaero_bypass_macs return comment \"openaero_acl_bypass_mac\";\n"
		_ = os.WriteFile(dstnatFile, []byte(dstnatContent), 0644)
	} else {
		deleteNftRulesByComment("dstnat", "openaero_acl_bypass")
		_ = os.Remove(tablePostFile)
		_ = os.Remove(dstnatFile)
		_ = runNftTransaction("flush set inet fw4 openaero_bypass\nflush set inet fw4 openaero_bypass_macs\n")
	}

	ensureRouterHosts()
}

// ApplyNetworkSettings applies nftables firewall redirection, IP policy routes, and sysctl optimizations
func ApplyNetworkSettings(cfg *config.AppConfig) {
	if cfg == nil || !cfg.Enabled {
		return
	}
	ensureSysctlNoRedirects()
	applyHighSpeedSysctl()

	_ = os.MkdirAll("/usr/share/nftables.d/chain-post/dstnat", 0755)
	dnsNftFile := "/usr/share/nftables.d/chain-post/dstnat/30-openaero.nft"
	dnsPort := cfg.DnsPort
	if dnsPort <= 0 {
		dnsPort = 1053
	}
	dnsNftContent := fmt.Sprintf("udp dport 53 redirect to :%d comment \"!openaero: redirect DNS\"\ntcp dport 53 redirect to :%d comment \"!openaero: redirect DNS\"\n", dnsPort, dnsPort)
	_ = config.AtomicWriteFile(dnsNftFile, []byte(dnsNftContent), 0644)

	deleteNftRulesByComment("dstnat", "!openaero: redirect DNS")
	deleteNftRulesByComment("forward", "!openaero: forward openaero0")
	deleteNftRulesByComment("forward", "!openaero: ipv6")
	deleteNftRulesByComment("srcnat", "!openaero: preserve LAN client")
	deleteNftRulesByComment("srcnat", "!openaero: bypass local router")

	var nftRules strings.Builder
	// 1. DNS Redirect (UDP/TCP 53 -> dnsPort)
	fmt.Fprintf(&nftRules, "add rule inet fw4 dstnat udp dport 53 redirect to :%d comment \"!openaero: redirect DNS\"\n", dnsPort)
	fmt.Fprintf(&nftRules, "add rule inet fw4 dstnat tcp dport 53 redirect to :%d comment \"!openaero: redirect DNS\"\n", dnsPort)
	// 2. TCP MSS Clamping & TUN Forward Accept (oifname / iifname openaero0)
	nftRules.WriteString("insert rule inet fw4 forward oifname \"openaero0\" accept comment \"!openaero: forward openaero0\"\n")
	nftRules.WriteString("insert rule inet fw4 forward iifname \"openaero0\" accept comment \"!openaero: forward openaero0\"\n")
	nftRules.WriteString("insert rule inet fw4 forward oifname \"openaero0\" tcp flags syn / syn,rst tcp option maxseg size set rt mtu comment \"!openaero: forward openaero0\"\n")
	nftRules.WriteString("insert rule inet fw4 forward iifname \"openaero0\" tcp flags syn / syn,rst tcp option maxseg size set rt mtu comment \"!openaero: forward openaero0\"\n")
	// 3. IPv6 Leak Guard (when IPv6 is disabled)
	ipv6NftFile := "/usr/share/nftables.d/chain-pre/forward/10-openaero-ipv6.nft"
	_ = os.MkdirAll("/usr/share/nftables.d/chain-pre/forward", 0755)
	if cfg.Ipv6Enabled {
		_ = os.Remove(ipv6NftFile)
		_ = exec.Command("ip", "-6", "route", "add", "default", "dev", "openaero0", "table", "2022").Run()
	} else {
		ipv6Rule := "meta nfproto ipv6 oifname != \"openaero0\" counter reject comment \"!openaero: ipv6 leak guard\"\n"
		_ = config.AtomicWriteFile(ipv6NftFile, []byte(ipv6Rule), 0644)
		_ = exec.Command("ip", "-6", "route", "del", "default", "dev", "openaero0", "table", "2022").Run()
		nftRules.WriteString("insert rule inet fw4 forward meta nfproto ipv6 oifname != \"openaero0\" counter reject comment \"!openaero: ipv6 leak guard\"\n")
	}

	_ = os.MkdirAll("/usr/share/nftables.d/chain-pre/srcnat", 0755)
	srcnatNftContent := "fib saddr type local return comment \"!openaero: bypass local router outbound from SNAT\"\noifname \"openaero0\" return comment \"!openaero: preserve LAN client original IP\"\n"
	_ = config.AtomicWriteFile("/usr/share/nftables.d/chain-pre/srcnat/10-openaero-srcnat.nft", []byte(srcnatNftContent), 0644)
	nftRules.WriteString("insert rule inet fw4 srcnat oifname \"openaero0\" return comment \"!openaero: preserve LAN client original IP\"\n")
	nftRules.WriteString("insert rule inet fw4 srcnat fib saddr type local return comment \"!openaero: bypass local router outbound from SNAT\"\n")

	if err := runNftTransaction(nftRules.String()); err != nil {
		log.Printf("[network] failed to apply core nftables rules: %v", err)
	}

	cleanIpRulesByPriority("8987")
	cleanIpRulesByPriority("8988")
	cleanIpRulesByPriority("8989")

	_ = runCmdLogged("ip", "rule", "add", "to", "223.5.5.5", "ipproto", "tcp", "lookup", "main", "priority", "8987")
	_ = runCmdLogged("ip", "rule", "add", "to", "119.29.29.29", "ipproto", "tcp", "lookup", "main", "priority", "8987")
	_ = runCmdLogged("ip", "rule", "add", "ipproto", "udp", "dport", "39999", "lookup", "main", "priority", "8987")

	dnsMode := strings.ToLower(strings.TrimSpace(cfg.DnsMode))
	if dnsMode == "" || dnsMode == "fake-ip" {
		fakeRange := cfg.FakeIpRange
		if fakeRange == "" {
			fakeRange = "198.18.0.0/15"
		}
		if cleanRange, err := ValidatePrefix(fakeRange); err == nil {
			fakeRange = cleanRange
		}
		_ = runCmdLogged("ip", "rule", "add", "to", fakeRange, "lookup", "2022", "priority", "8988")
		_ = runCmdLogged("ip", "route", "replace", fakeRange, "dev", "openaero0", "table", "2022")
		_ = runCmdLogged("ip", "route", "replace", fakeRange, "dev", "openaero0")
	}
	_ = runCmdLogged("ip", "rule", "add", "ipproto", "icmp", "lookup", "main", "priority", "8989")

	applyACLNetwork(cfg)
}

func ReloadSingBox(cfg *config.AppConfig) error {
	return ReloadSingBoxEx(cfg, false)
}

func ReloadSingBoxEx(cfg *config.AppConfig, forceRestart bool) error {
	nodeList, _ := config.LoadNodes()
	jsonStr, err := config.CompileSingBoxConfig(cfg, nodeList)
	if err != nil {
		log.Printf("[safe-apply] 编译配置失败: %v", err)
		return err
	}

	// 1. Static Syntax & Protocol Validation
	tmpFile := config.RunConfigFilePath + ".tmp"
	if err := os.WriteFile(tmpFile, []byte(jsonStr), 0644); err != nil {
		log.Printf("[safe-apply] 写入临时配置文件失败: %v", err)
		return err
	}

	checkCmd := exec.Command("/usr/bin/sing-box", "check", "-c", tmpFile)
	if out, err := checkCmd.CombinedOutput(); err != nil {
		log.Printf("[safe-apply] ⚠️ sing-box 校验未通过: %v, 输出: %s", err, string(out))
		_ = os.Remove(tmpFile)
		return fmt.Errorf("配置语法或协议校验未通过: %v (%s)", err, string(out))
	}

	// 2. Ensure checkpoint exists
	if !config.HasGoodCheckpoint() {
		config.BackupGoodConfig()
	}

	// 3. Atomically overwrite active configuration
	_ = os.Rename(tmpFile, config.RunConfigFilePath)
	_ = config.SaveConfig(cfg)

	// 4. Zero-downtime reload or procd restart
	sbPid := config.GetSingBoxPid()
	if forceRestart {
		log.Printf("[safe-apply] 🔄 执行 sing-box 进程重置重启...")
		_ = config.SignalSingBox(syscall.SIGTERM)
		for i := 0; i < 4; i++ {
			time.Sleep(300 * time.Millisecond)
			if config.GetSingBoxPid() > 0 {
				break
			}
		}
		if config.GetSingBoxPid() <= 0 {
			cmd := exec.Command("/usr/bin/sing-box", "run", "-c", config.RunConfigFilePath)
			if err := cmd.Start(); err == nil {
				go func() { _ = cmd.Wait() }()
			}
		}
	} else if sbPid <= 0 {
		for i := 0; i < 4; i++ {
			time.Sleep(250 * time.Millisecond)
			if sbPid = config.GetSingBoxPid(); sbPid > 0 {
				break
			}
		}
		if sbPid <= 0 {
			log.Printf("[safe-apply] 🚀 正在启动 sing-box 内核进程...")
			cmd := exec.Command("/usr/bin/sing-box", "run", "-c", config.RunConfigFilePath)
			if err := cmd.Start(); err == nil {
				go func() { _ = cmd.Wait() }()
			}
		} else {
			log.Printf("[safe-apply] 🚀 发送 SIGHUP 触发 sing-box (PID %d) 热重载...", sbPid)
			_ = config.SignalSingBox(syscall.SIGHUP)
		}
	} else {
		log.Printf("[safe-apply] 🚀 发送 SIGHUP 触发 sing-box (PID %d) 热重载...", sbPid)
		_ = config.SignalSingBox(syscall.SIGHUP)
	}

	ApplyNetworkSettings(cfg)
	config.EnsureTunRoutesAfterReload(cfg)

	// 5. Post-apply Canary Health Probe
	if len(nodeList) == 0 {
		log.Println("[safe-apply] 尚未配置任何代理节点，跳过金丝雀外网探针，直接提交基础配置")
		config.BackupGoodConfig()
		return nil
	}

	time.Sleep(1500 * time.Millisecond)
	socksPort := cfg.SocksPort
	if socksPort <= 0 {
		socksPort = 10808
	}

	probePassed := probeCanaryHealth(cfg.Mode, socksPort, 5*time.Second)
	if !probePassed {
		log.Printf("[safe-apply] ❌ 金丝雀探针失败，触发紧急安全回滚！")
		_ = config.RollbackGoodConfig()
		goodCfg, _ := config.LoadConfig()
		if goodCfg != nil {
			ApplyNetworkSettings(goodCfg)
		}
		sbPid = config.GetSingBoxPid()
		if sbPid > 0 {
			_ = config.SignalSingBox(syscall.SIGHUP)
		} else {
			cmd := exec.Command("/usr/bin/sing-box", "run", "-c", config.RunConfigFilePath)
			if err := cmd.Start(); err == nil {
				go func() { _ = cmd.Wait() }()
			}
		}
		time.Sleep(200 * time.Millisecond)
		if _, err := os.Stat("/sys/class/net/openaero0"); err == nil {
			_ = exec.Command("ip", "route", "add", "default", "dev", "openaero0", "table", "2022").Run()
		}
		return fmt.Errorf("ROLLED_BACK: 所选节点或设置导致网络不可用（金丝雀探测超时），系统已自动安全回退至上一正常配置！")
	}

	config.BackupGoodConfig()
	log.Printf("[safe-apply] ✅ 金丝雀健康自检通过，新配置已成功固化为安全检查点")
	return nil
}

func probeCanaryHealth(mode string, socksPort int, timeout time.Duration) bool {
	if config.GetSingBoxPid() <= 0 {
		log.Println("[canary] sing-box 进程未运行，探针判定失败")
		return false
	}

	if mode == "direct" {
		conn, err := net.DialTimeout("tcp", "223.5.5.5:53", timeout)
		if err != nil {
			log.Printf("[canary] 国内直连探测失败: %v", err)
			return false
		}
		conn.Close()
		return true
	}

	proxyURL, err := url.Parse(fmt.Sprintf("socks5://127.0.0.1:%d", socksPort))
	if err != nil {
		return false
	}

	transport := &http.Transport{
		Proxy: http.ProxyURL(proxyURL),
		DialContext: (&net.Dialer{
			Timeout: timeout,
		}).DialContext,
		DisableKeepAlives: true,
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}

	canaryURLs := []string{
		"http://cp.cloudflare.com/generate_204",
		"http://www.google.com/generate_204",
	}

	for attempt := 1; attempt <= 3; attempt++ {
		for _, u := range canaryURLs {
			t0 := time.Now()
			resp, err := client.Get(u)
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode == 200 || resp.StatusCode == 204 {
					log.Printf("[canary] 探针成功: %s (状态: %d, 耗时: %v, 尝试: %d)", u, resp.StatusCode, time.Since(t0), attempt)
					return true
				}
			} else {
				log.Printf("[canary] 探针重试 %s (尝试 %d): %v", u, attempt, err)
			}
		}
		if attempt < 3 {
			time.Sleep(600 * time.Millisecond)
		}
	}
	return false
}

func getProcessMemory(pid int) string {
	if pid <= 0 {
		return "0 MB"
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return "0 MB"
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				kb, _ := strconv.Atoi(fields[1])
				return fmt.Sprintf("%.1f MB", float64(kb)/1024.0)
			}
		}
	}
	return "0 MB"
}
