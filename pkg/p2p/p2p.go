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
	mu        sync.RWMutex
	conn      *net.UDPConn
	localAddr string // public address discovered via STUN

	links    map[string]*PeerLink // peerVIP string -> link
	onPacket func(packet []byte)  // callback for received data packets
	onEvent  func(Event)          // optional callback for P2P events

	unmatchedProbeCount int       // rate-limit logging of unmatched probes
	lastUnmatchedLog    time.Time // last time we logged unmatched probe details

	done      chan struct{}
	closeOnce sync.Once
}

// NewManager creates a P2P manager with a local UDP socket.
func NewManager(onPacket func(packet []byte)) (*Manager, error) {
	log.Printf("[P2P] 初始化 P2P 管理器...")

	// Bind to any available port.
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: 0})
	if err != nil {
		log.Printf("[P2P] 创建 UDP socket 失败: %v", err)
		return nil, err
	}
	log.Printf("[P2P] 本地 UDP socket 已绑定: %s", conn.LocalAddr().String())

	m := &Manager{
		conn:     conn,
		links:    make(map[string]*PeerLink),
		onPacket: onPacket,
		done:     make(chan struct{}),
	}

	// Discover public address via STUN.
	log.Printf("[P2P] 正在通过 STUN 发现公网地址...")
	pubAddr, err := stunDiscover(conn)
	if err != nil {
		log.Printf("[P2P] STUN 发现公网地址失败: %v (将仅使用本地地址)", err)
		m.localAddr = conn.LocalAddr().String()
	} else {
		m.localAddr = pubAddr
		log.Printf("[P2P] 公网 UDP 地址: %s", pubAddr)
	}

	go m.readLoop()
	log.Printf("[P2P] P2P 管理器初始化完成，开始监听 UDP 数据包")

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

// SetEventCallback sets an optional callback that is invoked for P2P events
// such as punch start, success, and timeout.
func (m *Manager) SetEventCallback(cb func(Event)) {
	m.mu.Lock()
	m.onEvent = cb
	m.mu.Unlock()
}

func (m *Manager) emitEvent(evt Event) {
	m.mu.RLock()
	cb := m.onEvent
	m.mu.RUnlock()
	if cb != nil {
		cb(evt)
	}
}

// AddPeer begins hole-punching to the given peer. peerAddr is the peer's
// public UDP endpoint as reported by the signaling server.
func (m *Manager) AddPeer(peerVIP net.IP, peerAddr string) error {
	log.Printf("[P2P] 添加对等节点: VIP=%s, 地址=%s", peerVIP, peerAddr)

	addr, err := net.ResolveUDPAddr("udp4", peerAddr)
	if err != nil {
		log.Printf("[P2P] 解析对端地址失败: %s -> %v", peerAddr, err)
		return err
	}

	vipStr := peerVIP.String()
	m.mu.Lock()
	// 检查是否已有活跃连接
	if existing, ok := m.links[vipStr]; ok && existing.Active {
		m.mu.Unlock()
		log.Printf("[P2P] 已有活跃连接到 %s, 跳过打洞", peerVIP)
		return nil
	}

	link := &PeerLink{
		PeerVIP:  peerVIP,
		PeerAddr: addr,
		Active:   false,
	}
	m.links[vipStr] = link
	m.mu.Unlock()

	log.Printf("[P2P] 开始 UDP 打洞: 本地=%s -> 对端=%s (VIP=%s)", m.localAddr, peerAddr, peerVIP)

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
	log.Printf("[P2P] 开始打洞过程: 目标=%s (%s), 本地=%s, 超时=%v",
		link.PeerVIP, link.PeerAddr, m.localAddr, PunchTimeout)

	// Detect potential hairpin NAT scenario (same public IP)
	localHost, _, localErr := net.SplitHostPort(m.localAddr)
	peerHost, _, peerErr := net.SplitHostPort(link.PeerAddr.String())
	samePublicIP := localErr == nil && peerErr == nil && localHost == peerHost
	if samePublicIP {
		log.Printf("[P2P] ⚠ 警告: 本地和对端公网IP相同 (%s), 两端可能在同一NAT后面。"+
			"如果路由器不支持 Hairpin NAT (NAT Loopback), UDP打洞将失败。"+
			"建议: 检查路由器是否开启NAT回环/Hairpin NAT功能。", localHost)
	}

	m.emitEvent(Event{Type: EventPunchStart, PeerVIP: link.PeerVIP, PeerAddr: link.PeerAddr.String()})

	deadline := time.After(PunchTimeout)
	ticker := time.NewTicker(ProbeInterval)
	defer ticker.Stop()

	probe := []byte(MagicProbe + ":" + generateP2PToken())
	probeCount := 0
	lastLogTime := time.Now()

	for {
		select {
		case <-m.done:
			log.Printf("[P2P] 打洞中断 (管理器关闭): %s", link.PeerVIP)
			return
		case <-deadline:
			if !link.Active {
				log.Printf("[P2P] ═══════════════════════════════════════════════")
				log.Printf("[P2P] 打洞超时: %s", link.PeerVIP)
				log.Printf("[P2P]   本地地址: %s", m.localAddr)
				log.Printf("[P2P]   对端地址: %s", link.PeerAddr)
				log.Printf("[P2P]   发送探测包: %d 个", probeCount)
				log.Printf("[P2P]   超时时间: %v", PunchTimeout)
				if samePublicIP {
					log.Printf("[P2P]   ⚠ 检测到相同公网IP: 可能是 Hairpin NAT 问题")
				}
				log.Printf("[P2P]   可能原因:")
				log.Printf("[P2P]     1. 对称NAT (Symmetric NAT) - STUN获取的端口不等于实际通信端口")
				log.Printf("[P2P]     2. 防火墙阻挡UDP数据包")
				log.Printf("[P2P]     3. 路由器不支持 Hairpin NAT (两端在同一NAT后)")
				log.Printf("[P2P]     4. 对端离线或P2P未初始化")
				log.Printf("[P2P]     5. STUN发现的地址不正确")
				log.Printf("[P2P] ═══════════════════════════════════════════════")
				m.emitEvent(Event{Type: EventPunchTimeout, PeerVIP: link.PeerVIP, PeerAddr: link.PeerAddr.String()})
			}
			return
		case <-ticker.C:
			if link.Active {
				log.Printf("[P2P] 打洞已成功, 停止发送探测包: %s", link.PeerVIP)
				return // Already established
			}
			probeCount++
			_, err := m.conn.WriteToUDP(probe, link.PeerAddr)
			if err != nil {
				log.Printf("[P2P] 发送探测包失败 #%d → %s: %v", probeCount, link.PeerAddr, err)
			}
			// Log progress every 2 seconds
			if time.Since(lastLogTime) >= 2*time.Second {
				log.Printf("[P2P] 打洞进行中: 目标=%s, 已发送 %d 个探测包, 尚未收到回应",
					link.PeerVIP, probeCount)
				lastLogTime = time.Now()
			}
		}
	}
}

// readLoop continuously reads from the UDP socket and dispatches packets.
func (m *Manager) readLoop() {
	buf := make([]byte, UDPReadBuf)
	pktCount := 0
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
			log.Printf("[P2P] 收到探测包: 来源=%s, 大小=%d字节", remoteAddr, n)
			m.handleProbe(remoteAddr)
			continue
		}

		// Real data packet - deliver via callback
		if m.onPacket != nil && n > 0 {
			pktCount++
			if pktCount <= 5 || pktCount%100 == 0 {
				log.Printf("[P2P] 收到P2P数据包 #%d: 来源=%s, 大小=%d字节", pktCount, remoteAddr, n)
			}
			pkt := make([]byte, n)
			copy(pkt, data)
			m.onPacket(pkt)
		}
	}
}

func (m *Manager) handleProbe(addr *net.UDPAddr) {
	var evt *Event

	m.mu.Lock()
	// Find which link this probe belongs to (match by addr)
	for _, link := range m.links {
		if link.PeerAddr.IP.Equal(addr.IP) && link.PeerAddr.Port == addr.Port {
			if !link.Active {
				link.Active = true
				log.Printf("[P2P] ✓ 打洞成功! 与 %s 建立直连 (UDP: %s) [精确匹配IP:Port]", link.PeerVIP, addr)
				vip := make(net.IP, len(link.PeerVIP))
				copy(vip, link.PeerVIP)
				evt = &Event{Type: EventPunchSuccess, PeerVIP: vip, PeerAddr: addr.String()}
			} else {
				log.Printf("[P2P] 收到已建立连接的探测包: %s (UDP: %s)", link.PeerVIP, addr)
			}
			m.mu.Unlock()
			if evt != nil {
				m.emitEvent(*evt)
			}
			return
		}
	}

	// Probe from unknown port, update addr if we have a link from that IP
	for _, link := range m.links {
		if link.PeerAddr.IP.Equal(addr.IP) {
			oldAddr := link.PeerAddr.String()
			link.PeerAddr = addr
			link.Active = true
			log.Printf("[P2P] ✓ 打洞成功! 与 %s 建立直连 (UDP: %s, 端口已从 %s 更新) [IP匹配,端口不同-可能NAT重映射]",
				link.PeerVIP, addr, oldAddr)
			vip := make(net.IP, len(link.PeerVIP))
			copy(vip, link.PeerVIP)
			evt = &Event{Type: EventPunchSuccess, PeerVIP: vip, PeerAddr: addr.String()}
			m.mu.Unlock()
			if evt != nil {
				m.emitEvent(*evt)
			}
			return
		}
	}

	// Log unmatched probe (rate-limited to avoid flooding)
	m.unmatchedProbeCount++
	if time.Since(m.lastUnmatchedLog) >= 5*time.Second {
		log.Printf("[P2P] 收到未匹配的探测包: 来源=%s (累计 %d 个未匹配, 已知对端: %d 个)",
			addr, m.unmatchedProbeCount, len(m.links))
		for vip, link := range m.links {
			log.Printf("[P2P]   已知对端: VIP=%s, 地址=%s, 活跃=%v", vip, link.PeerAddr, link.Active)
		}
		m.lastUnmatchedLog = time.Now()
	}
	m.mu.Unlock()
}

func isProbe(data []byte) bool {
	return len(data) >= len(MagicProbe) && string(data[:len(MagicProbe)]) == MagicProbe
}

func generateP2PToken() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}
