// Package p2p implements UDP hole-punching for direct peer-to-peer communication.
//
// The flow is:
//  1. Client opens a local UDP socket and discovers its public endpoint via STUN.
//  2. NAT type is detected by querying multiple STUN servers and comparing results.
//  3. Client sends a P2PPunchReq to the signaling server with the target peer's VIP.
//  4. Server relays a P2POffer to the target containing the requester's public UDP addr,
//     NAT type, and all candidate addresses.
//  5. Both sides send UDP probes to each other's candidate addresses. For Symmetric NAT
//     peers, probes are also sent to predicted port ranges based on observed port deltas.
//  6. Once either side receives a probe, the hole is punched and the link is up.
//  7. IP packets are sent over UDP instead of the TCP relay.
//  8. If hole-punching fails after a timeout the TCP relay continues to be used.
package p2p

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"strconv"
	"sync"
	"time"
)

const (
	// PunchTimeout is how long to attempt hole punching before giving up.
	// Extended from 10s to 20s to give Symmetric NAT port prediction more time.
	PunchTimeout = 20 * time.Second

	// ProbeInterval is how often to send UDP probes during hole punching.
	ProbeInterval = 100 * time.Millisecond

	// UDPReadBuf is the read buffer size for UDP packets.
	UDPReadBuf = 1500

	// MagicProbe is a small magic header sent in punch probes so we can
	// distinguish them from real data packets.
	MagicProbe = "GODQV_PROBE"

	// SymmetricPortSpread is how many predicted ports to try on each side
	// of the last known port for Symmetric NAT peers.
	SymmetricPortSpread = 32
)

// PeerLink represents a successfully established P2P UDP link to a peer.
type PeerLink struct {
	PeerVIP    net.IP
	PeerAddr   *net.UDPAddr   // Primary address (may be updated on probe receipt)
	Candidates []*net.UDPAddr // All candidate addresses including predicted ports
	PeerNAT    NATType        // Peer's NAT type
	Active     bool
}

// Manager handles P2P UDP connections for the local client.
type Manager struct {
	mu        sync.RWMutex
	conn      *net.UDPConn
	localAddr string // public address discovered via STUN
	natInfo   *NATInfo

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

	// Detect NAT type via multiple STUN queries.
	log.Printf("[P2P] 正在通过多STUN查询检测NAT类型...")
	natInfo, err := stunDetectNAT(conn)
	if err != nil {
		log.Printf("[P2P] NAT检测失败: %v (将仅使用本地地址)", err)
		m.localAddr = conn.LocalAddr().String()
		m.natInfo = &NATInfo{Type: NATUnknown, MappedAddr: m.localAddr}
	} else {
		m.localAddr = natInfo.MappedAddr
		m.natInfo = natInfo
		log.Printf("[P2P] 公网 UDP 地址: %s, NAT类型: %s", natInfo.MappedAddr, natInfo.Type)
		if natInfo.Type == NATSymmetric {
			log.Printf("[P2P] ⚠ 检测到对称NAT: 端口增量=%d, 最后端口=%d, 候选地址=%d个",
				natInfo.PortDelta, natInfo.LastPort, len(natInfo.Candidates))
		}
	}

	go m.readLoop()
	log.Printf("[P2P] P2P 管理器初始化完成，开始监听 UDP 数据包")

	return m, nil
}

// LocalAddr returns the discovered public UDP address.
func (m *Manager) LocalAddr() string {
	return m.localAddr
}

// NATInfo returns the detected NAT information.
func (m *Manager) NATInfo() *NATInfo {
	return m.natInfo
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
// primary public UDP endpoint. candidates is a list of additional addresses
// to try (may be nil). peerNAT is the peer's detected NAT type.
func (m *Manager) AddPeer(peerVIP net.IP, peerAddr string, candidates []string, peerNAT NATType) error {
	log.Printf("[P2P] 添加对等节点: VIP=%s, 主地址=%s, NAT=%s, 候选数=%d",
		peerVIP, peerAddr, peerNAT, len(candidates))

	addr, err := net.ResolveUDPAddr("udp4", peerAddr)
	if err != nil {
		log.Printf("[P2P] 解析对端地址失败: %s -> %v", peerAddr, err)
		return err
	}

	vipStr := peerVIP.String()
	m.mu.Lock()
	// Check if we already have an active connection.
	if existing, ok := m.links[vipStr]; ok && existing.Active {
		m.mu.Unlock()
		log.Printf("[P2P] 已有活跃连接到 %s, 跳过打洞", peerVIP)
		return nil
	}

	// Build candidate list.
	candidateAddrs := buildCandidateAddrs(addr, candidates, peerNAT)

	link := &PeerLink{
		PeerVIP:    peerVIP,
		PeerAddr:   addr,
		Candidates: candidateAddrs,
		PeerNAT:    peerNAT,
		Active:     false,
	}
	m.links[vipStr] = link
	m.mu.Unlock()

	log.Printf("[P2P] 开始 UDP 打洞: 本地=%s (NAT=%s) -> 对端=%s (NAT=%s), 总候选地址=%d",
		m.localAddr, m.natInfo.Type, peerAddr, peerNAT, len(candidateAddrs))

	// Start punching in background.
	go m.punchHole(link)

	return nil
}

// buildCandidateAddrs constructs the full list of UDP addresses to probe.
// For Symmetric NAT peers, it adds predicted port candidates.
func buildCandidateAddrs(primary *net.UDPAddr, candidates []string, peerNAT NATType) []*net.UDPAddr {
	seen := make(map[string]bool)
	var addrs []*net.UDPAddr

	// Add primary address.
	key := primary.String()
	seen[key] = true
	addrs = append(addrs, primary)

	// Add explicit candidates from signaling.
	for _, c := range candidates {
		if seen[c] {
			continue
		}
		a, err := net.ResolveUDPAddr("udp4", c)
		if err != nil {
			continue
		}
		seen[a.String()] = true
		addrs = append(addrs, a)
	}

	// For Symmetric NAT peers, add predicted port candidates.
	if peerNAT == NATSymmetric {
		addrs = appendPredictedPorts(addrs, primary, seen)
	}

	return addrs
}

// isValidPredictedPort checks if a port is in the valid range for NAT
// predicted ports. We use 1024-65535 since NATs typically allocate from the
// ephemeral port range, but some NATs may use registered ports (1024-49151).
func isValidPredictedPort(port int) bool {
	return port >= 1024 && port <= 65535
}

// appendPredictedPorts adds predicted port addresses for a Symmetric NAT peer.
// It tries ports both above and below the primary port with delta=1, creating
// a spread of candidate ports to catch the NAT's port allocation.
func appendPredictedPorts(addrs []*net.UDPAddr, primary *net.UDPAddr, seen map[string]bool) []*net.UDPAddr {
	basePort := primary.Port

	// Try ports around the primary port with delta 1.
	// For Symmetric NAT, port allocations are often sequential.
	for offset := 1; offset <= SymmetricPortSpread; offset++ {
		for _, dir := range []int{1, -1} {
			port := basePort + (offset * dir)
			if !isValidPredictedPort(port) {
				continue
			}
			addr := &net.UDPAddr{IP: primary.IP, Port: port}
			k := addr.String()
			if seen[k] {
				continue
			}
			seen[k] = true
			addrs = append(addrs, addr)
		}
	}

	log.Printf("[P2P] 为对称NAT对端添加端口预测候选: 基础端口=%d, 新增%d个候选",
		basePort, len(addrs)-1)

	return addrs
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

// punchHole attempts to establish a direct UDP link by sending probes to all
// candidate addresses. For Symmetric NAT peers, this includes a range of
// predicted ports based on observed port allocation patterns.
func (m *Manager) punchHole(link *PeerLink) {
	log.Printf("[P2P] 开始打洞过程: 目标=%s (%s), 本地=%s (NAT=%s), 对端NAT=%s, 候选数=%d, 超时=%v",
		link.PeerVIP, link.PeerAddr, m.localAddr, m.natInfo.Type, link.PeerNAT,
		len(link.Candidates), PunchTimeout)

	// Detect potential hairpin NAT scenario (same public IP).
	localHost, _, localErr := net.SplitHostPort(m.localAddr)
	peerHost, _, peerErr := net.SplitHostPort(link.PeerAddr.String())
	samePublicIP := localErr == nil && peerErr == nil && localHost == peerHost
	if samePublicIP {
		log.Printf("[P2P] ⚠ 警告: 本地和对端公网IP相同 (%s), 两端可能在同一NAT后面。"+
			"如果路由器不支持 Hairpin NAT (NAT Loopback), UDP打洞将失败。"+
			"建议: 检查路由器是否开启NAT回环/Hairpin NAT功能。", localHost)
	}

	// Log NAT combination for diagnostics.
	if link.PeerNAT == NATSymmetric || m.natInfo.Type == NATSymmetric {
		log.Printf("[P2P] ═══════════════════════════════════════════════")
		log.Printf("[P2P] 检测到对称NAT场景!")
		log.Printf("[P2P]   本地NAT: %s", m.natInfo.Type)
		log.Printf("[P2P]   对端NAT: %s", link.PeerNAT)
		if link.PeerNAT == NATSymmetric && m.natInfo.Type == NATSymmetric {
			log.Printf("[P2P]   ⚠ 双方都是对称NAT - 打洞非常困难, 将尝试端口预测")
		} else if link.PeerNAT == NATSymmetric {
			log.Printf("[P2P]   对端为对称NAT, 本地为锥形NAT - 使用端口预测策略")
		} else {
			log.Printf("[P2P]   本地为对称NAT, 对端为锥形NAT - 使用端口预测策略")
		}
		log.Printf("[P2P]   候选地址数: %d (包含端口预测)", len(link.Candidates))
		log.Printf("[P2P] ═══════════════════════════════════════════════")
	}

	m.emitEvent(Event{Type: EventPunchStart, PeerVIP: link.PeerVIP, PeerAddr: link.PeerAddr.String()})

	deadline := time.After(PunchTimeout)
	ticker := time.NewTicker(ProbeInterval)
	defer ticker.Stop()

	probe := []byte(MagicProbe + ":" + generateP2PToken())
	probeCount := 0
	lastLogTime := time.Now()
	candidateIdx := 0

	for {
		select {
		case <-m.done:
			log.Printf("[P2P] 打洞中断 (管理器关闭): %s", link.PeerVIP)
			return
		case <-deadline:
			if !link.Active {
				log.Printf("[P2P] ═══════════════════════════════════════════════")
				log.Printf("[P2P] 打洞超时: %s", link.PeerVIP)
				log.Printf("[P2P]   本地地址: %s (NAT=%s)", m.localAddr, m.natInfo.Type)
				log.Printf("[P2P]   对端地址: %s (NAT=%s)", link.PeerAddr, link.PeerNAT)
				log.Printf("[P2P]   发送探测包: %d 个 (到 %d 个候选地址)", probeCount, len(link.Candidates))
				log.Printf("[P2P]   超时时间: %v", PunchTimeout)
				if samePublicIP {
					log.Printf("[P2P]   ⚠ 检测到相同公网IP: 可能是 Hairpin NAT 问题")
				}
				log.Printf("[P2P]   可能原因:")
				log.Printf("[P2P]     1. 对称NAT (Symmetric NAT) - 端口预测未命中")
				log.Printf("[P2P]     2. 防火墙阻挡UDP数据包")
				log.Printf("[P2P]     3. 路由器不支持 Hairpin NAT (两端在同一NAT后)")
				log.Printf("[P2P]     4. 对端离线或P2P未初始化")
				log.Printf("[P2P]     5. 双方都是对称NAT且端口分配随机")
				log.Printf("[P2P]   将继续使用TCP中继通信")
				log.Printf("[P2P] ═══════════════════════════════════════════════")
				m.emitEvent(Event{Type: EventPunchTimeout, PeerVIP: link.PeerVIP, PeerAddr: link.PeerAddr.String()})
			}
			return
		case <-ticker.C:
			if link.Active {
				log.Printf("[P2P] 打洞已成功, 停止发送探测包: %s", link.PeerVIP)
				return // Already established
			}

			// Send probes to a batch of candidate addresses per tick.
			// For standard (cone) NAT, we only have a few candidates.
			// For Symmetric NAT, we have many predicted ports.
			batchSize := 8
			if len(link.Candidates) <= batchSize {
				// Few candidates: send to all each tick.
				for _, addr := range link.Candidates {
					probeCount++
					if _, err := m.conn.WriteToUDP(probe, addr); err != nil {
						log.Printf("[P2P] 发送探测包失败 #%d → %s: %v", probeCount, addr, err)
					}
				}
			} else {
				// Many candidates: rotate through them in batches,
				// always including the primary address.
				probeCount++
				if _, err := m.conn.WriteToUDP(probe, link.PeerAddr); err != nil {
					log.Printf("[P2P] 发送探测包失败 #%d → %s (主地址): %v", probeCount, link.PeerAddr, err)
				}

				for i := 0; i < batchSize-1; i++ {
					idx := candidateIdx % len(link.Candidates)
					probeCount++
					m.conn.WriteToUDP(probe, link.Candidates[idx])
					candidateIdx++
				}
			}

			// Log progress every 3 seconds.
			if time.Since(lastLogTime) >= 3*time.Second {
				log.Printf("[P2P] 打洞进行中: 目标=%s (NAT=%s), 已发送 %d 个探测包到 %d 个候选地址, 尚未收到回应",
					link.PeerVIP, link.PeerNAT, probeCount, len(link.Candidates))
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
	// Find which link this probe belongs to.

	// Pass 1: Exact IP:port match.
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

	// Pass 2: Match by IP and check if port is in candidates list.
	// This handles Symmetric NAT where the peer's actual port differs from
	// the primary STUN address but matches a predicted candidate.
	for _, link := range m.links {
		if !link.PeerAddr.IP.Equal(addr.IP) {
			continue
		}
		// Check if this port is in our candidate list.
		inCandidates := false
		for _, c := range link.Candidates {
			if c.IP.Equal(addr.IP) && c.Port == addr.Port {
				inCandidates = true
				break
			}
		}

		oldAddr := link.PeerAddr.String()
		link.PeerAddr = addr
		link.Active = true

		if inCandidates {
			log.Printf("[P2P] ✓ 打洞成功! 与 %s 建立直连 (UDP: %s, 从候选地址 %s 更新) [端口预测命中]",
				link.PeerVIP, addr, oldAddr)
		} else {
			log.Printf("[P2P] ✓ 打洞成功! 与 %s 建立直连 (UDP: %s, 端口从 %s 更新) [IP匹配,端口不同-NAT重映射]",
				link.PeerVIP, addr, oldAddr)
		}

		vip := make(net.IP, len(link.PeerVIP))
		copy(vip, link.PeerVIP)
		evt = &Event{Type: EventPunchSuccess, PeerVIP: vip, PeerAddr: addr.String()}
		m.mu.Unlock()
		if evt != nil {
			m.emitEvent(*evt)
		}
		return
	}

	// Pass 3: Check local/LAN candidates – the IP might be a private address
	// that matches a candidate for a peer on the same LAN.
	for _, link := range m.links {
		for _, c := range link.Candidates {
			if c.IP.Equal(addr.IP) {
				oldAddr := link.PeerAddr.String()
				link.PeerAddr = addr
				link.Active = true
				log.Printf("[P2P] ✓ 打洞成功! 与 %s 建立直连 (UDP: %s, 通过LAN候选地址匹配, 原地址=%s)",
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
	}

	// Log unmatched probe (rate-limited to avoid flooding).
	m.unmatchedProbeCount++
	if time.Since(m.lastUnmatchedLog) >= 5*time.Second {
		log.Printf("[P2P] 收到未匹配的探测包: 来源=%s (累计 %d 个未匹配, 已知对端: %d 个)",
			addr, m.unmatchedProbeCount, len(m.links))
		for vip, link := range m.links {
			log.Printf("[P2P]   已知对端: VIP=%s, 主地址=%s, NAT=%s, 候选数=%d, 活跃=%v",
				vip, link.PeerAddr, link.PeerNAT, len(link.Candidates), link.Active)
		}
		m.lastUnmatchedLog = time.Now()
	}
	m.mu.Unlock()
}

// PredictedPorts generates a list of predicted port numbers for a Symmetric
// NAT peer given the last observed port and port delta.
func PredictedPorts(lastPort, delta, count int) []int {
	if delta == 0 {
		delta = 1
	}
	var ports []int
	for i := 1; i <= count; i++ {
		p := lastPort + (delta * i)
		if isValidPredictedPort(p) {
			ports = append(ports, p)
		}
		// Also try the negative direction.
		p = lastPort - (delta * i)
		if isValidPredictedPort(p) {
			ports = append(ports, p)
		}
	}
	return ports
}

// FormatCandidateSummary returns a human-readable summary of candidates.
func FormatCandidateSummary(candidates []string) string {
	if len(candidates) == 0 {
		return "(无)"
	}
	if len(candidates) <= 3 {
		return fmt.Sprintf("%v", candidates)
	}
	return fmt.Sprintf("[%s, %s, ... 共%d个]", candidates[0], candidates[1], len(candidates))
}

func isProbe(data []byte) bool {
	return len(data) >= len(MagicProbe) && string(data[:len(MagicProbe)]) == MagicProbe
}

func generateP2PToken() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// portFromAddr extracts the port number from an "ip:port" string. Returns 0
// on error.
func portFromAddr(addr string) int {
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	p, _ := strconv.Atoi(portStr)
	return p
}
