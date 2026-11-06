package prewarm

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"testing"
	"time"
)

func TestBuildV2Packet(t *testing.T) {
	secret := []byte("test-secret-key-1234567890123456")
	relay := NewRelayWithSecret("127.0.0.1", 39999, secret, 1)

	packet := relay.buildV2Packet(CmdWakeup)

	// 1. Length check
	if len(packet) != 32 {
		t.Fatalf("Expected 32 bytes wire length, got %d", len(packet))
	}

	// 2. Magic check
	if !bytes.Equal(packet[0:4], Magic) {
		t.Errorf("Magic mismatch: got %v", packet[0:4])
	}

	// 3. Version check
	if packet[4] != 0x02 {
		t.Errorf("Version mismatch: expected 0x02, got 0x%02X", packet[4])
	}

	// 4. Cmd check
	if packet[5] != CmdWakeup {
		t.Errorf("Cmd mismatch: expected 0x01, got 0x%02X", packet[5])
	}

	// 5. Timestamp check
	ts := binary.BigEndian.Uint32(packet[6:10])
	now := uint32(time.Now().Unix())
	if ts > now+5 || now > ts+5 {
		t.Errorf("Timestamp out of range: ts=%d, now=%d", ts, now)
	}

	// 6. HMAC verification
	h := hmac.New(sha256.New, secret)
	h.Write(packet[:16])
	expectedSig := h.Sum(nil)[:16]

	if !hmac.Equal(packet[16:32], expectedSig) {
		t.Errorf("HMAC verification failed")
	}
}

func TestRelayNoSecretRefusal(t *testing.T) {
	relay := NewRelay("127.0.0.1", 39999, nil)
	relay.Start()
	if relay.started {
		t.Fatalf("Relay with nil secret must NOT start")
	}

	relayEmpty := NewRelayWithSecret("127.0.0.1", 39999, []byte(""), 1)
	relayEmpty.Start()
	if relayEmpty.started {
		t.Fatalf("Relay with empty secret must NOT start")
	}
}

func TestMultiTargetRelay(t *testing.T) {
	secret := []byte("test-secret-key-1234567890123456")
	relay := NewRelayWithSecret("", 39999, secret, 1)

	hosts := []string{"23.80.81.106", "38.105.30.170", "23.80.81.106"}
	relay.UpdateTargets(hosts, 39999)

	targets := relay.GetTargets()
	if len(targets) != 2 {
		t.Fatalf("Expected 2 deduplicated targets, got %d: %v", len(targets), targets)
	}

	if targets[0] != "23.80.81.106" || targets[1] != "38.105.30.170" {
		t.Errorf("Target list mismatch: %v", targets)
	}
}
