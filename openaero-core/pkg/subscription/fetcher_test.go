package subscription

import (
	"encoding/base64"
	"fmt"
	"testing"
)

func TestParseSsLink(t *testing.T) {
	// 1. SIP002 format: ss://BASE64(aes-256-gcm:pass123)@1.2.3.4:8388#MySSNode
	userInfo := base64.RawURLEncoding.EncodeToString([]byte("aes-256-gcm:pass123"))
	sip002Url := fmt.Sprintf("ss://%s@1.2.3.4:8388#MySSNode", userInfo)

	node1, err := parseSsLink(sip002Url)
	if err != nil {
		t.Fatalf("parseSsLink SIP002 failed: %v", err)
	}
	if node1.Server != "1.2.3.4" || node1.Port != 8388 || node1.Method != "aes-256-gcm" || node1.Password != "pass123" || node1.Remark != "MySSNode" {
		t.Errorf("Unexpected parsed SS node 1: %+v", node1)
	}

	// 2. Legacy format: ss://BASE64(aes-128-cfb:pass456@5.6.7.8:8443)#LegacyNode
	legacyBody := base64.StdEncoding.EncodeToString([]byte("aes-128-cfb:pass456@5.6.7.8:8443"))
	legacyUrl := fmt.Sprintf("ss://%s#LegacyNode", legacyBody)

	node2, err := parseSsLink(legacyUrl)
	if err != nil {
		t.Fatalf("parseSsLink legacy failed: %v", err)
	}
	if node2.Server != "5.6.7.8" || node2.Port != 8443 || node2.Method != "aes-128-cfb" || node2.Password != "pass456" || node2.Remark != "LegacyNode" {
		t.Errorf("Unexpected parsed SS node 2: %+v", node2)
	}
}

func TestParseTrojanLink(t *testing.T) {
	trojanUrl := "trojan://trojan-secret@my-trojan.com:443?sni=trojan.sni.org#TrojanVIP"
	node, err := parseTrojanLink(trojanUrl)
	if err != nil {
		t.Fatalf("parseTrojanLink failed: %v", err)
	}
	if node.Server != "my-trojan.com" || node.Port != 443 || node.Password != "trojan-secret" || node.Sni != "trojan.sni.org" || node.Remark != "TrojanVIP" {
		t.Errorf("Unexpected parsed Trojan node: %+v", node)
	}
}

func TestSanitizeRemark(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "URLEncodedChinese",
			input:    "%E9%A6%99%E6%B8%AF01",
			expected: "香港01",
		},
		{
			name:     "AirportAdSuffix",
			input:    "🇭🇰 香港 01 | 剩余流量 200G | 官网 xxx.com",
			expected: "🇭🇰 香港 01",
		},
		{
			name:     "ControlCharacters",
			input:    "\r\n\tSG-Node\x00",
			expected: "SG-Node",
		},
		{
			name:     "CorruptUTF8",
			input:    string([]byte{0xff, 0xfe, 0xfd}),
			expected: "Node",
		},
		{
			name:     "EmptyString",
			input:    "",
			expected: "Default Node",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := SanitizeRemark(tc.input)
			if res != tc.expected {
				t.Errorf("SanitizeRemark(%q) = %q; want %q", tc.input, res, tc.expected)
			}
		})
	}
}