package prewarm

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"net"
	"testing"
	"time"

	"openaero-core/pkg/config"
)

func TestProbeNodeOrttFullLink_MockServer(t *testing.T) {
	secret := []byte("ortt-default-psk-2026")

	// Start local mock UDP server for testing
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen packet: %v", err)
	}
	defer pc.Close()

	port := pc.LocalAddr().(*net.UDPAddr).Port

	stopChan := make(chan struct{})
	defer close(stopChan)

	go func() {
		buf := make([]byte, 256)
		for {
			_ = pc.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				select {
				case <-stopChan:
					return
				default:
					continue
				}
			}

			if n >= 32 && string(buf[0:4]) == "ORTT" && buf[5] == CmdPing {
				pong := make([]byte, 48)
				copy(pong[0:4], Magic)
				pong[4] = ProtocolVersion
				pong[5] = CmdPong
				copy(pong[6:10], buf[10:14]) // Echo nonce

				binary.BigEndian.PutUint32(pong[10:14], 120) // hits
				binary.BigEndian.PutUint32(pong[14:18], 10)  // misses
				binary.BigEndian.PutUint16(pong[18:20], 35)  // warm conns
				binary.BigEndian.PutUint32(pong[20:24], 0)   // dropped
				pong[24] = 0x07                              // DataPlaneFlags (0x01 | 0x02 | 0x04)

				h := hmac.New(sha256.New, secret)
				h.Write(pong[0:32])
				copy(pong[32:48], h.Sum(nil)[:16])

				_, _ = pc.WriteTo(pong, addr)
			}
		}
	}()

	pong, err := sendPingProbe("127.0.0.1", port, secret)
	if err != nil {
		t.Fatalf("sendPingProbe failed: %v", err)
	}

	if pong.hits != 120 || pong.misses != 10 || pong.warmConns != 35 || pong.flags != 0x07 {
		t.Errorf("pong data mismatch: %+v", pong)
	}

	// Negative test: wrong secret
	_, errBad := sendPingProbe("127.0.0.1", port, []byte("wrong_secret_1234567890123456"))
	if errBad == nil {
		t.Errorf("expected error with wrong secret, got nil")
	}

	// Test ProbeNodeOrttFullLink structure
	node := &config.Node{
		Id:     "test-node-1",
		Remark: "Test Node",
		Server: "127.0.0.1",
		Port:   10000,
	}
	resEmpty := ProbeNodeOrttFullLink(node, nil, 0)
	if resEmpty.Status != "broken" {
		t.Errorf("expected broken for nil secret, got %s", resEmpty.Status)
	}
}
