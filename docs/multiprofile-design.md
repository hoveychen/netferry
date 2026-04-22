# NetFerry Multi-Profile — ProfileGroup 设计文档

> 目标：允许用户同时激活多条 SSH tunnel，并通过现有的 destination 路由机制
> 选择每条 destination 走哪一条 tunnel。引入 **ProfileGroup** 作为新的顶层实体，
> 收纳一组 profile + 一套 destination rules，实现"场景化"切换。

---

## 1. 动机与目标

| 问题 | 现状根因 | 改进目标 |
|------|----------|----------|
| 同一时刻只能连一个 profile | `sidecar.rs:944` 的 `child: Mutex<Option<Child>>` 单实例锁；Go 侧 `cmd/tunnel/main.go` 整进程服务单 profile | 支持多 profile 同时在线 |
| Destination 路由只有 `tunnel / direct / blocked` 三值，无法选 tunnel | `internal/stats/stats.go:182-183` `routeModes` 以 host 为 key，value 不带 profile 归属 | 路由决策可精确到具体 profile |
| 路由规则是全局单例，多场景切换困难 | 规则持久化在 `src-tauri` 全局 routes 文件中 | 规则下沉到 ProfileGroup，切 group 即切场景 |
| "默认 profile" 没有明确归属 | 无此概念，单 profile 自然是默认 | `group.children[0]` 即该 group 的默认 profile |

非目标（本轮不做）：

- 多个 ProfileGroup 同时激活。本设计硬约束单活跃 group。
- 跨 ProfileGroup 的规则共享 / 继承。
- 移动端（netferry-mobile）多 profile。移动端本轮保持单 profile。

---

## 2. 整体架构

```
┌─────────────────────────────────────────────────────────────────┐
│  NetFerry Desktop (Tauri + React)                               │
│                                                                 │
│  GroupList → GroupEditorPage → activate_group(groupId)         │
│       │                                                         │
│       ▼                                                         │
│  ConnectionPage (多行: 每个 child profile 一行 stats)          │
│  DestinationsPage (下拉: Default / Tunnel→children / Direct / Blocked) │
└──────────────────────────┬──────────────────────────────────────┘
                           │ Tauri command
┌──────────────────────────▼──────────────────────────────────────┐
│  netferry-desktop/src-tauri (Rust)                              │
│                                                                 │
│  AppState.active_group_id  +  groups.rs CRUD                   │
│  sidecar::spawn_tunnel(group.json)                              │
└──────────────────────────┬──────────────────────────────────────┘
                           │ spawn subprocess
┌──────────────────────────▼──────────────────────────────────────┐
│  netferry-tunnel (Go, --group <path>)                           │
│                                                                 │
│  ┌─── firewall.Setup(unionSubnets) ──────────────────────┐     │
│  │  pf / nft / tproxy / windivert 一次劫持                │     │
│  └──────────────────┬─────────────────────────────────────┘     │
│                     │ redirect                                  │
│  ┌──────────────────▼──────────────────────────────────┐       │
│  │  proxy/listener.go (路由分发核心)                    │       │
│  │  peek host → counters.LookupRouteMode → switch       │       │
│  │    direct/blocked : 直连/拒绝                         │       │
│  │    tunnel{pid}    : sessionMgr.pools[pid]             │       │
│  │    default        : sessionMgr.pools[children[0]]     │       │
│  └──────────────────┬──────────────────────────────────┘       │
│                     │                                           │
│  ┌──────────────────▼──────────────────────────────────┐       │
│  │  sessionManager.pools: map[profileId]*mux.Pool       │       │
│  │    child_0: SSH → server A                            │       │
│  │    child_1: SSH → server B                            │       │
│  │    child_n: ...                                       │       │
│  └───────────────────────────────────────────────────────┘       │
└─────────────────────────────────────────────────────────────────┘
```

关键设计约束：

- **firewall 只有一个所有者。** pf/nft/tproxy/windivert 都是系统级 subnet redirect，无法在内核层让两个进程同时劫持 `0.0.0.0/0`。多 profile 的选择必须发生在被劫持之后的用户态。
- **路由决策以 destination 为粒度。** `routeModes[host]` 直接返回完整的路由决策（含目标 profileId），listener 据此分发。
- **session 生命周期独立。** 某个 child profile 的 SSH 连接断开，不影响其他 child 的流量继续转发。

---

## 3. 数据模型

### 3.1 TypeScript（前端 + 序列化到磁盘）

```ts
// 新增顶层
interface ProfileGroup {
  id: string;
  name: string;
  children: Profile[];                   // 有序; children[0] = default profile
  rules: Record<string, RouteMode>;      // key = destination host/IP
  priorities: Record<string, number>;    // 1-5
}

// 路由决策升级为 tagged union
type RouteMode =
  | { kind: "tunnel"; profileId: string }
  | { kind: "default" }     // 走 group.children[0]
  | { kind: "direct" }
  | { kind: "blocked" };

// GlobalSettings
interface GlobalSettings {
  activeGroupId: string | null;          // 取代旧的 autoConnectProfileId 语义
  autoActivateGroupId: string | null;    // 可选：启动时自动激活
  trayDisplayMode: TrayDisplayMode;
}

// DestinationSnapshot 扩展
interface DestinationSnapshot {
  // ... 原有字段不变 ...
  assignedProfileId?: string;            // 按规则应该走的 profile
  activeProfileId?: string;              // 实际走了哪个（掉线回退时会不同）
}
```

### 3.2 Go 侧

```go
// internal/stats/stats.go
type RouteMode struct {
    Kind      string `json:"kind"`      // tunnel | default | direct | blocked
    ProfileID string `json:"profileId,omitempty"`
}

type Counters struct {
    // ...
    routeModes map[string]RouteMode    // key = destKey(dstAddr, host)
    priorities map[string]int
    // 新增：
    activeGroup *ActiveGroup           // nil 表示尚未推送 group 信息
}

type ActiveGroup struct {
    GroupID          string
    DefaultProfileID string            // = children[0].id
    KnownProfileIDs  []string          // 当前 session manager 里确实开着 pool 的 id
}

// cmd/tunnel/main.go 新增
type sessionManager struct {
    pools map[string]*mux.MuxClient    // key = profileId
    mu    sync.RWMutex
}
```

### 3.3 文件布局（Tauri 持久化）

```
~/Library/Application Support/NetFerry/       # macOS 路径示例
  groups/
    <groupId>.json               # 自含: children[] + rules + priorities
  settings.json                  # 新增 activeGroupId
  profiles/                      # 【已弃用】P1 迁移后此目录清空并可删除
  priorities.json                # 【已弃用】P1 迁移进默认 group 的 rules
  routes.json                    # 【已弃用】同上
```

**子 profile 采用内嵌形态**：一个 `group.json` 自含 children，无外部引用。
换取的好处：导入导出整组配置只需搬一个文件；单元测试 fixture 简单。
代价：同一个 SSH server 配置在多个 group 中会重复存放一份，用户修改密钥
需要去所有 group 里改。考虑到 group 数量通常 ≤ 3，可接受。

---

## 4. 运行时 — Go 侧

### 4.1 入参 (`cmd/tunnel/main.go`)

```bash
# 新形态（桌面 & 多 profile CLI 场景）
netferry-tunnel --group /path/to/group.json

# 兼容旧用法（CLI 单 profile；内部自动包成单 child 的临时 group）
netferry-tunnel --remote user@host --identity ~/.ssh/id_rsa 0.0.0.0/0
```

### 4.2 sessionManager 建立

伪代码位置：`cmd/tunnel/main.go:338-352`（原单 pool 构建处）。

```go
sm := newSessionManager()
for _, child := range group.Children {
    pool, err := buildMuxPool(child)    // 封装原 main.go 里的 pool 构建
    if err != nil {
        log.Printf("child %s failed to connect: %v", child.ID, err)
        continue                          // 允许部分 child 失败不阻断启动
    }
    sm.Register(child.ID, pool)
}

if sm.Len() == 0 {
    return fmt.Errorf("no child profile connected successfully")
}
```

### 4.3 路由分发（核心改动）

位置：`internal/proxy/listener.go:135-151`。

```go
mode := counters.LookupRouteMode(dstAddr, host)
switch mode.Kind {
case "direct":
    dialDirect(conn, dstAddr)
case "blocked":
    conn.Close()
case "tunnel":
    pool := sm.Get(mode.ProfileID)
    if pool == nil {
        // child 掉线 fallback: reject with retriable error
        rejectRetriable(conn)
        counters.NoteFallback(dstAddr, host, mode.ProfileID, "pool_offline")
        return
    }
    forwardToPool(conn, dstAddr, pool)
case "default":
    fallthrough
default:
    pool := sm.Get(activeGroup.DefaultProfileID)
    if pool == nil {
        rejectRetriable(conn)
        return
    }
    forwardToPool(conn, dstAddr, pool)
}
```

### 4.4 firewall.Setup 入参

位置：`internal/firewall/*.go` 各 backend 的 `Setup`。

调用方把 **所有 active child 的 subnet 求并集** 后再传入；不改 backend 内部实现。
`autoExcludeLan` 等选项取并集（任一 child 开启即生效）。IPv6/UDP 相关开关按
6.2 节的规则提升到 group 层。

### 4.5 HTTP 控制面扩展

`internal/stats/stats.go` 现有：

- `GET /snapshot` — 返回 DestinationSnapshot[]
- `POST /routes` — 旧扁平 routes
- `POST /priorities`

新增与修改：

- `POST /group` — Rust 推送完整 ActiveGroup snapshot（含 default profileId 与已知 pool 列表）
- `POST /group/rules` — 推送 rules 的增量或全量
- `POST /group/priorities` — 同上
- `/routes` 保留兼容路径，内部翻译成新 schema

---

## 5. 运行时 — Rust 侧

### 5.1 AppState 变更 (`src-tauri/src/sidecar.rs:161-186`)

```rust
pub struct AppState {
    // 移除：
    // pub last_connected_profile: Mutex<Option<Profile>>,

    // 新增：
    pub active_group_id: Mutex<Option<String>>,

    // 保留：
    pub child: Mutex<Option<Child>>,
    pub helper_stream: Mutex<Option<UnixStream>>,
    pub stats_port: Mutex<Option<u16>>,
    // ...
}
```

单实例 child 锁语义保留：任意时刻只有一个 tunnel 进程在跑，切换活跃 group
时先 stop 再 start。未来 P5 做热增删时再演进这个锁。

### 5.2 Tauri commands (`src-tauri/src/commands.rs`)

| 旧命令 | 新命令 | 语义 |
|--------|--------|------|
| `connect(profile)` | `activate_group(groupId)` | 把 group.json 落到临时路径，spawn tunnel |
| `disconnect()` | `deactivate_group()` | 无变化 |
| `get_routes()` | `get_group_rules(groupId)` | 读 `groups/<id>.json` 里的 rules |
| `save_routes(routes)` | `save_group_rules(groupId, rules)` | 同上 |
| `get_priorities()` | `get_group_priorities(groupId)` | 同上 |
| — | `list_groups()` / `create_group` / `update_group` / `delete_group` | 新 CRUD |
| — | `reorder_children(groupId, ids[])` | 专用 API：改 children 顺序即换 default |

### 5.3 新增模块

- `src-tauri/src/groups.rs` — ProfileGroup CRUD + 与 profiles.rs 解耦
- `src-tauri/src/migrate_v2.rs` — 一次性迁移逻辑（见第 8 节）

### 5.4 Helper daemon

`macos` 的 helper daemon 接口无变化；仍旧是 "启动单个 tunnel 子进程，stdin
写 group.json 路径"。helper 不理解 group 语义。

---

## 6. 前端 UI 改造

### 6.1 Store 拆分与扩展

```
stores/
  profileStore.ts        # 保留：Profile 本身的 CRUD（对话框内用）
  groupStore.ts          # 新：Group CRUD + activeGroupId
  ruleStore.ts           # 重构：与 activeGroupId 绑定；切 group 自动重拉
  connectionStore.ts     # 重构：ConnectionStatus 变成 Map<profileId, Status>
  settingsStore.ts       # 扩展 activeGroupId
```

### 6.2 页面改造

| 文件 | 变化 |
|------|------|
| `components/ProfileList.tsx` → `GroupList.tsx` | 一级列表变成 group 列表；Profile 不在最外层单独列出 |
| `components/NewProfileDialog.tsx` | 保留为"向当前编辑中 group 添加 child"的子对话框 |
| `components/GroupEditorPage.tsx` | **新增**：group 详情 / children 列表 / 拖拽换顺序（即换 default） / rules 快照 |
| `components/ConnectionPage.tsx` | 从单 profile 视图改为活跃 group 概览：顶部 group 名，下方每个 child 一行（state / stats / pool 指示器 / 独立断开按钮） |
| `components/DestinationsPage.tsx` | 路由下拉四项：`Default (→ <child0.name>)` / `Tunnel → <child list>` / `Direct` / `Blocked`。`DestinationSnapshot` 行额外显示"实际走了哪个"徽章 |
| `components/ProfileDetailPage.tsx` | 保留但进入方式改为从 GroupEditorPage 点某个 child |
| `components/QrCodeExportDialog.tsx` | 导出单位从 profile 改为 group；QR payload schema 升级（附加版本号） |

### 6.3 per-child firewall 开关的归属

现有 Profile 有这些与 firewall 交互的字段：

- `method`（pf/nft/tproxy/windivert/socks5）
- `disableIpv6`
- `enableUdp`
- `blockUdp`
- `autoExcludeLan`
- `autoNets`

**归属决策**：这些字段 **上升到 ProfileGroup 层**，而不是每个 child 独立设置。
原因：firewall 只有一个所有者，method 必须统一；IPv6/UDP 开关在内核层也是
全局的。ProfileEditor 里这些开关灰化（readonly），真正的编辑在 GroupEditorPage。

迁移期：单 child group 时这些字段从 child 直接抬到 group；多 child 时需要
用户在 UI 确认一次。

---

## 7. CLI 适配

### 7.1 新用法

```bash
# 多 profile
sudo netferry-tunnel --group /path/to/mygroup.json

# 老用法（自动包装）
sudo netferry-tunnel --remote user@host 0.0.0.0/0
# 内部等价于：
# group := &ProfileGroup{
#   Children: []Profile{{Remote: "user@host", Subnets: ["0.0.0.0/0"]}},
#   Rules: {},  // 未设任何 destination 规则，全走 default = children[0]
# }
```

### 7.2 CLI 场景下的 rules 来源

headless CLI 通常没有交互式的 destination 管理需求，rules 就从 group.json
读，运行时不变。若用户想动态加规则，可以：

```bash
curl -X POST http://127.0.0.1:<statsPort>/group/rules -d '...'
```

（stats 端口 CLI 启动时会打日志输出。）

---

## 8. 迁移路径

### 8.1 数据迁移（P1 阶段一次性）

Tauri 启动时检测：

1. 若 `groups/` 目录存在 → 已迁移完成，跳过。
2. 否则读取：
   - 所有 `profiles/*.json`
   - 全局 `routes.json` + `priorities.json`
3. 创建默认 group：
   ```json
   {
     "id": "default",
     "name": "Default",
     "children": [...所有旧 profile, 顺序按修改时间],
     "rules": { "<host>": { "kind": "tunnel", "profileId": "<旧 autoConnect 指向>" } 或 { "kind": "direct" } 等 },
     "priorities": { ...原样 }
   }
   ```
4. 旧 `routes.json` 里值为 `"tunnel"` 的条目，绑定到迁移时选定的 default profile
   （= 旧 `autoConnectProfileId`，若无则 = children[0]）。
5. 写入 `groups/default.json`，更新 `settings.json` 设 `activeGroupId = "default"`。
6. 保留旧文件一个版本周期（移到 `legacy/` 子目录）以便回滚。

### 8.2 代码兼容策略

- Rust 侧 `connect(profile)` 命令保留一段时间，内部走"创建临时单 child group
  并 activate"的路径。前端旧代码不修改也能跑，方便 P1 独立发版。
- Go 侧老 CLI flag 集保留，自动包装成单 child group。

---

## 9. 分阶段落地

| Phase | 范围 | 代码涉及 | 独立发版 |
|-------|------|----------|---------|
| **P0** | 本设计文档定稿 + JSON schema 评审 | 仅文档 | ✓ |
| **P1** | 持久层引入 ProfileGroup + 迁移代码；运行时仍单 profile | `src-tauri/groups.rs`, `migrate_v2.rs`, `types.ts` | ✓（用户无感知） |
| **P2** | Go 多 backend：sessionManager + listener 分发 + stats 扩展 | `cmd/tunnel/main.go`, `internal/proxy/listener.go`, `internal/stats/stats.go` | ✓（CLI 先可用） |
| **P3** | 桌面新 UI：GroupEditorPage + DestinationsPage 三层菜单 + ConnectionPage 多行 | `src/components/*`, `src/stores/*` | ✓（完整功能上线） |
| **P4** | CLI `--group` 的正式化与文档 | `cmd/tunnel/main.go`, `README.md` | ✓ |
| **P5**（可选） | 活跃 group 内热增删 child；取消单实例 child 锁 | `sidecar.rs`, `cmd/tunnel/main.go` HTTP API | ✓ |

P2 之前交付的版本，用户行为与现在完全一致；P3 交付即全量多 profile 可用。

---

## 10. 未决问题

### 10.1 Child 掉线 fallback 策略

当某 destination 的规则是 `{kind: "tunnel", profileId: "child_X"}` 而 child_X
的 pool 此刻不在线时，新连接怎么处理？候选：

- **A. Reject with retriable error（倾向）**：客户端应用重试时会再碰到同一状态，
  UI 明确提示"待 child_X 重连"。不会静默把敏感流量漏到明文 direct。
- **B. Fallback 到 default (children[0])**：可用性好，但语义上"钉在 X"失效，
  敏感流量可能意外走其他 tunnel。
- **C. Fallback 到 direct**：最高可用性，但安全含义需用户显式同意。

建议 v1 走 A，用户诉求强烈时再做 per-rule 的 `fallback` 字段。

### 10.2 Auto-nets 多 child 合并

`--auto-nets` 会在运行时从 remote 发现可达 subnet。多 child 同时 auto-nets
时需要一个合并策略：

- 并集进 firewall（劫持范围 = 所有 child 发现的 subnet 总和）。
- destination 首次进入某 child auto-nets 范围时，默认 rule 设为
  `{kind: "tunnel", profileId: "<该 child>"}`？还是保持 default？
- 建议：仍保持 default，只把 subnet 加入劫持范围；是否钉到具体 child
  由用户通过 DestinationsPage 手动决定。

### 10.3 多 child 重名与 profileId 稳定性

UI 允许两个 child 同名（不同 remote）；listener 路由完全依赖 profileId。
profileId 必须在 group 生命周期内稳定；重命名 child 不得改变 id。已加入
编辑约束。

### 10.4 Mobile 是否跟进

本设计默认 netferry-mobile 保持单 profile 模式，`mobile/tunnel.go` 的
`tunnelSession` 不做 SessionManager 化。理由：移动端 UX 目前没有"多场景"
诉求，gomobile 接口改动会扩散到 iOS / Android 原生侧。待桌面端稳定后单开
设计文档评估。

---

## 11. 关键代码位置对照表

| 功能点 | 现有代码位置 | 改动类型 |
|--------|--------------|---------|
| 单进程锁 | `netferry-desktop/src-tauri/src/sidecar.rs:944-961` | 语义保留，移除 `last_connected_profile` |
| Tunnel CLI 入参 | `netferry-relay/cmd/tunnel/main.go:46-76` | 新增 `--group`，保留旧 flag |
| SSH mux 池构建 | `netferry-relay/cmd/tunnel/main.go:338-352` | 改为 sessionManager |
| 路由决策 | `netferry-relay/internal/proxy/listener.go:135-151` | **核心改动** |
| routeModes 存储 | `netferry-relay/internal/stats/stats.go:182-183` | value 从 string 变结构体 |
| DestinationSnapshot | `netferry-relay/internal/stats/stats.go:240-253` | 加两个 profileId 字段 |
| LookupRouteMode | `netferry-relay/internal/stats/stats.go:495-506` | 返回新结构体 |
| pf anchor | `netferry-relay/internal/firewall/pf.go:204` | 不变（仍 per-proxyPort） |
| Rust rules 命令 | `netferry-desktop/src-tauri/src/commands.rs` | 签名加 groupId |
| 前端路由 dropdown | `netferry-desktop/src/components/DestinationsPage.tsx` | 四选项菜单 |
| 前端连接状态 | `netferry-desktop/src/stores/connectionStore.ts` | 状态 map 化 |
