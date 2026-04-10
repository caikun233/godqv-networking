//go:build linux

package tunnel

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	tunDevice = "/dev/net/tun"
	ifnameSize = 16
)

// LinuxTUN implements Device for Linux using /dev/net/tun.
type LinuxTUN struct {
	file   *os.File
	name   string
	config Config
}

// CreateTUN creates a new TUN device on Linux.
func CreateTUN(cfg Config) (Device, error) {
	if cfg.MTU == 0 {
		cfg.MTU = 1400
	}
	if cfg.Name == "" {
		cfg.Name = "godqv0"
	}

	// Open TUN device
	fd, err := unix.Open(tunDevice, unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", tunDevice, err)
	}

	// Configure TUN interface
	var ifr [40]byte // struct ifreq
	copy(ifr[:ifnameSize], cfg.Name)
	// IFF_TUN | IFF_NO_PI
	ifr[ifnameSize] = 0x01 // IFF_TUN
	ifr[ifnameSize+1] = 0x10 // IFF_NO_PI (high byte)

	// Actually IFF_TUN = 0x0001, IFF_NO_PI = 0x1000
	flags := uint16(unix.IFF_TUN | unix.IFF_NO_PI)
	*(*uint16)(unsafe.Pointer(&ifr[ifnameSize])) = flags

	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), unix.TUNSETIFF, uintptr(unsafe.Pointer(&ifr[0])))
	if errno != 0 {
		unix.Close(fd)
		return nil, fmt.Errorf("TUNSETIFF: %w", errno)
	}

	// Get actual interface name
	name := strings.TrimRight(string(ifr[:ifnameSize]), "\x00")

	file := os.NewFile(uintptr(fd), tunDevice)
	tun := &LinuxTUN{
		file:   file,
		name:   name,
		config: cfg,
	}

	// Configure the interface
	if err := tun.configure(); err != nil {
		file.Close()
		return nil, fmt.Errorf("configure: %w", err)
	}

	return tun, nil
}

func (t *LinuxTUN) configure() error {
	ones, _ := t.config.Subnet.Mask.Size()
	addr := fmt.Sprintf("%s/%d", t.config.Address.String(), ones)

	// Set IP address
	cmd := exec.Command("ip", "addr", "add", addr, "dev", t.name)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ip addr add: %s: %w", string(out), err)
	}

	// Set MTU and bring up
	cmd = exec.Command("ip", "link", "set", "dev", t.name, "mtu", fmt.Sprintf("%d", t.config.MTU), "up")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ip link set: %s: %w", string(out), err)
	}

	return nil
}

func (t *LinuxTUN) Name() string {
	return t.name
}

func (t *LinuxTUN) Read(buf []byte) (int, error) {
	return t.file.Read(buf)
}

func (t *LinuxTUN) Write(buf []byte) (int, error) {
	return t.file.Write(buf)
}

func (t *LinuxTUN) Close() error {
	return t.file.Close()
}
