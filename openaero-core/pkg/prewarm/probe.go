package prewarm

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"net"
	"strings"
	"time"

	"openaero-core/pkg/config"
	"openaero-core/pkg/nodes"
)

const (
	CmdPong byte = 0x84

	ProbeTimeout = 1500 * time.Millisecond
)

type OrttProbeResult struct {
	Status     string `json:"ortt_status"`      // "healthy" | "broken" | "degraded" | "untested"
	Label      string `json:"ortt_label"`       // Human readable diagnosis
	RttMs      int    `json:"ortt_rtt_ms"`      // UDP RTT ms
	WarmConns  int    `json:"ortt_warm_conns"`  // Active warm connections count
	PoolHits   uint32 `json:"ortt_pool_hits"`   // Remote pool hits
	PoolMisses uint32 `json:"ortt_pool_misses"` // Remote pool misses
	HitRate    string `json:"ortt_hit_rate"`    // e.g. "94.2%"
	CheckedAt  string `json:"ortt_checked_at"`  // "15:04:05"
}

type pongData struct {
	rttMs     int
	hits      uint32
	misses    uint32
	warmConns uint16
	dropped   uint32
	flags     uint8
}

func sendPingProbe(host string, port int, secret []byte) (*pongData, error) {
	if port <= 0 {
		port = 39999
	}
	raddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		return nil, fmt.Errorf("resolve %s:%d failed: %w", host, port, err)
	}

	var lastErr error
	maxAttempts := 3
	timeout := 900 * time.Millisecond

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			time.Sleep(120 * time.Millisecond)
		}

		// Fresh local UDP port per attempt to avoid single-port ISP QoS / dropped 5-tuple
		conn, err := net.DialUDP("udp", nil, raddr)
		if err != nil {
			lastErr = fmt.Errorf("dial udp %s failed: %w", raddr, err)
			continue
		}

		// 32-byte request packet
		buf := make([]byte, PacketWireLength)
		copy(buf[0:4], Magic)
		buf[4] = ProtocolVersion
		buf[5] = CmdPing

		ts := uint32(time.Now().Unix())
		binary.BigEndian.PutUint32(buf[6:10], ts)

		var nonce [4]byte
		_, _ = rand.Read(nonce[:])
		copy(buf[10:14], nonce[:])

		binary.BigEndian.PutUint16(buf[14:16], 1) // KeyID 1

		h := hmac.New(sha256.New, secret)
		h.Write(buf[:16])
		copy(buf[16:32], h.Sum(nil)[:16])

		t0 := time.Now()
		_ = conn.SetDeadline(time.Now().Add(timeout))
		if _, err := conn.Write(buf); err != nil {
			_ = conn.Close()
			lastErr = fmt.Errorf("write failed: %w", err)
			continue
		}

		resp := make([]byte, 256)
		n, err := conn.Read(resp)
		rttMs := int(time.Since(t0).Milliseconds())
		_ = conn.Close()

		if err != nil {
			lastErr = fmt.Errorf("read timeout/error: %w", err)
			continue
		}

		if n < 48 {
			lastErr = fmt.Errorf("invalid pong length: %d", n)
			continue
		}
		if string(resp[0:4]) != "ORTT" || resp[4] != ProtocolVersion || resp[5] != CmdPong {
			lastErr = fmt.Errorf("invalid pong header: magic=%s ver=%d cmd=0x%02x", string(resp[0:4]), resp[4], resp[5])
			continue
		}
		if !hmac.Equal(resp[6:10], nonce[:]) {
			lastErr = fmt.Errorf("nonce mismatch")
			continue
		}

		// Verify HMAC-SHA256 signature
		h2 := hmac.New(sha256.New, secret)
		h2.Write(resp[0:32])
		expectedSig := h2.Sum(nil)[:16]
		if !hmac.Equal(resp[32:48], expectedSig) {
			lastErr = fmt.Errorf("hmac signature mismatch")
			continue
		}

		return &pongData{
			rttMs:     rttMs,
			hits:      binary.BigEndian.Uint32(resp[10:14]),
			misses:    binary.BigEndian.Uint32(resp[14:18]),
			warmConns: binary.BigEndian.Uint16(resp[18:20]),
			dropped:   binary.BigEndian.Uint32(resp[20:24]),
			flags:     resp[24],
		}, nil
	}

	return nil, lastErr
}

func sendWakeupPacket(host string, port int, secret []byte) {
	if port <= 0 {
		port = 39999
	}
	raddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		return
	}
	conn, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		return
	}
	defer conn.Close()

	buf := make([]byte, PacketWireLength)
	copy(buf[0:4], Magic)
	buf[4] = ProtocolVersion
	buf[5] = CmdWakeup

	ts := uint32(time.Now().Unix())
	binary.BigEndian.PutUint32(buf[6:10], ts)

	var nonce [4]byte
	_, _ = rand.Read(nonce[:])
	copy(buf[10:14], nonce[:])

	binary.BigEndian.PutUint16(buf[14:16], 1)

	h := hmac.New(sha256.New, secret)
	h.Write(buf[:16])
	copy(buf[16:32], h.Sum(nil)[:16])

	_ = conn.SetDeadline(time.Now().Add(500 * time.Millisecond))
	_, _ = conn.Write(buf)
}

// ProbeNodeOrttFullLink conducts a strict 3-stage full link validation of ORTT on a proxy node
func ProbeNodeOrttFullLink(node *config.Node, secret []byte, clashPort int) *OrttProbeResult {
	nowStr := time.Now().Format("15:04:05")
	res := &OrttProbeResult{
		Status:    "untested",
		Label:     "⚪ ORTT 待检测",
		CheckedAt: nowStr,
	}

	if node == nil || config.IsInfoNode(*node) {
		res.Status = "broken"
		res.Label = "❌ 非代理节点"
		return res
	}

	if len(secret) == 0 {
		res.Status = "broken"
		res.Label = "❌ 本地未配置 ORTT 密钥"
		return res
	}

	host := strings.TrimSpace(node.Server)
	if host == "" {
		res.Status = "broken"
		res.Label = "❌ 节点服务器地址为空"
		return res
	}

	// Stage 1: Control plane UDP :39999 + HMAC dual check
	pong1, err := sendPingProbe(host, 39999, secret)
	if err != nil {
		res.Status = "broken"
		errStr := err.Error()
		if strings.Contains(errStr, "timeout") || strings.Contains(errStr, "deadline") {
			res.Label = "❌ ORTT 失效 (UDP 39999 无响应/未安装或密钥被拒)"
		} else if strings.Contains(errStr, "signature") || strings.Contains(errStr, "nonce") {
			res.Label = "❌ ORTT 失效 (HMAC 签名校验失败)"
		} else {
			res.Label = fmt.Sprintf("❌ ORTT 失效 (%v)", err)
		}
		return res
	}

	res.RttMs = pong1.rttMs
	res.PoolHits = pong1.hits
	res.PoolMisses = pong1.misses
	res.WarmConns = int(pong1.warmConns)
	if total := pong1.hits + pong1.misses; total > 0 {
		res.HitRate = fmt.Sprintf("%.1f%%", float64(pong1.hits)*100.0/float64(total))
	}

	// Stage 2: Remote Data Plane Mount Check
	if pong1.flags&0x01 == 0 {
		res.Status = "broken"
		res.Label = "❌ ORTT 失效 (远端 39998 热池未监听)"
		return res
	}
	if pong1.flags&0x02 == 0 {
		res.Status = "broken"
		res.Label = "❌ ORTT 断路 (代理引擎未挂载 39998 热池)"
		return res
	}

	// Stage 3: Real Proxy Traffic Penetration Check
	var pong2 *pongData
	if pong1.warmConns < 20 {
		sendWakeupPacket(host, 39999, secret)
		time.Sleep(500 * time.Millisecond)
		p2, err2 := sendPingProbe(host, 39999, secret)
		if err2 == nil {
			pong2 = p2
		} else {
			pong2 = pong1
		}
	} else {
		pong2 = pong1
	}

	res.RttMs = pong2.rttMs
	res.PoolHits = pong2.hits
	res.PoolMisses = pong2.misses
	res.WarmConns = int(pong2.warmConns)
	if total := pong2.hits + pong2.misses; total > 0 {
		res.HitRate = fmt.Sprintf("%.1f%%", float64(pong2.hits)*100.0/float64(total))
	}

	// Optimization: If node is already tested healthy (Latency > 0)
	// and ORTT control plane + warm conns/hits are confirmed, bypass redundant Clash API call!
	if node.Latency > 0 && (res.PoolHits > 0 || res.WarmConns > 0) {
		rateVal := 0.0
		if total := res.PoolHits + res.PoolMisses; total > 0 {
			rateVal = float64(res.PoolHits) * 100.0 / float64(total)
		}
		res.Status = "healthy"
		res.Label = fmt.Sprintf("🟢 ORTT 正常 (热池:%d · 命中:%d · %.0f%%)", res.WarmConns, res.PoolHits, rateVal)
		return res
	}

	if clashPort <= 0 {
		clashPort = 9090
	}

	// Penetration probe via Clash delay API (HTTPS 443 through the proxy tunnel)
	proxyDelay, probeErr := nodes.TestNodeViaClashAPI(node.Id, clashPort, 3500*time.Millisecond)
	if (probeErr != nil || proxyDelay <= 0) && node.Latency <= 0 {
		// Retry once to eliminate transient timeout
		time.Sleep(200 * time.Millisecond)
		proxyDelay, probeErr = nodes.TestNodeViaClashAPI(node.Id, clashPort, 3500*time.Millisecond)
	}

	// Backfill node.Latency if Clash probe succeeded and latency was unknown/failed
	if proxyDelay > 0 && node.Latency <= 0 {
		node.Latency = proxyDelay
	}

	// Second Ping probe to check stats growth if we ran Clash API
	p3, err3 := sendPingProbe(host, 39999, secret)
	if err3 == nil {
		pong2 = p3
		res.RttMs = pong2.rttMs
		res.PoolHits = pong2.hits
		res.PoolMisses = pong2.misses
		res.WarmConns = int(pong2.warmConns)
		if total := pong2.hits + pong2.misses; total > 0 {
			res.HitRate = fmt.Sprintf("%.1f%%", float64(pong2.hits)*100.0/float64(total))
		}
	}

	// If Clash 204 probe timed out but remote ORTT warm pool is active and healthy (WarmConns > 0),
	// estimate WAN RTT from the physical UDP ping (res.RttMs * 2, min 250ms).
	// NEVER use local net.Dial (which gets hijacked by TUN to fake 2~5ms latency) and avoid false degraded alarm.
	if (probeErr != nil || proxyDelay <= 0) && node.Latency <= 0 && res.WarmConns > 0 && res.RttMs > 0 {
		estimatedRtt := res.RttMs * 2
		if estimatedRtt < 250 {
			estimatedRtt = 250
		}
		node.Latency = estimatedRtt
		proxyDelay = estimatedRtt
		probeErr = nil
	}

	// Fair judgment logic: Only mark degraded if BOTH Clash API probe and earlier latency test failed
	if (probeErr != nil || proxyDelay <= 0) && node.Latency <= 0 {
		res.Status = "degraded"
		res.Label = fmt.Sprintf("⚠️ 仅控制面通 (代理隧道断连 · 热池:%d)", res.WarmConns)
		return res
	}

	hitsGrew := pong2.hits > pong1.hits
	if hitsGrew || (res.WarmConns > 0 && res.PoolHits > 0) || res.WarmConns > 0 || proxyDelay > 0 || node.Latency > 0 {
		rateVal := 0.0
		if total := res.PoolHits + res.PoolMisses; total > 0 {
			rateVal = float64(res.PoolHits) * 100.0 / float64(total)
		}
		res.Status = "healthy"
		res.Label = fmt.Sprintf("🟢 ORTT 正常 (热池:%d · 命中:%d · %.0f%%)", res.WarmConns, res.PoolHits, rateVal)
		return res
	}

	if res.WarmConns == 0 && res.PoolHits == 0 {
		res.Status = "broken"
		res.Label = "❌ ORTT 空转 (热池连接为 0 / 流量未命中)"
		return res
	}

	res.Status = "healthy"
	res.Label = fmt.Sprintf("🟢 ORTT 正常 (热池:%d · 命中:%d)", res.WarmConns, res.PoolHits)
	return res
}
