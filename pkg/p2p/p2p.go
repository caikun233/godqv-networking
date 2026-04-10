// Package p2p implements UDP hole-punching for direct peer-to-peer communication.
//
// The flow is:
//  1. Client opens a local UDP socket and discovers its public endpoint via STUN.
//  2. Client sends a P2PPunchReq to the signaling server with the target peer's VIP.
//  3. Server relays a P2POffer to the target containing the requester's public UDP addr.
//  4. Both sides start sending small UDP probes to each other's public endpoint.
//  5. Once either side receives a probe, the hole is punched and the link is up.
//  6. IP packets are sent over UDP instead of the TCP relay.
//  7. If hole-punching fails after a timeout the TCP relay continues to be used.
package p2p

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"net"
	"sync"
	"time"
)

const (
	// PunchTimeout is how long to attempt hole punching before giving up.
	PunchTimeout = 10 * time.Second

	// ProbeInterval is how often to send UDP probes during hole punching.
	ProbeInterval = 200 * time.Millisecond

	// UDPReadBuf is the read buffer size for UDP packets.
	UDPReadBuf = 1500

	// MagicProbe is a small magic header sent in punch probes so we can
	// distinguish them from real data packets.
	MagicProbe = "GODQV_PROBE"
)

// PeerLink represents a successfully established P2P UDP link to a peer.
type PeerLink struct {
	PeerVIP  net.IP
	PeerAddr *net.UDPAddr
	Active   bool
}

// Manager handles P2P UDP connections for the local client.
type Manager struct {
	mu       sync.RWMutex
	conn     *net.UDPConn
	localAddr string // public address discovered via STUN

	links    map[string]*PeerLink // peerVIP string -> link
	onPacket func(packet []byte)  // callback for received data packets

	done     chan struct{}
	closeOnce sync.Once
}

// NewManager creates a P2P manager with a local UDP socket.
func NewManager(onPacket func(packet []byte)) (*Manager, error) {
	// Bind to any available port.
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: 0})
	if err != nil {
		return nil, err
	}

	m := &Manager{
		conn:     conn,
		links:    make(map[string]*PeerLink),
		onPacket: onPacket,
		done:     make(chan struct{}),
	}

	// Discover public address via STUN.
	pubAddr, err := stunDiscover(conn)
	if err != nil {
		log.Printf("[P2P] STUN 发现公网地址失败: %v (将仅使用本地地址)", err)
		m.localAddr = conn.LocalAddr().String()
	} else {
		m.localAddr = pubAddr
		log.Printf("[P2P] 公网 UDP 地址: %s", pubAddr)
	}

	go m.readLoop()

	return m, nil
}

// LocalAddr returns the discovered public UDP address.
func (m *Manager) LocalAddr() string {
	return m.localAddr
}

// Close shuts down the P2P manager.
func (m *Manager) Close() error {
	var err error
	m.closeOnce.Do(func() {
		close(m.done)
		err = m.conn.Close()
	})
	return err
}

// AddPeer begins hole-punching to the given peer. peerAddr is the peer's
// public UDP endpoint as reported by the signaling server.
func (m *Manager) AddPeer(peerVIP net.IP, peerAddr string) error {
	addr, err := net.ResolveUDPAddr("udp4", peerAddr)
	if err != nil {
		return err
	}

	vipStr := peerVIP.String()
	m.mu.Lock()
	link := &PeerLink{
		PeerVIP:  peerVIP,
		PeerAddr: addr,
		Active:   false,
	}
	m.links[vipStr] = link
	m.mu.Unlock()

	// Start punching in background.
	go m.punchHole(link)

	return nil
}

// SendPacket sends a raw IP packet to the peer over UDP if a direct link
// exists. Returns true if sent via P2P, false if the caller should fall back
// to TCP relay.
func (m *Manager) SendPacket(dstVIP net.IP, packet []byte) bool {
	m.mu.RLock()
	link, ok := m.links[dstVIP.String()]
	m.mu.RUnlock()

	if !ok || !link.Active {
		return false
	}

	_, err := m.conn.WriteToUDP(packet, link.PeerAddr)
	if err != nil {
		log.Printf("[P2P] UDP 发送失败 → %s: %v", link.PeerAddr, err)
		return false
	}
	return true
}

// GetActiveLink checks whether a direct link to the given VIP is active.
func (m *Manager) GetActiveLink(vip net.IP) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	link, ok := m.links[vip.String()]
	return ok && link.Active
}

// punchHole attempts to establish a direct UDP link by repeatedly sending probes.
func (m *Manager) punchHole(link *PeerLink) {
	deadline := time.After(PunchTimeout)
	ticker := time.NewTicker(ProbeInterval)
	defer ticker.Stop()

	probe := []byte(MagicProbe + ":" + generateP2PToken())

	for {
		select {
		case <-m.done:
			return
		case <-deadline:
			if !link.Active {
				log.Printf("[P2P] 打洞超时: %s", link.PeerVIP)
			}
			return
		case <-ticker.C:
			if link.Active {
				return // Already established
			}
			m.conn.WriteToUDP(probe, link.PeerAddr)
		}
	}
}

// readLoop continuously reads from the UDP socket and dispatches packets.
func (m *Manager) readLoop() {
	buf := make([]byte, UDPReadBuf)
	for {
		select {
		case <-m.done:
			return
		default:
		}

		m.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		n, remoteAddr, err := m.conn.ReadFromUDP(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			select {
			case <-m.done:
				return
			default:
				log.Printf("[P2P] UDP 读取错误: %v", err)
				continue
			}
		}

		data := buf[:n]

		// Check if it's a probe
		if isProbe(data) {
			m.handleProbe(remoteAddr)
			continue
		}

		// Real data packet - deliver via callback
		if m.onPacket != nil && n > 0 {
			pkt := make([]byte, n)
			copy(pkt, data)
			m.onPacket(pkt)
		}
	}
}

func (m *Manager) handleProbe(addr *net.UDPAddr) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Find which link this probe belongs to (match by addr)
	for _, link := range m.links {
		if link.PeerAddr.IP.Equal(addr.IP) && link.PeerAddr.Port == addr.Port {
			if !link.Active {
				link.Active = true
				log.Printf("[P2P] 打洞成功! 与 %s 建立直连 (UDP: %s)", link.PeerVIP, addr)
			}
			return
		}
	}

	// Probe from unknown source, update addr if we have a link from that IP
	for _, link := range m.links {
		if link.PeerAddr.IP.Equal(addr.IP) {
			link.PeerAddr = addr
			link.Active = true
			log.Printf("[P2P] 打洞成功! 与 %s 建立直连 (UDP: %s, 端口已更新)", link.PeerVIP, addr)
			return
		}
	}
}

func isProbe(data []byte) bool {
	return len(data) >= len(MagicProbe) && string(data[:len(MagicProbe)]) == MagicProbe
}

func generateP2PToken() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}
