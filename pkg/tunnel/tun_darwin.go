//go:build darwin

package tunnel

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
)

// DarwinTUN implements Device for macOS using utun.
type DarwinTUN struct {
	file   *os.File
	name   string
	config Config
}

// CreateTUN creates a new TUN device on macOS.
func CreateTUN(cfg Config) (Device, error) {
	if cfg.MTU == 0 {
		cfg.MTU = 1400
	}

	// On macOS, we use utun devices
	// Try to find an available utun device
	for i := 0; i < 256; i++ {
		name := fmt.Sprintf("utun%d", i)
		fd, err := createUtun(i)
		if err != nil {
			continue
		}
		file := os.NewFile(uintptr(fd), name)
		tun := &DarwinTUN{
			file:   file,
			name:   name,
			config: cfg,
		}
		if err := tun.configure(); err != nil {
			file.Close()
			return nil, err
		}
		return tun, nil
	}
	return nil, fmt.Errorf("no available utun device")
}

func createUtun(num int) (int, error) {
	// Use socket-based utun creation
	fd, err := unix_socket(30, 2, 6) // PF_SYSTEM, SOCK_DGRAM, SYSPROTO_CONTROL
	if err != nil {
		return 0, err
	}
	_ = num
	return fd, nil
}

// Simplified: use exec to configure since direct syscall is complex on macOS
func unix_socket(domain, typ, proto int) (int, error) {
	return 0, fmt.Errorf("not implemented - use water library or similar")
}

func (t *DarwinTUN) configure() error {
	addr := t.config.Address.String()
	// Calculate peer address (use .1 of subnet as peer)
	peerIP := make(net.IP, 4)
	copy(peerIP, t.config.Subnet.IP.To4())
	peerIP[3] = 1

	cmd := exec.Command("ifconfig", t.name, addr, peerIP.String(), "mtu", strconv.Itoa(t.config.MTU), "up")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ifconfig: %s: %w", string(out), err)
	}

	// Add route
	ones, _ := t.config.Subnet.Mask.Size()
	cmd = exec.Command("route", "add", "-net", fmt.Sprintf("%s/%d", t.config.Subnet.IP, ones), "-interface", t.name)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("route add: %s: %w", string(out), err)
	}

	return nil
}

func (t *DarwinTUN) Name() string {
	return t.name
}

func (t *DarwinTUN) Read(buf []byte) (int, error) {
	// macOS utun prepends a 4-byte header
	tmp := make([]byte, len(buf)+4)
	n, err := t.file.Read(tmp)
	if err != nil {
		return 0, err
	}
	if n <= 4 {
		return 0, fmt.Errorf("short read")
	}
	copy(buf, tmp[4:n])
	return n - 4, nil
}

func (t *DarwinTUN) Write(buf []byte) (int, error) {
	// macOS utun expects a 4-byte header (AF_INET = 2)
	tmp := make([]byte, len(buf)+4)
	tmp[3] = 2 // AF_INET
	copy(tmp[4:], buf)
	n, err := t.file.Write(tmp)
	if err != nil {
		return 0, err
	}
	return n - 4, nil
}

func (t *DarwinTUN) Close() error {
	return t.file.Close()
}
