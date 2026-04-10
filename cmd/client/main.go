package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	"github.com/caikun233/godqv-networking/internal/client"
	"github.com/caikun233/godqv-networking/pkg/tunnel"
)

var (
	configFile = flag.String("config", "client.json", "配置文件路径")
	serverAddr = flag.String("server", "", "服务器地址 (例如: example.com:9527)")
	username   = flag.String("user", "", "用户名")
	password   = flag.String("pass", "", "密码")
	roomName   = flag.String("room", "", "房间名称")
	roomPass   = flag.String("roompass", "", "房间密码")
	genConfig  = flag.Bool("genconfig", false, "生成示例配置文件")
	noTun      = flag.Bool("notun", false, "不创建TUN设备 (仅测试连接)")
)

// tunWrapper wraps a tunnel.Device for use as client.TunWriter.
type tunWrapper struct {
	dev tunnel.Device
}

func (tw *tunWrapper) WritePacket(packet []byte) error {
	_, err := tw.dev.Write(packet)
	return err
}

func main() {
	flag.Parse()

	fmt.Println("╔══════════════════════════════════════╗")
	fmt.Println("║       神区互联 - GodQV Networking     ║")
	fmt.Println("║             客户端 Client              ║")
	fmt.Println("╚══════════════════════════════════════╝")

	if *genConfig {
		generateDefaultConfig()
		return
	}

	cfg := loadConfig()

	// Create and connect client
	c := client.New(cfg)

	if err := c.Connect(); err != nil {
		log.Fatalf("连接失败，请检查服务器地址和用户凭据")
	}
	defer c.Close()

	// Join room
	if err := c.JoinRoom(); err != nil {
		log.Fatalf("加入房间失败: %v", err)
	}

	// Setup TUN device if not in test mode
	var tunDev tunnel.Device
	if !*noTun {
		if runtime.GOOS == "linux" || runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
			var err error
			subnet := c.Subnet()
			tunDev, err = tunnel.CreateTUN(tunnel.Config{
				Name:    "godqv0",
				Address: c.VirtualIP(),
				Subnet:  subnet,
				MTU:     1400,
			})
			if err != nil {
				log.Printf("警告: 创建TUN设备失败 (需要管理员/root权限): %v", err)
				log.Println("继续以无TUN模式运行...")
			} else {
				defer tunDev.Close()
				log.Printf("TUN设备已创建: %s", tunDev.Name())

				// Set up bidirectional packet forwarding
				c.SetTunWriter(&tunWrapper{dev: tunDev})

				// Read from TUN and send to server
				go func() {
					buf := make([]byte, 1500)
					for {
						n, err := tunDev.Read(buf)
						if err != nil {
							log.Printf("读取TUN设备失败: %v", err)
							return
						}
						if n > 0 {
							if err := c.SendPacket(buf[:n]); err != nil {
								log.Printf("发送数据包失败: %v", err)
								return
							}
						}
					}
				}()
			}
		}
	} else {
		log.Println("以无TUN模式运行 (仅测试连接)")
	}

	// Start receiving
	c.StartReceiving()

	// Print status
	printStatus(c, tunDev)

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Interactive command loop
	go interactiveLoop(c)

	select {
	case <-sigCh:
		log.Println("正在断开连接...")
	case <-c.Done():
		log.Println("连接已断开")
	}
}

func printStatus(c *client.Client, tunDev tunnel.Device) {
	fmt.Println("\n┌─────────────────────────────────────┐")
	fmt.Printf("│ 虚拟IP: %-28s│\n", c.VirtualIP())
	subnet := c.Subnet()
	fmt.Printf("│ 子网:   %-28s│\n", subnet.String())
	if tunDev != nil {
		fmt.Printf("│ TUN设备: %-27s│\n", tunDev.Name())
	}
	fmt.Println("├─────────────────────────────────────┤")
	fmt.Println("│ 命令: peers - 查看在线节点           │")
	fmt.Println("│       quit  - 退出                   │")
	fmt.Println("└─────────────────────────────────────┘")
}

func interactiveLoop(c *client.Client) {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch line {
		case "peers":
			peers := c.GetPeers()
			fmt.Printf("\n在线节点 (%d):\n", len(peers))
			for _, p := range peers {
				status := "在线"
				if !p.Online {
					status = "离线"
				}
				fmt.Printf("  %s - %s [%s]\n", p.Username, p.VirtualIP, status)
			}
		case "quit", "exit":
			fmt.Println("正在退出...")
			c.Close()
			os.Exit(0)
		case "":
			// ignore
		default:
			fmt.Println("未知命令。可用命令: peers, quit")
		}
	}
}

func loadConfig() client.Config {
	// Command line flags take priority
	if *serverAddr != "" && *username != "" {
		return client.Config{
			ServerAddr: *serverAddr,
			Username:   *username,
			Password:   *password,
			RoomName:   firstNonEmpty(*roomName, "default"),
			RoomPass:   *roomPass,
		}
	}

	data, err := os.ReadFile(*configFile)
	if err != nil {
		if os.IsNotExist(err) {
			log.Fatalf("配置文件 %s 不存在。请使用 -genconfig 生成示例配置，或使用命令行参数", *configFile)
		}
		log.Fatalf("读取配置文件失败: %v", err)
	}

	var cfg client.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		log.Fatalf("解析配置文件失败: %v", err)
	}

	return cfg
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func generateDefaultConfig() {
	cfg := client.Config{
		ServerAddr: "your-server.com:9527",
		Username:   "user1",
		Password:   "password1",
		RoomName:   "default",
		RoomPass:   "",
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		log.Fatalf("生成配置失败: %v", err)
	}

	filename := "client.json"
	if err := os.WriteFile(filename, data, 0600); err != nil {
		log.Fatalf("写入配置文件失败: %v", err)
	}
	fmt.Printf("已生成示例配置文件: %s\n", filename)
	fmt.Println("请修改配置后启动客户端")
}
