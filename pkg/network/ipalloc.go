// Package network manages virtual IP address allocation for rooms.
package network

import (
	"fmt"
	"net"
	"sync"
)

// IPAllocator manages virtual IP allocation for a subnet.
type IPAllocator struct {
	mu      sync.Mutex
	network net.IPNet
	used    map[string]bool // IP string -> in use
	nextIP  net.IP
}

// NewIPAllocator creates a new allocator for the given CIDR.
func NewIPAllocator(cidr string) (*IPAllocator, error) {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, fmt.Errorf("parse CIDR: %w", err)
	}

	// Start from .1 (skip network address)
	startIP := make(net.IP, len(network.IP))
	copy(startIP, network.IP)
	startIP = startIP.To4()
	startIP[3] = 1

	return &IPAllocator{
		network: *network,
		used:    make(map[string]bool),
		nextIP:  startIP,
	}, nil
}

// Allocate returns the next available IP address.
func (a *IPAllocator) Allocate() (net.IP, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Try to find an unused IP
	ip := make(net.IP, 4)
	copy(ip, a.nextIP.To4())

	for attempts := 0; attempts < 254; attempts++ {
		if !a.used[ip.String()] && a.network.Contains(ip) && !a.isBroadcast(ip) {
			a.used[ip.String()] = true
			// Advance nextIP
			a.nextIP = a.incrementIP(ip)
			result := make(net.IP, 4)
			copy(result, ip)
			return result, nil
		}
		ip = a.incrementIP(ip)
		if !a.network.Contains(ip) {
			// Wrap around
			ip = make(net.IP, len(a.network.IP))
			copy(ip, a.network.IP)
			ip = ip.To4()
			ip[3] = 1
		}
	}

	return nil, fmt.Errorf("no available IP addresses in %s", a.network.String())
}

// Release returns an IP address to the pool.
func (a *IPAllocator) Release(ip net.IP) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.used, ip.To4().String())
}

// Subnet returns the managed subnet.
func (a *IPAllocator) Subnet() net.IPNet {
	return a.network
}

func (a *IPAllocator) incrementIP(ip net.IP) net.IP {
	result := make(net.IP, 4)
	copy(result, ip.To4())
	for i := 3; i >= 0; i-- {
		result[i]++
		if result[i] != 0 {
			break
		}
	}
	return result
}

func (a *IPAllocator) isBroadcast(ip net.IP) bool {
	ip4 := ip.To4()
	for i := range ip4 {
		if ip4[i]|a.network.Mask[i] != 0xff {
			return false
		}
	}
	return true
}
