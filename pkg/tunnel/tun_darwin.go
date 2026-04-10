//go:build darwin

package tunnel

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"unsafe"
)

// DarwinTUN implements Device for macOS using utun.
type DarwinTUN struct {
	file   *os.File
	name   string
	config Config
}

// System constants for macOS utun
const (
	sysSIOCGIFNAME = 0xc0206910 // SIOCGIFNAME
	pfSystem        = 30         // PF_SYSTEM
	sockDgram       = 2          // SOCK_DGRAM
	sysprotoControl = 2          // SYSPROTO_CONTROL
	afSysControl    = 2          // AF_SYS_CONTROL
	utunControl     = "com.apple.net.utun_control"
	utunOptIfname   = 2          // UTUN_OPT_IFNAME
)

type sockaddrCtl struct {
	scLen      uint8
	scFamily   uint8
	ssSysaddr  uint16
	scID       uint32
	scUnit     uint32
	scReserved [5]uint32
}

type ctlInfo struct {
	ctlID   uint32
	ctlName [96]byte
}

// CreateTUN creates a new TUN device on macOS.
func CreateTUN(cfg Config) (Device, error) {
	if cfg.MTU == 0 {
		cfg.MTU = 1400
	}

	fd, err := syscall.Socket(pfSystem, sockDgram, sysprotoControl)
	if err != nil {
		return nil, fmt.Errorf("create socket: %w", err)
	}

	var info ctlInfo
	copy(info.ctlName[:], utunControl)

	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd),
		uintptr(0xc0644e03), // CTLIOCGINFO
		uintptr(unsafe.Pointer(&info)))
	if errno != 0 {
		syscall.Close(fd)
		return nil, fmt.Errorf("CTLIOCGINFO: %w", errno)
	}

	// Try to find an available utun device (starting from utun0)
	for unitNum := uint32(0); unitNum < 256; unitNum++ {
		addr := sockaddrCtl{
			scLen:     uint8(unsafe.Sizeof(sockaddrCtl{})),
			scFamily:  afSysControl,
			scID:      info.ctlID,
			scUnit:    unitNum + 1, // utun unit numbers are 1-based
		}

		_, _, errno = syscall.Syscall(syscall.SYS_CONNECT, uintptr(fd),
			uintptr(unsafe.Pointer(&addr)),
			uintptr(unsafe.Sizeof(addr)))
		if errno == 0 {
			// Get the actual interface name
			name := fmt.Sprintf("utun%d", unitNum)

			if err := syscall.SetNonblock(fd, false); err != nil {
				syscall.Close(fd)
				return nil, fmt.Errorf("set nonblock: %w", err)
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
	}

	syscall.Close(fd)
	return nil, fmt.Errorf("no available utun device")
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
	// macOS utun prepends a 4-byte protocol header
	tmp := make([]byte, len(buf)+4)
	n, err := t.file.Read(tmp)
	if err != nil {
		return 0, err
	}
	if n <= 4 {
		return 0, fmt.Errorf("short read from utun")
	}
	copy(buf, tmp[4:n])
	return n - 4, nil
}

func (t *DarwinTUN) Write(buf []byte) (int, error) {
	// macOS utun expects a 4-byte protocol header (AF_INET = 2 in network byte order)
	tmp := make([]byte, len(buf)+4)
	binary.BigEndian.PutUint32(tmp[:4], 2) // AF_INET
	copy(tmp[4:], buf)
	n, err := t.file.Write(tmp)
	if err != nil {
		return 0, err
	}
	if n <= 4 {
		return 0, nil
	}
	return n - 4, nil
}

func (t *DarwinTUN) Close() error {
	return t.file.Close()
}
