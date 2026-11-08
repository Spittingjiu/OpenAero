//go:build linux

package config

import (
	"encoding/binary"
	"net"
	"syscall"
)

type rtAttr struct {
	attrType uint16
	value    []byte
}

func parseRtAttrs(b []byte) []rtAttr {
	var attrs []rtAttr
	for len(b) >= 4 {
		rtaLen := binary.LittleEndian.Uint16(b[0:2])
		rtaType := binary.LittleEndian.Uint16(b[2:4])
		if rtaLen < 4 || int(rtaLen) > len(b) {
			break
		}
		val := b[4:rtaLen]
		attrs = append(attrs, rtAttr{attrType: rtaType, value: val})
		aligned := (int(rtaLen) + 3) & ^3
		if aligned > len(b) {
			break
		}
		b = b[aligned:]
	}
	return attrs
}

// CheckKernelRulesAndRoutes inspects kernel FIB policy rules (priorities 8987, 8988, 8989)
// and table 2022 routes in-memory via Netlink with zero subprocess forks.
// Returns (rulesOK, routesOK).
func CheckKernelRulesAndRoutes(isFakeIP bool, fakeRange string) (bool, bool) {
	// 1. Check Policy Rules via RTM_GETRULE
	ribRules, err := syscall.NetlinkRIB(syscall.RTM_GETRULE, syscall.AF_INET)
	if err != nil {
		return false, false
	}
	ruleMsgs, err := syscall.ParseNetlinkMessage(ribRules)
	if err != nil {
		return false, false
	}

	var expectedFakeIP net.IP
	var expectedFakeDstLen uint8 = 15
	if isFakeIP {
		if _, ipNet, err := net.ParseCIDR(fakeRange); err == nil {
			expectedFakeIP = ipNet.IP
			if sz, _ := ipNet.Mask.Size(); sz > 0 {
				expectedFakeDstLen = uint8(sz)
			}
		} else {
			expectedFakeIP = net.ParseIP("198.18.0.0")
		}
	}

	ip223 := net.ParseIP("223.5.5.5")
	ip119 := net.ParseIP("119.29.29.29")
	ipLan := net.ParseIP("192.168.0.0")

	has8987_223 := false
	has8987_119 := false
	has8987_lan := false
	has8987_other := false
	has8988 := false
	has8989 := false
	count8987 := 0

	for _, m := range ruleMsgs {
		if m.Header.Type != 32 || len(m.Data) < 12 { // 32 = RTM_NEWRULE
			continue
		}
		dstLen := m.Data[1]
		table := uint32(m.Data[4])
		attrs := parseRtAttrs(m.Data[12:])

		var prio uint32 = 0
		var dstIP net.IP

		for _, a := range attrs {
			switch a.attrType {
			case 6: // FRA_PRIORITY
				if len(a.value) >= 4 {
					prio = binary.LittleEndian.Uint32(a.value)
				}
			case 15: // FRA_TABLE
				if len(a.value) >= 4 {
					table = binary.LittleEndian.Uint32(a.value)
				}
			case 1: // FRA_DST
				if len(a.value) == 4 {
					dstIP = net.IP(a.value)
				}
			}
		}

		if prio == 8987 {
			count8987++
			if dstIP != nil && dstIP.Equal(ip223) && dstLen == 32 {
				has8987_223 = true
			} else if dstIP != nil && dstIP.Equal(ip119) && dstLen == 32 {
				has8987_119 = true
			} else if dstIP != nil && dstIP.Equal(ipLan) && dstLen == 16 {
				has8987_lan = true
			} else {
				has8987_other = true
			}
		} else if prio == 8988 {
			if table == 2022 {
				if expectedFakeIP != nil && dstIP != nil && dstIP.Equal(expectedFakeIP) && dstLen == expectedFakeDstLen {
					has8988 = true
				} else if dstLen == expectedFakeDstLen {
					has8988 = true
				}
			}
		} else if prio == 8989 {
			has8989 = true
		}
	}

	rulesOK := has8987_223 && has8987_119 && has8987_lan && (has8987_other || count8987 >= 4) && has8989
	if isFakeIP && !has8988 {
		rulesOK = false
	}

	// 2. Check Table 2022 Routes via RTM_GETROUTE
	ribRoutes, err := syscall.NetlinkRIB(syscall.RTM_GETROUTE, syscall.AF_INET)
	if err != nil {
		return rulesOK, false
	}
	routeMsgs, err := syscall.ParseNetlinkMessage(ribRoutes)
	if err != nil {
		return rulesOK, false
	}

	hasSplitRoutes := false
	hasDefaultRoute := false
	hasFakeRoute := false

	for _, m := range routeMsgs {
		if m.Header.Type != 24 || len(m.Data) < 12 { // 24 = RTM_NEWROUTE
			continue
		}
		dstLen := m.Data[1]
		table := uint32(m.Data[4])
		attrs := parseRtAttrs(m.Data[12:])

		var dstIP net.IP
		for _, a := range attrs {
			switch a.attrType {
			case 15: // RTA_TABLE
				if len(a.value) >= 4 {
					table = binary.LittleEndian.Uint32(a.value)
				}
			case 1: // RTA_DST
				if len(a.value) == 4 {
					dstIP = net.IP(a.value)
				}
			}
		}

		if table == 2022 {
			if dstLen == 0 {
				hasDefaultRoute = true
			} else if dstLen >= 1 && dstLen <= 8 {
				hasSplitRoutes = true
			}
			if isFakeIP {
				if expectedFakeIP != nil && dstIP != nil && dstIP.Equal(expectedFakeIP) && dstLen == expectedFakeDstLen {
					hasFakeRoute = true
				} else if dstLen == expectedFakeDstLen {
					hasFakeRoute = true
				}
			}
		}
	}

	routesOK := false
	if hasSplitRoutes {
		// Crucial anti-looping rule: When sing-box auto_route generated split routes,
		// table 2022 MUST NOT have a default (0.0.0.0/0) route that captures route_exclude_address.
		if !hasDefaultRoute && (!isFakeIP || hasFakeRoute) {
			routesOK = true
		}
	} else {
		// Fallback when sing-box auto_route split routes are not present
		if hasDefaultRoute && (!isFakeIP || hasFakeRoute) {
			routesOK = true
		}
	}

	return rulesOK, routesOK
}
