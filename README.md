# 神区互联 - GodQV Networking

[![Build and Release](https://github.com/caikun233/godqv-networking/actions/workflows/build.yml/badge.svg)](https://github.com/caikun233/godqv-networking/actions/workflows/build.yml)

类似 Radmin LAN 的虚拟局域网组网工具。让不同网络中的设备像在同一局域网中一样互相访问。

**这不是 VPN**，而是虚拟局域网组网工具，主要用于：
- 🎮 远程联机游戏（局域网联机）
- 💻 远程办公内网访问
- 🔧 跨网络设备互联互通

## 架构概览

```
┌──────────┐     Internet      ┌──────────────┐      Internet     ┌──────────┐
│ 客户端 A  │ ◄──────────────► │  神区互联服务端  │ ◄──────────────► │ 客户端 B  │
│ 10.100.1.1│     TCP:9527     │   (你的服务器)  │     TCP:9527    │10.100.1.2│
└──────────┘                   └──────────────┘                   └──────────┘
     │                                                                  │
     └──────────── 虚拟局域网 10.100.1.0/24 ──────────────────────────────┘
                    (互相可 ping / 访问服务)
```

## 功能特性

- ✅ **虚拟组网**：通过 TUN 虚拟网卡创建虚拟局域网
- ✅ **房间系统**：支持多个独立的虚拟网络房间
- ✅ **自动 IP 分配**：每个房间独立子网，自动分配虚拟 IP
- ✅ **用户认证**：用户名/密码认证
- ✅ **房间密码**：可选的房间密码保护
- ✅ **跨平台**：支持 Linux / Windows / macOS
- ✅ **心跳检测**：自动检测断线客户端
- ✅ **节点感知**：实时显示同房间在线节点

## 快速开始

### 1. 下载

从 [Releases](https://github.com/caikun233/godqv-networking/releases) 下载对应平台的二进制文件，或自行编译：

```bash
# 克隆仓库
git clone https://github.com/caikun233/godqv-networking.git
cd godqv-networking

# 编译（需要 Go 1.21+）
make build

# 或交叉编译所有平台
make build-all
```

### 2. 部署服务端

#### 方式一：直接部署

```bash
# 1. 生成配置文件
./godqv-server -genconfig

# 2. 编辑配置文件
vim server.json
```

**server.json 配置说明：**

```json
{
  "listen_addr": ":9527",
  "users": {
    "admin": "修改为强密码",
    "player1": "修改为强密码",
    "player2": "修改为强密码"
  },
  "rooms": {
    "default": {
      "password": "",
      "subnet": "10.100.1.0/24"
    },
    "game-room": {
      "password": "房间密码",
      "subnet": "10.100.2.0/24"
    }
  }
}
```

| 字段 | 说明 |
|------|------|
| `listen_addr` | 监听地址和端口，默认 `:9527` |
| `users` | 用户名和密码映射 |
| `rooms` | 预定义房间配置 |
| `rooms.*.password` | 房间密码，留空则不需要密码 |
| `rooms.*.subnet` | 该房间的虚拟子网 CIDR |

```bash
# 3. 启动服务端
./godqv-server -config server.json

# 或使用命令行参数指定监听地址
./godqv-server -config server.json -listen :9527
```

#### 方式二：systemd 服务（推荐用于 Linux 服务器）

```bash
# 1. 将二进制文件复制到系统目录
sudo cp godqv-server /usr/local/bin/
sudo chmod +x /usr/local/bin/godqv-server

# 2. 创建配置目录和文件
sudo mkdir -p /etc/godqv
sudo cp server.json /etc/godqv/server.json
sudo chmod 600 /etc/godqv/server.json

# 3. 创建 systemd 服务文件
sudo tee /etc/systemd/system/godqv-server.service << 'EOF'
[Unit]
Description=GodQV Networking Server (神区互联)
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/godqv-server -config /etc/godqv/server.json
Restart=always
RestartSec=5
User=root

[Install]
WantedBy=multi-user.target
EOF

# 4. 启动并设为开机自启
sudo systemctl daemon-reload
sudo systemctl enable godqv-server
sudo systemctl start godqv-server

# 5. 查看运行状态
sudo systemctl status godqv-server

# 6. 查看日志
sudo journalctl -u godqv-server -f
```

#### 方式三：Docker 部署

```bash
# 使用 Docker 运行
docker run -d \
  --name godqv-server \
  -p 9527:9527 \
  -v $(pwd)/server.json:/etc/godqv/server.json \
  --restart=always \
  golang:1.24-alpine \
  sh -c "go install github.com/caikun233/godqv-networking/cmd/server@latest && godqv-server -config /etc/godqv/server.json"
```

**⚠️ 防火墙设置**：确保服务器的 9527 端口（TCP）已开放：

```bash
# Ubuntu/Debian (ufw)
sudo ufw allow 9527/tcp

# CentOS/RHEL (firewalld)
sudo firewall-cmd --permanent --add-port=9527/tcp
sudo firewall-cmd --reload

# 云服务器还需在安全组中放行 9527 端口
```

### 3. 使用客户端

#### 方式一：配置文件

```bash
# 1. 生成配置文件
./godqv-client -genconfig

# 2. 编辑配置文件
vim client.json
```

**client.json 配置说明：**

```json
{
  "server_addr": "你的服务器IP:9527",
  "username": "player1",
  "password": "你的密码",
  "room_name": "default",
  "room_password": ""
}
```

| 字段 | 说明 |
|------|------|
| `server_addr` | 服务器地址和端口 |
| `username` | 你的用户名（需在服务端配置中存在） |
| `password` | 你的密码 |
| `room_name` | 要加入的房间名 |
| `room_password` | 房间密码（如果有） |

```bash
# 3. 启动客户端（需要管理员/root 权限来创建虚拟网卡）
sudo ./godqv-client -config client.json
```

#### 方式二：命令行参数

```bash
# Linux/macOS（需要 sudo）
sudo ./godqv-client -server "服务器IP:9527" -user player1 -pass "密码" -room default

# Windows（需要管理员权限运行）
godqv-client.exe -server "服务器IP:9527" -user player1 -pass "密码" -room default
```

#### 方式三：仅测试连接（不创建虚拟网卡）

```bash
# 不需要管理员权限
./godqv-client -server "服务器IP:9527" -user player1 -pass "密码" -room default -notun
```

### 4. 验证连接

客户端连接成功后会显示：
```
╔══════════════════════════════════════╗
║       神区互联 - GodQV Networking     ║
║             客户端 Client              ║
╚══════════════════════════════════════╝

┌─────────────────────────────────────┐
│ 虚拟IP: 10.100.1.1                  │
│ 子网:   10.100.1.0/24               │
│ TUN设备: godqv0                      │
├─────────────────────────────────────┤
│ 命令: peers - 查看在线节点           │
│       quit  - 退出                   │
└─────────────────────────────────────┘
```

在两台客户端都连接后，可以互相 ping：

```bash
# 在客户端 A 上 ping 客户端 B
ping 10.100.1.2
```

## 客户端交互命令

| 命令 | 说明 |
|------|------|
| `peers` | 显示当前房间在线节点列表 |
| `quit` / `exit` | 断开连接并退出 |

## 项目结构

```
.
├── cmd/
│   ├── server/         # 服务端入口
│   │   └── main.go
│   └── client/         # 客户端入口
│       └── main.go
├── internal/
│   ├── server/         # 服务端核心逻辑
│   │   └── server.go
│   └── client/         # 客户端核心逻辑
│       └── client.go
├── pkg/
│   ├── protocol/       # 通信协议定义和序列化
│   │   ├── protocol.go
│   │   ├── serialize.go
│   │   └── protocol_test.go
│   ├── network/        # 虚拟IP分配
│   │   ├── ipalloc.go
│   │   └── ipalloc_test.go
│   └── tunnel/         # TUN 虚拟网卡（跨平台）
│       ├── tunnel.go
│       ├── tun_linux.go
│       ├── tun_darwin.go
│       └── tun_windows.go
├── .github/workflows/  # CI/CD
│   └── build.yml
├── Makefile
├── go.mod
└── README.md
```

## 工作原理

1. **服务端**：监听 TCP 连接，管理用户认证、房间和虚拟 IP 分配
2. **客户端连接**：客户端通过 TCP 连接到服务端，完成认证后加入指定房间
3. **虚拟网卡**：客户端创建 TUN 虚拟网卡，分配服务端指定的虚拟 IP
4. **流量转发**：
   - 客户端捕获发往虚拟网段的流量
   - 通过 TCP 连接发送到服务端
   - 服务端根据目的 IP 转发给对应客户端
   - 目标客户端将流量注入本地虚拟网卡
5. **心跳保活**：客户端每 20 秒发送心跳，服务端 90 秒无心跳断开连接

## 协议格式

```
┌─────────┬──────────┬───────────────┬─────────────┐
│ Version │ MsgType  │ Payload Length│   Payload    │
│ 1 byte  │ 1 byte   │   2 bytes     │  N bytes     │
└─────────┴──────────┴───────────────┴─────────────┘
```

## 端口说明

| 端口 | 协议 | 用途 |
|------|------|------|
| 9527 | TCP  | 服务端监听端口（可配置） |

## 平台支持

| 平台 | 架构 | 虚拟网卡技术 |
|------|------|------------|
| Linux | amd64 / arm64 | TUN (/dev/net/tun) |
| Windows | amd64 | Wintun |
| macOS | amd64 / arm64 | utun |

## 常见问题

**Q: 需要管理员权限吗？**
A: 创建虚拟网卡需要管理员/root 权限。使用 `-notun` 参数可以在无权限的情况下测试连接。

**Q: 客户端自动创建房间？**
A: 如果服务端配置中没有预定义的房间，客户端加入时会自动创建。首个加入者设置的密码将成为房间密码。

**Q: 支持多少个客户端？**
A: 每个 /24 子网支持最多 253 个客户端。可以通过配置更大的子网或创建多个房间来扩展。

**Q: 流量是否加密？**
A: 当前版本使用 TCP 明文传输。生产环境建议在前面加一层 TLS（如使用 nginx 反向代理或未来版本将内置 TLS）。

## 开发

```bash
# 运行测试
make test

# 本地编译
make build

# 交叉编译所有平台
make build-all

# 清理构建产物
make clean
```

## License

MIT
