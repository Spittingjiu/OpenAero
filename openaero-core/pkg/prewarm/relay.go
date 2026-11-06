package prewarm

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"
)

// GlobalRelay holds the singleton 0-RTT prewarm relay instance so active node switches can update its target dynamically (27-Audit Defect 4)
var GlobalRelay *Relay

// ORTT Protocol v2.0 Wire Specification
var Magic = []byte{0x4F, 0x52, 0x54, 0x54} // "ORTT"

const (
	ProtocolVersion  byte = 0x02
	PacketWireLength int  = 32

	CmdWakeup    byte = 0x01
	CmdHeartbeat byte = 0x02
	CmdSleep     byte = 0x03
	CmdPing      byte = 0x04
)

type Relay struct {
	mu       sync.RWMutex
	vpsHosts []string
	vpsPort  int
	secret   []byte
	keyID    uint16
	stopChan chan struct{}
	started  bool
}

func NewRelay(vpsHost string, vpsPort int, secret []byte) *Relay {
	return NewRelayWithSecret(vpsHost, vpsPort, secret, 1)
}

func NewRelayWithSecret(vpsHost string, vpsPort int, secret []byte, keyID uint16) *Relay {
	if vpsPort <= 0 {
		vpsPort = 39999
	}
	var hosts []string
	if strings.TrimSpace(vpsHost) != "" {
		hosts = []string{strings.TrimSpace(vpsHost)}
	}
	return &Relay{
		vpsHosts: hosts,
		vpsPort:  vpsPort,
		secret:   secret,
		keyID:    keyID,
		stopChan: make(chan struct{}),
	}
}

func (r *Relay) Start() {
	if r == nil {
		return
	}
	r.mu.Lock()
	if len(r.secret) == 0 {
		r.mu.Unlock()
		log.Println("[ortt] ⚠️ 未配置 ortt_secret 鉴权密钥，ORTT 预热模块拒绝启动")
		return
	}
	if r.started {
		r.mu.Unlock()
		return
	}
	r.started = true
	hostsCount := len(r.vpsHosts)
	r.mu.Unlock()

	go func() {
		if hostsCount > 0 {
			// Send initial authenticated wakeup packet to all current targets
			r.sendSignal(CmdWakeup)
		}

		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-r.stopChan:
				r.sendSignal(CmdSleep)
				return
			case <-ticker.C:
				r.sendSignal(CmdHeartbeat)
			}
		}
	}()
}

// UpdateTargets dynamically updates the set of 0-RTT prewarm target VPS hosts (Multi-Target Relay)
func (r *Relay) UpdateTargets(vpsHosts []string, vpsPort int) {
	if r == nil {
		return
	}
	validHosts := make([]string, 0, len(vpsHosts))
	seen := make(map[string]bool)
	for _, h := range vpsHosts {
		h = strings.TrimSpace(h)
		if h != "" && !seen[h] {
			seen[h] = true
			validHosts = append(validHosts, h)
		}
	}

	r.mu.Lock()
	if vpsPort > 0 {
		r.vpsPort = vpsPort
	}
	oldSet := make(map[string]bool)
	for _, h := range r.vpsHosts {
		oldSet[h] = true
	}
	var newlyAdded []string
	for _, h := range validHosts {
		if !oldSet[h] {
			newlyAdded = append(newlyAdded, h)
		}
	}
	r.vpsHosts = validHosts
	wasStarted := r.started
	r.mu.Unlock()

	if !wasStarted && len(validHosts) > 0 {
		r.Start()
	} else if len(newlyAdded) > 0 {
		go func() {
			for _, nh := range newlyAdded {
				r.sendSignalToHost(nh, CmdWakeup)
			}
		}()
	}
}

// UpdateTarget updates single target for backward compatibility
func (r *Relay) UpdateTarget(vpsHost string, vpsPort int) {
	vpsHost = strings.TrimSpace(vpsHost)
	if vpsHost == "" {
		r.UpdateTargets(nil, vpsPort)
		return
	}
	r.UpdateTargets([]string{vpsHost}, vpsPort)
}

// GetTargets returns a copy of current active prewarm target hosts
func (r *Relay) GetTargets() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	res := make([]string, len(r.vpsHosts))
	copy(res, r.vpsHosts)
	return res
}

// GetSecret returns a copy of current prewarm secret
func (r *Relay) GetSecret() []byte {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	res := make([]byte, len(r.secret))
	copy(res, r.secret)
	return res
}

func (r *Relay) UpdateSecret(secret []byte) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.secret = secret
}

func (r *Relay) Stop() {
	select {
	case <-r.stopChan:
		return
	default:
		close(r.stopChan)
	}
}

// buildV2Packet builds a synchronized 32-byte authenticated control packet
func (r *Relay) buildV2Packet(cmd byte) []byte {
	buf := make([]byte, PacketWireLength)
	copy(buf[0:4], Magic)
	buf[4] = ProtocolVersion
	buf[5] = cmd

	// Timestamp (Anti-replay window)
	ts := uint32(time.Now().Unix())
	binary.BigEndian.PutUint32(buf[6:10], ts)

	// Random 4-byte nonce
	var nonce [4]byte
	_, _ = rand.Read(nonce[:])
	copy(buf[10:14], nonce[:])

	// KeyID
	binary.BigEndian.PutUint16(buf[14:16], r.keyID)

	// HMAC-SHA256 signature over raw[:16], truncated to 16 bytes
	h := hmac.New(sha256.New, r.secret)
	h.Write(buf[:16])
	sig := h.Sum(nil)[:16]
	copy(buf[16:32], sig)

	return buf
}

func (r *Relay) sendSignal(cmd byte) {
	r.mu.RLock()
	hosts := make([]string, len(r.vpsHosts))
	copy(hosts, r.vpsHosts)
	secLen := len(r.secret)
	r.mu.RUnlock()

	if len(hosts) == 0 || secLen == 0 {
		return
	}

	for _, host := range hosts {
		r.sendSignalToHost(host, cmd)
	}
}

func (r *Relay) sendSignalToHost(host string, cmd byte) {
	r.mu.RLock()
	port := r.vpsPort
	secLen := len(r.secret)
	r.mu.RUnlock()

	if host == "" || secLen == 0 {
		return
	}
	addr := fmt.Sprintf("%s:%d", host, port)
	raddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return
	}

	conn, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		return
	}
	defer conn.Close()

	packet := r.buildV2Packet(cmd)
	_, _ = conn.Write(packet)
	if cmd == CmdWakeup || cmd == CmdSleep {
		log.Printf("[ortt] 已向 VPS %s 发送 ORTT 0-RTT 极速预热 v2.0 鉴权指令 (0x%02X, 32 字节)", addr, cmd)
	}
}
