package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"openaero-core/pkg/config"
)

const (
	ApiTokenPath = "/etc/openaero/.api_token"
)

var (
	sessionCache sync.Map // sid -> cachedSession
	sidRegex     = regexp.MustCompile(`^[0-9a-fA-F]{32,64}$`)
)

type cachedSession struct {
	validUntil time.Time
	valid      bool
}

// PublicNode represents a desensitized, secure view of a proxy node safe for UI exposure
type PublicNode struct {
	Id             string `json:"id"`
	Remark         string `json:"remark"`
	Type           string `json:"type"`
	Server         string `json:"server"`
	Port           int    `json:"port"`
	Network        string `json:"network,omitempty"`
	TLS            bool   `json:"tls,omitempty"`
	Sni            string `json:"sni,omitempty"`
	Latency        int    `json:"latency"`
	IsActive       bool   `json:"is_active"`
	OrttStatus     string `json:"ortt_status"`
	OrttLabel      string `json:"ortt_label"`
	OrttRttMs      int    `json:"ortt_rtt_ms"`
	OrttWarmConns  int    `json:"ortt_warm_conns"`
	OrttPoolHits   uint32 `json:"ortt_pool_hits"`
	OrttPoolMisses uint32 `json:"ortt_pool_misses"`
	OrttHitRate    string `json:"ortt_hit_rate"`
	OrttCheckedAt  string `json:"ortt_checked_at"`
}

func ToPublicNode(n *config.Node) *PublicNode {
	if n == nil {
		return nil
	}
	tls := false
	if n.Type == "vless" || n.Type == "hysteria2" || n.Type == "vmess" || n.Sni != "" {
		tls = true
	}
	return &PublicNode{
		Id:             n.Id,
		Remark:         n.Remark,
		Type:           n.Type,
		Server:         n.Server,
		Port:           n.Port,
		Network:        n.Method,
		TLS:            tls,
		Sni:            n.Sni,
		Latency:        n.Latency,
		IsActive:       n.IsActive,
		OrttStatus:     n.OrttStatus,
		OrttLabel:      n.OrttLabel,
		OrttRttMs:      n.OrttRttMs,
		OrttWarmConns:  n.OrttWarmConns,
		OrttPoolHits:   n.OrttPoolHits,
		OrttPoolMisses: n.OrttPoolMisses,
		OrttHitRate:    n.OrttHitRate,
		OrttCheckedAt:  n.OrttCheckedAt,
	}
}

func ToPublicNodes(nodes []config.Node) []PublicNode {
	res := make([]PublicNode, 0, len(nodes))
	for i := range nodes {
		if p := ToPublicNode(&nodes[i]); p != nil {
			res = append(res, *p)
		}
	}
	return res
}

func EnsureApiToken() (string, error) {
	if data, err := os.ReadFile(ApiTokenPath); err == nil {
		tok := strings.TrimSpace(string(data))
		if len(tok) >= 32 {
			return tok, nil
		}
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("failed to generate random token: %w", err)
	}
	token := hex.EncodeToString(buf)
	_ = os.MkdirAll("/etc/openaero", 0755)
	if err := os.WriteFile(ApiTokenPath, []byte(token+"\n"), 0600); err != nil {
		return "", fmt.Errorf("failed to write %s: %w", ApiTokenPath, err)
	}
	_ = os.Chmod(ApiTokenPath, 0600)
	log.Printf("[api] 🔑 已生成并更新 API 鉴权令牌 (%s, 0600)", ApiTokenPath)
	return token, nil
}

func GetApiToken() string {
	tok, err := EnsureApiToken()
	if err != nil {
		return ""
	}
	return tok
}

func CheckLuCISession(sid string) bool {
	if !sidRegex.MatchString(sid) {
		return false
	}
	now := time.Now()
	if val, ok := sessionCache.Load(sid); ok {
		sess := val.(cachedSession)
		if now.Before(sess.validUntil) {
			return sess.valid
		}
	}

	cmdArg := fmt.Sprintf(`{"ubus_rpc_session":"%s"}`, sid)
	out, err := exec.Command("ubus", "call", "session", "get", cmdArg).Output()
	if err != nil {
		sessionCache.Store(sid, cachedSession{validUntil: now.Add(10 * time.Second), valid: false})
		return false
	}

	var res struct {
		Username string `json:"username"`
	}
	isValid := false
	if json.Unmarshal(out, &res) == nil && res.Username == "root" {
		isValid = true
	} else if strings.Contains(string(out), `"username":"root"`) || strings.Contains(string(out), `"username": "root"`) {
		isValid = true
	}

	ttl := 60 * time.Second
	if !isValid {
		ttl = 10 * time.Second
	}
	sessionCache.Store(sid, cachedSession{validUntil: now.Add(ttl), valid: isValid})
	return isValid
}

func (s *Server) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 1. Enforce max body size: 1MB to prevent OOM
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

		// 2. CSRF Origin check on POST requests
		if r.Method == http.MethodPost {
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || u.Host != r.Host {
					w.Header().Set("Content-Type", "application/json; charset=utf-8")
					w.WriteHeader(http.StatusForbidden)
					_ = json.NewEncoder(w).Encode(map[string]string{"error": "Forbidden: CSRF check failed"})
					return
				}
			}
		}

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		// 3. Dual-track authentication
		authorized := false

		// Channel A: Token check via X-OpenAero-Token or Authorization: Bearer
		reqToken := r.Header.Get("X-OpenAero-Token")
		if reqToken == "" {
			if authHdr := r.Header.Get("Authorization"); strings.HasPrefix(authHdr, "Bearer ") {
				reqToken = strings.TrimPrefix(authHdr, "Bearer ")
			}
		}
		if reqToken != "" {
			serverToken := GetApiToken()
			if serverToken != "" && subtle.ConstantTimeCompare([]byte(reqToken), []byte(serverToken)) == 1 {
				authorized = true
			}
		}

		// Channel B: LuCI Session check via sysauth / sysauth_http cookie
		if !authorized {
			for _, cookie := range r.Cookies() {
				if cookie.Name == "sysauth" || cookie.Name == "sysauth_http" {
					if CheckLuCISession(cookie.Value) {
						authorized = true
						break
					}
				}
			}
		}

		if !authorized {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Unauthorized"})
			return
		}

		next(w, r)
	}
}
