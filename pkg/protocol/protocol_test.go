package protocol

import (
	"bytes"
	"net"
	"testing"
)

func TestMessageRoundTrip(t *testing.T) {
	original := &Message{
		Type:    MsgTypeAuth,
		Payload: []byte("hello world"),
	}

	var buf bytes.Buffer
	if err := WriteMessage(&buf, original); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	decoded, err := ReadMessage(&buf)
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	if decoded.Type != original.Type {
		t.Errorf("Type mismatch: got %v, want %v", decoded.Type, original.Type)
	}
	if !bytes.Equal(decoded.Payload, original.Payload) {
		t.Errorf("Payload mismatch")
	}
}

func TestAuthRoundTrip(t *testing.T) {
	req := &AuthRequest{Username: "testuser", Password: "testpass"}
	data, err := EncodeAuthRequest(req)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded, err := DecodeAuthRequest(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if decoded.Username != req.Username || decoded.Password != req.Password {
		t.Errorf("Mismatch: got %+v, want %+v", decoded, req)
	}
}

func TestAuthResponseRoundTrip(t *testing.T) {
	resp := &AuthResponse{Success: true, Message: "ok", Token: "abc123"}
	data, err := EncodeAuthResponse(resp)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded, err := DecodeAuthResponse(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if decoded.Success != resp.Success || decoded.Message != resp.Message || decoded.Token != resp.Token {
		t.Errorf("Mismatch: got %+v, want %+v", decoded, resp)
	}
}

func TestJoinRoomRoundTrip(t *testing.T) {
	req := &JoinRoomRequest{RoomName: "test-room", Password: "roompass"}
	data, err := EncodeJoinRoomRequest(req)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded, err := DecodeJoinRoomRequest(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if decoded.RoomName != req.RoomName || decoded.Password != req.Password {
		t.Errorf("Mismatch: got %+v, want %+v", decoded, req)
	}
}

func TestJoinRoomResponseRoundTrip(t *testing.T) {
	_, subnet, _ := net.ParseCIDR("10.100.0.0/24")
	resp := &JoinRoomResponse{
		Success:   true,
		Message:   "joined",
		VirtualIP: net.ParseIP("10.100.0.5").To4(),
		Subnet:    *subnet,
		RoomName:  "test-room",
	}
	data, err := EncodeJoinRoomResponse(resp)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded, err := DecodeJoinRoomResponse(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !decoded.Success || decoded.Message != "joined" || decoded.RoomName != "test-room" {
		t.Errorf("Mismatch: got %+v", decoded)
	}
	if !decoded.VirtualIP.Equal(resp.VirtualIP) {
		t.Errorf("VirtualIP mismatch: got %v, want %v", decoded.VirtualIP, resp.VirtualIP)
	}
}

func TestPeerUpdateRoundTrip(t *testing.T) {
	update := &PeerUpdate{
		RoomName: "test-room",
		Peers: []PeerInfo{
			{Username: "alice", VirtualIP: net.ParseIP("10.100.0.1").To4(), Online: true},
			{Username: "bob", VirtualIP: net.ParseIP("10.100.0.2").To4(), Online: false},
		},
	}
	data, err := EncodePeerUpdate(update)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded, err := DecodePeerUpdate(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if decoded.RoomName != update.RoomName || len(decoded.Peers) != 2 {
		t.Errorf("Mismatch: got %+v", decoded)
	}
	if decoded.Peers[0].Username != "alice" || !decoded.Peers[0].Online {
		t.Errorf("Peer 0 mismatch: got %+v", decoded.Peers[0])
	}
}

func TestErrorMsgRoundTrip(t *testing.T) {
	e := &ErrorMsg{Code: 404, Message: "not found"}
	data, err := EncodeErrorMsg(e)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded, err := DecodeErrorMsg(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if decoded.Code != 404 || decoded.Message != "not found" {
		t.Errorf("Mismatch: got %+v", decoded)
	}
}
