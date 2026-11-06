package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"openaero-core/pkg/config"
)

func TestAuthMiddleware(t *testing.T) {
	// Setup test token
	tmpToken := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	_ = os.MkdirAll("/etc/openaero", 0755)
	_ = os.WriteFile(ApiTokenPath, []byte(tmpToken+"\n"), 0600)
	defer os.Remove(ApiTokenPath)

	srv := &Server{}
	testHandler := srv.authMiddleware(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	// 1. Unauthenticated request -> must return 401 Unauthorized
	reqUnauth := httptest.NewRequest("GET", "/api/status", nil)
	recUnauth := httptest.NewRecorder()
	testHandler(recUnauth, reqUnauth)
	if recUnauth.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401 for unauthenticated request, got %d", recUnauth.Code)
	}

	// 2. Request with valid X-OpenAero-Token -> must return 200 OK
	reqAuth := httptest.NewRequest("GET", "/api/status", nil)
	reqAuth.Header.Set("X-OpenAero-Token", tmpToken)
	recAuth := httptest.NewRecorder()
	testHandler(recAuth, reqAuth)
	if recAuth.Code != http.StatusOK {
		t.Errorf("Expected 200 for authenticated request, got %d", recAuth.Code)
	}

	// 3. Request with valid Bearer token in Authorization header -> must return 200 OK
	reqBearer := httptest.NewRequest("GET", "/api/status", nil)
	reqBearer.Header.Set("Authorization", "Bearer "+tmpToken)
	recBearer := httptest.NewRecorder()
	testHandler(recBearer, reqBearer)
	if recBearer.Code != http.StatusOK {
		t.Errorf("Expected 200 for Bearer auth request, got %d", recBearer.Code)
	}

	// 4. Request with invalid token -> must return 401 Unauthorized
	reqBadToken := httptest.NewRequest("GET", "/api/status", nil)
	reqBadToken.Header.Set("X-OpenAero-Token", "wrong-token-12345")
	recBadToken := httptest.NewRecorder()
	testHandler(recBadToken, reqBadToken)
	if recBadToken.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401 for invalid token, got %d", recBadToken.Code)
	}

	// 5. CSRF Origin check on POST: Mismatched Origin must return 403 Forbidden
	reqCsrf := httptest.NewRequest("POST", "http://router.lan/api/config", bytes.NewReader([]byte("{}")))
	reqCsrf.Header.Set("X-OpenAero-Token", tmpToken)
	reqCsrf.Header.Set("Origin", "http://evil-attacker.com")
	recCsrf := httptest.NewRecorder()
	testHandler(recCsrf, reqCsrf)
	if recCsrf.Code != http.StatusForbidden {
		t.Errorf("Expected 403 for mismatched CSRF origin, got %d", recCsrf.Code)
	}

	// 6. CSRF Origin check on POST: Matching Origin must pass
	reqCsrfOk := httptest.NewRequest("POST", "http://router.lan/api/config", bytes.NewReader([]byte("{}")))
	reqCsrfOk.Header.Set("X-OpenAero-Token", tmpToken)
	reqCsrfOk.Header.Set("Origin", "http://router.lan")
	recCsrfOk := httptest.NewRecorder()
	testHandler(recCsrfOk, reqCsrfOk)
	if recCsrfOk.Code != http.StatusOK {
		t.Errorf("Expected 200 for matching CSRF origin, got %d", recCsrfOk.Code)
	}
}

func TestPublicNodeMasking(t *testing.T) {
	node := config.Node{
		Id:          "node-sensitive-1",
		Remark:      "Hong Kong Node",
		Type:        "vless",
		Server:      "103.42.183.142",
		Port:        443,
		Password:    "super-secret-password-12345",
		Uuid:        "88888888-4444-4444-4444-121212121212",
		PublicKey:   "abcdef1234567890abcdef1234567890",
		ShortId:     "123456",
		Flow:        "xtls-rprx-vision",
		Sni:         "hk.example.com",
		Latency:     35,
		IsActive:    true,
	}

	pub := ToPublicNode(&node)
	if pub == nil {
		t.Fatalf("ToPublicNode returned nil")
	}

	b, err := json.Marshal(pub)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	jsonStr := string(b)

	// Verify sensitive fields are completely omitted from JSON
	sensitiveFields := []string{"super-secret-password-12345", "88888888-4444-4444-4444-121212121212", "abcdef1234567890abcdef1234567890", "123456", "uuid", "password", "public_key", "short_id"}
	for _, field := range sensitiveFields {
		if bytes.Contains(b, []byte(field)) {
			t.Errorf("PublicNode leaked sensitive field %q: %s", field, jsonStr)
		}
	}

	// Verify required public fields are present
	if pub.Id != node.Id || pub.Remark != node.Remark || pub.Server != node.Server || pub.Port != node.Port || pub.Latency != node.Latency || !pub.IsActive {
		t.Errorf("PublicNode public field mismatch: %+v", pub)
	}
}

func TestNetworkValidation(t *testing.T) {
	// 1. IP validation
	validIPs, err := ValidateIPList([]string{"192.168.1.100", " 10.0.0.1 ", "2001:db8::1"})
	if err != nil || len(validIPs) != 3 {
		t.Errorf("Expected valid IPs to pass, got: %v, %v", validIPs, err)
	}

	_, err = ValidateIPList([]string{"192.168.1.999"})
	if err == nil {
		t.Errorf("Expected invalid IP 192.168.1.999 to be rejected")
	}

	_, err = ValidateIPList([]string{"192.168.1.1; rm -rf /"})
	if err == nil {
		t.Errorf("Expected malicious injection string to be rejected")
	}

	// 2. MAC validation
	validMACs, err := ValidateMACList([]string{"AA:BB:CC:DD:EE:FF", "00-11-22-33-44-55"})
	if err != nil || len(validMACs) != 2 {
		t.Errorf("Expected valid MACs to pass, got: %v, %v", validMACs, err)
	}

	_, err = ValidateMACList([]string{"not-a-mac"})
	if err == nil {
		t.Errorf("Expected invalid MAC to be rejected")
	}

	// 3. Prefix validation
	cleanPrefix, err := ValidatePrefix("198.18.0.0/15")
	if err != nil || cleanPrefix != "198.18.0.0/15" {
		t.Errorf("Expected valid prefix 198.18.0.0/15, got %s, %v", cleanPrefix, err)
	}

	_, err = ValidatePrefix("198.18.0.0/99")
	if err == nil {
		t.Errorf("Expected invalid prefix to be rejected")
	}

	// 4. Mode validation
	if err := ValidateMode("rule"); err != nil {
		t.Errorf("Expected 'rule' mode to pass")
	}
	if err := ValidateMode("invalid_mode"); err == nil {
		t.Errorf("Expected 'invalid_mode' to be rejected")
	}

	// 5. Connection ID regex
	if !connIdRegex.MatchString("conn-12345-abc") {
		t.Errorf("Expected valid conn ID to match")
	}
	if connIdRegex.MatchString("conn/../../../etc/passwd") {
		t.Errorf("Expected path traversal conn ID to be rejected")
	}
}
