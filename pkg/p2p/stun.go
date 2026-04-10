package p2p

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"log"
	"math"
	"net"
	"strconv"
	"time"
)

// NATType represents the detected NAT type.
type NATType uint8

const (
	// NATUnknown means NAT type could not be determined.
	NATUnknown NATType = 0
	// NATCone represents any cone-type NAT (Full Cone, Restricted Cone, or
	// Port-Restricted Cone). Standard hole-punching works for these.
	NATCone NATType = 1
	// NATSymmetric represents a Symmetric NAT which assigns a different
	// external port for each destination. Requires port prediction or relay.
	NATSymmetric NATType = 2
)

func (n NATType) String() string {
	switch n {
	case NATCone:
		return "锥形NAT (Cone)"
	case NATSymmetric:
		return "对称NAT (Symmetric)"
	default:
		return "未知"
	}
}

// NATInfo contains the result of NAT type detection.
type NATInfo struct {
	Type       NATType
	MappedAddr string   // Primary mapped address from STUN
	Candidates []string // All discovered candidate addresses (STUN reflexive + local)
	PortDelta  int      // Observed port allocation delta (for Symmetric NAT prediction)
	LastPort   int      // Last observed external port from STUN
}

// Default public STUN servers (from different providers for NAT detection).
var stunServers = []string{
	"stun.l.google.com:19302",
	"stun1.l.google.com:19302",
	"stun2.l.google.com:19302",
	"stun3.l.google.com:19302",
	"stun4.l.google.com:19302",
}

// STUN message constants (RFC 5389).
const (
	stunMagicCookie       = 0x2112A442
	stunBindRequest       = 0x0001
	stunBindSuccess       = 0x0101
	stunAttrXORMappedAddr = 0x0020
	stunAttrMappedAddr    = 0x0001
	stunHeaderSize        = 20
)

// stunDetectNAT queries multiple STUN servers from the same socket to detect
// the NAT type and gather candidate addresses. It returns NATInfo with the
// detected type, primary mapped address, all candidates, and port delta.
func stunDetectNAT(conn *net.UDPConn) (*NATInfo, error) {
	info := &NATInfo{Type: NATUnknown}

	// Query multiple STUN servers to compare mapped addresses.
	type stunResult struct {
		server string
		addr   string
		ip     string
		port   int
	}
	var results []stunResult
	var lastErr error

	for i, server := range stunServers {
		log.Printf("[STUN] 查询服务器 %d/%d: %s", i+1, len(stunServers), server)
		addr, err := stunQuery(conn, server)
		if err != nil {
			log.Printf("[STUN] 服务器 %s 失败: %v", server, err)
			lastErr = err
			continue
		}
		host, portStr, err := net.SplitHostPort(addr)
		if err != nil {
			log.Printf("[STUN] 解析地址失败 %s: %v", addr, err)
			continue
		}
		port, err := strconv.Atoi(portStr)
		if err != nil {
			log.Printf("[STUN] 解析端口失败 %s: %v", addr, err)
			continue
		}
		results = append(results, stunResult{server: server, addr: addr, ip: host, port: port})
		log.Printf("[STUN] 服务器 %s → 映射地址: %s", server, addr)

		// We need at least 3 results for reliable detection; stop after that.
		if len(results) >= 3 {
			break
		}
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("all STUN servers failed: %w", lastErr)
	}

	// Use the first successful result as the primary address.
	info.MappedAddr = results[0].addr

	// Determine NAT type by comparing mapped ports across servers.
	if len(results) >= 2 {
		allSamePort := true
		for i := 1; i < len(results); i++ {
			if results[i].port != results[0].port {
				allSamePort = false
				break
			}
		}

		if allSamePort {
			info.Type = NATCone
			log.Printf("[STUN] NAT类型检测: 锥形NAT (所有STUN服务器返回相同端口 %d)", results[0].port)
		} else {
			info.Type = NATSymmetric
			// Calculate port delta from consecutive STUN results.
			deltas := make([]int, 0, len(results)-1)
			for i := 1; i < len(results); i++ {
				d := results[i].port - results[i-1].port
				deltas = append(deltas, d)
			}
			// Use the most common delta, or average if they vary.
			info.PortDelta = mostCommonDelta(deltas)
			info.LastPort = results[len(results)-1].port

			ports := make([]int, len(results))
			for i, r := range results {
				ports[i] = r.port
			}
			log.Printf("[STUN] NAT类型检测: 对称NAT (端口变化: %v, 预测增量=%d, 最后端口=%d)",
				ports, info.PortDelta, info.LastPort)
		}
	} else {
		log.Printf("[STUN] 仅获得1个STUN结果, 无法确定NAT类型")
	}

	// Add all STUN reflexive addresses as candidates (deduplicated).
	seen := make(map[string]bool)
	for _, r := range results {
		if !seen[r.addr] {
			info.Candidates = append(info.Candidates, r.addr)
			seen[r.addr] = true
		}
	}

	// Add local network addresses as LAN candidates.
	localPort := conn.LocalAddr().(*net.UDPAddr).Port
	localCandidates := gatherLocalCandidates(localPort)
	for _, c := range localCandidates {
		if !seen[c] {
			info.Candidates = append(info.Candidates, c)
			seen[c] = true
		}
	}

	log.Printf("[STUN] NAT检测完成: 类型=%s, 主地址=%s, 候选地址=%d个",
		info.Type, info.MappedAddr, len(info.Candidates))

	return info, nil
}

// gatherLocalCandidates returns local (private) network addresses with the
// given port. These are used for same-LAN peer connectivity.
func gatherLocalCandidates(port int) []string {
	var candidates []string
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return candidates
	}
	for _, addr := range addrs {
		ipnet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipnet.IP.To4()
		if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			continue
		}
		candidates = append(candidates, fmt.Sprintf("%s:%d", ip.String(), port))
	}
	return candidates
}

// mostCommonDelta returns the most representative port delta from a list.
// If deltas are consistent, returns the common value. Otherwise returns the
// average (clamped to [1, 100]). When port allocation appears random (delta
// >100 or inconsistent), returns 1 to enable sequential spread probing.
func mostCommonDelta(deltas []int) int {
	if len(deltas) == 0 {
		return 1
	}
	if len(deltas) == 1 {
		d := deltas[0]
		if d <= 0 {
			return 1
		}
		if d > 100 {
			return 1 // Random allocation detected; use delta=1 for sequential spread probing
		}
		return d
	}

	// Check if all deltas are the same.
	allSame := true
	for i := 1; i < len(deltas); i++ {
		if deltas[i] != deltas[0] {
			allSame = false
			break
		}
	}
	if allSame && deltas[0] > 0 && deltas[0] <= 100 {
		return deltas[0]
	}

	// Calculate average.
	sum := 0
	for _, d := range deltas {
		sum += int(math.Abs(float64(d)))
	}
	avg := sum / len(deltas)
	if avg <= 0 {
		return 1
	}
	if avg > 100 {
		return 1 // Random allocation detected; use delta=1 for sequential spread probing
	}
	return avg
}

// stunDiscover sends a STUN Binding Request and returns the reflexive
// (public) transport address as "ip:port". This is a simple single-query
// fallback used when full NAT detection isn't needed.
func stunDiscover(conn *net.UDPConn) (string, error) {
	var lastErr error
	for i, server := range stunServers {
		log.Printf("[STUN] 尝试服务器 %d/%d: %s", i+1, len(stunServers), server)
		addr, err := stunQuery(conn, server)
		if err != nil {
			log.Printf("[STUN] 服务器 %s 失败: %v", server, err)
			lastErr = err
			continue
		}
		log.Printf("[STUN] 成功! 公网地址: %s (通过 %s)", addr, server)
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
