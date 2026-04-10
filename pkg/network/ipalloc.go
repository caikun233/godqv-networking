// Package network manages virtual IP address allocation for rooms.
package network

import (
	"fmt"
	"net"
	"sync"
	"time"
)

// DefaultLeaseDuration is the default time an IP is reserved for a user after
// disconnecting. Within this window a reconnecting user receives the same IP.
const DefaultLeaseDuration = 30 * time.Minute

// lease tracks a reserved IP for a user that has disconnected.
type lease struct {
	ip        net.IP
	expiresAt time.Time
}

// IPAllocator manages virtual IP allocation for a subnet.
type IPAllocator struct {
	mu            sync.Mutex
	network       net.IPNet
	used          map[string]bool   // IP string -> in use
	userToIP      map[string]net.IP // username -> currently assigned IP
	leases        map[string]*lease // username -> reserved lease (after disconnect)
	leaseDuration time.Duration
	nextIP        net.IP
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
		network:       *network,
		used:          make(map[string]bool),
		userToIP:      make(map[string]net.IP),
		leases:        make(map[string]*lease),
		leaseDuration: DefaultLeaseDuration,
		nextIP:        startIP,
	}, nil
}

// Allocate returns the next available IP address (without lease tracking).
func (a *IPAllocator) Allocate() (net.IP, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.allocateLocked()
}

// AllocateForUser returns an IP for the given username. If the user has a
// valid lease from a previous session the same IP is returned; otherwise a
// new IP is allocated.
func (a *IPAllocator) AllocateForUser(username string) (net.IP, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Expire stale leases first.
	a.expireLeasesLocked()

	// Check for an existing lease.
	if l, ok := a.leases[username]; ok {
		ipStr := l.ip.String()
		// The IP may still be marked as used if leases overlap, but since
		// it is reserved for this user we can hand it back.
		a.used[ipStr] = true
		result := make(net.IP, 4)
		copy(result, l.ip.To4())
		a.userToIP[username] = result
		delete(a.leases, username)
		return result, nil
	}

	ip, err := a.allocateLocked()
	if err != nil {
		return nil, err
	}
	a.userToIP[username] = ip
	return ip, nil
}

// Release returns an IP address to the pool immediately.
func (a *IPAllocator) Release(ip net.IP) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.used, ip.To4().String())
}

// ReleaseWithLease marks the IP as available for others after leaseDuration,
// but reserves it for username so that a reconnect within that window gets
// the same address back.
func (a *IPAllocator) ReleaseWithLease(ip net.IP, username string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	delete(a.userToIP, username)

	reserved := make(net.IP, 4)
	copy(reserved, ip.To4())
	a.leases[username] = &lease{
		ip:        reserved,
		expiresAt: time.Now().Add(a.leaseDuration),
	}
	// The IP stays in the "used" set while the lease is active so that
	// it is not handed out to another user.
}

// expireLeasesLocked removes expired leases and frees the associated IPs.
// Must be called with a.mu held.
func (a *IPAllocator) expireLeasesLocked() {
	now := time.Now()
	for user, l := range a.leases {
		if now.After(l.expiresAt) {
			delete(a.used, l.ip.String())
			delete(a.leases, user)
		}
	}
}

// allocateLocked finds and marks the next free IP. Must be called with a.mu held.
func (a *IPAllocator) allocateLocked() (net.IP, error) {
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
