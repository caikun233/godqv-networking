// Package server implements the 神区互联 server.
package server

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/caikun233/godqv-networking/pkg/network"
	"github.com/caikun233/godqv-networking/pkg/protocol"
)

// Config holds server configuration.
type Config struct {
	ListenAddr  string            `json:"listen_addr"`
	SubnetCIDR  string            `json:"subnet_cidr"` // Base subnet for rooms
	Users       map[string]string `json:"users"`        // username -> password
	RoomConfigs map[string]RoomConfig `json:"rooms"`    // room name -> config
}

// RoomConfig holds per-room configuration.
type RoomConfig struct {
	Password string `json:"password"` // Empty = no password
	Subnet   string `json:"subnet"`   // e.g., "10.100.1.0/24"
}

// Client represents a connected client.
type Client struct {
	conn      net.Conn
	username  string
	token     string
	virtualIP net.IP
	roomName  string
	mu        sync.Mutex
	lastPing  time.Time
}

// Room represents a virtual network room.
type Room struct {
	Name      string
	Password  string
	Allocator *network.IPAllocator
	Clients   map[string]*Client // virtualIP -> client
	mu        sync.RWMutex
}

// Server is the main server struct.
type Server struct {
	config  Config
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

	// Initialize configured rooms
	for name, rc := range cfg.RoomConfigs {
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

		case protocol.MsgTypeJoinRoom:
			if client == nil {
				s.sendError(conn, 401, "未认证")
				return
			}
			if err := s.handleJoinRoom(client, msg.Payload); err != nil {
				log.Printf("[Server] 加入房间失败 %s: %v", remoteAddr, err)
			}

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
		}
	}
}

func (s *Server) handleAuth(conn net.Conn, payload []byte) (*Client, error) {
	req, err := protocol.DecodeAuthRequest(payload)
	if err != nil {
		return nil, fmt.Errorf("decode auth: %w", err)
	}

	// Verify credentials
	expectedPass, exists := s.config.Users[req.Username]
	if !exists || expectedPass != req.Password {
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
		// Auto-create room with default subnet
		roomNum := len(s.rooms) + 1
		subnet := fmt.Sprintf("10.100.%d.0/24", roomNum)
		alloc, err := network.NewIPAllocator(subnet)
		if err != nil {
			s.mu.Unlock()
			return fmt.Errorf("create room allocator: %w", err)
		}
		room = &Room{
			Name:      req.RoomName,
			Password:  req.Password,
			Allocator: alloc,
			Clients:   make(map[string]*Client),
		}
		s.rooms[req.RoomName] = room
		log.Printf("[Server] 自动创建房间: %s (子网: %s)", req.RoomName, subnet)
	}
	s.mu.Unlock()

	// Check room password
	if room.Password != "" && room.Password != req.Password {
		resp := &protocol.JoinRoomResponse{
			Success: false,
			Message: "房间密码错误",
		}
		data, _ := protocol.EncodeJoinRoomResponse(resp)
		return protocol.WriteMessage(client.conn, &protocol.Message{Type: protocol.MsgTypeJoinRoomResp, Payload: data})
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
