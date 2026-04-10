//go:build windows

package tunnel

import (
	"fmt"
	"net"
	"os/exec"
	"strings"

	"golang.zx2c4.com/wintun"
)

// WindowsTUN implements Device for Windows using Wintun.
type WindowsTUN struct {
	adapter *wintun.Adapter
	session wintun.Session
	name    string
	config  Config
}

// CreateTUN creates a new TUN device on Windows using Wintun.
func CreateTUN(cfg Config) (Device, error) {
	if cfg.MTU == 0 {
		cfg.MTU = 1400
	}
	if cfg.Name == "" {
		cfg.Name = "GodQV"
	}

	adapter, err := wintun.CreateAdapter(cfg.Name, "GodQV Networking", nil)
	if err != nil {
		return nil, fmt.Errorf("create wintun adapter: %w", err)
	}

	session, err := adapter.StartSession(0x800000) // 8MB ring
	if err != nil {
		adapter.Close()
		return nil, fmt.Errorf("start session: %w", err)
	}

	tun := &WindowsTUN{
		adapter: adapter,
		session: session,
		name:    cfg.Name,
		config:  cfg,
	}

	if err := tun.configure(); err != nil {
		session.End()
		adapter.Close()
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

	// Add route
	gateway := make(net.IP, 4)
	copy(gateway, t.config.Address.To4())
	cmd = exec.Command("route", "add",
		t.config.Subnet.IP.String(),
		"mask", net.IP(t.config.Subnet.Mask).String(),
		gateway.String(),
		"metric", "10",
	)
	cmd.CombinedOutput() // Best effort

	return nil
}

func (t *WindowsTUN) Name() string {
	return t.name
}

func (t *WindowsTUN) Read(buf []byte) (int, error) {
	packet, err := t.session.ReceivePacket()
	if err != nil {
		return 0, err
	}
	n := copy(buf, packet)
	t.session.ReleaseReceivePacket(packet)
	return n, nil
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
	t.session.End()
	t.adapter.Close()
	return nil
}
