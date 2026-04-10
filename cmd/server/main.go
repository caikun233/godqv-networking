package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/caikun233/godqv-networking/internal/server"
	"github.com/caikun233/godqv-networking/pkg/store"
)

var (
	configFile = flag.String("config", "server.json", "配置文件路径")
	listenAddr = flag.String("listen", ":9527", "监听地址")
	genConfig  = flag.Bool("genconfig", false, "生成示例配置文件")
)

func main() {
	flag.Parse()

	fmt.Println("╔══════════════════════════════════════╗")
	fmt.Println("║       神区互联 - GodQV Networking     ║")
	fmt.Println("║             服务端 Server              ║")
	fmt.Println("╚══════════════════════════════════════╝")

	if *genConfig {
		generateDefaultConfig()
		return
	}

	cfg := loadConfig()

	srv, err := server.New(cfg)
	if err != nil {
		log.Fatalf("创建服务器失败: %v", err)
	}

	// Handle graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		log.Println("[Server] 正在关闭服务器...")
		srv.Stop()
		os.Exit(0)
	}()

	if err := srv.Start(); err != nil {
		log.Fatalf("服务器启动失败: %v", err)
	}
}

func loadConfig() server.Config {
	data, err := os.ReadFile(*configFile)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("配置文件 %s 不存在，使用默认配置", *configFile)
			return defaultConfig()
		}
		log.Fatalf("读取配置文件失败: %v", err)
	}

	var cfg server.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		log.Fatalf("解析配置文件失败: %v", err)
	}

	if cfg.ListenAddr == "" {
		cfg.ListenAddr = *listenAddr
	}

	return cfg
}

func defaultConfig() server.Config {
	return server.Config{
		ListenAddr: *listenAddr,
		Users: map[string]string{
			"admin": "admin123",
			"user1": "password1",
		},
		RoomConfigs: map[string]server.RoomConfig{
			"default": {
				Password: "",
				Subnet:   "10.100.1.0/24",
			},
		},
	}
}

func generateDefaultConfig() {
	cfg := server.Config{
		ListenAddr: ":9527",
		Database: &store.Config{
			Host:     "localhost",
			Port:     5432,
			User:     "godqv",
			Password: "godqv_password",
			Database: "godqv",
			SSLMode:  "disable",
		},
		Users: map[string]string{
			"admin": "admin123",
		},
		RoomConfigs: map[string]server.RoomConfig{
			"default": {
				Password: "default123",
				Subnet:   "10.100.1.0/24",
			},
		},
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		log.Fatalf("生成配置失败: %v", err)
	}

	filename := "server.json"
	if err := os.WriteFile(filename, data, 0600); err != nil {
		log.Fatalf("写入配置文件失败: %v", err)
	}
	fmt.Printf("已生成示例配置文件: %s\n", filename)
	fmt.Println("请修改 database 配置以连接 PostgreSQL")
	fmt.Println("若不配置 database 字段，将使用 JSON 内置用户 (兼容旧模式)")
}
