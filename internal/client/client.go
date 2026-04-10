// Package client implements the 神区互联 client.
package client

import (
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/caikun233/godqv-networking/pkg/p2p"
	"github.com/caikun233/godqv-networking/pkg/protocol"
)

// Config holds client configuration.
type Config struct {
	ServerAddr string `json:"server_addr"`
	Username   string `json:"username"`
	Password   string `json:"password"`
	RoomName   string `json:"room_name"`
	RoomPass   string `json:"room_password"`
}

// PeerInfo represents a peer in the network.
type PeerInfo struct {
	Username  string
	VirtualIP net.IP
	Online    bool
	P2P       bool // true if direct UDP link is active
}

// Client is the main client struct.
type Client struct {
	config    Config
	conn      net.Conn
	token     string
	virtualIP net.IP
	subnet    net.IPNet
	roomName  string
	peers     []PeerInfo
	peersMu   sync.RWMutex
	tunWriter TunWriter
	p2pMgr    *p2p.Manager
	mu        sync.Mutex
	done      chan struct{}
	closeOnce sync.Once
	onPeerUpdate  func([]PeerInfo)
	onP2PEvent    func(p2p.Event)
}

// TunWriter is an interface for writing packets to the TUN device.
type TunWriter interface {
	WritePacket(packet []byte) error
}

// New creates a new client.
func New(cfg Config) *Client {
	return &Client{
		config: cfg,
		done:   make(chan struct{}),
	}
}

// SetConfig updates the client configuration. Must be called before JoinRoom.
func (c *Client) SetConfig(cfg Config) {
	c.config = cfg
}

// ServerAddr returns the configured server address.
func (c *Client) ServerAddr() string {
	return c.config.ServerAddr
}

// Username returns the configured username.
func (c *Client) Username() string {
	return c.config.Username
}

// SetTunWriter sets the TUN device writer for receiving packets.
func (c *Client) SetTunWriter(tw TunWriter) {
	c.tunWriter = tw
}

// SetPeerUpdateCallback sets a callback for peer updates.
func (c *Client) SetPeerUpdateCallback(cb func([]PeerInfo)) {
	c.onPeerUpdate = cb
}

// SetP2PEventCallback sets a callback for P2P hole-punching events.
func (c *Client) SetP2PEventCallback(cb func(p2p.Event)) {
	c.onP2PEvent = cb
}

// Connect connects to the server and authenticates.
func (c *Client) Connect() error {
	log.Printf("[Client] 连接到服务器 %s...", c.config.ServerAddr)

	conn, err := net.DialTimeout("tcp", c.config.ServerAddr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("连接失败: %w", err)
	}
	c.conn = conn

	// Authenticate
	authReq := &protocol.AuthRequest{
		Username: c.config.Username,
		Password: c.config.Password,
	}
	data, err := protocol.EncodeAuthRequest(authReq)
	if err != nil {
		conn.Close()
		return fmt.Errorf("编码认证请求失败: %w", err)
	}

	if err := protocol.WriteMessage(conn, &protocol.Message{
		Type:    protocol.MsgTypeAuth,
		Payload: data,
	}); err != nil {
		conn.Close()
		return fmt.Errorf("发送认证请求失败: %w", err)
	}

	// Read auth response
	msg, err := protocol.ReadMessage(conn)
	if err != nil {
		conn.Close()
		return fmt.Errorf("读取认证响应失败: %w", err)
	}

	if msg.Type != protocol.MsgTypeAuthResp {
		conn.Close()
		return fmt.Errorf("意外的消息类型: %d", msg.Type)
	}

	authResp, err := protocol.DecodeAuthResponse(msg.Payload)
	if err != nil {
		conn.Close()
		return fmt.Errorf("解码认证响应失败: %w", err)
	}

	if !authResp.Success {
		conn.Close()
		return fmt.Errorf("认证失败: %s", authResp.Message)
	}

	c.token = authResp.Token
	log.Printf("[Client] 认证成功，欢迎 %s", c.config.Username)

	return nil
}

// Register sends a registration request to the server. The connection must
// already be established (call after dialing, before Connect/auth).
func (c *Client) Register(serverAddr, username, password string) error {
	conn, err := net.DialTimeout("tcp", serverAddr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("连接失败: %w", err)
	}
	defer conn.Close()

	req := &protocol.RegisterRequest{
		Username: username,
		Password: password,
	}
	data, err := protocol.EncodeRegisterRequest(req)
	if err != nil {
		return fmt.Errorf("编码注册请求失败: %w", err)
	}

	if err := protocol.WriteMessage(conn, &protocol.Message{
		Type:    protocol.MsgTypeRegister,
		Payload: data,
	}); err != nil {
		return fmt.Errorf("发送注册请求失败: %w", err)
	}

	msg, err := protocol.ReadMessage(conn)
	if err != nil {
		return fmt.Errorf("读取注册响应失败: %w", err)
	}

	if msg.Type != protocol.MsgTypeRegisterResp {
		return fmt.Errorf("意外的消息类型: %d", msg.Type)
	}

	resp, err := protocol.DecodeRegisterResponse(msg.Payload)
	if err != nil {
		return fmt.Errorf("解码注册响应失败: %w", err)
	}

	if !resp.Success {
		return fmt.Errorf("注册失败: %s", resp.Message)
	}

	return nil
}

// CreateRoom sends a create-room request to the server.
func (c *Client) CreateRoom(name, password string) error {
	req := &protocol.CreateRoomRequest{
		RoomName: name,
		Password: password,
	}
	data, err := protocol.EncodeCreateRoomRequest(req)
	if err != nil {
		return fmt.Errorf("编码创建房间请求失败: %w", err)
	}

	c.mu.Lock()
	err = protocol.WriteMessage(c.conn, &protocol.Message{
		Type:    protocol.MsgTypeCreateRoom,
		Payload: data,
	})
	c.mu.Unlock()
	if err != nil {
		return fmt.Errorf("发送创建房间请求失败: %w", err)
	}

	msg, err := protocol.ReadMessage(c.conn)
	if err != nil {
		return fmt.Errorf("读取创建房间响应失败: %w", err)
	}

	if msg.Type != protocol.MsgTypeCreateRoomResp {
		return fmt.Errorf("意外的消息类型: %d", msg.Type)
	}

	resp, err := protocol.DecodeCreateRoomResponse(msg.Payload)
	if err != nil {
		return fmt.Errorf("解码创建房间响应失败: %w", err)
	}

	if !resp.Success {
		return fmt.Errorf("创建房间失败: %s", resp.Message)
	}

	log.Printf("[Client] 房间创建成功: %s", resp.Message)
	return nil
}

// ListRooms requests the list of available rooms from the server.
func (c *Client) ListRooms() ([]protocol.RoomInfo, error) {
	c.mu.Lock()
	err := protocol.WriteMessage(c.conn, &protocol.Message{
		Type: protocol.MsgTypeListRooms,
	})
	c.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("发送列表请求失败: %w", err)
	}

	msg, err := protocol.ReadMessage(c.conn)
	if err != nil {
		return nil, fmt.Errorf("读取列表响应失败: %w", err)
	}

	if msg.Type != protocol.MsgTypeListRoomsResp {
		return nil, fmt.Errorf("意外的消息类型: %d", msg.Type)
	}

	resp, err := protocol.DecodeListRoomsResponse(msg.Payload)
	if err != nil {
		return nil, fmt.Errorf("解码列表响应失败: %w", err)
	}

	return resp.Rooms, nil
}

// JoinRoom joins a virtual network room.
func (c *Client) JoinRoom() error {
	req := &protocol.JoinRoomRequest{
		RoomName: c.config.RoomName,
		Password: c.config.RoomPass,
	}
	data, err := protocol.EncodeJoinRoomRequest(req)
	if err != nil {
		return fmt.Errorf("编码加入请求失败: %w", err)
	}

	if err := protocol.WriteMessage(c.conn, &protocol.Message{
		Type:    protocol.MsgTypeJoinRoom,
		Payload: data,
	}); err != nil {
		return fmt.Errorf("发送加入请求失败: %w", err)
	}

	// Read join response
	msg, err := protocol.ReadMessage(c.conn)
	if err != nil {
		return fmt.Errorf("读取加入响应失败: %w", err)
	}

	if msg.Type != protocol.MsgTypeJoinRoomResp {
		return fmt.Errorf("意外的消息类型: %d", msg.Type)
	}

	resp, err := protocol.DecodeJoinRoomResponse(msg.Payload)
	if err != nil {
		return fmt.Errorf("解码加入响应失败: %w", err)
	}

	if !resp.Success {
		return fmt.Errorf("加入房间失败: %s", resp.Message)
	}

	c.virtualIP = resp.VirtualIP
	c.subnet = resp.Subnet
	c.roomName = resp.RoomName

	log.Printf("[Client] 已加入房间 %s, 虚拟IP: %s, 子网: %s", resp.RoomName, resp.VirtualIP, resp.Subnet.String())

	return nil
}

// InitP2P initialises the P2P UDP manager. Call after JoinRoom and setting
// the TUN writer, before StartReceiving.
func (c *Client) InitP2P() error {
	log.Printf("[P2P] 正在初始化P2P管理器...")
	mgr, err := p2p.NewManager(func(packet []byte) {
		// Data received directly via P2P – write to TUN.
		if c.tunWriter != nil {
			c.tunWriter.WritePacket(packet)
		}
	})
	if err != nil {
		return fmt.Errorf("init P2P: %w", err)
	}
	c.p2pMgr = mgr

	// Wire P2P event callback if set.
	if c.onP2PEvent != nil {
		mgr.SetEventCallback(c.onP2PEvent)
	}

	// Report our public UDP address to the server via a P2POffer so the
	// server knows where to direct other peers.
	log.Printf("[P2P] 向服务器报告本地公网UDP地址: %s (VIP=%s)", mgr.LocalAddr(), c.virtualIP)
	offer := &protocol.P2POffer{
		FromVIP: c.virtualIP,
		UDPAddr: mgr.LocalAddr(),
	}
	data, _ := protocol.EncodeP2POffer(offer)
	c.mu.Lock()
	err = protocol.WriteMessage(c.conn, &protocol.Message{Type: protocol.MsgTypeP2POffer, Payload: data})
	c.mu.Unlock()
	if err != nil {
		log.Printf("[P2P] 发送P2POffer到服务器失败: %v", err)
		return fmt.Errorf("send P2P offer: %w", err)
	}
	log.Printf("[P2P] P2P初始化完成, 等待对端信令消息...")

	return nil
}

// VirtualIP returns the assigned virtual IP.
func (c *Client) VirtualIP() net.IP {
	return c.virtualIP
}

// Subnet returns the virtual network subnet.
func (c *Client) Subnet() net.IPNet {
	return c.subnet
}

// GetPeers returns current peer list.
func (c *Client) GetPeers() []PeerInfo {
	c.peersMu.RLock()
	defer c.peersMu.RUnlock()
	result := make([]PeerInfo, len(c.peers))
	copy(result, c.peers)
	return result
}

// SendPacket sends an IP packet. Tries P2P first, falls back to TCP relay.
func (c *Client) SendPacket(packet []byte) error {
	// Try P2P if available. Verify IPv4 header before extracting dest IP.
	if c.p2pMgr != nil && len(packet) >= 20 && packet[0]>>4 == 4 {
		dstIP := net.IP(packet[16:20])
		if c.p2pMgr.SendPacket(dstIP, packet) {
			return nil // Sent via P2P!
		}
	}

	// Fall back to TCP relay.
	c.mu.Lock()
	defer c.mu.Unlock()
	return protocol.WriteMessage(c.conn, &protocol.Message{
		Type:    protocol.MsgTypeData,
		Payload: packet,
	})
}

// StartReceiving starts the message receiving loop.
func (c *Client) StartReceiving() {
	log.Printf("[Client] 开始接收消息循环")
	go c.receiveLoop()
	go c.keepaliveLoop()
}

// Close closes the connection safely. It can be called multiple times.
func (c *Client) Close() error {
	var err error
	c.closeOnce.Do(func() {
		close(c.done)
		if c.p2pMgr != nil {
			c.p2pMgr.Close()
		}
		if c.conn != nil {
			// Send leave message
			protocol.WriteMessage(c.conn, &protocol.Message{Type: protocol.MsgTypeLeave})
			err = c.conn.Close()
		}
	})
	return err
}

// Done returns a channel that is closed when the client is done.
func (c *Client) Done() <-chan struct{} {
	return c.done
}

func (c *Client) receiveLoop() {
	defer func() {
		c.closeOnce.Do(func() {
			close(c.done)
		})
	}()

	for {
		select {
		case <-c.done:
			return
		default:
		}

		if err := c.conn.SetReadDeadline(time.Now().Add(60 * time.Second)); err != nil {
			return
		}

		msg, err := protocol.ReadMessage(c.conn)
		if err != nil {
			log.Printf("[Client] 接收消息失败: %v", err)
			return
		}

		switch msg.Type {
		case protocol.MsgTypeData:
			if c.tunWriter != nil {
				if err := c.tunWriter.WritePacket(msg.Payload); err != nil {
					log.Printf("[Client] 写入TUN设备失败: %v", err)
				}
			}

		case protocol.MsgTypePeerUpdate:
			update, err := protocol.DecodePeerUpdate(msg.Payload)
			if err != nil {
				log.Printf("[Client] 解码节点更新失败: %v", err)
				continue
			}
			c.peersMu.Lock()
			c.peers = make([]PeerInfo, len(update.Peers))
			for i, p := range update.Peers {
				isP2P := false
				if c.p2pMgr != nil {
					isP2P = c.p2pMgr.GetActiveLink(p.VirtualIP)
				}
				c.peers[i] = PeerInfo{
					Username:  p.Username,
					VirtualIP: p.VirtualIP,
					Online:    p.Online,
					P2P:       isP2P,
				}
			}
			c.peersMu.Unlock()

			log.Printf("[Client] 节点更新 - 房间 %s, %d 个节点在线", update.RoomName, len(update.Peers))
			for _, p := range update.Peers {
				log.Printf("  - %s (%s) [在线: %v]", p.Username, p.VirtualIP, p.Online)
			}

			// Attempt P2P hole-punching for new peers.
			if c.p2pMgr != nil {
				for _, p := range update.Peers {
					if !p.VirtualIP.Equal(c.virtualIP) && !c.p2pMgr.GetActiveLink(p.VirtualIP) {
						c.requestP2PPunch(p.VirtualIP)
					}
				}
			}

			if c.onPeerUpdate != nil {
				c.onPeerUpdate(c.GetPeers())
			}

		case protocol.MsgTypePong:
			// Keepalive response received

		case protocol.MsgTypeError:
			errMsg, err := protocol.DecodeErrorMsg(msg.Payload)
			if err == nil {
				log.Printf("[Client] 服务器错误 [%d]: %s", errMsg.Code, errMsg.Message)
			}

		case protocol.MsgTypeP2POffer:
			c.handleP2POffer(msg.Payload)

		case protocol.MsgTypeP2PPunchResp:
			c.handleP2PPunchResp(msg.Payload)

		case protocol.MsgTypeP2PAnswer:
			c.handleP2PAnswer(msg.Payload)

		default:
			log.Printf("[Client] 收到未知消息类型: 0x%02x, 载荷大小=%d字节", msg.Type, len(msg.Payload))
		}
	}
}

func (c *Client) requestP2PPunch(targetVIP net.IP) {
	log.Printf("[P2P] 发送打洞请求: 目标VIP=%s", targetVIP)
	req := &protocol.P2PPunchRequest{TargetVIP: targetVIP}
	data, _ := protocol.EncodeP2PPunchRequest(req)
	c.mu.Lock()
	err := protocol.WriteMessage(c.conn, &protocol.Message{Type: protocol.MsgTypeP2PPunchReq, Payload: data})
	c.mu.Unlock()
	if err != nil {
		log.Printf("[P2P] 发送打洞请求失败: 目标VIP=%s, 错误=%v", targetVIP, err)
	}
}

func (c *Client) handleP2POffer(payload []byte) {
	offer, err := protocol.DecodeP2POffer(payload)
	if err != nil {
		log.Printf("[P2P] 解码P2POffer失败: %v", err)
		return
	}
	if c.p2pMgr == nil {
		log.Printf("[P2P] 收到P2POffer但P2P管理器未初始化, 忽略: FromVIP=%s, UDPAddr=%s", offer.FromVIP, offer.UDPAddr)
		return
	}
	if offer.UDPAddr == "" {
		log.Printf("[P2P] 收到P2POffer但对端无UDP地址: FromVIP=%s", offer.FromVIP)
		return
	}
	log.Printf("[P2P] 收到打洞请求: 来自=%s, 对端UDP=%s, Token=%s", offer.FromVIP, offer.UDPAddr, offer.Token)
	log.Printf("[P2P]   本地UDP地址=%s, 准备添加对端并发送应答", c.p2pMgr.LocalAddr())
	c.p2pMgr.AddPeer(offer.FromVIP, offer.UDPAddr)

	// Send answer back
	answer := &protocol.P2PAnswer{
		FromVIP:  c.virtualIP,
		UDPAddr:  c.p2pMgr.LocalAddr(),
		Token:    offer.Token,
		Accepted: true,
	}
	data, _ := protocol.EncodeP2PAnswer(answer)
	c.mu.Lock()
	protocol.WriteMessage(c.conn, &protocol.Message{Type: protocol.MsgTypeP2PAnswer, Payload: data})
	c.mu.Unlock()
	log.Printf("[P2P] 已发送P2PAnswer: 本地VIP=%s, 本地UDP=%s, Token=%s", c.virtualIP, c.p2pMgr.LocalAddr(), offer.Token)
}

func (c *Client) handleP2PPunchResp(payload []byte) {
	resp, err := protocol.DecodeP2PPunchResponse(payload)
	if err != nil {
		log.Printf("[P2P] 解码P2PPunchResponse失败: %v", err)
		return
	}
	if c.p2pMgr == nil {
		log.Printf("[P2P] 收到P2PPunchResp但P2P管理器未初始化: PeerVIP=%s, PeerAddr=%s", resp.PeerVIP, resp.PeerAddr)
		return
	}
	if resp.PeerAddr == "" {
		log.Printf("[P2P] 收到P2PPunchResp但对端无UDP地址: PeerVIP=%s (对端可能未初始化P2P)", resp.PeerVIP)
		return
	}
	log.Printf("[P2P] 收到打洞响应: 对端VIP=%s, 对端UDP=%s, Token=%s", resp.PeerVIP, resp.PeerAddr, resp.Token)
	c.p2pMgr.AddPeer(resp.PeerVIP, resp.PeerAddr)
}

func (c *Client) handleP2PAnswer(payload []byte) {
	answer, err := protocol.DecodeP2PAnswer(payload)
	if err != nil {
		log.Printf("[P2P] 解码P2PAnswer失败: %v", err)
		return
	}
	if c.p2pMgr == nil {
		log.Printf("[P2P] 收到P2PAnswer但P2P管理器未初始化: FromVIP=%s", answer.FromVIP)
		return
	}
	if answer.UDPAddr == "" {
		log.Printf("[P2P] 收到P2PAnswer但对端无UDP地址: FromVIP=%s, Accepted=%v", answer.FromVIP, answer.Accepted)
		return
	}
	if !answer.Accepted {
		log.Printf("[P2P] 对端拒绝P2P连接: FromVIP=%s, UDPAddr=%s", answer.FromVIP, answer.UDPAddr)
		return
	}
	log.Printf("[P2P] 收到打洞应答: 对端VIP=%s, 对端UDP=%s, Token=%s, Accepted=%v",
		answer.FromVIP, answer.UDPAddr, answer.Token, answer.Accepted)
	c.p2pMgr.AddPeer(answer.FromVIP, answer.UDPAddr)
}

func (c *Client) keepaliveLoop() {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-c.done:
			return
		case <-ticker.C:
			c.mu.Lock()
			err := protocol.WriteMessage(c.conn, &protocol.Message{Type: protocol.MsgTypePing})
			c.mu.Unlock()
			if err != nil {
				log.Printf("[Client] 发送心跳失败: %v", err)
				return
			}
		}
	}
}
