package traffic

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type TrafficStats struct {
	SpeedRxBps   uint64 `json:"speed_rx_bps"`
	SpeedTxBps   uint64 `json:"speed_tx_bps"`
	SpeedRxHuman string `json:"speed_rx_human"`
	SpeedTxHuman string `json:"speed_tx_human"`
	TotalRxBytes uint64 `json:"total_rx_bytes"`
	TotalTxBytes uint64 `json:"total_tx_bytes"`
	TotalRxHuman string `json:"total_rx_human"`
	TotalTxHuman string `json:"total_tx_human"`
	ActiveConns  int    `json:"active_conns"`
	Success      bool   `json:"success"`
}

type TrafficMonitor struct {
	mu           sync.RWMutex
	iface        string
	clashPort    int
	lastRx       uint64
	lastTx       uint64
	lastTime     time.Time
	stats        TrafficStats
	httpClient   *http.Client
}

func NewTrafficMonitor(iface string, clashPort int) *TrafficMonitor {
	if iface == "" {
		iface = "openaero0"
	}
	if clashPort <= 0 {
		clashPort = 9090
	}
	return &TrafficMonitor{
		iface:      iface,
		clashPort:  clashPort,
		lastTime:   time.Now(),
		httpClient: &http.Client{Timeout: 500 * time.Millisecond},
		stats: TrafficStats{
			SpeedRxHuman: "0 B/s",
			SpeedTxHuman: "0 B/s",
			TotalRxHuman: "0 B",
			TotalTxHuman: "0 B",
			Success:      true,
		},
	}
}

func readUint64(path string) (uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
}

func FormatBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// FormatSpeed formats byte speed into human readable string
func FormatSpeed(b int64) string {
	if b <= 0 {
		return "0 B/s"
	}
	return FormatBytes(uint64(b)) + "/s"
}

// ShouldFilterConnection returns true for internal loopback probes, DNS queries, and prewarm UDP sessions
// while preserving legitimate router TCP proxy connections (172.19.0.1 TCP).
func ShouldFilterConnection(srcIP, dstPort, network string) bool {
	srcIP = strings.TrimSpace(srcIP)
	dstPort = strings.TrimSpace(dstPort)
	network = strings.ToLower(strings.TrimSpace(network))
	if srcIP == "127.0.0.1" || srcIP == "::1" || dstPort == "39999" || dstPort == "53" || dstPort == "1053" {
		return true
	}
	if srcIP == "172.19.0.1" && network == "udp" {
		return true
	}
	return false
}

func (tm *TrafficMonitor) Update() TrafficStats {
	now := time.Now()
	var rx, tx uint64
	activeConns := 0
	clashOk := false

	// 26-Audit Defect 3: Read accurate downloadTotal (rx) and uploadTotal (tx) from Clash API /connections
	// to avoid TUN stack="mixed" double-counted loopback mirroring (rx == tx).
	resp, err := tm.httpClient.Get(fmt.Sprintf("http://127.0.0.1:%d/connections", tm.clashPort))
	if err == nil && resp.StatusCode == http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		var connResp struct {
			DownloadTotal uint64 `json:"downloadTotal"`
			UploadTotal   uint64 `json:"uploadTotal"`
			Connections   []struct {
				Metadata struct {
					Network         string `json:"network"`
					SourceIP        string `json:"sourceIP"`
					DestinationPort string `json:"destinationPort"`
				} `json:"metadata"`
			} `json:"connections"`
		}
		if json.Unmarshal(body, &connResp) == nil {
			rx = connResp.DownloadTotal
			tx = connResp.UploadTotal
			for _, c := range connResp.Connections {
				if !ShouldFilterConnection(c.Metadata.SourceIP, c.Metadata.DestinationPort, c.Metadata.Network) {
					activeConns++
				}
			}
			clashOk = true
		}
	} else if resp != nil {
		_ = resp.Body.Close()
	}

	if !clashOk {
		rxPath := fmt.Sprintf("/sys/class/net/%s/statistics/rx_bytes", tm.iface)
		txPath := fmt.Sprintf("/sys/class/net/%s/statistics/tx_bytes", tm.iface)
		var errRx, errTx error
		rx, errRx = readUint64(rxPath)
		tx, errTx = readUint64(txPath)
		if errRx != nil {
			rx = 0
		}
		if errTx != nil {
			tx = 0
		}
	}

	tm.mu.Lock()
	defer tm.mu.Unlock()

	interval := now.Sub(tm.lastTime).Seconds()
	if interval > 0 && tm.lastRx > 0 && rx >= tm.lastRx {
		tm.stats.SpeedRxBps = uint64(float64(rx-tm.lastRx) / interval)
	} else {
		tm.stats.SpeedRxBps = 0
	}

	if interval > 0 && tm.lastTx > 0 && tx >= tm.lastTx {
		tm.stats.SpeedTxBps = uint64(float64(tx-tm.lastTx) / interval)
	} else {
		tm.stats.SpeedTxBps = 0
	}

	tm.stats.SpeedRxHuman = FormatBytes(tm.stats.SpeedRxBps) + "/s"
	tm.stats.SpeedTxHuman = FormatBytes(tm.stats.SpeedTxBps) + "/s"
	tm.stats.TotalRxBytes = rx
	tm.stats.TotalTxBytes = tx
	tm.stats.TotalRxHuman = FormatBytes(rx)
	tm.stats.TotalTxHuman = FormatBytes(tx)
	tm.stats.ActiveConns = activeConns
	tm.stats.Success = true

	tm.lastRx = rx
	tm.lastTx = tx
	tm.lastTime = now

	return tm.stats
}
