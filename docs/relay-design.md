# NetFerry Relay — Go 重写设计文档

> 目标：用 Go 重写 sshuttle 的远端 server 和本地 tunnel client，彻底消除 Python 单线程
> 事件循环瓶颈，实现接近 SSH 链路上限的带宽，并引入版本化部署避免重复上传。

---

## 1. 动机与目标

| 问题 | 根因 | 改进目标 |
|------|------|----------|
| 带宽严重受限（通常 <50 Mbps） | Python 单线程 `select()` 事件循环 + 2 KB 写入上限 | Go goroutine 并发 + 64 KB+ 块 |
| `--no-latency-control` 改善速度但引发乱序 | 全局 stop-the-world PING/PONG 流控 | Per-channel 自然 backpressure |
| 每次连接都重新上传 Python 源码（~200 KB 压缩包） | 无版本缓存机制 | 版本化缓存，热启动跳过上传 |
| PyInstaller bundle 体积大、启动慢 | Python 运行时打包 | 单静态 Go binary |

---

## 2. 整体架构

```
┌─────────────────────────────────────────────────────────────────┐
│  macOS / Windows / Linux 本地机器                               │
│                                                                 │
│  ┌──────────────┐  Tauri IPC  ┌───────────────────────────────┐│
│  │  NetFerry UI │────────────▶│   netferry-desktop (Rust)     ││
│  │  (React)     │             │   sidecar.rs: spawn & monitor ││
│  └──────────────┘             └───────────────┬───────────────┘│
│                                               │ spawn subprocess
│                               ┌───────────────▼───────────────┐│
│                               │  netferry-tunnel (Go sidecar) ││
│                               │  ┌────────────────────────┐   ││
│  intercepted TCP/UDP ────────▶│  │ Transparent Proxy       │   ││
│  (pf / iptables redirect)     │  │ local :port listener    │   ││
│                               │  └──────────┬─────────────┘   ││
│                               │             │ mux protocol     ││
│                               │  ┌──────────▼─────────────┐   ││
│                               │  │  Mux Client             │   ││
│                               │  │  (per-channel goroutine)│   ││
│                               │  └──────────┬─────────────┘   ││
│                               │             │ SSH stdin/stdout ││
│                               └─────────────┼─────────────────┘│
└─────────────────────────────────────────────┼───────────────────┘
                                              │ SSH over TCP
┌─────────────────────────────────────────────┼───────────────────┐
│  Remote Host (Linux/macOS)                  │                   │
│                               ┌─────────────▼─────────────────┐│
│                               │  netferry-server (Go binary)  ││
│                               │  ~/.cache/netferry/server-V-A ││
│                               │  ┌────────────────────────┐   ││
│                               │  │  Mux Server             │   ││
│                               │  │  (one goroutine/channel)│   ││
│                               │  └──┬──────────────────────┘   ││
│                               │     │ per connection            ││
│                               │  ┌──▼────┐ ┌───────┐ ┌──────┐ ││
│                               │  │TCP dial│ │DNS UDP│ │Routes│ ││
│                               │  └───────┘ └───────┘ └──────┘ ││
│                               └───────────────────────────────┘│
└─────────────────────────────────────────────────────────────────┘
```

---

## 3. 项目目录结构

```
netferry/
├── netferry-relay/               ← 新建 Go module (module: github.com/hoveychen/netferry/relay)
│   ├── go.mod
│   ├── Makefile                  ← 交叉编译所有 server targets
│   ├── cmd/
│   │   ├── server/               ← 远端 binary（在 remote host 运行）
│   │   │   └── main.go
│   │   └── tunnel/               ← 本地 sidecar（替换 Python sshuttle bundle）
│   │       ├── main.go
│   │       └── embed.go          ← //go:embed binaries/server-*
│   ├── internal/
│   │   ├── mux/                  ← mux 协议 (server 和 client 共用)
│   │   │   ├── protocol.go       ← 常量、帧格式、encode/decode
│   │   │   ├── server.go         ← MuxServer: 处理 stdin/stdout
│   │   │   └── client.go         ← MuxClient: 发送请求、管理 channel
│   │   ├── deploy/               ← SSH 部署逻辑
│   │   │   ├── deploy.go         ← 主流程: detect → check → upload → exec
│   │   │   ├── arch.go           ← uname -ms 解析
│   │   │   └── version.go        ← 版本路径约定
│   │   ├── sshconn/              ← SSH 连接与认证
│   │   │   ├── client.go         ← 建立 *ssh.Client
│   │   │   ├── auth.go           ← agent + identity file
│   │   │   ├── config.go         ← ~/.ssh/config 解析
│   │   │   └── proxy.go          ← ProxyJump / ProxyCommand
│   │   ├── firewall/             ← 防火墙规则管理
│   │   │   ├── firewall.go       ← 接口定义
│   │   │   ├── pf.go             ← macOS pf (darwin only)
│   │   │   ├── nft.go            ← Linux nftables
│   │   │   └── ipt.go            ← Linux iptables fallback
│   │   ├── proxy/                ← 本地透明代理
│   │   │   ├── listener.go       ← TCP accept loop
│   │   │   ├── dstlookup.go      ← 查询原始目标 (pf / SO_ORIGINAL_DST)
│   │   │   └── dns.go            ← 本地 DNS 拦截与转发
│   │   └── routes/               ← 路由管理 (auto-nets)
│   │       └── routes.go         ← 本地路由表操作
│   └── binaries/                 ← 构建产物 (gitignore)
│       ├── server-linux-amd64
│       ├── server-linux-arm64
│       ├── server-linux-mipsle
│       ├── server-darwin-amd64
│       └── server-darwin-arm64
│
└── netferry-desktop/src-tauri/
    ├── binaries/
    │   └── netferry-tunnel-*     ← Go tunnel sidecar (替换 Python bundle)
    └── src/
        └── sidecar.rs            ← 更新 build_args() 和日志解析
```

---

## 4. 远端 Server (`cmd/server`)

### 4.1 启动与初始化

```
stdin (binary, via SSH) ──▶ MuxServer ──▶ per-channel goroutines
stdout (binary, via SSH) ◀── MuxServer ◀─ per-channel results
```

Server 在被部署到远端后，通过 SSH 的 stdin/stdout 通信。启动流程：

1. 向 **stdout** 写同步握手头 `\x00\x00SSHUTTLE0001`（12 bytes）
   → client 读取验证后才开始 mux 通信（握手失败意味着 binary 损坏）
2. 发送 `CMD_ROUTES`（如果 `--auto-nets`）——client 据此更新防火墙子网列表
3. 进入主 loop：从 stdin 读帧，分发到各 goroutine

### 4.2 并发模型（核心改进）

```go
// 每个 TCP_CONNECT 创建一对 goroutine，共享 mux writer channel
func handleTCPConnect(channel uint16, dstIP string, dstPort int, muxOut chan<- Frame) {
    conn, err := net.DialTCP("tcp", nil, &net.TCPAddr{IP: net.ParseIP(dstIP), Port: dstPort})
    if err != nil {
        muxOut <- Frame{channel, CMD_TCP_EOF, nil}
        return
    }

    // goroutine 1: remote TCP → mux out
    go func() {
        buf := make([]byte, 65536)  // 64KB，无 2048 限制
        for {
            n, err := conn.Read(buf)
            if n > 0 {
                muxOut <- Frame{channel, CMD_TCP_DATA, buf[:n]}
            }
            if err != nil { break }
        }
        muxOut <- Frame{channel, CMD_TCP_EOF, nil}
    }()

    // goroutine 2: mux in → remote TCP (由 channel inbox goroutine 驱动)
}
```

每个 channel 有独立的 `inbox chan Frame`（buffered，256KB 等价），写满即停，形成自然 backpressure。

### 4.3 功能清单

| 功能 | 实现方式 |
|------|----------|
| TCP 代理 | `net.Dial` + goroutine pair + `io.Copy` |
| DNS 代理 | UDP socket，与 Python 实现一致 |
| UDP 代理 | UDP socket per channel |
| 路由列表 (`--auto-nets`) | `ip route` 或 `netstat -rn` 解析 |
| Host watch | 暂时 stub（功能较少用到） |

### 4.4 流控设计（替换全局 PING/PONG）

**问题根因回顾**：Python 的 `LATENCY_BUFFER_SIZE=32768` 触发全局暂停；Go 不需要这个机制。

**Go 方案**：
- `muxOut chan Frame` 使用有界 buffer（如 1000 帧 × 平均 8KB ≈ 8MB max in-flight）
- 当 `muxOut` 写满，生产者 goroutine 自然阻塞
- 阻塞传导到 `conn.Read`，TCP 层自动收紧发送方窗口
- **无需任何显式 PING/PONG 流控**

保留 `CMD_PING/CMD_PONG` 仅用于 keepalive 检测连接是否存活。

---

## 5. 本地 Tunnel Client (`cmd/tunnel`)

### 5.1 CLI 接口

新 CLI，干净设计（不再 wrap sshuttle 参数）。`sidecar.rs` 的 `build_args()` 同步更新。

```
netferry-tunnel [flags] SUBNET [SUBNET...]

必填:
  --remote user@host[:port]    远端 SSH 目标

可选:
  --identity /path/to/key      SSH 私钥（空则用 SSH agent）
  --exclude SUBNET             排除子网（可多次）
  --auto-nets                  自动从远端路由表添加子网
  --dns                        拦截 DNS 请求并转发到远端
  --dns-target IP[@PORT]       指定远端 DNS 服务器（默认系统 DNS）
  --method auto|pf|nft|ipt     防火墙方法（默认 auto）
  --no-ipv6                    不处理 IPv6
  --verbose / -v               增加日志详细程度
  --extra-ssh-opts "..."       额外 SSH 参数（如 -o StrictHostKeyChecking=no）
```

### 5.2 主流程

```
main()
  ├── 解析 CLI
  ├── parseSSHConfig(host)               # 读 ~/.ssh/config，处理 ProxyJump/ProxyCommand
  ├── dialSSH(host, auth)                # 建立 *ssh.Client
  ├── sshRemoteIP := client.RemoteAddr() # 记录 SSH 服务器 IP，用于自动排除（见 5.4）
  ├── deploy.EnsureServer(client)        # 版本检查 + 条件上传 + 获取远端路径
  ├── session.Start(remotePath)          # 启动远端 server
  ├── readSyncHeader(session.Stdout)     # 读取并校验 "\x00\x00SSHUTTLE0001"
  │                                      # 失败则表示远端 binary 损坏，报错退出
  ├── muxClient.Start(session)           # 启动 mux 协议（读 CMD_ROUTES 若 auto-nets）
  ├── pickFreePort()                     # 随机本地代理端口
  ├── pickFreeDNSPort()                  # 随机本地 DNS 端口（若 --dns）
  ├── effectiveSubnets := subnets        # 合并 auto-nets 返回的远端路由到子网列表
  ├── firewall.Setup(effectiveSubnets, port, dnsPort, excludes+sshRemoteIP)
  ├── defer firewall.Restore()           # 保证退出时清理（信号和正常退出均触发）
  ├── fmt.Fprintln(os.Stderr, "c : Connected to server.")  # ← Tauri 监测此行
  ├── dns.Listen(dnsPort, muxClient)     # DNS 代理（若 --dns）
  └── proxy.Listen(port, muxClient)      # TCP 代理，阻塞直到断开
```

### 5.3 日志格式（保持 Tauri 兼容）

`sidecar.rs` 当前依赖以下日志行触发状态变更，必须保持兼容：

```
# 触发 state → "connected"
stderr: "c : Connected to server."

# 触发 ConnectionEvent（连接明细面板）
stderr: "c : Accept TCP: 192.168.1.5:54321 -> 10.0.0.1:443."

# 触发 TunnelError
stderr 含 "fatal:" / "connection refused" / "permission denied" 等
```

Go 实现中用同格式输出，不依赖 Python 的 `helpers.logprefix`。

### 5.3 SSH 服务器 IP 自动排除

这是一个**容易漏掉的关键细节**：当 sshuttle 拦截所有 TCP 流量（如 subnet=0.0.0.0/0）时，
SSH 连接本身也会被 pf/iptables 重定向到本地代理，导致死循环。

**解决方式**：在构建防火墙规则时，自动将 SSH 服务器 IP 加入排除列表：

```go
// sshRemoteIP 从 *ssh.Client.RemoteAddr() 获取
// 若用了 ProxyJump，取最终 hop 的 IP
excludes = append(excludes, sshRemoteIP+"/32")
```

同理，若用了 ProxyJump（A → B → C），需要排除 A→B 和 B→C 两段的 IP，
但实践中只排除最终目标 C（B 的 IP 是内网，通常不在 subnet 内）。

### 5.4 DNS 服务器自动检测

当 `--dns` 启用时，需要知道拦截哪些 DNS 服务器 IP：

```go
// macOS
nslist := parseScutilDNS()   // scutil --dns | grep "nameserver\[" | awk '{print $3}'
// Linux
nslist := parseResolvConf()  // /etc/resolv.conf nameserver 行
// 回退
nslist := []string{"8.8.8.8", "8.8.4.4"}
```

DNS 服务器 IP 列表用于构建 pf `table <dns_servers>` 或 nftables set，
仅重定向到这些服务器的 DNS 请求。

### 5.5 SSH 私钥加密处理

当 identity file 有密码保护且无 SSH agent 时：

```go
_, err := ssh.ParsePrivateKey(keyBytes)
if err != nil {
    // 尝试解析为加密 key
    if _, ok := err.(*ssh.PassphraseMissingError); ok {
        // 从 SSH_ASKPASS 或 stderr 提示输入密码
        // 若无交互终端且无 SSH_ASKPASS：输出 "fatal: key requires passphrase,
        //   set SSH_ASKPASS or use ssh-agent" 并退出
    }
}
```

macOS helper 场景（无 TTY）：必须使用 SSH agent（通过 Keychain）或无密码 key。
用户在 Profile 配置页可看到"使用 SSH agent"的提示。

---

## 6. SSH 部署协议（版本化）

### 6.1 完整流程

```
Step ①  arch := session.Run("uname -ms")
         → "Linux x86_64" → "linux-amd64"

Step ②  remotePath := "~/.cache/netferry/server-VERSION-ARCH"
         exists := session.Run("test -x " + remotePath) == nil

Step ③  if !exists:
             binary := embed.ReadFile("binaries/server-" + arch)
             uploadBinary(client, binary, remotePath)
             cleanOldVersions(client, arch)   // 删除同 arch 下其他版本

Step ④  session.Start("exec " + remotePath)
         // 此 session 的 stdin/stdout 即为 mux 协议通道
```

所有 Step ①②③④ 均在同一 `*ssh.Client` 的不同 session 上运行，**共用一条 TCP 连接**，无需 ControlMaster。

### 6.2 版本字符串

```go
// 在 cmd/tunnel/main.go 和 cmd/server/main.go 中
var Version = "dev"  // 由构建时 -ldflags "-X main.Version=vX.Y.Z" 注入
```

构建时版本源：
```makefile
VERSION := $(shell git describe --tags --always --dirty)
```

远端缓存路径示例：`~/.cache/netferry/server-v0.2.0-linux-amd64`

### 6.3 旧版本清理

```bash
# 上传新版本后执行（best-effort，失败不影响连接）
ls ~/.cache/netferry/server-*-ARCH 2>/dev/null | grep -v VERSION | xargs rm -f
```

### 6.4 嵌入策略

```go
// cmd/tunnel/embed.go
package main

import "embed"

//go:embed binaries/server-linux-amd64
//go:embed binaries/server-linux-arm64
//go:embed binaries/server-linux-mipsle
//go:embed binaries/server-darwin-amd64
//go:embed binaries/server-darwin-arm64
var serverBinaries embed.FS
```

`binaries/` 在 `.gitignore` 中，由 `make build-servers` 生成后再 `make build-tunnel` 嵌入。

### 6.5 远端目录可用性

优先 `$HOME/.cache/netferry/`（跨重启持久化），若 `$HOME` 不可写则回退 `/tmp/.netferry/`：

```bash
# 上传命令
mkdir -p ~/.cache/netferry 2>/dev/null || mkdir -p /tmp/.netferry
cat > PATH && chmod +x PATH
```

### 6.6 并发安全（两个进程同时连同一远端）

使用 `ln` 原子替换：先写到 `path.tmp`，再 `mv path.tmp path`。`test -x path` + `exec path` 天然幂等。

---

## 7. SSH 认证

### 7.1 认证链（按优先级）

```
1. SSH Agent（SSH_AUTH_SOCK 环境变量）    ← 主路径（macOS keychain / ssh-agent）
2. --identity 指定的私钥文件              ← 用户显式配置
3. ~/.ssh/id_ed25519, ~/.ssh/id_rsa 等   ← 常规默认路径
```

使用 `golang.org/x/crypto/ssh/agent` 连接 Unix socket，不依赖系统 `ssh` 命令。

### 7.2 ~/.ssh/config 解析

使用 `github.com/kevinburke/ssh_config` 解析，覆盖：
- `HostName`, `Port`, `User`（DNS/port/user 解析）
- `IdentityFile`（key 路径）
- `ProxyJump`（原生 Go SSH 链式 Dial 实现）
- `ProxyCommand`（shell out，见 7.4）
- `StrictHostKeyChecking`, `UserKnownHostsFile`（known_hosts 验证）

### 7.3 Known Hosts 验证

```go
knownHostsCallback, err := knownhosts.New(expandHome("~/.ssh/known_hosts"))
// 严格验证，不接受未知 host（生产安全要求）
// 通过 --extra-ssh-opts "-o StrictHostKeyChecking=no" 可绕过（用户显式）
```

### 7.4 ProxyCommand 处理

```go
// ProxyCommand 需要 shell out，因为 Go SSH 库不内置支持
func dialViaProxyCommand(cmd string, dst string) (net.Conn, error) {
    // 替换 %h %p %r 占位符
    // 运行命令，把其 stdin/stdout 包装成 net.Conn
    c := exec.Command("sh", "-c", cmd)
    // 返回 ReadWriteCloser → 包装成 net.Conn 供 ssh.Dial 使用
}
```

### 7.5 macOS Helper 环境变量透传

`helper_ipc.rs` 已经将 `SSH_AUTH_SOCK`, `HOME`, `USER` 注入到 tunnel 进程环境中，
Go 代码直接读取即可，无需额外处理。

---

## 8. Mux 协议

### 8.1 Wire 格式（保持与现有协议兼容）

```
┌───┬───┬────────┬────────┬──────────────┐
│'S'│'S'│channel │  cmd   │   datalen    │ data...
│ 1 │ 1 │ 2B BE  │ 2B BE  │   2B BE      │
└───┴───┴────────┴────────┴──────────────┘
  magic            HDR_LEN = 8 bytes total
```

复用现有 cmd 常量，确保协议层面向下兼容（未来若需要，可通过 PING payload 协商升级）。

### 8.2 数据块大小

| | Python | Go |
|--|--|--|
| 读取块 | `min(1MB, LATENCY_BUFFER_SIZE)` | 65536 字节 |
| 写入块 | **2048 字节上限** → 核心瓶颈 | **无上限**，`io.Copy` 64KB buffer |
| 每秒需要循环次数（100Mbps） | ~6100 | ~190 |

### 8.3 Go Mux 实现关键路径

```
stdin reader goroutine
  └─ decode frame → dispatch to channel.inbox chan

channel.inbox goroutine
  └─ write to conn (TCP/DNS/UDP)

conn reader goroutine
  └─ read from conn → encode frame → muxOut chan

muxOut writer goroutine
  └─ encode frame → write to stdout
```

所有 goroutine 通过 channel 通信，不共享 select 循环。

---

## 9. 防火墙集成

### 9.1 macOS pf（主平台）

**设置步骤**（需 root）：

```bash
# 1. 启用 pf，获取 token（用于退出时精准关闭）
pfctl -E   # → 输出 "Token : XXXXXXXXXX"

# 2. 创建 anchor 规则（通过 stdin pipe 避免临时文件）
pfctl -a "netferry-PORT" -f - <<EOF
rdr pass on lo0 inet proto tcp from ! 127.0.0.1 to SUBNET \
    -> 127.0.0.1 port PORT
pass out route-to lo0 inet proto tcp to SUBNET keep state
# DNS 重定向（若 --dns）
rdr pass on lo0 inet proto udp to <dns_servers> port 53 \
    -> 127.0.0.1 port DNSPORT
pass out route-to lo0 inet proto udp to <dns_servers> port 53 keep state
EOF

# 3. 处理 lo0 skip（macOS 默认 skip lo）
pfctl -f - <<< "pass on lo"
```

**原始目标 IP 查询**（DIOCNATLOOK ioctl）：

被 pf 重定向到本地端口的连接，需要查询原始目标 IP/Port 才能知道"要代理到哪里"。

```go
// 在 Go 中通过 syscall 调用 DIOCNATLOOK ioctl
// 需要构造与 Darwin pfioc_natlook 结构体内存布局一致的 []byte
// DIOCNATLOOK = 0xC0504417 (Darwin)
fd := openPFDev()  // os.OpenFile("/dev/pf", os.O_RDWR, 0)
syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), DIOCNATLOOK, uintptr(unsafe.Pointer(&natlook)))
```

这是实现中最平台相关的部分，需要精确匹配 Darwin 内核的结构体布局（offset 见 pf.py Darwin class）。

**退出清理**：
```bash
pfctl -a "netferry-PORT" -F all   # 删除 anchor 规则
pfctl -X TOKEN                    # 释放 pf 启用 token（若无其他用户，自动 disable pf）
```

### 9.2 Linux nftables（优先）

```bash
# 创建 table + chain
nft add table ip netferry
nft add chain ip netferry prerouting '{ type nat hook prerouting priority -100; }'
nft add chain ip netferry output '{ type nat hook output priority -100; }'

# 添加重定向规则（每个子网一条）
nft add rule ip netferry prerouting ip daddr SUBNET tcp dport != PORT redirect to :PORT
nft add rule ip netferry output ip daddr SUBNET tcp dport != PORT redirect to :PORT

# 清理
nft delete table ip netferry
```

### 9.3 Linux iptables（回退）

```bash
iptables -t nat -N NETFERRY
iptables -t nat -A OUTPUT -m addrtype --dst-type LOCAL -j RETURN
iptables -t nat -A OUTPUT -d SUBNET -p tcp -j REDIRECT --to-ports PORT
iptables -t nat -A PREROUTING -d SUBNET -p tcp -j REDIRECT --to-ports PORT

# 清理
iptables -t nat -F NETFERRY
iptables -t nat -X NETFERRY
```

### 9.4 Linux 原始目标 IP 查询

Linux 使用 `getsockopt(SO_ORIGINAL_DST)` 或 `IP6T_SO_ORIGINAL_DST`（IPv6）：

```go
addr, err := syscall.GetsockoptIPv6Mreq(int(conn.Fd()), syscall.IPPROTO_IP, SO_ORIGINAL_DST)
```

### 9.5 信号处理与异常退出

```go
sigCh := make(chan os.Signal, 1)
signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
defer firewall.Restore()  // 通过 defer 保证清理
// macOS helper kill 时会发 SIGKILL，defer 不执行
// → 所以 pf anchor 用固定名 "netferry-PORT"，下次启动时先清理同名 anchor
```

**启动时清理残留规则**（处理 SIGKILL 场景）：
```go
func init() {
    // 尝试清理所有以 "netferry-" 开头的 pf anchor
    cleanStalePFAnchors()
}
```

---

## 10. 本地透明代理

### 10.1 TCP 代理流程

```
被 pf/iptables 重定向的 TCP 连接
  → proxy.Listen() 接受
  → dstLookup(conn) → 原始目标 IP:Port（DIOCNATLOOK 或 SO_ORIGINAL_DST）
  → muxClient.OpenChannel(family, dstIP, dstPort)
  → goroutine: io.Copy(conn → muxChannel)
  → goroutine: io.Copy(muxChannel → conn)
```

### 10.2 DNS 代理流程

```
UDP :DNSPORT 监听
  → 接收 DNS query
  → muxClient.DNSRequest(data)
  → 等待 CMD_DNS_RESPONSE
  → 返回给请求方
```

---

## 11. 构建与打包

### 11.1 交叉编译目标

```makefile
# netferry-relay/Makefile

VERSION := $(shell git describe --tags --always --dirty)
LDFLAGS := -ldflags "-X main.Version=$(VERSION) -s -w"

TARGETS = \
    linux/amd64    \
    linux/arm64    \
    linux/mipsle   \
    darwin/amd64   \
    darwin/arm64

build-servers:
    $(foreach T,$(TARGETS), \
        GOOS=$(word 1,$(subst /, ,$(T))) \
        GOARCH=$(word 2,$(subst /, ,$(T))) \
        CGO_ENABLED=0 \
        go build $(LDFLAGS) -o binaries/server-$(subst /,-,$(T)) ./cmd/server; \
    )

build-tunnel: build-servers
    GOOS=$(HOST_GOOS) GOARCH=$(HOST_GOARCH) CGO_ENABLED=0 \
    go build $(LDFLAGS) -o dist/netferry-tunnel ./cmd/tunnel

# 针对 Tauri sidecar 命名约定的打包目标
package-sidecar:
    cp dist/netferry-tunnel \
       ../netferry-desktop/src-tauri/binaries/netferry-tunnel-$(TAURI_TARGET)
```

### 11.2 更新 `build_sidecar.py`

现有 `build_sidecar.py` 使用 PyInstaller 构建 Python bundle。新版本改为：

```python
# 调用 Go 构建
def build_go_tunnel(target: str, output_name: str) -> Path:
    goos, goarch = go_triple_from_rust_triple(target)  # e.g. aarch64-apple-darwin → darwin/arm64
    subprocess.run([
        "go", "build",
        "-ldflags", f"-X main.Version={version} -s -w",
        "-o", str(build_dir / "netferry-tunnel"),
        "./cmd/tunnel"
    ], cwd=relay_dir, env={**os.environ, "GOOS": goos, "GOARCH": goarch, "CGO_ENABLED": "0"}, check=True)
```

### 11.3 Tauri `sidecar.rs` 变更

#### `resolve_sshuttle_exe()` → `resolve_tunnel_exe()`
文件名不变（`netferry-tunnel`），查找逻辑不变。

#### `build_args()` 更新

```rust
fn build_args(profile: &Profile) -> Vec<String> {
    let mut args = vec!["-v".to_string()];
    args.push("--remote".to_string());
    args.push(profile.remote.clone());

    for subnet in &profile.subnets { args.push(subnet.clone()); }
    if profile.auto_nets { args.push("--auto-nets".to_string()); }
    for subnet in &profile.exclude_subnets {
        args.push("--exclude".to_string());
        args.push(subnet.clone());
    }
    if !profile.identity_file.trim().is_empty() {
        args.push("--identity".to_string());
        args.push(profile.identity_file.clone());
    }
    match profile.dns {
        DnsMode::Off => {}
        _ => args.push("--dns".to_string()),
    }
    if let Some(ns) = &profile.dns_target {
        if !ns.is_empty() {
            args.push("--dns-target".to_string());
            args.push(ns.clone());
        }
    }
    if profile.disable_ipv6 { args.push("--no-ipv6".to_string()); }
    if !profile.method.is_empty() && profile.method != "auto" {
        args.push("--method".to_string());
        args.push(profile.method.clone());
    }
    if let Some(extra) = &profile.extra_ssh_options {
        if !extra.trim().is_empty() {
            args.push("--extra-ssh-opts".to_string());
            args.push(extra.clone());
        }
    }
    // latency_buffer_size 在 Go 实现中不需要（无全局流控）
    args
}
```

注意：`--ssh-cmd` 参数（及其中的 `-i` 和 `-F` 逻辑）被拆解为独立的 `--identity` 和 SSH config 自动读取，**不再需要 ssh wrapper 脚本**。`helper_ipc.rs` 中构造 ssh wrapper 的代码可以删除。

#### 日志解析不变

```rust
// 以下两行解析逻辑不需要改变
if !tunnel_connected && log_line.contains("Connected to server.") { ... }
if log_line.contains("Accept TCP:") { ... }
```

---

## 12. `models.rs` 字段兼容性

| 字段 | 现状 | 处理 |
|------|------|------|
| `remote_python` | 指定远端 Python 解释器路径 | **废弃**，Go server 不需要，忽略此字段（保留 serde 兼容读取） |
| `latency_buffer_size` | 控制 Python 流控缓冲区 | **废弃**，Go 用 Go channel buffer，此参数静默忽略 |
| `method` | pf / nat / nft / auto | **保留**，传递给 Go tunnel |
| `extra_ssh_options` | 额外 ssh 参数 | **保留**，作为 `--extra-ssh-opts` 传递 |

UI 侧可以在后续迭代中隐藏废弃字段，当前版本只需静默忽略即可。

---

## 13. 依赖清单（Go）

```
golang.org/x/crypto/ssh              # SSH 客户端协议
golang.org/x/crypto/ssh/agent        # SSH agent 认证
github.com/kevinburke/ssh_config     # ~/.ssh/config 解析
github.com/spf13/cobra (可选)         # CLI 参数解析，或用标准 flag 包
```

server binary 目标：**零外部依赖**（只用标准库），确保在 minimal Linux 环境（容器、NAS、路由器）可运行。

tunnel binary 可以有依赖，但保持 CGO_ENABLED=0 静态链接。

---

## 14. 迁移策略

### Phase 1（本 PR）：替换 server + tunnel

- Go server + Go tunnel 全量替换
- 保持 Tauri sidecar 接口完全兼容
- `build_sidecar.py` 改为调用 Go 构建
- `sidecar.rs` 更新 `build_args()`

### Phase 2（后续）：清理遗留代码

- 从 `models.rs` 移除 `remote_python`, `latency_buffer_size` 字段
- 从 `helper_ipc.rs` 移除 ssh wrapper 脚本生成代码
- 从 `sidecar.rs` 移除 `SudoHelper`（macOS helper 已不需要 sudo）
- 移除 `third_party/sshuttle` submodule
- 移除 `netferry/` Python 包和 PyInstaller 相关构建工具

---

## 15. V1 范围外（暂不实现）

| 功能 | 原因 |
|------|------|
| Windows 支持 | WinDivert 复杂；现有 Python 版本仍可用 |
| UDP 透明代理（tproxy） | 需要 TPROXY iptables target，复杂且少用 |
| Host watch（自动 /etc/hosts） | 功能冷门，可后续添加 |
| 自动断线重连 | 当前 Python 版也无此功能，Tauri 侧处理重连 |
| IPv6 完整支持 | 基础已预留，但端到端测试留到 Phase 2 |
| 协议版本协商（NF v2） | 当前只有一对 client/server，无需协商 |

---

## 16. 审查清单

在实现之前，以下每一项都需要确认无遗漏：

- [x] **macOS pf DIOCNATLOOK** — Darwin 结构体偏移量必须精确匹配内核（`RULE_ACTION_OFFSET=3068`，`pfioc_rule` = 3104 bytes）；用 Go `unsafe` 实现，需单独写测试验证 offset
- [x] **pfctl -E token 管理** — Darwin 用 token 而非直接 disable，多客户端并发时需正确 ref-count
- [x] **SSH agent 转发** — macOS helper 运行 tunnel 时注入 `SSH_AUTH_SOCK`；Go 代码通过环境变量自动获取，无需额外处理
- [x] **SSH known_hosts 严格验证** — 默认 strict，不自动接受陌生 host；明确文档化如何通过 `--extra-ssh-opts` 绕过
- [x] **ProxyCommand shell out** — 不破坏 Go 纯静态特性：仅 exec sh，不引入 cgo
- [x] **远端 SIGKILL 残留** — 启动时主动清理 pf anchor（`pfctl -a "netferry-*" -F all`），防止上次 SIGKILL 后规则残留导致网络故障
- [x] **并发上传同一远端** — 原子 mv 操作防竞争
- [x] **远端 ~/.cache 不存在** — mkdir -p + fallback to /tmp
- [x] **embed binaries 体积** — 5 个平台 server binary，每个约 3-5MB，tunnel binary 合计 ~20MB，可接受
- [x] **`remote_python` 字段废弃兼容** — serde 保留字段，Go 侧忽略，UI 后续迭代隐藏
- [x] **日志格式严格匹配** — "Connected to server." 和 "Accept TCP: src -> dst." 格式需与 sidecar.rs 解析完全一致
- [x] **auto-nets 不是路由表修改** — client 收到 CMD_ROUTES 后，将远端路由合并到防火墙子网列表（更新 pf/iptables 规则），**不是**调用 `ip route add`；路由表不需要修改，因为 pf 已经在 L4 层拦截
- [x] **go:embed 构建时序** — `make build-servers` 必须在 `make build-tunnel` 之前完成；CI 中需显式依赖
- [x] **macOS sandbox / entitlements** — tunnel binary 需要 `com.apple.security.network.client` 和 `com.apple.security.network.server` entitlements；现有 sidecar-entitlements.plist 需审查是否已包含
- [x] **Windows 静默降级** — build_sidecar.py 检测到 Windows target 时跳过 Go 构建，继续使用 Python bundle；防止 CI 中断
- [x] **SSH 服务器 IP 自动排除** — 必须将 SSH 连接目标 IP 加入 firewall exclude 列表，否则 0.0.0.0/0 模式会重定向 SSH 连接自身造成死循环（见 5.3）
- [x] **DNS 服务器检测** — `--dns` 启用时需从 scutil/resolv.conf 获取当前 DNS 服务器 IP 列表，用于构建 pf table 或 nft set（见 5.4）
- [x] **同步握手头读取** — client 在 `deploy.EnsureServer` 后必须读取并校验 `\x00\x00SSHUTTLE0001`，才能认为 server 正常启动；"Connected to server." 日志必须在此之后输出
- [x] **auto-nets 时序** — CMD_ROUTES 必须在 firewall.Setup 之前完成（或动态更新防火墙规则），因为规则必须包含远端路由子网
- [x] **SSH key 密码保护** — 无 agent 且 key 有密码时，在 macOS helper（无 TTY）场景下无法交互式输入；应给出明确错误信息指引用户使用 ssh-agent 或 Keychain
