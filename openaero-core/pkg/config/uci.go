package config

import (
	"fmt"
	"os"
	"strings"
)

const (
	UciOpenAeroPath = "/etc/config/openaero"
	UciDhcpPath     = "/etc/config/dhcp"
)

// SyncUciOpenAero updates option enabled and option mode in /etc/config/openaero without process fork
func SyncUciOpenAero(enabled bool, mode string) error {
	enVal := "0"
	if enabled {
		enVal = "1"
	}

	data, err := os.ReadFile(UciOpenAeroPath)
	if err != nil {
		if mode == "" {
			mode = "rule"
		}
		// If file doesn't exist, create default standard structure
		defaultContent := fmt.Sprintf("config global 'config'\n\toption enabled '%s'\n\toption mode '%s'\n", enVal, mode)
		return AtomicWriteFile(UciOpenAeroPath, []byte(defaultContent), 0644)
	}

	lines := strings.Split(string(data), "\n")
	newLines := make([]string, 0, len(lines)+2)
	inGlobalConfig := false
	hasEnabled := false
	hasMode := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "config global") || strings.HasPrefix(trimmed, "config openaero") {
			inGlobalConfig = true
			newLines = append(newLines, line)
			continue
		}
		if inGlobalConfig && strings.HasPrefix(trimmed, "config ") {
			inGlobalConfig = false
		}

		if inGlobalConfig {
			if strings.HasPrefix(trimmed, "option enabled") {
				newLines = append(newLines, fmt.Sprintf("\toption enabled '%s'", enVal))
				hasEnabled = true
				continue
			}
			if strings.HasPrefix(trimmed, "option mode") {
				if mode != "" {
					newLines = append(newLines, fmt.Sprintf("\toption mode '%s'", mode))
				} else {
					newLines = append(newLines, line)
				}
				hasMode = true
				continue
			}
		}
		newLines = append(newLines, line)
	}

	// If missing in global section, inject it
	if !hasEnabled || (!hasMode && mode != "") {
		finalLines := make([]string, 0, len(newLines)+2)
		inserted := false
		for _, line := range newLines {
			finalLines = append(finalLines, line)
			trimmed := strings.TrimSpace(line)
			if !inserted && (strings.HasPrefix(trimmed, "config global") || strings.HasPrefix(trimmed, "config openaero")) {
				if !hasEnabled {
					finalLines = append(finalLines, fmt.Sprintf("\toption enabled '%s'", enVal))
					hasEnabled = true
				}
				if !hasMode && mode != "" {
					finalLines = append(finalLines, fmt.Sprintf("\toption mode '%s'", mode))
					hasMode = true
				}
				inserted = true
			}
		}
		newLines = finalLines
	}

	content := strings.Join(newLines, "\n")
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return AtomicWriteFile(UciOpenAeroPath, []byte(content), 0644)
}

// SetUciDhcpBypass configures or cleans up DHCP option 3 & 6 in /etc/config/dhcp without process fork
func SetUciDhcpBypass(enable bool, routerIP string) error {
	data, err := os.ReadFile(UciDhcpPath)
	if err != nil {
		return err
	}

	lines := strings.Split(string(data), "\n")
	newLines := make([]string, 0, len(lines)+2)
	inLanConfig := false
	inserted := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "config dhcp 'lan'") || strings.HasPrefix(trimmed, "config dhcp lan") {
			inLanConfig = true
			newLines = append(newLines, line)
			continue
		}
		if inLanConfig && strings.HasPrefix(trimmed, "config ") {
			// Leaving lan config section
			if enable && routerIP != "" && !inserted {
				newLines = append(newLines, fmt.Sprintf("\tlist dhcp_option '3,%s'", routerIP))
				newLines = append(newLines, fmt.Sprintf("\tlist dhcp_option '6,%s'", routerIP))
				inserted = true
			}
			inLanConfig = false
		}

		if inLanConfig {
			// Strip existing dhcp_option 3 and 6
			if strings.HasPrefix(trimmed, "list dhcp_option") && (strings.Contains(trimmed, "3,") || strings.Contains(trimmed, "6,")) {
				continue
			}
		}
		newLines = append(newLines, line)
	}

	if inLanConfig && enable && routerIP != "" && !inserted {
		newLines = append(newLines, fmt.Sprintf("\tlist dhcp_option '3,%s'", routerIP))
		newLines = append(newLines, fmt.Sprintf("\tlist dhcp_option '6,%s'", routerIP))
		inserted = true
	}

	content := strings.Join(newLines, "\n")
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return AtomicWriteFile(UciDhcpPath, []byte(content), 0644)
}
