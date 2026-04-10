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
		ones, bits := resp.Subnet.Mask.Size()
		if err := binary.Write(&buf, binary.BigEndian, uint8(ones)); err != nil {
			return nil, err
		}
		_ = bits
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
