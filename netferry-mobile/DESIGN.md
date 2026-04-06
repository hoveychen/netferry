# NetFerry Mobile — UI & Architecture Design

## Architecture Overview

```
┌───────────────────────────────────────┐
│  Native UI (SwiftUI / Kotlin Compose) │
│  - Profile list, detail, connection   │
└──────────────┬────────────────────────┘
               │ gomobile bind (.xcframework / .aar)
┌──────────────▼────────────────────────┐
│  Go Engine (netferry-relay/mobile/)   │
│  - SSH connect, mux tunnel            │
│  - Local SOCKS5 proxy + DNS relay     │
│  - Stats reporting via callback       │
└──────────────┬────────────────────────┘
               │
┌──────────────▼────────────────────────┐
│  Native VPN Layer                     │
│  iOS: NEPacketTunnelProvider          │
│       + NEProxySettings (PAC→SOCKS5)  │
│  Android: VpnService + tun2socks     │
│           routes TUN → SOCKS5 proxy   │
└───────────────────────────────────────┘
```

## Go Engine API (gomobile)

```go
// Create engine with platform callbacks
engine := mobile.NewEngine(callback)

// Start tunnel — connects SSH, starts local SOCKS5 + DNS relay
err := engine.Start(configJSON)

// After Start(), native side reads ports:
socksPort := engine.GetSOCKSPort() // int32
dnsPort   := engine.GetDNSPort()   // int32

// Periodic stats delivered via callback.OnStats(json)
// State changes via callback.OnStateChange(state)

// Stop
engine.Stop()
```

## Traffic Routing

### iOS
- Uses `NEProxySettings` with a PAC script: `SOCKS 127.0.0.1:{socksPort}`
- DNS via `NEDNSSettings` pointing to the local DNS relay
- No TUN packet processing needed — iOS respects system proxy settings

### Android
- Uses `VpnService` to create a TUN interface capturing all traffic
- Requires **tun2socks** to convert TUN IP packets → SOCKS5 connections
- DNS server set to `127.0.0.1` (Go engine's DNS relay)
- Socket protection: Go engine calls `PlatformCallback.ProtectSocket(fd)` → `VpnService.protect(fd)`

## Screens

### 1. Profile List (Home)
- List of SSH profiles with avatar (first letter)
- Each row: name, remote host, connect button
- Tap row → Profile Detail
- FAB/button → New Profile
- Top-right → Settings gear

### 2. Profile Detail / Edit
- Name, SSH Remote (user@host:port)
- Identity Key (paste PEM or import from file)
- Jump Hosts (optional, collapsible)
- Routing: Subnets, Exclude Subnets
- DNS: On/Off + optional DNS target
- Advanced (collapsible): Pool Size, MTU, Disable IPv6
- Save / Delete buttons

### 3. Connection Screen
- Shows when connected/connecting
- Status indicator (connecting spinner, connected green dot)
- Profile name + remote
- Stats: Upload/Download speed, Active connections, Total connections
- Speed chart (last 60s)
- Disconnect button
- Log viewer (collapsible)

### 4. Settings
- Language: English / 中文
- Auto-connect profile
- About / Version

## Navigation

```
TabBar (iOS) / BottomNav (Android):
  ├── Profiles (home)
  └── Settings

Profile row tap → ProfileDetail (push)
Connect button → ConnectionScreen (fullscreen modal)
```

## Mobile-Specific Considerations

### Profile Storage
- Profiles stored as JSON in app sandbox (UserDefaults on iOS, SharedPreferences on Android)
- Same fields as desktop Profile type
- Export/Import via encrypted .nfprofile files (future)

### Identity Keys
- On mobile, NO file system access for identity files
- Must paste PEM key directly or import from file picker
- `identityKey` field is mandatory (`identityFile` not supported)

### VPN Permission
- iOS: Request VPN configuration permission on first connect
- Android: Show VPN consent dialog (`VpnService.prepare()`)

### Reconnection
- Auto-reconnect on network change (WiFi → Cellular)
- iOS NE handles this via `NEOnDemandRule`
- Android: `NetworkCallback` + service restart

## Config JSON Format (Go ↔ Native)

```json
{
  "remote": "user@host:22",
  "identityKey": "-----BEGIN OPENSSH PRIVATE KEY-----\n...",
  "jumpHosts": [{"remote": "jump@host:22", "identityKey": "..."}],
  "subnets": ["0.0.0.0/0"],
  "excludeSubnets": ["10.0.0.0/8"],
  "autoNets": false,
  "dns": true,
  "dnsTarget": "",
  "poolSize": 2,
  "disableIpv6": false,
  "mtu": 1500
}
```

## Build

### Go Mobile Library

```bash
# Install gomobile
go install golang.org/x/mobile/cmd/gomobile@latest
gomobile init

# Build for iOS
cd netferry-relay
gomobile bind -target=ios -o mobile/NetFerryEngine.xcframework ./mobile

# Build for Android
gomobile bind -target=android -androidapi=26 -o mobile/mobile.aar ./mobile
```

Or use the Makefile:
```bash
cd netferry-relay/mobile && make all
```
