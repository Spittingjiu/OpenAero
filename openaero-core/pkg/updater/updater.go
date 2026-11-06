package updater

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"openaero-core/pkg/config"
)

var (
	versionMu            sync.RWMutex
	cachedSingBoxVersion string
)

type GitHubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []GitHubAsset `json:"assets"`
}

type GitHubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Digest             string `json:"digest"`
}

func GetTargetArchPattern() string {
	arch := runtime.GOARCH
	switch arch {
	case "amd64":
		return "linux-amd64.tar.gz"
	case "arm64":
		return "linux-arm64.tar.gz"
	case "mipsle":
		return "linux-mipsle.tar.gz"
	case "arm":
		return "linux-armv7.tar.gz"
	default:
		return fmt.Sprintf("linux-%s.tar.gz", arch)
	}
}

func GetCurrentSingBoxVersion() string {
	versionMu.RLock()
	if cachedSingBoxVersion != "" {
		v := cachedSingBoxVersion
		versionMu.RUnlock()
		return v
	}
	versionMu.RUnlock()

	out, err := exec.Command("/usr/bin/sing-box", "version").Output()
	if err != nil {
		return "未安装"
	}
	lines := strings.Split(string(out), "\n")
	ver := "未知"
	if len(lines) > 0 {
		ver = strings.TrimSpace(lines[0])
	}
	if ver != "" && ver != "未知" {
		versionMu.Lock()
		cachedSingBoxVersion = ver
		versionMu.Unlock()
	}
	return ver
}

// VerifyFileSHA256 verifies that the file at filePath matches expectedDigest (hex string or sha256:<hex>)
func VerifyFileSHA256(filePath, expectedDigest string) error {
	expected := strings.TrimSpace(expectedDigest)
	if strings.HasPrefix(expected, "sha256:") {
		expected = strings.TrimPrefix(expected, "sha256:")
	}
	expected = strings.ToLower(expected)
	if expected == "" {
		return nil
	}

	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file for sha256 check: %w", err)
	}
	defer f.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return fmt.Errorf("failed to read file for sha256 check: %w", err)
	}

	actual := hex.EncodeToString(hasher.Sum(nil))
	if actual != expected {
		return fmt.Errorf("sha256 digest mismatch: expected %s, got %s", expected, actual)
	}
	return nil
}

// VerifySrsContent verifies that the byte slice represents a valid sing-box .srs binary rule file
func VerifySrsContent(data []byte) error {
	if len(data) <= 64 {
		return fmt.Errorf("invalid .srs rule file: size too small (%d bytes <= 64)", len(data))
	}
	// Check magic bytes: 'S', 'R', 'S' (0x53, 0x52, 0x53)
	if !bytes.Equal(data[:3], []byte{0x53, 0x52, 0x53}) {
		return fmt.Errorf("invalid .srs rule file: magic header mismatch (%x, expected 535253)", data[:3])
	}
	return nil
}

func CheckLatestSingBox() (string, string, string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	req, _ := http.NewRequest("GET", "https://api.github.com/repos/SagerNet/sing-box/releases/latest", nil)
	req.Header.Set("User-Agent", "OpenAero-Router")

	resp, err := client.Do(req)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to check github release: %w", err)
	}
	defer resp.Body.Close()

	var rel GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", "", "", fmt.Errorf("failed to decode release json: %w", err)
	}

	targetPattern := GetTargetArchPattern()
	downloadUrl := ""
	digest := ""
	var checksumAsset *GitHubAsset

	for i := range rel.Assets {
		asset := &rel.Assets[i]
		if strings.Contains(asset.Name, targetPattern) {
			downloadUrl = asset.BrowserDownloadURL
			digest = asset.Digest
		}
		if strings.Contains(strings.ToLower(asset.Name), "checksum") {
			checksumAsset = asset
		}
	}

	// If asset digest is missing, attempt to fetch and parse checksums file
	if digest == "" && checksumAsset != nil && downloadUrl != "" {
		if cResp, err := client.Get(checksumAsset.BrowserDownloadURL); err == nil && cResp.StatusCode == http.StatusOK {
			defer cResp.Body.Close()
			cData, _ := io.ReadAll(cResp.Body)
			for _, line := range strings.Split(string(cData), "\n") {
				fields := strings.Fields(line)
				if len(fields) >= 2 && strings.Contains(fields[1], targetPattern) {
					digest = fields[0]
					break
				}
			}
		}
	}

	return rel.TagName, downloadUrl, digest, nil
}

func UpdateSingBoxCore() (string, error) {
	tag, downloadUrl, expectedDigest, err := CheckLatestSingBox()
	if err != nil {
		return "", err
	}
	pattern := GetTargetArchPattern()
	if downloadUrl == "" {
		return "", fmt.Errorf("no matching %s asset found for %s", pattern, tag)
	}

	// 1. Download tar.gz to temporary file
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(downloadUrl)
	if err != nil {
		return "", fmt.Errorf("download error: %w", err)
	}
	defer resp.Body.Close()

	tmpTar := "/tmp/sing-box-download.tar.gz"
	f, err := os.Create(tmpTar)
	if err != nil {
		return "", err
	}
	_, err = io.Copy(f, resp.Body)
	f.Close()
	if err != nil {
		_ = os.Remove(tmpTar)
		return "", err
	}
	defer os.Remove(tmpTar)

	// 2. Strict SHA-256 Digest Validation
	if expectedDigest != "" {
		if err := VerifyFileSHA256(tmpTar, expectedDigest); err != nil {
			_ = os.Remove(tmpTar)
			log.Printf("[updater] ❌ SHA-256 校验失败: %v", err)
			return "", fmt.Errorf("sha256 digest verification failed: %w", err)
		}
		log.Printf("[updater] ✅ sing-box 安装包官方 SHA-256 哈希校验通过")
	}

	// 3. Extract binary
	gzFile, err := os.Open(tmpTar)
	if err != nil {
		return "", err
	}
	defer gzFile.Close()

	gr, err := gzip.NewReader(gzFile)
	if err != nil {
		return "", err
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	tmpBin := "/tmp/sing-box-new"
	found := false

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}

		if strings.HasSuffix(hdr.Name, "/sing-box") || hdr.Name == "sing-box" {
			outBin, err := os.OpenFile(tmpBin, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0755)
			if err != nil {
				return "", err
			}
			_, err = io.Copy(outBin, tr)
			outBin.Close()
			if err != nil {
				_ = os.Remove(tmpBin)
				return "", err
			}
			found = true
			break
		}
	}

	if !found {
		return "", fmt.Errorf("sing-box binary not found in archive")
	}
	defer os.Remove(tmpBin)

	// 4. Verify new binary execution before touching live file
	cmd := exec.Command(tmpBin, "version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.Remove(tmpBin)
		return "", fmt.Errorf("verification of new sing-box binary failed: %s", string(out))
	}

	// 5. Backup live binary to /usr/bin/sing-box.bak
	targetBin := "/usr/bin/sing-box"
	backupBin := "/usr/bin/sing-box.bak"
	hasBackup := false
	if _, err := os.Stat(targetBin); err == nil {
		if err := copyFile(targetBin, backupBin); err == nil {
			hasBackup = true
			_ = os.Chmod(backupBin, 0755)
		}
	}

	// 6. Atomically replace /usr/bin/sing-box
	if err := copyFile(tmpBin, targetBin); err != nil {
		return "", fmt.Errorf("failed to update %s: %w", targetBin, err)
	}
	_ = os.Chmod(targetBin, 0755)

	// 7. Verify live binary and rollback on failure
	verifyCmd := exec.Command(targetBin, "version")
	if vOut, err := verifyCmd.CombinedOutput(); err != nil {
		log.Printf("[updater] ⚠️ 新内核安装后验证失败 (%v): %s, 正在自动回滚旧版本...", err, string(vOut))
		if hasBackup {
			_ = copyFile(backupBin, targetBin)
			_ = os.Chmod(targetBin, 0755)
		}
		return "", fmt.Errorf("new sing-box verification failed on host, rolled back to backup")
	}

	newVer := strings.TrimSpace(strings.Split(string(out), "\n")[0])
	versionMu.Lock()
	cachedSingBoxVersion = newVer
	versionMu.Unlock()

	// Graceful restart via procd
	_ = exec.Command("/etc/init.d/openaero", "restart").Run()

	return strings.TrimSpace(string(out)), nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

func UpdateRuleSets() error {
	defaultRules := []string{
		"geosite-google.srs",
		"geosite-google-gemini.srs",
		"geosite-youtube.srs",
		"geosite-openai.srs",
		"geosite-telegram.srs",
		"geoip-telegram.srs",
		"geosite-twitter.srs",
		"geosite-netflix.srs",
		"geosite-github.srs",
		"geosite-category-ads-all.srs",
		"geosite-tencent.srs",
		"geosite-steam@cn.srs",
		"geosite-category-games@cn.srs",
		"geosite-microsoft@cn.srs",
		"geosite-apple@cn.srs",
		"geosite-cn.srs",
		"geoip-cn.srs",
	}

	seen := make(map[string]bool)
	rules := make([]string, 0)
	for _, r := range defaultRules {
		seen[r] = true
		rules = append(rules, r)
	}

	// Dynamically discover any additional rule sets from AppConfig
	if cfg, err := config.LoadConfig(); err == nil && cfg != nil {
		for _, tag := range cfg.DirectRuleSetTags {
			fn := tag
			if !strings.HasSuffix(fn, ".srs") {
				fn += ".srs"
			}
			if !seen[fn] {
				seen[fn] = true
				rules = append(rules, fn)
			}
		}
		for _, grp := range cfg.StrategyGroups {
			for _, tag := range grp.RuleSetTags {
				fn := tag
				if !strings.HasSuffix(fn, ".srs") {
					fn += ".srs"
				}
				if !seen[fn] {
					seen[fn] = true
					rules = append(rules, fn)
				}
			}
		}
	}

	rulesDir := "/etc/openaero/rules"
	_ = os.MkdirAll(rulesDir, 0755)

	client := &http.Client{Timeout: 30 * time.Second}
	proxyClient := &http.Client{
		Timeout: 20 * time.Second,
		Transport: &http.Transport{
			Proxy: func(req *http.Request) (*url.URL, error) {
				return url.Parse("http://127.0.0.1:10809")
			},
		},
	}

	metaGeositeUrl := "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geosite/"
	metaGeoipUrl := "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geoip/"
	sagerGeositeUrl := "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/"
	sagerGeoipUrl := "https://raw.githubusercontent.com/SagerNet/sing-geoip/rule-set/"

	for _, rule := range rules {
		var primaryUrl, fallbackUrl string
		if strings.HasPrefix(rule, "geoip-") {
			name := strings.TrimSuffix(strings.TrimPrefix(rule, "geoip-"), ".srs")
			primaryUrl = metaGeoipUrl + name + ".srs"
			fallbackUrl = sagerGeoipUrl + rule
		} else {
			name := strings.TrimSuffix(strings.TrimPrefix(rule, "geosite-"), ".srs")
			primaryUrl = metaGeositeUrl + name + ".srs"
			fallbackUrl = sagerGeositeUrl + rule
		}

		// Try downloading from primary (MetaCubeX) or fallback (SagerNet) with proxy and mirror failover
		var resp *http.Response
		var err error
		candidateUrls := []string{
			primaryUrl,
			"https://ghproxy.net/" + primaryUrl,
			fallbackUrl,
			"https://ghproxy.net/" + fallbackUrl,
		}

		for _, u := range candidateUrls {
			if r, e := client.Get(u); e == nil && r.StatusCode == http.StatusOK {
				resp = r
				break
			}
			if r, e := proxyClient.Get(u); e == nil && r.StatusCode == http.StatusOK {
				resp = r
				break
			}
		}

		if resp == nil || resp.StatusCode != http.StatusOK {
			log.Printf("[updater] ⚠️ 下载规则集 %s 失败，跳过保持原文件", rule)
			continue
		}

		destPath := filepath.Join(rulesDir, rule)
		tmpPath := destPath + ".tmp"

		// Limit reader to 20MB to prevent memory exhaustion
		limitedReader := io.LimitReader(resp.Body, 20<<20)
		bodyData, err := io.ReadAll(limitedReader)
		resp.Body.Close()

		if err != nil {
			log.Printf("[updater] ⚠️ 读取规则集 %s 数据失败: %v", rule, err)
			continue
		}

		// Validate .srs size (>64 bytes) and magic header (0x53, 0x52, 0x53)
		if err := VerifySrsContent(bodyData); err != nil {
			log.Printf("[updater] ⚠️ 规则集 %s 损坏或非法 (%v)，丢弃临时文件保留生产文件", rule, err)
			_ = os.Remove(tmpPath)
			continue
		}

		// Write to temporary file first, then atomically rename
		if err := os.WriteFile(tmpPath, bodyData, 0644); err == nil {
			_ = os.Rename(tmpPath, destPath)
			log.Printf("[updater] ✅ 规则集 %s 原子更新成功 (%d 字节)", rule, len(bodyData))
		} else {
			_ = os.Remove(tmpPath)
		}
	}

	// Graceful reload of sing-box rules + restore TUN & Fake-IP routes
	_ = config.SignalSingBox(syscall.SIGHUP)
	config.EnsureTunRoutesAfterReload(nil)
	return nil
}
