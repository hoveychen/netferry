# NetFerry iOS

SwiftUI-based iOS app for the NetFerry SSH tunnel.

## Requirements

- Xcode 15.0+
- iOS 17.0+ deployment target
- Apple Developer account with Network Extension capability

## Project Setup

### 1. Create Xcode Project

Since there is no `.xcodeproj` committed (it contains machine-specific paths), create one in Xcode:

1. Open Xcode → File → New → Project → iOS → App
2. Product Name: `NetFerry`, Organization: `com.netferry`
3. Interface: SwiftUI, Language: Swift
4. Add the existing source files from `NetFerry/` directory

### 2. Add Network Extension Target

1. File → New → Target → Network Extension
2. Provider Type: Packet Tunnel Provider
3. Product Name: `PacketTunnel`
4. Bundle ID: `com.netferry.app.PacketTunnel`
5. Add files from `PacketTunnel/` directory

### 3. Configure Capabilities

**App Target:**
- Network Extensions (Packet Tunnel)
- Personal VPN
- App Groups: `group.com.netferry.app`

**Extension Target:**
- Network Extensions (Packet Tunnel)
- App Groups: `group.com.netferry.app`

### 4. Integrate Go Engine

Build the gomobile framework:

```bash
cd netferry-relay/mobile
make ios
```

Then add `NetFerryEngine.xcframework` to both targets:
1. Drag `NetFerryEngine.xcframework` into the Xcode project
2. Ensure both the app and PacketTunnel targets link against it
3. Set "Embed & Sign" for the app target, "Do Not Embed" for the extension

### 5. Entitlements

Both targets need entitlements files with:

```xml
<key>com.apple.developer.networking.networkextension</key>
<array>
    <string>packet-tunnel-provider</string>
</array>
<key>com.apple.security.application-groups</key>
<array>
    <string>group.com.netferry.app</string>
</array>
```

## Architecture

```
NetFerryApp.swift          → App entry point
├── ProfileListView        → Home screen, profile list
├── ProfileDetailView      → Create/edit profiles
├── ConnectionView         → Connection status + live stats
└── SettingsView           → Language, version info

Stores/
├── ProfileStore           → JSON file persistence (App Group shared)
└── VPNManager             → NETunnelProviderManager wrapper

PacketTunnel/
└── PacketTunnelProvider   → NEPacketTunnelProvider (runs in extension process)
    - Starts Go engine
    - Configures NEProxySettings (PAC → SOCKS5 proxy)
    - Configures NEDNSSettings (DNS relay)
    - Communicates with app via sendProviderMessage
```

## Traffic Flow

```
App traffic → System proxy (PAC) → SOCKS5 127.0.0.1:{port} → Go engine → mux → SSH → remote
DNS queries → NEDNSSettings → DNS relay 127.0.0.1:{port} → Go engine → mux → SSH → remote
```

The Go engine runs inside the PacketTunnel extension process. It starts:
- A local SOCKS5 proxy (random port)
- A local DNS relay (random port)

These ports are read via `engine.getSOCKSPort()` / `engine.getDNSPort()` and configured
in `NEPacketTunnelNetworkSettings`.
