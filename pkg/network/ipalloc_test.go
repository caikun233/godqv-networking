package network

import (
	"net"
	"testing"
	"time"
)

func TestIPAllocator(t *testing.T) {
	alloc, err := NewIPAllocator("10.100.0.0/24")
	if err != nil {
		t.Fatalf("NewIPAllocator: %v", err)
	}

	// First allocation should be .1
	ip1, err := alloc.Allocate()
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if !ip1.Equal(net.ParseIP("10.100.0.1").To4()) {
		t.Errorf("Expected 10.100.0.1, got %v", ip1)
	}

	// Second should be .2
	ip2, err := alloc.Allocate()
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if !ip2.Equal(net.ParseIP("10.100.0.2").To4()) {
		t.Errorf("Expected 10.100.0.2, got %v", ip2)
	}

	// Release .1 and reallocate
	alloc.Release(ip1)
	ip3, err := alloc.Allocate()
	if err != nil {
		t.Fatalf("Allocate after release: %v", err)
	}
	// Should get .3 next (nextIP advanced), then wrap to find .1
	// Actually nextIP is at .3, so we get .3
	if !ip3.Equal(net.ParseIP("10.100.0.3").To4()) {
		t.Errorf("Expected 10.100.0.3, got %v", ip3)
	}

	subnet := alloc.Subnet()
	if subnet.String() != "10.100.0.0/24" {
		t.Errorf("Unexpected subnet: %s", subnet.String())
	}
}

func TestIPAllocatorExhaustion(t *testing.T) {
	// Small subnet: /30 gives 4 addresses, 2 usable (.1 and .2)
	alloc, err := NewIPAllocator("10.0.0.0/30")
	if err != nil {
		t.Fatalf("NewIPAllocator: %v", err)
	}

	ip1, err := alloc.Allocate()
	if err != nil {
		t.Fatalf("Allocate 1: %v", err)
	}
	if ip1 == nil {
		t.Fatal("Expected non-nil IP")
	}

	ip2, err := alloc.Allocate()
	if err != nil {
		t.Fatalf("Allocate 2: %v", err)
	}
	if ip2 == nil {
		t.Fatal("Expected non-nil IP")
	}

	// Third allocation should fail
	_, err = alloc.Allocate()
	if err == nil {
		t.Error("Expected error on exhausted pool")
	}
}

func TestAllocateForUserReusesLease(t *testing.T) {
	alloc, err := NewIPAllocator("10.100.0.0/24")
	if err != nil {
		t.Fatalf("NewIPAllocator: %v", err)
	}

	// Allocate an IP for alice.
	ip1, err := alloc.AllocateForUser("alice")
	if err != nil {
		t.Fatalf("AllocateForUser: %v", err)
	}
	if !ip1.Equal(net.ParseIP("10.100.0.1").To4()) {
		t.Fatalf("Expected 10.100.0.1, got %v", ip1)
	}

	// Allocate for bob.
	ip2, err := alloc.AllocateForUser("bob")
	if err != nil {
		t.Fatalf("AllocateForUser: %v", err)
	}
	if !ip2.Equal(net.ParseIP("10.100.0.2").To4()) {
		t.Fatalf("Expected 10.100.0.2, got %v", ip2)
	}

	// alice disconnects – release with lease.
	alloc.ReleaseWithLease(ip1, "alice")

	// alice reconnects – should get the same IP back.
	ip3, err := alloc.AllocateForUser("alice")
	if err != nil {
		t.Fatalf("AllocateForUser after reconnect: %v", err)
	}
	if !ip3.Equal(ip1) {
		t.Errorf("Expected alice to get %v again, got %v", ip1, ip3)
	}
}

func TestLeaseExpiresAndIPIsReused(t *testing.T) {
	alloc, err := NewIPAllocator("10.0.0.0/30")
	if err != nil {
		t.Fatalf("NewIPAllocator: %v", err)
	}
	// Use a tiny lease duration for testing.
	alloc.leaseDuration = 1 * time.Millisecond

	ip1, err := alloc.AllocateForUser("alice")
	if err != nil {
		t.Fatalf("AllocateForUser: %v", err)
	}

	ip2, err := alloc.AllocateForUser("bob")
	if err != nil {
		t.Fatalf("AllocateForUser: %v", err)
	}

	// Both usable IPs are taken. alice disconnects.
	alloc.ReleaseWithLease(ip1, "alice")

	// Immediately, charlie can NOT allocate because alice's lease is active.
	_, err = alloc.AllocateForUser("charlie")
	if err == nil {
		t.Fatal("Expected error while lease is active")
	}

	// Wait for the lease to expire.
	time.Sleep(10 * time.Millisecond)

	// Now charlie should be able to allocate the freed IP.
	ip3, err := alloc.AllocateForUser("charlie")
	if err != nil {
		t.Fatalf("AllocateForUser after lease expiry: %v", err)
	}
	if !ip3.Equal(ip1) {
		t.Errorf("Expected charlie to get %v after lease expiry, got %v", ip1, ip3)
	}

	_ = ip2 // bob still holds this
}

func TestReleaseWithLeaseBlocksOtherUsers(t *testing.T) {
	alloc, err := NewIPAllocator("10.0.0.0/30")
	if err != nil {
		t.Fatalf("NewIPAllocator: %v", err)
	}

	ip1, err := alloc.AllocateForUser("alice")
	if err != nil {
		t.Fatalf("AllocateForUser: %v", err)
	}

	// alice disconnects with lease.
	alloc.ReleaseWithLease(ip1, "alice")

	// bob tries to allocate – alice's IP is still reserved, so bob gets .2
	ip2, err := alloc.AllocateForUser("bob")
	if err != nil {
		t.Fatalf("AllocateForUser: %v", err)
	}
	if ip2.Equal(ip1) {
		t.Errorf("Expected bob NOT to get alice's leased IP %v", ip1)
	}

	// Pool should now be exhausted (/30 = 2 usable IPs: .1 leased, .2 for bob).
	_, err = alloc.AllocateForUser("charlie")
	if err == nil {
		t.Error("Expected error on exhausted pool with active lease")
	}
}
