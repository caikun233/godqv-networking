// Package tunnel provides TUN device abstraction for virtual networking.
package tunnel

import (
	"net"
)

// Device represents a TUN device interface.
type Device interface {
	// Name returns the device name.
	Name() string
	// Read reads a packet from the TUN device.
	Read(buf []byte) (int, error)
	// Write writes a packet to the TUN device.
	Write(buf []byte) (int, error)
	// Close closes the TUN device.
	Close() error
}

// Config holds TUN device configuration.
type Config struct {
	Name    string     // Device name (e.g., "godqv0")
	Address net.IP     // Virtual IP address
	Subnet  net.IPNet  // Virtual network subnet
	MTU     int        // MTU size (default 1400)
}
