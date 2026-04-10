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

func TestRegisterRequestRoundTrip(t *testing.T) {
	req := &RegisterRequest{Username: "newuser", Password: "newpass"}
	data, err := EncodeRegisterRequest(req)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded, err := DecodeRegisterRequest(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if decoded.Username != req.Username || decoded.Password != req.Password {
		t.Errorf("Mismatch: got %+v, want %+v", decoded, req)
	}
}

func TestRegisterRequestPasswordlessRoundTrip(t *testing.T) {
	req := &RegisterRequest{Username: "nopassuser", Password: ""}
	data, err := EncodeRegisterRequest(req)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded, err := DecodeRegisterRequest(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if decoded.Username != req.Username || decoded.Password != "" {
		t.Errorf("Mismatch: got %+v, want %+v", decoded, req)
	}
}

func TestRegisterResponseRoundTrip(t *testing.T) {
	resp := &RegisterResponse{Success: true, Message: "注册成功"}
	data, err := EncodeRegisterResponse(resp)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded, err := DecodeRegisterResponse(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if decoded.Success != resp.Success || decoded.Message != resp.Message {
		t.Errorf("Mismatch: got %+v, want %+v", decoded, resp)
	}
}

func TestCreateRoomRequestRoundTrip(t *testing.T) {
	req := &CreateRoomRequest{RoomName: "my-room", Password: "secret123"}
	data, err := EncodeCreateRoomRequest(req)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded, err := DecodeCreateRoomRequest(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if decoded.RoomName != req.RoomName || decoded.Password != req.Password {
		t.Errorf("Mismatch: got %+v, want %+v", decoded, req)
	}
}

func TestCreateRoomResponseRoundTrip(t *testing.T) {
	resp := &CreateRoomResponse{Success: false, Message: "房间已存在"}
	data, err := EncodeCreateRoomResponse(resp)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded, err := DecodeCreateRoomResponse(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if decoded.Success != resp.Success || decoded.Message != resp.Message {
		t.Errorf("Mismatch: got %+v, want %+v", decoded, resp)
	}
}

func TestListRoomsResponseRoundTrip(t *testing.T) {
	resp := &ListRoomsResponse{
		Rooms: []RoomInfo{
			{Name: "room-1", CreatedBy: "alice"},
			{Name: "room-2", CreatedBy: "bob"},
		},
	}
	data, err := EncodeListRoomsResponse(resp)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded, err := DecodeListRoomsResponse(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(decoded.Rooms) != 2 {
		t.Fatalf("Expected 2 rooms, got %d", len(decoded.Rooms))
	}
	if decoded.Rooms[0].Name != "room-1" || decoded.Rooms[0].CreatedBy != "alice" {
		t.Errorf("Room 0 mismatch: got %+v", decoded.Rooms[0])
	}
	if decoded.Rooms[1].Name != "room-2" || decoded.Rooms[1].CreatedBy != "bob" {
		t.Errorf("Room 1 mismatch: got %+v", decoded.Rooms[1])
	}
}

func TestListRoomsResponseEmptyRoundTrip(t *testing.T) {
	resp := &ListRoomsResponse{Rooms: []RoomInfo{}}
	data, err := EncodeListRoomsResponse(resp)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded, err := DecodeListRoomsResponse(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(decoded.Rooms) != 0 {
		t.Errorf("Expected 0 rooms, got %d", len(decoded.Rooms))
	}
}

func TestP2PPunchRequestRoundTrip(t *testing.T) {
	req := &P2PPunchRequest{TargetVIP: net.ParseIP("10.100.1.5").To4()}
	data, err := EncodeP2PPunchRequest(req)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded, err := DecodeP2PPunchRequest(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !decoded.TargetVIP.Equal(req.TargetVIP) {
		t.Errorf("TargetVIP mismatch: got %v, want %v", decoded.TargetVIP, req.TargetVIP)
	}
}

func TestP2PPunchResponseRoundTrip(t *testing.T) {
	resp := &P2PPunchResponse{
		PeerVIP:  net.ParseIP("10.100.1.2").To4(),
		PeerAddr: "203.0.113.5:12345",
		Token:    "abc123token",
	}
	data, err := EncodeP2PPunchResponse(resp)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded, err := DecodeP2PPunchResponse(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !decoded.PeerVIP.Equal(resp.PeerVIP) || decoded.PeerAddr != resp.PeerAddr || decoded.Token != resp.Token {
		t.Errorf("Mismatch: got %+v, want %+v", decoded, resp)
	}
}

func TestP2POfferRoundTrip(t *testing.T) {
	offer := &P2POffer{
		FromVIP: net.ParseIP("10.100.1.1").To4(),
		UDPAddr: "198.51.100.10:54321",
		Token:   "offer-token",
	}
	data, err := EncodeP2POffer(offer)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded, err := DecodeP2POffer(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !decoded.FromVIP.Equal(offer.FromVIP) || decoded.UDPAddr != offer.UDPAddr || decoded.Token != offer.Token {
		t.Errorf("Mismatch: got %+v, want %+v", decoded, offer)
	}
}

func TestP2PAnswerRoundTrip(t *testing.T) {
	answer := &P2PAnswer{
		FromVIP:  net.ParseIP("10.100.1.3").To4(),
		UDPAddr:  "192.0.2.50:9999",
		Token:    "answer-token",
		Accepted: true,
	}
	data, err := EncodeP2PAnswer(answer)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded, err := DecodeP2PAnswer(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !decoded.FromVIP.Equal(answer.FromVIP) || decoded.UDPAddr != answer.UDPAddr ||
		decoded.Token != answer.Token || decoded.Accepted != answer.Accepted {
		t.Errorf("Mismatch: got %+v, want %+v", decoded, answer)
	}
}

func TestP2PAnswerRejectedRoundTrip(t *testing.T) {
	answer := &P2PAnswer{
		FromVIP:  net.ParseIP("10.100.1.4").To4(),
		UDPAddr:  "",
		Token:    "reject-token",
		Accepted: false,
	}
	data, err := EncodeP2PAnswer(answer)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded, err := DecodeP2PAnswer(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if decoded.Accepted != false || decoded.Token != "reject-token" {
		t.Errorf("Mismatch: got %+v, want %+v", decoded, answer)
	}
}
