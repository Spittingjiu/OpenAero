//go:build linux

package config

import (
	"fmt"
	"os"
	"strings"
	"syscall"
)

// SignalSingBox sends a signal directly to the sing-box process using native syscall.Kill (0 external fork)
func SignalSingBox(sig syscall.Signal) error {
	pid := GetSingBoxPid()
	if pid <= 0 {
		return fmt.Errorf("sing-box is not running")
	}
	return syscall.Kill(pid, sig)
}

// WriteSysctl sets a kernel parameter by writing directly to /proc/sys without process fork
func WriteSysctl(key, val string) error {
	relPath := strings.ReplaceAll(key, ".", "/")
	procPath := "/proc/sys/" + relPath
	return os.WriteFile(procPath, []byte(val+"\n"), 0644)
}
