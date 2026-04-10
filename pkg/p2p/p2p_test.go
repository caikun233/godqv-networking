package p2p

import (
	"net"
	"testing"
)

// TestAddPeerDeduplication verifies that calling AddPeer multiple times for
// the same VIP does not start duplicate punch goroutines.
func TestAddPeerDeduplication(t *testing.T) {
	// Create a manager with a real UDP socket.
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: 0})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	m := &Manager{
		conn:  conn,
		links: make(map[string]*PeerLink),
		done:  make(chan struct{}),
		natInfo: &NATInfo{
			Type:       NATCone,
			MappedAddr: "127.0.0.1:12345",
		},
		localAddr: "127.0.0.1:12345",
	}
	defer m.Close()

	peerVIP := net.IPv4(10, 100, 2, 3)

	// First call should succeed and start punching.
	if err := m.AddPeer(peerVIP, "127.0.0.1:9999", nil, NATCone); err != nil {
		t.Fatalf("first AddPeer: %v", err)
	}

	// punching flag is set synchronously in AddPeer before the goroutine
	// starts, so no sleep is needed.
	m.mu.RLock()
	link1 := m.links[peerVIP.String()]
	m.mu.RUnlock()

	if link1 == nil {
		t.Fatal("expected link to exist after first AddPeer")
	}
	if !link1.punching {
		t.Fatal("expected punching=true after first AddPeer")
	}

	// Second call with same VIP should be skipped (dedup).
	if err := m.AddPeer(peerVIP, "127.0.0.1:8888", nil, NATCone); err != nil {
		t.Fatalf("second AddPeer: %v", err)
	}

	m.mu.RLock()
	link2 := m.links[peerVIP.String()]
	m.mu.RUnlock()

	// The link should be the same object (not replaced).
	if link2 != link1 {
		t.Fatal("expected second AddPeer to be deduplicated, but link was replaced")
	}

	// Third call also deduplicated.
	if err := m.AddPeer(peerVIP, "127.0.0.1:7777", nil, NATSymmetric); err != nil {
		t.Fatalf("third AddPeer: %v", err)
	}

	m.mu.RLock()
	link3 := m.links[peerVIP.String()]
	m.mu.RUnlock()

	if link3 != link1 {
		t.Fatal("expected third AddPeer to be deduplicated, but link was replaced")
	}
}

// TestAddPeerSkipsActive verifies that AddPeer is a no-op when a link
// is already active.
func TestAddPeerSkipsActive(t *testing.T) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: 0})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	m := &Manager{
		conn:  conn,
		links: make(map[string]*PeerLink),
		done:  make(chan struct{}),
		natInfo: &NATInfo{
			Type:       NATCone,
			MappedAddr: "127.0.0.1:12345",
		},
		localAddr: "127.0.0.1:12345",
	}
	defer m.Close()

	peerVIP := net.IPv4(10, 100, 2, 5)
	peerAddr, _ := net.ResolveUDPAddr("udp4", "127.0.0.1:5555")

	// Pre-populate an active link.
	m.mu.Lock()
	m.links[peerVIP.String()] = &PeerLink{
		PeerVIP:  peerVIP,
		PeerAddr: peerAddr,
		Active:   true,
		punching: false,
	}
	m.mu.Unlock()

	// AddPeer should skip because link is active.
	if err := m.AddPeer(peerVIP, "127.0.0.1:6666", nil, NATCone); err != nil {
		t.Fatalf("AddPeer: %v", err)
	}

	m.mu.RLock()
	link := m.links[peerVIP.String()]
	m.mu.RUnlock()

	// Address should NOT have been updated.
	if link.PeerAddr.Port != 5555 {
		t.Fatalf("expected port to remain 5555, got %d", link.PeerAddr.Port)
	}
}
