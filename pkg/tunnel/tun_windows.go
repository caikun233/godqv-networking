//go:build windows

package tunnel

import (
	"crypto/sha256"
	"fmt"
	"net"
	"os/exec"
	"strings"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wintun"
)

// WindowsTUN implements Device for Windows using Wintun.
type WindowsTUN struct {
	adapter *wintun.Adapter
	session wintun.Session
	name    string
	config  Config
	// closeEvent is signaled when the device is closed so that blocking
	// Read calls can be interrupted.
	closeEvent windows.Handle
}

// deterministicGUID generates a deterministic GUID from an adapter name so
// that Windows reuses the same network connection profile across restarts
// instead of creating "GodQV Networking 2", "GodQV Networking 3", etc.
func deterministicGUID(name string) *windows.GUID {
	h := sha256.Sum256([]byte("godqv-networking:" + name))
	return &windows.GUID{
		Data1: uint32(h[0])<<24 | uint32(h[1])<<16 | uint32(h[2])<<8 | uint32(h[3]),
		Data2: uint16(h[4])<<8 | uint16(h[5]),
		Data3: uint16(h[6])<<8 | uint16(h[7]),
		Data4: [8]byte{h[8], h[9], h[10], h[11], h[12], h[13], h[14], h[15]},
	}
}

// CreateTUN creates a new TUN device on Windows using Wintun.
func CreateTUN(cfg Config) (Device, error) {
	if cfg.MTU == 0 {
		cfg.MTU = 1400
	}
	if cfg.Name == "" {
		cfg.Name = "GodQV"
	}

	// Extract embedded wintun.dll to the executable's directory.
	if err := ensureWintunDLL(); err != nil {
		return nil, fmt.Errorf("ensure wintun.dll: %w", err)
	}

	guid := deterministicGUID(cfg.Name)

	// Try to clean up any stale wintun adapters with the same tunnel type,
	// except the one we are about to create/use to avoid lingering numbered adapters.
	exec.Command("powershell", "-Command",
		fmt.Sprintf("Get-NetAdapter | Where-Object { $_.InterfaceDescription -match 'Virtual LAN Ethernet Adapter' -and $_.Name -ne '%s' } | Remove-NetAdapter -Confirm:$false", cfg.Name)).Run()

	// Try to close any stale adapter with the same name first so we get a
	// clean session. Errors here are expected if no such adapter exists.
	if old, err := wintun.OpenAdapter(cfg.Name); err == nil {
		old.Close()
	}

	adapter, err := wintun.CreateAdapter(cfg.Name, "GodQV Networking Virtual LAN Ethernet Adapter", guid)
	if err != nil {
		return nil, fmt.Errorf("create wintun adapter: %w", err)
	}

	session, err := adapter.StartSession(0x800000) // 8MB ring
	if err != nil {
		adapter.Close()
		return nil, fmt.Errorf("start session: %w", err)
	}

	closeEvent, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		session.End()
		adapter.Close()
		return nil, fmt.Errorf("create close event: %w", err)
	}

	tun := &WindowsTUN{
		adapter:    adapter,
		session:    session,
		name:       cfg.Name,
		config:     cfg,
		closeEvent: closeEvent,
	}

	if err := tun.configure(); err != nil {
		session.End()
		adapter.Close()
		windows.CloseHandle(closeEvent)
		return nil, fmt.Errorf("configure: %w", err)
	}

	return tun, nil
}

func (t *WindowsTUN) configure() error {
	addr := t.config.Address.String()

	// Set IP address using netsh
	cmd := exec.Command("netsh", "interface", "ip", "set", "address",
		fmt.Sprintf("name=%s", t.name),
		"source=static",
		fmt.Sprintf("addr=%s", addr),
		fmt.Sprintf("mask=%s", net.IP(t.config.Subnet.Mask).String()),
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("netsh set address: %s: %w", strings.TrimSpace(string(out)), err)
	}

	// Set MTU
	cmd = exec.Command("netsh", "interface", "ipv4", "set", "subinterface",
		t.name,
		fmt.Sprintf("mtu=%d", t.config.MTU),
		"store=active",
	)
	cmd.CombinedOutput() // Best effort

	// Since we set a static IP and subnet mask via netsh, Windows will automatically
	// create the correct On-Link route for the subnet. Manually adding the route
	// with a self-referencing gateway often results in routing loops or conflicts.

	return nil
}

func (t *WindowsTUN) Name() string {
	return t.name
}

func (t *WindowsTUN) Read(buf []byte) (int, error) {
	readWait := t.session.ReadWaitEvent()
	events := [2]windows.Handle{readWait, t.closeEvent}

	for {
		packet, err := t.session.ReceivePacket()
		if err == nil {
			n := copy(buf, packet)
			t.session.ReleaseReceivePacket(packet)
			return n, nil
		}
		// ERROR_NO_MORE_ITEMS (259) means no packet is available yet.
		// Wait for either a new packet or the device being closed.
		result, waitErr := windows.WaitForMultipleObjects(events[:], false, windows.INFINITE)
		if waitErr != nil {
			return 0, fmt.Errorf("wait for read event: %w", waitErr)
		}
		switch result {
		case windows.WAIT_OBJECT_0: // readWait signaled – packet available
			continue
		case windows.WAIT_OBJECT_0 + 1: // closeEvent signaled
			return 0, fmt.Errorf("TUN device closed")
		default:
			return 0, fmt.Errorf("unexpected wait result: %d", result)
		}
	}
}

func (t *WindowsTUN) Write(buf []byte) (int, error) {
	packet, err := t.session.AllocateSendPacket(len(buf))
	if err != nil {
		return 0, err
	}
	copy(packet, buf)
	t.session.SendPacket(packet)
	return len(buf), nil
}

func (t *WindowsTUN) Close() error {
	// Signal the close event to unblock any pending Read calls.
	windows.SetEvent(t.closeEvent)
	t.session.End()
	t.adapter.Close()
	windows.CloseHandle(t.closeEvent)
	return nil
}
