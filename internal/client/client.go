// Package client implements the 神区互联 client.
package client

import (
	"fmt"
	"log"
	"net"
	"sync"
	"time"

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
	mu        sync.Mutex
	done      chan struct{}
	onPeerUpdate func([]PeerInfo)
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

// SetTunWriter sets the TUN device writer for receiving packets.
func (c *Client) SetTunWriter(tw TunWriter) {
	c.tunWriter = tw
}

// SetPeerUpdateCallback sets a callback for peer updates.
func (c *Client) SetPeerUpdateCallback(cb func([]PeerInfo)) {
	c.onPeerUpdate = cb
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

// SendPacket sends an IP packet to the server for routing.
func (c *Client) SendPacket(packet []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return protocol.WriteMessage(c.conn, &protocol.Message{
		Type:    protocol.MsgTypeData,
		Payload: packet,
	})
}

// StartReceiving starts the message receiving loop.
func (c *Client) StartReceiving() {
	go c.receiveLoop()
	go c.keepaliveLoop()
}

// Close closes the connection.
func (c *Client) Close() error {
	close(c.done)
	if c.conn != nil {
		// Send leave message
		protocol.WriteMessage(c.conn, &protocol.Message{Type: protocol.MsgTypeLeave})
		return c.conn.Close()
	}
	return nil
}

// Done returns a channel that is closed when the client is done.
func (c *Client) Done() <-chan struct{} {
	return c.done
}

func (c *Client) receiveLoop() {
	defer func() {
		select {
		case <-c.done:
		default:
			close(c.done)
		}
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
				c.peers[i] = PeerInfo{
					Username:  p.Username,
					VirtualIP: p.VirtualIP,
					Online:    p.Online,
				}
			}
			c.peersMu.Unlock()

			log.Printf("[Client] 节点更新 - 房间 %s, %d 个节点在线", update.RoomName, len(update.Peers))
			for _, p := range update.Peers {
				log.Printf("  - %s (%s) [在线: %v]", p.Username, p.VirtualIP, p.Online)
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
		}
	}
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
