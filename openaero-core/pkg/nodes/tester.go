package nodes

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"openaero-core/pkg/config"
)

const (
	PrimaryProbeURL  = "https://www.gstatic.com/generate_204"
	BackupProbeURL   = "https://cp.cloudflare.com/generate_204"
	DefaultProbeURL  = PrimaryProbeURL
	DefaultClashPort = 9090
)

func probeSingleClashDelay(nodeTag, probeURL string, clashPort int, timeoutMs int64) (int, error) {
	apiURL := fmt.Sprintf("http://127.0.0.1:%d/proxies/%s/delay?url=%s&timeout=%d",
		clashPort, url.PathEscape(nodeTag), url.QueryEscape(probeURL), timeoutMs)

	client := &http.Client{Timeout: time.Duration(timeoutMs)*time.Millisecond + 500*time.Millisecond}
	resp, err := client.Get(apiURL)
	if err != nil {
		return -1, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return -1, fmt.Errorf("clash api returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return -1, err
	}

	var res struct {
		Delay   int    `json:"delay"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return -1, err
	}

	if res.Delay <= 0 {
		return -1, fmt.Errorf("invalid delay: %s", res.Message)
	}

	return res.Delay, nil
}

// TestNodeViaClashAPI probes latency of a specific outbound via sing-box's Clash API with dual URL auto-retry
func TestNodeViaClashAPI(nodeTag string, clashPort int, timeout time.Duration) (int, error) {
	if clashPort <= 0 {
		clashPort = DefaultClashPort
	}
	timeoutMs := timeout.Milliseconds()
	if timeoutMs <= 0 {
		timeoutMs = 3500
	}

	// 1. Primary: https://www.gstatic.com/generate_204 (hits ORTT prewarmed 443 pool)
	delay, err := probeSingleClashDelay(nodeTag, PrimaryProbeURL, clashPort, timeoutMs)
	if err == nil && delay > 0 {
		// Warm-up second sample: if cold-start TLS/Reality handshake inflated latency > 300ms,
		// immediately take a second measurement on the warmed path and keep the lower RTT.
		if delay > 300 {
			if delay2, err2 := probeSingleClashDelay(nodeTag, PrimaryProbeURL, clashPort, 2000); err2 == nil && delay2 > 0 && delay2 < delay {
				delay = delay2
			}
		}
		return delay, nil
	}

	// 2. Backup: https://cp.cloudflare.com/generate_204
	delay, backupErr := probeSingleClashDelay(nodeTag, BackupProbeURL, clashPort, timeoutMs)
	if backupErr == nil && delay > 0 {
		if delay > 300 {
			if delay2, err2 := probeSingleClashDelay(nodeTag, BackupProbeURL, clashPort, 2000); err2 == nil && delay2 > 0 && delay2 < delay {
				delay = delay2
			}
		}
		return delay, nil
	}

	if err != nil {
		return -1, err
	}
	return -1, backupErr
}

// TestNodesConcurrently tests latency of nodes concurrently using True RTT via Clash API with fallback
func TestNodesConcurrently(nodeList []config.Node, concurrency int, timeout time.Duration) []config.Node {
	results := make([]config.Node, len(nodeList))
	copy(results, nodeList)

	if concurrency <= 0 || concurrency > 4 {
		concurrency = 4 // Tuned to 4 to eliminate Reality X25519 CPU scheduling delay on embedded ARM64
	}
	if timeout <= 0 {
		timeout = 3500 * time.Millisecond
	}

	// Check if Clash API is accessible
	clashAvailable := false
	checkClient := &http.Client{Timeout: 300 * time.Millisecond}
	if resp, err := checkClient.Get(fmt.Sprintf("http://127.0.0.1:%d/proxies", DefaultClashPort)); err == nil {
		if resp.StatusCode == http.StatusOK {
			clashAvailable = true
		}
		_ = resp.Body.Close()
	}

	var wg sync.WaitGroup
	semaphore := make(chan struct{}, concurrency)

	for i := range results {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			node := &results[idx]
			if config.IsInfoNode(*node) {
				node.Latency = -1
				return
			}

			// 1. Primary: sing-box Clash API True RTT (with dual URL auto-retry)
			if clashAvailable {
				delay, err := TestNodeViaClashAPI(node.Id, DefaultClashPort, timeout)
				if err == nil && delay > 0 {
					node.Latency = delay
					return
				}
				// When sing-box is running with TUN, NEVER fallback to local net.Dial.
				// Local TCP dial gets hijacked by openaero0 TUN causing fake 2~5ms latency!
				node.Latency = -1
				return
			}

			// 2. Fallback: Direct TCP Dial ONLY if sing-box is NOT active (cold start before daemon boots)
			if strings.TrimSpace(node.Server) != "" && node.Port > 0 {
				target := fmt.Sprintf("%s:%d", strings.TrimSpace(node.Server), node.Port)
				dialer := &net.Dialer{
					Timeout: timeout,
				}

				t0 := time.Now()
				conn, err := dialer.Dial("tcp", target)
				if err == nil {
					_ = conn.Close()
					latency := time.Since(t0).Milliseconds()
					if latency <= 0 {
						latency = 1
					}
					node.Latency = int(latency)
					return
				}
			}

			node.Latency = -1
		}(i)
	}

	wg.Wait()
	return results
}
