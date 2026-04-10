package p2p

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net"
	"time"
)

// Default public STUN servers.
var stunServers = []string{
	"stun.l.google.com:19302",
	"stun1.l.google.com:19302",
	"stun2.l.google.com:19302",
}

// STUN message constants (RFC 5389).
const (
	stunMagicCookie = 0x2112A442
	stunBindRequest = 0x0001
	stunBindSuccess = 0x0101
	stunAttrXORMappedAddr = 0x0020
	stunAttrMappedAddr    = 0x0001
	stunHeaderSize        = 20
)

// stunDiscover sends a STUN Binding Request and returns the reflexive
// (public) transport address as "ip:port".
func stunDiscover(conn *net.UDPConn) (string, error) {
	var lastErr error
	for _, server := range stunServers {
		addr, err := stunQuery(conn, server)
		if err != nil {
			lastErr = err
			continue
		}
		return addr, nil
	}
	return "", fmt.Errorf("all STUN servers failed: %w", lastErr)
}

func stunQuery(conn *net.UDPConn, server string) (string, error) {
	serverAddr, err := net.ResolveUDPAddr("udp4", server)
	if err != nil {
		return "", err
	}

	// Build STUN Binding Request (minimal, no attributes).
	txID := make([]byte, 12)
	rand.Read(txID) // RFC 5389: transaction ID must be cryptographically random
	// RFC 5389: type(2) + length(2) + magic cookie(4) + transaction ID(12) = 20 bytes
	req := make([]byte, stunHeaderSize)
	binary.BigEndian.PutUint16(req[0:2], stunBindRequest)
	binary.BigEndian.PutUint16(req[2:4], 0) // message length = 0
	binary.BigEndian.PutUint32(req[4:8], stunMagicCookie)
	copy(req[8:20], txID)

	conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.WriteToUDP(req, serverAddr); err != nil {
		return "", fmt.Errorf("send STUN: %w", err)
	}

	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 512)
	n, _, err := conn.ReadFromUDP(buf)
	if err != nil {
		return "", fmt.Errorf("recv STUN: %w", err)
	}

	return parseSTUNResponse(buf[:n])
}

func parseSTUNResponse(data []byte) (string, error) {
	if len(data) < stunHeaderSize {
		return "", fmt.Errorf("STUN response too short")
	}

	msgType := binary.BigEndian.Uint16(data[0:2])
	if msgType != stunBindSuccess {
		return "", fmt.Errorf("unexpected STUN response type: 0x%04x", msgType)
	}

	msgLen := binary.BigEndian.Uint16(data[2:4])
	if int(msgLen)+stunHeaderSize > len(data) {
		return "", fmt.Errorf("STUN response truncated")
	}

	// Parse attributes looking for XOR-MAPPED-ADDRESS or MAPPED-ADDRESS.
	offset := stunHeaderSize
	end := stunHeaderSize + int(msgLen)
	for offset+4 <= end {
		attrType := binary.BigEndian.Uint16(data[offset : offset+2])
		attrLen := binary.BigEndian.Uint16(data[offset+2 : offset+4])
		attrVal := data[offset+4 : offset+4+int(attrLen)]

		switch attrType {
		case stunAttrXORMappedAddr:
			return parseXORMappedAddress(attrVal, data[4:8])
		case stunAttrMappedAddr:
			return parseMappedAddress(attrVal)
		}

		// Attributes are padded to 4-byte boundaries.
		offset += 4 + int(attrLen)
		if offset%4 != 0 {
			offset += 4 - (offset % 4)
		}
	}

	return "", fmt.Errorf("no mapped address in STUN response")
}

func parseXORMappedAddress(val []byte, magicCookie []byte) (string, error) {
	if len(val) < 8 {
		return "", fmt.Errorf("XOR-MAPPED-ADDRESS too short")
	}
	family := val[1]
	if family != 0x01 { // IPv4
		return "", fmt.Errorf("only IPv4 supported, got family %d", family)
	}
	xport := binary.BigEndian.Uint16(val[2:4])
	port := xport ^ binary.BigEndian.Uint16(magicCookie[0:2])
	ip := make(net.IP, 4)
	for i := 0; i < 4; i++ {
		ip[i] = val[4+i] ^ magicCookie[i]
	}
	return fmt.Sprintf("%s:%d", ip.String(), port), nil
}

func parseMappedAddress(val []byte) (string, error) {
	if len(val) < 8 {
		return "", fmt.Errorf("MAPPED-ADDRESS too short")
	}
	family := val[1]
	if family != 0x01 {
		return "", fmt.Errorf("only IPv4 supported")
	}
	port := binary.BigEndian.Uint16(val[2:4])
	ip := net.IP(val[4:8])
	return fmt.Sprintf("%s:%d", ip.String(), port), nil
}
