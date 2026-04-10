# 神区互联 - GodQV Networking

[![Build and Release](https://github.com/caikun233/godqv-networking/actions/workflows/build.yml/badge.svg)](https://github.com/caikun233/godqv-networking/actions/workflows/build.yml)

类似 Radmin LAN 的虚拟局域网组网工具。让不同网络中的设备像在同一局域网中一样互相访问。

**这不是 VPN**，而是虚拟局域网组网工具，主要用于：
- 🎮 远程联机游戏（局域网联机）
- 💻 远程办公内网访问
- 🔧 跨网络设备互联互通

## 架构概览

```
                          ┌──────────────┐
                          │  信令服务器   │
                          │  TCP:9527    │
                          │  (PostgreSQL) │
                          └──────┬───────┘
                                 │ 信令 / 认证 / 保底中继
                    ┌────────────┼────────────┐
                    │            │            │
              ┌─────┴────┐            ┌─────┴────┐
              │ 客户端 A  │◄──UDP直连──►│ 客户端 B  │
              │ 10.100.1.1│  P2P打洞   │10.100.1.2│
              └──────────┘            └──────────┘
                    │                        │
                    └──── 虚拟局域网 10.100.1.0/24 ────┘
                            (互相可 ping / 访问服务)
```

## 功能特性

- ✅ **虚拟组网**：通过 TUN 虚拟网卡创建虚拟局域网
- ✅ **P2P 直连**：UDP 打洞实现端到端直连，延迟更低
- ✅ **TCP 保底中继**：打洞失败时自动回退到服务器中继
- ✅ **图形界面 (GUI)**：Fyne 跨平台 GUI 客户端
- ✅ **命令行客户端 (CLI)**：支持脚本和自动化
- ✅ **PostgreSQL 持久化**：用户和房间数据存储在 PostgreSQL
- ✅ **用户注册**：客户端可直接通过 TCP 注册用户
- ✅ **无密码登录**：支持仅用户名登录（无密码账户）
- ✅ **房间系统**：支持多个独立的虚拟网络房间
- ✅ **房间密码**：房间必须设置密码（bcrypt 加密存储）
- ✅ **自动 IP 分配**：每个房间独立子网，自动分配虚拟 IP
- ✅ **跨平台**：支持 Linux / Windows / macOS
- ✅ **心跳检测**：自动检测断线客户端
- ✅ **节点感知**：实时显示同房间在线节点及连接模式（P2P/中继）
- ✅ **向后兼容**：不配置数据库时退回 JSON 配置模式

## 快速开始

### 1. 下载

从 [Releases](https://github.com/caikun233/godqv-networking/releases) 下载对应平台的二进制文件，或自行编译：

```bash
# 克隆仓库
git clone https://github.com/caikun233/godqv-networking.git
cd godqv-networking

# 编译服务端 + CLI 客户端（需要 Go 1.25+）
make build

# 编译 GUI 客户端（需要 CGO 和 OpenGL 开发库）
make gui

# 或交叉编译所有平台
make build-all
```

### 2. 部署服务端

#### 准备 PostgreSQL（推荐）

```bash
# 创建数据库和用户
sudo -u postgres psql
CREATE USER godqv WITH PASSWORD 'your_secure_password';
CREATE DATABASE godqv OWNER godqv;
\q
```

#### 生成并编辑配置文件

```bash
# 生成示例配置
./godqv-server -genconfig
```

**server.json 配置说明：**

```json
{
  "listen_addr": ":9527",
  "database": {
    "host": "localhost",
    "port": 5432,
    "user": "godqv",
    "password": "your_secure_password",
    "database": "godqv",
    "ssl_mode": "disable"
  },
  "users": {
    "admin": "admin123"
  },
  "rooms": {
    "default": {
      "password": "default123",
      "subnet": "10.100.1.0/24"
    }
  }
}
```

| 字段 | 说明 |
|------|------|
| `listen_addr` | 监听地址和端口，默认 `:9527` |
| `database` | PostgreSQL 连接配置（可选，不配置则使用 JSON 内置用户） |
| `database.host` | PostgreSQL 服务器地址 |
| `database.port` | PostgreSQL 端口，默认 5432 |
| `database.user` | PostgreSQL 用户名 |
| `database.password` | PostgreSQL 密码 |
| `database.database` | 数据库名称 |
| `database.ssl_mode` | SSL 模式（disable/require/verify-full） |
| `users` | 内置用户（仅在不配置 database 时使用） |
| `rooms` | 预定义房间（会自动加载到内存） |

```bash
# 启动服务端
./godqv-server -config server.json
```

### 3. 使用 GUI 客户端

```bash
# 启动 GUI 客户端（需要管理员权限创建虚拟网卡）
sudo ./godqv-gui
```

GUI 提供以下功能：
- 📝 输入服务器地址和用户名登录
- 📝 注册新用户（支持无密码注册）
- 📋 浏览和加入可用房间
- ➕ 创建新房间（需设置密码）
- 👥 查看在线节点列表及连接模式（P2P直连/TCP中继）
- 🔌 一键断开连接

### 4. 使用 CLI 客户端

#### 注册新用户

```bash
# 注册有密码的用户
./godqv-client -register -server "服务器IP:9527" -user myname -pass "mypassword"

# 注册无密码用户
./godqv-client -register -server "服务器IP:9527" -user myname
```

#### 创建房间

```bash
# 连接后创建房间
./godqv-client -server "服务器IP:9527" -user myname -pass "mypassword" -createroom "my-room" -createpass "roompassword"
```

#### 列出房间

```bash
./godqv-client -server "服务器IP:9527" -user myname -pass "mypassword" -listrooms
```

#### 加入房间并组网

```bash
# Linux/macOS（需要 sudo）
sudo ./godqv-client -server "服务器IP:9527" -user myname -pass "mypassword" -room "my-room" -roompass "roompassword"

# Windows（需要管理员权限运行）
godqv-client.exe -server "服务器IP:9527" -user myname -pass "mypassword" -room "my-room" -roompass "roompassword"
```

#### 禁用 P2P（仅使用 TCP 中继）

```bash
sudo ./godqv-client -server "服务器IP:9527" -user myname -pass "mypassword" -room default -roompass "roompassword" -nop2p
```

#### 仅测试连接（不创建虚拟网卡）

```bash
./godqv-client -server "服务器IP:9527" -user myname -pass "mypassword" -room default -roompass "roompassword" -notun
```

### 5. 验证连接

客户端连接成功后会显示：
```
┌─────────────────────────────────────┐
│ 虚拟IP: 10.100.1.1                  │
│ 子网:   10.100.1.0/24               │
│ TUN设备: godqv0                      │
├─────────────────────────────────────┤
│ 命令: peers - 查看在线节点           │
│       rooms - 列出可用房间           │
│       quit  - 退出                   │
└─────────────────────────────────────┘
```

在两台客户端都连接后，可以互相 ping：

```bash
ping 10.100.1.2
```

## 客户端交互命令

| 命令 | 说明 |
|------|------|
| `peers` | 显示当前房间在线节点列表（含 P2P/中继模式） |
| `rooms` | 列出服务器上可用的房间 |
| `quit` / `exit` | 断开连接并退出 |

## 项目结构

```
.
├── cmd/
│   ├── server/         # 服务端入口
│   │   └── main.go
│   ├── client/         # CLI 客户端入口
│   │   └── main.go
│   └── gui/            # GUI 客户端入口
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
│   ├── store/          # PostgreSQL 持久化层
│   │   └── store.go
│   ├── p2p/            # P2P UDP 打洞
│   │   ├── p2p.go
│   │   └── stun.go
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

### 连接流程

1. **用户注册/认证**：客户端通过 TCP 连接到服务端，完成注册或认证（密码使用 bcrypt 加密存储在 PostgreSQL）
2. **加入房间**：选择或创建房间，验证房间密码后加入
3. **虚拟网卡**：客户端创建 TUN 虚拟网卡，分配服务端指定的虚拟 IP
4. **P2P 打洞**：
   - 客户端通过 STUN 发现自己的公网 UDP 地址
   - 通过信令服务器交换双方的 UDP 地址
   - 双方同时发送 UDP 探测包进行 NAT 打洞
   - 打洞成功后数据直接通过 UDP 端到端传输
5. **TCP 保底**：如果 P2P 打洞失败（如对称 NAT），自动回退到服务器 TCP 中继
6. **心跳保活**：客户端每 20 秒发送心跳，服务端 90 秒无心跳断开连接

### 数据传输优先级

```
数据包 → 检查 P2P 直连 → 有: UDP 直发 → 目标
                         → 无: TCP 中继 → 服务器 → 目标
```

## 协议格式

```
┌─────────┬──────────┬───────────────┬─────────────┐
│ Version │ MsgType  │ Payload Length│   Payload    │
│ 1 byte  │ 1 byte   │   2 bytes     │  N bytes     │
└─────────┴──────────┴───────────────┴─────────────┘
```

### 消息类型

| 类型 | 代码 | 方向 | 说明 |
|------|------|------|------|
| Auth | 0x01 | C→S | 认证请求 |
| JoinRoom | 0x02 | C→S | 加入房间 |
| Data | 0x03 | C↔S | IP 数据包 |
| Ping/Pong | 0x04/0x84 | C↔S | 心跳 |
| Leave | 0x05 | C→S | 离开房间 |
| Register | 0x06 | C→S | 注册用户 |
| CreateRoom | 0x07 | C→S | 创建房间 |
| ListRooms | 0x08 | C→S | 列出房间 |
| P2POffer | 0x10 | C↔S | P2P 打洞请求 |
| P2PAnswer | 0x11 | C↔S | P2P 打洞应答 |
| P2PPunchReq | 0x12 | C→S | 请求打洞协调 |
| P2PPunchResp | 0x13 | S→C | 打洞协调响应 |

## 端口说明

| 端口 | 协议 | 用途 |
|------|------|------|
| 9527 | TCP | 服务端信令 + 保底中继（可配置） |
| 随机 | UDP | 客户端 P2P 直连（自动分配） |

## 平台支持

| 平台 | 架构 | 虚拟网卡技术 |
|------|------|------------|
| Linux | amd64 / arm64 | TUN (/dev/net/tun) |
| Windows | amd64 | Wintun |
| macOS | amd64 / arm64 | utun |

## 常见问题

**Q: 需要管理员权限吗？**
A: 创建虚拟网卡需要管理员/root 权限。使用 `-notun` 参数可以在无权限的情况下测试连接。

**Q: P2P 打洞失败怎么办？**
A: 打洞失败时会自动回退到 TCP 中继模式。在 `peers` 命令输出中可以看到每个节点的连接模式（P2P直连/TCP中继）。对称型 NAT 可能无法打洞。

**Q: 必须使用 PostgreSQL 吗？**
A: 不是。不配置 `database` 字段时，服务端会使用 `users` 字段中的内置用户（兼容旧模式）。但此模式下不支持用户注册和动态房间创建。

**Q: 房间可以不设密码吗？**
A: 创建新房间时必须设置密码。旧配置文件中的无密码房间在兼容模式下仍然可用。

**Q: 支持多少个客户端？**
A: 每个 /24 子网支持最多 253 个客户端。可以创建多个房间来扩展。

**Q: 流量是否加密？**
A: TCP 信令和中继流量未加密（建议使用 nginx + TLS 反代）。P2P UDP 直连流量也未加密。未来版本将考虑内置 TLS/DTLS。

## 开发

```bash
# 运行测试
make test

# 本地编译（服务端 + CLI 客户端）
make build

# 编译 GUI 客户端
make gui

# 交叉编译所有平台
make build-all

# 清理构建产物
make clean
```

### GUI 编译依赖

GUI 客户端基于 [Fyne](https://fyne.io/) 框架，编译时需要 CGO 和以下系统库：

```bash
# Ubuntu/Debian
sudo apt-get install libgl-dev libx11-dev libxcursor-dev libxrandr-dev libxinerama-dev libxi-dev libxxf86vm-dev

# Fedora
sudo dnf install mesa-libGL-devel libX11-devel libXcursor-devel libXrandr-devel libXinerama-devel libXi-devel libXxf86vm-devel
```

## License

MIT
