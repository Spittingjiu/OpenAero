package subscription

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type AsyncProgress struct {
	State     string `json:"state"` // IDLE, FETCHING, PARSING, COMPILING, RELOADING, SUCCESS, FAILED
	Percent   int    `json:"percent"`
	Message   string `json:"message"`
	LastError string `json:"last_error,omitempty"`
	UpdatedAt string `json:"updated_at"`
}

type SubscriptionManager struct {
	mu       sync.RWMutex
	progress AsyncProgress
}

var GlobalSubManager = &SubscriptionManager{
	progress: AsyncProgress{
		State:     "IDLE",
		Percent:   0,
		Message:   "准备就绪",
		UpdatedAt: time.Now().Format("2006-01-02 15:04:05"),
	},
}

func (sm *SubscriptionManager) SetProgress(state string, percent int, msg string, errStr string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.progress = AsyncProgress{
		State:     state,
		Percent:   percent,
		Message:   msg,
		LastError: errStr,
		UpdatedAt: time.Now().Format("2006-01-02 15:04:05"),
	}
}

func (sm *SubscriptionManager) GetProgress() AsyncProgress {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.progress
}

// SetupCrontab manages /etc/crontabs/root entry for openaero-core auto-update
func SetupCrontab(interval int) error {
	const cronFile = "/etc/crontabs/root"
	content := ""
	if data, err := os.ReadFile(cronFile); err == nil {
		lines := strings.Split(string(data), "\n")
		var filtered []string
		for _, line := range lines {
			if !strings.Contains(line, "openaero-core -update-subs") && strings.TrimSpace(line) != "" {
				filtered = append(filtered, line)
			}
		}
		content = strings.Join(filtered, "\n")
		if content != "" {
			content += "\n"
		}
	}

	cronCmd := ""
	switch interval {
	case 1: // Daily at 04:30
		cronCmd = "30 4 * * * /usr/bin/openaero-core -update-subs -silent >/dev/null 2>&1\n"
	case 2: // Every 2 days
		cronCmd = "30 4 */2 * * /usr/bin/openaero-core -update-subs -silent >/dev/null 2>&1\n"
	case 7: // Weekly
		cronCmd = "30 4 * * 0 /usr/bin/openaero-core -update-subs -silent >/dev/null 2>&1\n"
	}

	if cronCmd != "" {
		content += cronCmd
	}

	// Ensure directory exists
	_ = os.MkdirAll("/etc/crontabs", 0755)
	if err := os.WriteFile(cronFile, []byte(content), 0644); err != nil {
		return fmt.Errorf("write crontab failed: %w", err)
	}

	_ = exec.Command("/etc/init.d/cron", "reload").Run()
	return nil
}
