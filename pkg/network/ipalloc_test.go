package network

import (
	"net"
	"testing"
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
