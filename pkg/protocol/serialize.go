package protocol

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
)

// Helper functions for reading/writing strings and basic types.

func writeString(w io.Writer, s string) error {
	b := []byte(s)
	if len(b) > 65535 {
		return fmt.Errorf("string too long: %d", len(b))
	}
	if err := binary.Write(w, binary.BigEndian, uint16(len(b))); err != nil {
		return err
	}
	_, err := w.Write(b)
	return err
}

func readString(r io.Reader) (string, error) {
	var length uint16
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		return "", err
	}
	b := make([]byte, length)
	if _, err := io.ReadFull(r, b); err != nil {
		return "", err
	}
	return string(b), nil
}

func writeBool(w io.Writer, v bool) error {
	b := byte(0)
	if v {
		b = 1
	}
	_, err := w.Write([]byte{b})
	return err
}

func readBool(r io.Reader) (bool, error) {
	b := make([]byte, 1)
	if _, err := io.ReadFull(r, b); err != nil {
		return false, err
	}
	return b[0] != 0, nil
}

func writeStringSlice(w io.Writer, ss []string) error {
	if err := binary.Write(w, binary.BigEndian, uint16(len(ss))); err != nil {
		return err
	}
	for _, s := range ss {
		if err := writeString(w, s); err != nil {
			return err
		}
	}
	return nil
}

func readStringSlice(r io.Reader) ([]string, error) {
	var count uint16
	if err := binary.Read(r, binary.BigEndian, &count); err != nil {
		return nil, err
	}
	ss := make([]string, count)
	for i := range ss {
		s, err := readString(r)
		if err != nil {
			return nil, err
		}
		ss[i] = s
	}
	return ss, nil
}

func writeIP(w io.Writer, ip net.IP) error {
	ip4 := ip.To4()
	if ip4 == nil {
		return fmt.Errorf("only IPv4 supported")
	}
	_, err := w.Write(ip4)
	return err
}

func readIP(r io.Reader) (net.IP, error) {
	b := make([]byte, 4)
	if _, err := io.ReadFull(r, b); err != nil {
		return nil, err
	}
	return net.IP(b), nil
}

// EncodeAuthRequest serializes an AuthRequest.
func EncodeAuthRequest(req *AuthRequest) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeString(&buf, req.Username); err != nil {
		return nil, err
	}
	if err := writeString(&buf, req.Password); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DecodeAuthRequest deserializes an AuthRequest.
func DecodeAuthRequest(data []byte) (*AuthRequest, error) {
	r := bytes.NewReader(data)
	username, err := readString(r)
	if err != nil {
		return nil, err
	}
	password, err := readString(r)
	if err != nil {
		return nil, err
	}
	return &AuthRequest{Username: username, Password: password}, nil
}

// EncodeAuthResponse serializes an AuthResponse.
func EncodeAuthResponse(resp *AuthResponse) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeBool(&buf, resp.Success); err != nil {
		return nil, err
	}
	if err := writeString(&buf, resp.Message); err != nil {
		return nil, err
	}
	if err := writeString(&buf, resp.Token); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DecodeAuthResponse deserializes an AuthResponse.
func DecodeAuthResponse(data []byte) (*AuthResponse, error) {
	r := bytes.NewReader(data)
	success, err := readBool(r)
	if err != nil {
		return nil, err
	}
	message, err := readString(r)
	if err != nil {
		return nil, err
	}
	token, err := readString(r)
	if err != nil {
		return nil, err
	}
	return &AuthResponse{Success: success, Message: message, Token: token}, nil
}

// EncodeJoinRoomRequest serializes a JoinRoomRequest.
func EncodeJoinRoomRequest(req *JoinRoomRequest) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeString(&buf, req.RoomName); err != nil {
		return nil, err
	}
	if err := writeString(&buf, req.Password); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DecodeJoinRoomRequest deserializes a JoinRoomRequest.
func DecodeJoinRoomRequest(data []byte) (*JoinRoomRequest, error) {
	r := bytes.NewReader(data)
	roomName, err := readString(r)
	if err != nil {
		return nil, err
	}
	password, err := readString(r)
	if err != nil {
		return nil, err
	}
	return &JoinRoomRequest{RoomName: roomName, Password: password}, nil
}

// EncodeJoinRoomResponse serializes a JoinRoomResponse.
func EncodeJoinRoomResponse(resp *JoinRoomResponse) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeBool(&buf, resp.Success); err != nil {
		return nil, err
	}
	if err := writeString(&buf, resp.Message); err != nil {
		return nil, err
	}
	if resp.Success {
		if err := writeIP(&buf, resp.VirtualIP); err != nil {
			return nil, err
		}
		ones, _ := resp.Subnet.Mask.Size()
		if err := binary.Write(&buf, binary.BigEndian, uint8(ones)); err != nil {
			return nil, err
		}
		if err := writeString(&buf, resp.RoomName); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

// DecodeJoinRoomResponse deserializes a JoinRoomResponse.
func DecodeJoinRoomResponse(data []byte) (*JoinRoomResponse, error) {
	r := bytes.NewReader(data)
	success, err := readBool(r)
	if err != nil {
		return nil, err
	}
	message, err := readString(r)
	if err != nil {
		return nil, err
	}
	resp := &JoinRoomResponse{Success: success, Message: message}
	if success {
		ip, err := readIP(r)
		if err != nil {
			return nil, err
		}
		resp.VirtualIP = ip

		var maskBits uint8
		if err := binary.Read(r, binary.BigEndian, &maskBits); err != nil {
			return nil, err
		}
		resp.Subnet = net.IPNet{
			IP:   ip.Mask(net.CIDRMask(int(maskBits), 32)),
			Mask: net.CIDRMask(int(maskBits), 32),
		}

		roomName, err := readString(r)
		if err != nil {
			return nil, err
		}
		resp.RoomName = roomName
	}
	return resp, nil
}

// EncodePeerUpdate serializes a PeerUpdate.
func EncodePeerUpdate(update *PeerUpdate) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeString(&buf, update.RoomName); err != nil {
		return nil, err
	}
	if err := binary.Write(&buf, binary.BigEndian, uint16(len(update.Peers))); err != nil {
		return nil, err
	}
	for _, peer := range update.Peers {
		if err := writeString(&buf, peer.Username); err != nil {
			return nil, err
		}
		if err := writeIP(&buf, peer.VirtualIP); err != nil {
			return nil, err
		}
		if err := writeBool(&buf, peer.Online); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

// DecodePeerUpdate deserializes a PeerUpdate.
func DecodePeerUpdate(data []byte) (*PeerUpdate, error) {
	r := bytes.NewReader(data)
	roomName, err := readString(r)
	if err != nil {
		return nil, err
	}
	var count uint16
	if err := binary.Read(r, binary.BigEndian, &count); err != nil {
		return nil, err
	}
	peers := make([]PeerInfo, count)
	for i := range peers {
		username, err := readString(r)
		if err != nil {
			return nil, err
		}
		ip, err := readIP(r)
		if err != nil {
			return nil, err
		}
		online, err := readBool(r)
		if err != nil {
			return nil, err
		}
		peers[i] = PeerInfo{Username: username, VirtualIP: ip, Online: online}
	}
	return &PeerUpdate{RoomName: roomName, Peers: peers}, nil
}

// EncodeErrorMsg serializes an ErrorMsg.
func EncodeErrorMsg(e *ErrorMsg) ([]byte, error) {
	var buf bytes.Buffer
	if err := binary.Write(&buf, binary.BigEndian, e.Code); err != nil {
		return nil, err
	}
	if err := writeString(&buf, e.Message); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DecodeErrorMsg deserializes an ErrorMsg.
func DecodeErrorMsg(data []byte) (*ErrorMsg, error) {
	r := bytes.NewReader(data)
	var code uint16
	if err := binary.Read(r, binary.BigEndian, &code); err != nil {
		return nil, err
	}
	message, err := readString(r)
	if err != nil {
		return nil, err
	}
	return &ErrorMsg{Code: code, Message: message}, nil
}

// EncodeRegisterRequest serializes a RegisterRequest.
func EncodeRegisterRequest(req *RegisterRequest) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeString(&buf, req.Username); err != nil {
		return nil, err
	}
	if err := writeString(&buf, req.Password); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DecodeRegisterRequest deserializes a RegisterRequest.
func DecodeRegisterRequest(data []byte) (*RegisterRequest, error) {
	r := bytes.NewReader(data)
	username, err := readString(r)
	if err != nil {
		return nil, err
	}
	password, err := readString(r)
	if err != nil {
		return nil, err
	}
	return &RegisterRequest{Username: username, Password: password}, nil
}

// EncodeRegisterResponse serializes a RegisterResponse.
func EncodeRegisterResponse(resp *RegisterResponse) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeBool(&buf, resp.Success); err != nil {
		return nil, err
	}
	if err := writeString(&buf, resp.Message); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DecodeRegisterResponse deserializes a RegisterResponse.
func DecodeRegisterResponse(data []byte) (*RegisterResponse, error) {
	r := bytes.NewReader(data)
	success, err := readBool(r)
	if err != nil {
		return nil, err
	}
	message, err := readString(r)
	if err != nil {
		return nil, err
	}
	return &RegisterResponse{Success: success, Message: message}, nil
}

// EncodeCreateRoomRequest serializes a CreateRoomRequest.
func EncodeCreateRoomRequest(req *CreateRoomRequest) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeString(&buf, req.RoomName); err != nil {
		return nil, err
	}
	if err := writeString(&buf, req.Password); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DecodeCreateRoomRequest deserializes a CreateRoomRequest.
func DecodeCreateRoomRequest(data []byte) (*CreateRoomRequest, error) {
	r := bytes.NewReader(data)
	name, err := readString(r)
	if err != nil {
		return nil, err
	}
	password, err := readString(r)
	if err != nil {
		return nil, err
	}
	return &CreateRoomRequest{RoomName: name, Password: password}, nil
}

// EncodeCreateRoomResponse serializes a CreateRoomResponse.
func EncodeCreateRoomResponse(resp *CreateRoomResponse) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeBool(&buf, resp.Success); err != nil {
		return nil, err
	}
	if err := writeString(&buf, resp.Message); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DecodeCreateRoomResponse deserializes a CreateRoomResponse.
func DecodeCreateRoomResponse(data []byte) (*CreateRoomResponse, error) {
	r := bytes.NewReader(data)
	success, err := readBool(r)
	if err != nil {
		return nil, err
	}
	message, err := readString(r)
	if err != nil {
		return nil, err
	}
	return &CreateRoomResponse{Success: success, Message: message}, nil
}

// EncodeListRoomsResponse serializes a ListRoomsResponse.
func EncodeListRoomsResponse(resp *ListRoomsResponse) ([]byte, error) {
	var buf bytes.Buffer
	if err := binary.Write(&buf, binary.BigEndian, uint16(len(resp.Rooms))); err != nil {
		return nil, err
	}
	for _, r := range resp.Rooms {
		if err := writeString(&buf, r.Name); err != nil {
			return nil, err
		}
		if err := writeString(&buf, r.CreatedBy); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

// DecodeListRoomsResponse deserializes a ListRoomsResponse.
func DecodeListRoomsResponse(data []byte) (*ListRoomsResponse, error) {
	r := bytes.NewReader(data)
	var count uint16
	if err := binary.Read(r, binary.BigEndian, &count); err != nil {
		return nil, err
	}
	rooms := make([]RoomInfo, count)
	for i := range rooms {
		name, err := readString(r)
		if err != nil {
			return nil, err
		}
		createdBy, err := readString(r)
		if err != nil {
			return nil, err
		}
		rooms[i] = RoomInfo{Name: name, CreatedBy: createdBy}
	}
	return &ListRoomsResponse{Rooms: rooms}, nil
}

// EncodeP2PPunchRequest serializes a P2PPunchRequest.
func EncodeP2PPunchRequest(req *P2PPunchRequest) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeIP(&buf, req.TargetVIP); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DecodeP2PPunchRequest deserializes a P2PPunchRequest.
func DecodeP2PPunchRequest(data []byte) (*P2PPunchRequest, error) {
	r := bytes.NewReader(data)
	ip, err := readIP(r)
	if err != nil {
		return nil, err
	}
	return &P2PPunchRequest{TargetVIP: ip}, nil
}

// EncodeP2PPunchResponse serializes a P2PPunchResponse.
func EncodeP2PPunchResponse(resp *P2PPunchResponse) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeIP(&buf, resp.PeerVIP); err != nil {
		return nil, err
	}
	if err := writeString(&buf, resp.PeerAddr); err != nil {
		return nil, err
	}
	if err := writeString(&buf, resp.Token); err != nil {
		return nil, err
	}
	// New fields: NATType + Candidates
	buf.WriteByte(resp.NATType)
	if err := writeStringSlice(&buf, resp.Candidates); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DecodeP2PPunchResponse deserializes a P2PPunchResponse.
func DecodeP2PPunchResponse(data []byte) (*P2PPunchResponse, error) {
	r := bytes.NewReader(data)
	ip, err := readIP(r)
	if err != nil {
		return nil, err
	}
	addr, err := readString(r)
	if err != nil {
		return nil, err
	}
	token, err := readString(r)
	if err != nil {
		return nil, err
	}
	resp := &P2PPunchResponse{PeerVIP: ip, PeerAddr: addr, Token: token}
	// Read new fields if present (backward compatibility).
	if r.Len() > 0 {
		natType, err := r.ReadByte()
		if err == nil {
			resp.NATType = natType
		}
		if r.Len() > 0 {
			candidates, err := readStringSlice(r)
			if err == nil {
				resp.Candidates = candidates
			}
		}
	}
	return resp, nil
}

// EncodeP2POffer serializes a P2POffer.
func EncodeP2POffer(offer *P2POffer) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeIP(&buf, offer.FromVIP); err != nil {
		return nil, err
	}
	if err := writeString(&buf, offer.UDPAddr); err != nil {
		return nil, err
	}
	if err := writeString(&buf, offer.Token); err != nil {
		return nil, err
	}
	// New fields: NATType + Candidates
	buf.WriteByte(offer.NATType)
	if err := writeStringSlice(&buf, offer.Candidates); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DecodeP2POffer deserializes a P2POffer.
func DecodeP2POffer(data []byte) (*P2POffer, error) {
	r := bytes.NewReader(data)
	ip, err := readIP(r)
	if err != nil {
		return nil, err
	}
	addr, err := readString(r)
	if err != nil {
		return nil, err
	}
	token, err := readString(r)
	if err != nil {
		return nil, err
	}
	offer := &P2POffer{FromVIP: ip, UDPAddr: addr, Token: token}
	// Read new fields if present.
	if r.Len() > 0 {
		natType, err := r.ReadByte()
		if err == nil {
			offer.NATType = natType
		}
		if r.Len() > 0 {
			candidates, err := readStringSlice(r)
			if err == nil {
				offer.Candidates = candidates
			}
		}
	}
	return offer, nil
}

// EncodeP2PAnswer serializes a P2PAnswer.
func EncodeP2PAnswer(answer *P2PAnswer) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeIP(&buf, answer.FromVIP); err != nil {
		return nil, err
	}
	if err := writeString(&buf, answer.UDPAddr); err != nil {
		return nil, err
	}
	if err := writeString(&buf, answer.Token); err != nil {
		return nil, err
	}
	if err := writeBool(&buf, answer.Accepted); err != nil {
		return nil, err
	}
	// New fields: NATType + Candidates
	buf.WriteByte(answer.NATType)
	if err := writeStringSlice(&buf, answer.Candidates); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DecodeP2PAnswer deserializes a P2PAnswer.
func DecodeP2PAnswer(data []byte) (*P2PAnswer, error) {
	r := bytes.NewReader(data)
	ip, err := readIP(r)
	if err != nil {
		return nil, err
	}
	addr, err := readString(r)
	if err != nil {
		return nil, err
	}
	token, err := readString(r)
	if err != nil {
		return nil, err
	}
	accepted, err := readBool(r)
	if err != nil {
		return nil, err
	}
	answer := &P2PAnswer{FromVIP: ip, UDPAddr: addr, Token: token, Accepted: accepted}
	// Read new fields if present.
	if r.Len() > 0 {
		natType, err := r.ReadByte()
		if err == nil {
			answer.NATType = natType
		}
		if r.Len() > 0 {
			candidates, err := readStringSlice(r)
			if err == nil {
				answer.Candidates = candidates
			}
		}
	}
	return answer, nil
}
