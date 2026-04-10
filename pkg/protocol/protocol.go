// Package protocol defines the wire protocol for 神区互联 (GodQV Networking).
// All messages between server and client use a simple TLV (Type-Length-Value) framing.
package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
)

// Protocol version
const Version = 1

// Maximum payload size (64KB)
const MaxPayloadSize = 65536

// Message types
type MsgType uint8

const (
	// Client -> Server
	MsgTypeAuth       MsgType = 0x01 // Authentication request
	MsgTypeJoinRoom   MsgType = 0x02 // Join a room/network
	MsgTypeData       MsgType = 0x03 // Ethernet/IP frame data
	MsgTypePing       MsgType = 0x04 // Keepalive ping
	MsgTypeLeave      MsgType = 0x05 // Leave room
	MsgTypeRegister   MsgType = 0x06 // Register new user account
	MsgTypeCreateRoom MsgType = 0x07 // Create a new room
	MsgTypeListRooms  MsgType = 0x08 // List available rooms

	// P2P signaling (Client <-> Server)
	MsgTypeP2POffer     MsgType = 0x10 // P2P connection offer (UDP candidate)
	MsgTypeP2PAnswer    MsgType = 0x11 // P2P connection answer
	MsgTypeP2PPunchReq  MsgType = 0x12 // Request hole-punch coordination
	MsgTypeP2PPunchResp MsgType = 0x13 // Hole-punch coordination response

	// Server -> Client
	MsgTypeAuthResp       MsgType = 0x81 // Auth response
	MsgTypeJoinRoomResp   MsgType = 0x82 // Join room response (includes assigned virtual IP)
	MsgTypePeerUpdate     MsgType = 0x83 // Peer list update
	MsgTypePong           MsgType = 0x84 // Keepalive pong
	MsgTypeError          MsgType = 0x85 // Error message
	MsgTypeRegisterResp   MsgType = 0x86 // Register response
	MsgTypeCreateRoomResp MsgType = 0x87 // Create room response
	MsgTypeListRoomsResp  MsgType = 0x88 // List rooms response
)

// Frame format:
// [1 byte: version] [1 byte: msg_type] [2 bytes: payload_length] [payload]
const HeaderSize = 4

// Message represents a protocol message.
type Message struct {
	Type    MsgType
	Payload []byte
}

// AuthRequest is sent by client to authenticate.
type AuthRequest struct {
	Username string
	Password string
}

// AuthResponse is sent by server after authentication.
type AuthResponse struct {
	Success bool
	Message string
	Token   string // Session token for subsequent requests
}

// JoinRoomRequest is sent by client to join a virtual network room.
type JoinRoomRequest struct {
	RoomName string
	Password string // Room password (optional)
}

// JoinRoomResponse is sent by server after joining a room.
type JoinRoomResponse struct {
	Success   bool
	Message   string
	VirtualIP net.IP // Assigned virtual IP (e.g., 10.x.x.x)
	Subnet    net.IPNet
	RoomName  string
}

// PeerInfo describes a peer in the network.
type PeerInfo struct {
	Username  string
	VirtualIP net.IP
	Online    bool
}

// PeerUpdate is sent by server when peers change.
type PeerUpdate struct {
	RoomName string
	Peers    []PeerInfo
}

// ErrorMsg is sent by server on errors.
type ErrorMsg struct {
	Code    uint16
	Message string
}

// RegisterRequest is sent by client to create a new user account.
type RegisterRequest struct {
	Username string
	Password string // May be empty for passwordless accounts
}

// RegisterResponse is sent by server after registration.
type RegisterResponse struct {
	Success bool
	Message string
}

// CreateRoomRequest is sent by client to create a new room.
type CreateRoomRequest struct {
	RoomName string
	Password string // Required – rooms must have a password
}

// CreateRoomResponse is sent by server after room creation.
type CreateRoomResponse struct {
	Success bool
	Message string
}

// ListRoomsResponse is sent by server with available rooms.
type ListRoomsResponse struct {
	Rooms []RoomInfo
}

// RoomInfo describes a room in the room listing.
type RoomInfo struct {
	Name      string
	CreatedBy string
}

// P2PPunchRequest is sent to request UDP hole-punch coordination for a peer.
type P2PPunchRequest struct {
	TargetVIP net.IP // Virtual IP of the target peer
}

// P2PPunchResponse carries the public UDP endpoint discovered via STUN or
// reported by the peer so both sides can attempt hole-punching.
type P2PPunchResponse struct {
	PeerVIP  net.IP // Virtual IP of the peer
	PeerAddr string // Public UDP endpoint (ip:port) of the peer
	Token    string // Opaque token to correlate punches
}

// P2POffer carries a UDP hole-punch offer from one peer to another (relayed
// through the signaling server).
type P2POffer struct {
	FromVIP  net.IP
	UDPAddr  string // sender's public UDP endpoint
	Token    string
}

// P2PAnswer carries a UDP hole-punch answer from the target peer.
type P2PAnswer struct {
	FromVIP  net.IP
	UDPAddr  string
	Token    string
	Accepted bool
}

// WriteMessage writes a framed message to the writer.
func WriteMessage(w io.Writer, msg *Message) error {
	if len(msg.Payload) > MaxPayloadSize {
		return errors.New("payload too large")
	}

	header := make([]byte, HeaderSize)
	header[0] = Version
	header[1] = byte(msg.Type)
	binary.BigEndian.PutUint16(header[2:4], uint16(len(msg.Payload)))

	if _, err := w.Write(header); err != nil {
		return fmt.Errorf("write header: %w", err)
	}
	if len(msg.Payload) > 0 {
		if _, err := w.Write(msg.Payload); err != nil {
			return fmt.Errorf("write payload: %w", err)
		}
	}
	return nil
}

// ReadMessage reads a framed message from the reader.
func ReadMessage(r io.Reader) (*Message, error) {
	header := make([]byte, HeaderSize)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}

	ver := header[0]
	if ver != Version {
		return nil, fmt.Errorf("unsupported protocol version: %d", ver)
	}

	msgType := MsgType(header[1])
	payloadLen := binary.BigEndian.Uint16(header[2:4])

	if int(payloadLen) > MaxPayloadSize {
		return nil, fmt.Errorf("payload too large: %d", payloadLen)
	}

	payload := make([]byte, payloadLen)
	if payloadLen > 0 {
		if _, err := io.ReadFull(r, payload); err != nil {
			return nil, fmt.Errorf("read payload: %w", err)
		}
	}

	return &Message{
		Type:    msgType,
		Payload: payload,
	}, nil
}
