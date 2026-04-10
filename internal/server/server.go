// Package server implements the 神区互联 server.
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/caikun233/godqv-networking/pkg/network"
	"github.com/caikun233/godqv-networking/pkg/protocol"
	"github.com/caikun233/godqv-networking/pkg/store"
)

// Config holds server configuration.
type Config struct {
	ListenAddr  string            `json:"listen_addr"`
	SubnetCIDR  string            `json:"subnet_cidr"` // Base subnet for rooms
	Users       map[string]string `json:"users"`        // Legacy: username -> password (used when DB is nil)
	RoomConfigs map[string]RoomConfig `json:"rooms"`    // Legacy: room name -> config (used when DB is nil)
	Database    *store.Config     `json:"database"`     // PostgreSQL configuration (optional)
}

// RoomConfig holds per-room configuration.
type RoomConfig struct {
	Password string `json:"password"` // Empty = no password
	Subnet   string `json:"subnet"`   // e.g., "10.100.1.0/24"
}

// Client represents a connected client.
type Client struct {
	conn       net.Conn
	username   string
	token      string
	virtualIP  net.IP
	roomName   string
	udpAddr    string   // public UDP endpoint for P2P
	natType    uint8    // NAT type (0=unknown, 1=cone, 2=symmetric)
	candidates []string // candidate UDP addresses for P2P
	mu         sync.Mutex
	lastPing   time.Time
}

// Room represents a virtual network room.
type Room struct {
	Name      string
	Password  string // used only for legacy (non-DB) mode
	Allocator *network.IPAllocator
	Clients   map[string]*Client // virtualIP -> client
	mu        sync.RWMutex
}

// Server is the main server struct.
type Server struct {
	config  Config
	store   *store.Store // nil when running without database
	rooms   map[string]*Room
	clients map[string]*Client // token -> client
	mu      sync.RWMutex
	ln      net.Listener
}

// New creates a new server with the given config.
func New(cfg Config) (*Server, error) {
	s := &Server{
		config:  cfg,
		rooms:   make(map[string]*Room),
		clients: make(map[string]*Client),
	}

	// Connect to PostgreSQL if configured.
	if cfg.Database != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		st, err := store.New(ctx, *cfg.Database)
		if err != nil {
			return nil, fmt.Errorf("init store: %w", err)
		}
		s.store = st
		log.Println("[Server] 已连接 PostgreSQL 数据库")

		// Load rooms from DB.
		rooms, err := st.ListRooms(ctx)
		if err != nil {
			st.Close()
			return nil, fmt.Errorf("load rooms: %w", err)
		}
		for _, r := range rooms {
			alloc, err := network.NewIPAllocator(r.Subnet)
			if err != nil {
				log.Printf("[Server] 警告: 加载房间 %s 失败 (子网 %s): %v", r.Name, r.Subnet, err)
				continue
			}
			s.rooms[r.Name] = &Room{
				Name:      r.Name,
				Allocator: alloc,
				Clients:   make(map[string]*Client),
			}
		}
	}

	// Initialize configured rooms (legacy JSON config or defaults).
	for name, rc := range cfg.RoomConfigs {
		if _, exists := s.rooms[name]; exists {
			continue // already loaded from DB
		}
		subnet := rc.Subnet
		if subnet == "" {
			subnet = "10.100.0.0/24"
		}
		alloc, err := network.NewIPAllocator(subnet)
		if err != nil {
			return nil, fmt.Errorf("init room %s: %w", name, err)
		}
		s.rooms[name] = &Room{
			Name:      name,
			Password:  rc.Password,
			Allocator: alloc,
			Clients:   make(map[string]*Client),
		}
	}

	return s, nil
}

// Start begins listening for connections.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.config.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	s.ln = ln
	log.Printf("[Server] 神区互联服务端已启动，监听地址: %s", s.config.ListenAddr)

	// Start keepalive checker
	go s.keepaliveChecker()

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("[Server] Accept error: %v", err)
			continue
		}
		go s.handleConnection(conn)
	}
}

// Stop stops the server.
func (s *Server) Stop() error {
	if s.store != nil {
		s.store.Close()
	}
	if s.ln != nil {
		return s.ln.Close()
	}
	return nil
}

func (s *Server) handleConnection(conn net.Conn) {
	remoteAddr := conn.RemoteAddr().String()
	log.Printf("[Server] 新连接: %s", remoteAddr)
	defer func() {
		conn.Close()
		log.Printf("[Server] 连接断开: %s", remoteAddr)
	}()

	var client *Client

	for {
		if err := conn.SetReadDeadline(time.Now().Add(60 * time.Second)); err != nil {
			return
		}

		msg, err := protocol.ReadMessage(conn)
		if err != nil {
			if client != nil {
				s.handleDisconnect(client)
			}
			return
		}

		switch msg.Type {
		case protocol.MsgTypeAuth:
			client, err = s.handleAuth(conn, msg.Payload)
			if err != nil {
				log.Printf("[Server] 认证失败 %s: %v", remoteAddr, err)
				return
			}

		case protocol.MsgTypeRegister:
			s.handleRegister(conn, msg.Payload)

		case protocol.MsgTypeJoinRoom:
			if client == nil {
				s.sendError(conn, 401, "未认证")
				return
			}
			if err := s.handleJoinRoom(client, msg.Payload); err != nil {
				log.Printf("[Server] 加入房间失败 %s: %v", remoteAddr, err)
			}

		case protocol.MsgTypeCreateRoom:
			if client == nil {
				s.sendError(conn, 401, "未认证")
				return
			}
			s.handleCreateRoom(conn, client, msg.Payload)

		case protocol.MsgTypeListRooms:
			s.handleListRooms(conn)

		case protocol.MsgTypeData:
			if client == nil || client.roomName == "" {
				continue
			}
			s.handleData(client, msg.Payload)

		case protocol.MsgTypePing:
			if client != nil {
				client.mu.Lock()
				client.lastPing = time.Now()
				client.mu.Unlock()
			}
			s.sendPong(conn)

		case protocol.MsgTypeLeave:
			if client != nil {
				s.handleLeave(client)
			}

		case protocol.MsgTypeP2PPunchReq:
			if client != nil {
				s.handleP2PPunchReq(client, msg.Payload)
			}

		case protocol.MsgTypeP2POffer:
			if client != nil {
				s.handleP2POffer(client, msg.Payload)
			}

		case protocol.MsgTypeP2PAnswer:
			if client != nil {
				s.handleP2PAnswer(client, msg.Payload)
			}
		}
	}
}

func (s *Server) handleAuth(conn net.Conn, payload []byte) (*Client, error) {
	req, err := protocol.DecodeAuthRequest(payload)
	if err != nil {
		return nil, fmt.Errorf("decode auth: %w", err)
	}

	var authenticated bool

	if s.store != nil {
		// Database mode: verify against PostgreSQL
		ok, err := s.store.AuthenticateUser(context.Background(), req.Username, req.Password)
		if err != nil {
			log.Printf("[Server] 数据库认证错误: %v", err)
		}
		authenticated = ok
	} else {
		// Legacy mode: verify against JSON config
		expectedPass, exists := s.config.Users[req.Username]
		authenticated = exists && expectedPass == req.Password
	}

	if !authenticated {
		resp := &protocol.AuthResponse{
			Success: false,
			Message: "用户名或密码错误",
		}
		data, _ := protocol.EncodeAuthResponse(resp)
		protocol.WriteMessage(conn, &protocol.Message{Type: protocol.MsgTypeAuthResp, Payload: data})
		return nil, fmt.Errorf("invalid credentials for %s", req.Username)
	}

	// Generate session token
	token := generateToken()

	client := &Client{
		conn:     conn,
		username: req.Username,
		token:    token,
		lastPing: time.Now(),
	}

	s.mu.Lock()
	s.clients[token] = client
	s.mu.Unlock()

	resp := &protocol.AuthResponse{
		Success: true,
		Message: fmt.Sprintf("欢迎, %s!", req.Username),
		Token:   token,
	}
	data, _ := protocol.EncodeAuthResponse(resp)
	protocol.WriteMessage(conn, &protocol.Message{Type: protocol.MsgTypeAuthResp, Payload: data})

	log.Printf("[Server] 用户 %s 认证成功", req.Username)
	return client, nil
}

func (s *Server) handleRegister(conn net.Conn, payload []byte) {
	req, err := protocol.DecodeRegisterRequest(payload)
	if err != nil {
		s.sendRegisterResp(conn, false, "请求格式错误")
		return
	}

	if req.Username == "" {
		s.sendRegisterResp(conn, false, "用户名不能为空")
		return
	}

	if s.store == nil {
		s.sendRegisterResp(conn, false, "服务器未配置数据库，不支持注册")
		return
	}

	exists, err := s.store.UserExists(context.Background(), req.Username)
	if err != nil {
		log.Printf("[Server] 检查用户是否存在失败: %v", err)
		s.sendRegisterResp(conn, false, "服务器内部错误")
		return
	}
	if exists {
		s.sendRegisterResp(conn, false, "用户名已存在")
		return
	}

	if err := s.store.CreateUser(context.Background(), req.Username, req.Password); err != nil {
		log.Printf("[Server] 创建用户失败: %v", err)
		s.sendRegisterResp(conn, false, "注册失败")
		return
	}

	log.Printf("[Server] 新用户注册: %s", req.Username)
	s.sendRegisterResp(conn, true, "注册成功")
}

func (s *Server) sendRegisterResp(conn net.Conn, success bool, message string) {
	resp := &protocol.RegisterResponse{Success: success, Message: message}
	data, _ := protocol.EncodeRegisterResponse(resp)
	protocol.WriteMessage(conn, &protocol.Message{Type: protocol.MsgTypeRegisterResp, Payload: data})
}

func (s *Server) handleCreateRoom(conn net.Conn, client *Client, payload []byte) {
	req, err := protocol.DecodeCreateRoomRequest(payload)
	if err != nil {
		s.sendCreateRoomResp(conn, false, "请求格式错误")
		return
	}

	if req.RoomName == "" {
		s.sendCreateRoomResp(conn, false, "房间名称不能为空")
		return
	}
	if req.Password == "" {
		s.sendCreateRoomResp(conn, false, "房间密码不能为空")
		return
	}

	// Check if room already exists
	s.mu.RLock()
	_, exists := s.rooms[req.RoomName]
	s.mu.RUnlock()
	if exists {
		s.sendCreateRoomResp(conn, false, "房间名称已存在")
		return
	}

	// Allocate subnet
	s.mu.Lock()
	roomNum := len(s.rooms) + 1
	subnet := fmt.Sprintf("10.100.%d.0/24", roomNum)
	alloc, err := network.NewIPAllocator(subnet)
	if err != nil {
		s.mu.Unlock()
		s.sendCreateRoomResp(conn, false, "创建房间失败")
		return
	}
	room := &Room{
		Name:      req.RoomName,
		Password:  req.Password,
		Allocator: alloc,
		Clients:   make(map[string]*Client),
	}
	s.rooms[req.RoomName] = room
	s.mu.Unlock()

	// Persist to DB if available
	if s.store != nil {
		if err := s.store.CreateRoom(context.Background(), req.RoomName, req.Password, subnet, client.username); err != nil {
			log.Printf("[Server] 保存房间到数据库失败: %v", err)
			// Room already created in memory, continue
		}
	}

	log.Printf("[Server] 用户 %s 创建房间: %s (子网: %s)", client.username, req.RoomName, subnet)
	s.sendCreateRoomResp(conn, true, fmt.Sprintf("房间 %s 创建成功", req.RoomName))
}

func (s *Server) sendCreateRoomResp(conn net.Conn, success bool, message string) {
	resp := &protocol.CreateRoomResponse{Success: success, Message: message}
	data, _ := protocol.EncodeCreateRoomResponse(resp)
	protocol.WriteMessage(conn, &protocol.Message{Type: protocol.MsgTypeCreateRoomResp, Payload: data})
}

func (s *Server) handleListRooms(conn net.Conn) {
	s.mu.RLock()
	rooms := make([]protocol.RoomInfo, 0, len(s.rooms))
	for name := range s.rooms {
		rooms = append(rooms, protocol.RoomInfo{Name: name})
	}
	s.mu.RUnlock()

	resp := &protocol.ListRoomsResponse{Rooms: rooms}
	data, _ := protocol.EncodeListRoomsResponse(resp)
	protocol.WriteMessage(conn, &protocol.Message{Type: protocol.MsgTypeListRoomsResp, Payload: data})
}

func (s *Server) handleJoinRoom(client *Client, payload []byte) error {
	req, err := protocol.DecodeJoinRoomRequest(payload)
	if err != nil {
		return fmt.Errorf("decode join room: %w", err)
	}

	// Leave current room if any
	if client.roomName != "" {
		s.handleLeave(client)
	}

	s.mu.Lock()
	room, exists := s.rooms[req.RoomName]
	if !exists {
		s.mu.Unlock()
		resp := &protocol.JoinRoomResponse{
			Success: false,
			Message: "房间不存在",
		}
		data, _ := protocol.EncodeJoinRoomResponse(resp)
		return protocol.WriteMessage(client.conn, &protocol.Message{Type: protocol.MsgTypeJoinRoomResp, Payload: data})
	}
	s.mu.Unlock()

	// Check room password
	if s.store != nil {
		// DB mode: verify against hashed password in database
		_, err := s.store.AuthenticateRoom(context.Background(), req.RoomName, req.Password)
		if err != nil {
			resp := &protocol.JoinRoomResponse{
				Success: false,
				Message: "房间密码错误",
			}
			data, _ := protocol.EncodeJoinRoomResponse(resp)
			return protocol.WriteMessage(client.conn, &protocol.Message{Type: protocol.MsgTypeJoinRoomResp, Payload: data})
		}
	} else {
		// Legacy mode: plain text comparison
		if room.Password != "" && room.Password != req.Password {
			resp := &protocol.JoinRoomResponse{
				Success: false,
				Message: "房间密码错误",
			}
			data, _ := protocol.EncodeJoinRoomResponse(resp)
			return protocol.WriteMessage(client.conn, &protocol.Message{Type: protocol.MsgTypeJoinRoomResp, Payload: data})
		}
	}

	// Allocate virtual IP (reuses lease from previous session if available)
	vip, err := room.Allocator.AllocateForUser(client.username)
	if err != nil {
		resp := &protocol.JoinRoomResponse{
			Success: false,
			Message: "IP地址池已满",
		}
		data, _ := protocol.EncodeJoinRoomResponse(resp)
		return protocol.WriteMessage(client.conn, &protocol.Message{Type: protocol.MsgTypeJoinRoomResp, Payload: data})
	}

	client.mu.Lock()
	client.virtualIP = vip
	client.roomName = req.RoomName
	client.mu.Unlock()

	room.mu.Lock()
	room.Clients[vip.String()] = client
	room.mu.Unlock()

	subnet := room.Allocator.Subnet()
	resp := &protocol.JoinRoomResponse{
		Success:   true,
		Message:   fmt.Sprintf("已加入房间 %s", req.RoomName),
		VirtualIP: vip,
		Subnet:    subnet,
		RoomName:  req.RoomName,
	}
	data, _ := protocol.EncodeJoinRoomResponse(resp)
	if err := protocol.WriteMessage(client.conn, &protocol.Message{Type: protocol.MsgTypeJoinRoomResp, Payload: data}); err != nil {
		return err
	}

	log.Printf("[Server] 用户 %s 加入房间 %s, 虚拟IP: %s", client.username, req.RoomName, vip)

	// Broadcast peer update
	s.broadcastPeerUpdate(room)

	return nil
}

func (s *Server) handleData(client *Client, payload []byte) {
	// Payload is raw IP packet data. Extract destination IP to route.
	if len(payload) < 20 {
		return
	}

	// IPv4 destination is at bytes 16-19
	dstIP := net.IP(payload[16:20])

	s.mu.RLock()
	room, exists := s.rooms[client.roomName]
	s.mu.RUnlock()
	if !exists {
		return
	}

	room.mu.RLock()
	target, exists := room.Clients[dstIP.String()]
	room.mu.RUnlock()

	if exists && target != client {
		target.mu.Lock()
		defer target.mu.Unlock()
		protocol.WriteMessage(target.conn, &protocol.Message{
			Type:    protocol.MsgTypeData,
			Payload: payload,
		})
	} else if isBroadcast(dstIP, room.Allocator.Subnet()) {
		// Broadcast to all clients in the room
		room.mu.RLock()
		clients := make([]*Client, 0, len(room.Clients))
		for _, c := range room.Clients {
			if c != client {
				clients = append(clients, c)
			}
		}
		room.mu.RUnlock()

		for _, c := range clients {
			c.mu.Lock()
			protocol.WriteMessage(c.conn, &protocol.Message{
				Type:    protocol.MsgTypeData,
				Payload: payload,
			})
			c.mu.Unlock()
		}
	}
}

func (s *Server) handleLeave(client *Client) {
	client.mu.Lock()
	roomName := client.roomName
	vip := client.virtualIP
	client.roomName = ""
	client.virtualIP = nil
	client.mu.Unlock()

	if roomName == "" {
		return
	}

	s.mu.RLock()
	room, exists := s.rooms[roomName]
	s.mu.RUnlock()
	if !exists {
		return
	}

	room.mu.Lock()
	delete(room.Clients, vip.String())
	room.mu.Unlock()

	if vip != nil {
		room.Allocator.ReleaseWithLease(vip, client.username)
	}

	log.Printf("[Server] 用户 %s 离开房间 %s", client.username, roomName)
	s.broadcastPeerUpdate(room)
}

func (s *Server) handleDisconnect(client *Client) {
	s.handleLeave(client)

	s.mu.Lock()
	delete(s.clients, client.token)
	s.mu.Unlock()

	log.Printf("[Server] 用户 %s 断开连接", client.username)
}

// ---------- P2P Signaling ----------

func (s *Server) handleP2PPunchReq(client *Client, payload []byte) {
	req, err := protocol.DecodeP2PPunchRequest(payload)
	if err != nil {
		log.Printf("[Server-P2P] 解码PunchReq失败 (来自 %s): %v", client.username, err)
		return
	}

	log.Printf("[Server-P2P] 收到打洞请求: %s (VIP=%s) → 目标VIP=%s",
		client.username, client.virtualIP, req.TargetVIP)

	s.mu.RLock()
	room, exists := s.rooms[client.roomName]
	s.mu.RUnlock()
	if !exists {
		log.Printf("[Server-P2P] 打洞请求失败: 客户端 %s 不在任何房间中", client.username)
		return
	}

	room.mu.RLock()
	target, exists := room.Clients[req.TargetVIP.String()]
	room.mu.RUnlock()
	if !exists {
		log.Printf("[Server-P2P] 打洞请求失败: 目标VIP %s 不在房间 %s 中", req.TargetVIP, room.Name)
		return
	}

	// Generate a token to correlate the punch
	token := generateToken()

	// Send the requester's info to the target as an offer
	client.mu.Lock()
	clientVIP := client.virtualIP
	clientUDP := client.udpAddr
	clientNAT := client.natType
	clientCandidates := make([]string, len(client.candidates))
	copy(clientCandidates, client.candidates)
	client.mu.Unlock()

	log.Printf("[Server-P2P] 中继打洞: %s (UDP=%s, NAT=%d) ←→ %s (UDP=%s, NAT=%d), Token=%s",
		client.username, clientUDP, clientNAT, target.username, target.udpAddr, target.natType, token)

	if clientUDP == "" {
		log.Printf("[Server-P2P] ⚠ 警告: 请求方 %s 没有UDP地址 (P2P可能未初始化)", client.username)
	}

	offer := &protocol.P2POffer{
		FromVIP:    clientVIP,
		UDPAddr:    clientUDP,
		Token:      token,
		NATType:    clientNAT,
		Candidates: clientCandidates,
	}
	data, _ := protocol.EncodeP2POffer(offer)
	target.mu.Lock()
	protocol.WriteMessage(target.conn, &protocol.Message{Type: protocol.MsgTypeP2POffer, Payload: data})
	target.mu.Unlock()

	// Send punch response back to requester with target info
	target.mu.Lock()
	targetUDP := target.udpAddr
	targetVIP := target.virtualIP
	targetNAT := target.natType
	targetCandidates := make([]string, len(target.candidates))
	copy(targetCandidates, target.candidates)
	target.mu.Unlock()

	if targetUDP == "" {
		log.Printf("[Server-P2P] ⚠ 警告: 目标方 %s 没有UDP地址 (P2P可能未初始化)", target.username)
	}

	resp := &protocol.P2PPunchResponse{
		PeerVIP:    targetVIP,
		PeerAddr:   targetUDP,
		Token:      token,
		NATType:    targetNAT,
		Candidates: targetCandidates,
	}
	respData, _ := protocol.EncodeP2PPunchResponse(resp)
	client.mu.Lock()
	protocol.WriteMessage(client.conn, &protocol.Message{Type: protocol.MsgTypeP2PPunchResp, Payload: respData})
	client.mu.Unlock()

	log.Printf("[Server-P2P] 打洞信令已发送: Offer→%s, PunchResp→%s", target.username, client.username)
}

func (s *Server) handleP2POffer(client *Client, payload []byte) {
	offer, err := protocol.DecodeP2POffer(payload)
	if err != nil {
		log.Printf("[Server-P2P] 解码P2POffer失败 (来自 %s): %v", client.username, err)
		return
	}

	// Update client's known UDP address, NAT type, and candidates.
	client.mu.Lock()
	oldUDP := client.udpAddr
	client.udpAddr = offer.UDPAddr
	client.natType = offer.NATType
	client.candidates = make([]string, len(offer.Candidates))
	copy(client.candidates, offer.Candidates)
	client.mu.Unlock()

	log.Printf("[Server-P2P] 收到P2POffer: 来自=%s (VIP=%s), UDP=%s, NAT=%d, 候选数=%d",
		client.username, client.virtualIP, offer.UDPAddr, offer.NATType, len(offer.Candidates))
	if oldUDP != "" && oldUDP != offer.UDPAddr {
		log.Printf("[Server-P2P] 客户端 %s UDP地址已更新: %s → %s", client.username, oldUDP, offer.UDPAddr)
	}

	// Forward to target peer
	s.mu.RLock()
	room, exists := s.rooms[client.roomName]
	s.mu.RUnlock()
	if !exists {
		log.Printf("[Server-P2P] 转发P2POffer失败: 客户端 %s 不在任何房间中", client.username)
		return
	}

	offer.FromVIP = client.virtualIP
	data, _ := protocol.EncodeP2POffer(offer)

	// Find target – this offer should be addressed to someone. Since the protocol
	// doesn't include a target field, the server simply relays to ALL peers.
	room.mu.RLock()
	peers := make([]*Client, 0, len(room.Clients))
	for _, c := range room.Clients {
		if c != client {
			peers = append(peers, c)
		}
	}
	room.mu.RUnlock()

	log.Printf("[Server-P2P] 转发P2POffer到 %d 个对端", len(peers))
	for _, c := range peers {
		c.mu.Lock()
		protocol.WriteMessage(c.conn, &protocol.Message{Type: protocol.MsgTypeP2POffer, Payload: data})
		c.mu.Unlock()
	}
}

func (s *Server) handleP2PAnswer(client *Client, payload []byte) {
	answer, err := protocol.DecodeP2PAnswer(payload)
	if err != nil {
		log.Printf("[Server-P2P] 解码P2PAnswer失败 (来自 %s): %v", client.username, err)
		return
	}

	// Update client's known UDP address, NAT type, and candidates.
	client.mu.Lock()
	oldUDP := client.udpAddr
	client.udpAddr = answer.UDPAddr
	client.natType = answer.NATType
	client.candidates = make([]string, len(answer.Candidates))
	copy(client.candidates, answer.Candidates)
	client.mu.Unlock()

	log.Printf("[Server-P2P] 收到P2PAnswer: 来自=%s (VIP=%s), UDP=%s, NAT=%d, 候选数=%d, Accepted=%v",
		client.username, client.virtualIP, answer.UDPAddr, answer.NATType, len(answer.Candidates), answer.Accepted)
	if oldUDP != "" && oldUDP != answer.UDPAddr {
		log.Printf("[Server-P2P] 客户端 %s UDP地址已更新: %s → %s", client.username, oldUDP, answer.UDPAddr)
	}

	// Forward to the peer identified by the answer's target
	s.mu.RLock()
	room, exists := s.rooms[client.roomName]
	s.mu.RUnlock()
	if !exists {
		log.Printf("[Server-P2P] 转发P2PAnswer失败: 客户端 %s 不在任何房间中", client.username)
		return
	}

	answer.FromVIP = client.virtualIP
	data, _ := protocol.EncodeP2PAnswer(answer)

	room.mu.RLock()
	peers := make([]*Client, 0, len(room.Clients))
	for _, c := range room.Clients {
		if c != client {
			peers = append(peers, c)
		}
	}
	room.mu.RUnlock()

	log.Printf("[Server-P2P] 转发P2PAnswer到 %d 个对端", len(peers))
	for _, c := range peers {
		c.mu.Lock()
		protocol.WriteMessage(c.conn, &protocol.Message{Type: protocol.MsgTypeP2PAnswer, Payload: data})
		c.mu.Unlock()
	}
}

// ---------- Utilities ----------

func (s *Server) broadcastPeerUpdate(room *Room) {
	room.mu.RLock()
	peers := make([]protocol.PeerInfo, 0, len(room.Clients))
	clients := make([]*Client, 0, len(room.Clients))
	for _, c := range room.Clients {
		c.mu.Lock()
		peers = append(peers, protocol.PeerInfo{
			Username:  c.username,
			VirtualIP: c.virtualIP,
			Online:    true,
		})
		clients = append(clients, c)
		c.mu.Unlock()
	}
	room.mu.RUnlock()

	update := &protocol.PeerUpdate{
		RoomName: room.Name,
		Peers:    peers,
	}
	data, err := protocol.EncodePeerUpdate(update)
	if err != nil {
		log.Printf("[Server] 编码节点更新失败: %v", err)
		return
	}

	for _, c := range clients {
		c.mu.Lock()
		protocol.WriteMessage(c.conn, &protocol.Message{
			Type:    protocol.MsgTypePeerUpdate,
			Payload: data,
		})
		c.mu.Unlock()
	}
}

func (s *Server) sendError(conn net.Conn, code uint16, message string) {
	e := &protocol.ErrorMsg{Code: code, Message: message}
	data, _ := protocol.EncodeErrorMsg(e)
	protocol.WriteMessage(conn, &protocol.Message{Type: protocol.MsgTypeError, Payload: data})
}

func (s *Server) sendPong(conn net.Conn) {
	protocol.WriteMessage(conn, &protocol.Message{Type: protocol.MsgTypePong})
}

func (s *Server) keepaliveChecker() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		s.mu.RLock()
		clients := make([]*Client, 0, len(s.clients))
		for _, c := range s.clients {
			clients = append(clients, c)
		}
		s.mu.RUnlock()

		for _, c := range clients {
			c.mu.Lock()
			if time.Since(c.lastPing) > 90*time.Second {
				log.Printf("[Server] 用户 %s 心跳超时，断开连接", c.username)
				c.conn.Close()
			}
			c.mu.Unlock()
		}
	}
}

func generateToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func isBroadcast(ip net.IP, subnet net.IPNet) bool {
	ip4 := ip.To4()
	if ip4 == nil {
		return false
	}
	for i := range ip4 {
		if ip4[i]|subnet.Mask[i] != 0xff {
			return false
		}
	}
	return true
}
