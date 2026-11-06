package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestVerifySrsContent(t *testing.T) {
	// 1. Valid SRS data
	validSRS := make([]byte, 100)
	validSRS[0] = 0x53 // 'S'
	validSRS[1] = 0x52 // 'R'
	validSRS[2] = 0x53 // 'S'
	if err := VerifySrsContent(validSRS); err != nil {
		t.Errorf("Expected valid SRS to pass, got error: %v", err)
	}

	// 2. Too short (< 64 bytes)
	shortSRS := []byte{0x53, 0x52, 0x53, 0x01, 0x02}
	if err := VerifySrsContent(shortSRS); err == nil {
		t.Errorf("Expected short SRS to fail, but passed")
	}

	// 3. Invalid magic
	badMagic := make([]byte, 100)
	badMagic[0] = 0x47
	badMagic[1] = 0x49
	badMagic[2] = 0x54
	if err := VerifySrsContent(badMagic); err == nil {
		t.Errorf("Expected bad magic to fail, but passed")
	}
}

func TestVerifyFileSHA256(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.bin")
	content := []byte("Hello OpenAero Security Hardening 2026")
	if err := os.WriteFile(testFile, content, 0644); err != nil {
		t.Fatalf("Failed to write temp test file: %v", err)
	}

	hasher := sha256.New()
	hasher.Write(content)
	expectedHex := hex.EncodeToString(hasher.Sum(nil))

	// 1. Exact match
	if err := VerifyFileSHA256(testFile, expectedHex); err != nil {
		t.Errorf("Expected SHA256 match, got error: %v", err)
	}

	// 2. Match with "sha256:" prefix
	if err := VerifyFileSHA256(testFile, "sha256:"+expectedHex); err != nil {
		t.Errorf("Expected SHA256 match with prefix, got error: %v", err)
	}

	// 3. Mismatch
	badHex := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if err := VerifyFileSHA256(testFile, badHex); err == nil {
		t.Errorf("Expected mismatch error, got nil")
	}
}
