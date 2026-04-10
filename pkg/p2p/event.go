package p2p

import "net"

// EventType identifies the kind of P2P event.
type EventType int

const (
	// EventPunchStart is emitted when hole-punching begins for a peer.
	EventPunchStart EventType = iota
	// EventPunchSuccess is emitted when hole-punching succeeds.
	EventPunchSuccess
	// EventPunchTimeout is emitted when hole-punching times out.
	EventPunchTimeout
)

// Event carries information about a P2P hole-punching event.
type Event struct {
	Type     EventType
	PeerVIP  net.IP
	PeerAddr string // UDP endpoint (may be empty for some events)
}
