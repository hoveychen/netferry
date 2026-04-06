# NetFerry iOS

SSH tunnel VPN client for iOS, powered by the NetFerry Go engine.

## Prerequisites

- Xcode 15.0+ (iOS 17 SDK)
- Go 1.21+
- gomobile (`go install golang.org/x/mobile/cmd/gomobile@latest && gomobile init`)
- Apple Developer account with Network Extension entitlement

## Build the Go Framework

From the repository root:

```bash
cd netferry-relay
gomobile bind -target=ios -o ../netferry-mobile/ios/NetFerry/NetFerryEngine.xcframework ./mobile/
```

This produces `NetFerryEngine.xcframework` which contains the Go engine for both iOS device and simulator.

## Xcode Project Setup

Since the `.xcodeproj` cannot be generated as a text file, create it manually in Xcode:

### 1. Create the project

1. Open Xcode, select **File > New > Project**
2. Choose **iOS > App**, click Next
3. Product Name: `NetFerry`
4. Team: your Apple Developer team
5. Organization Identifier: `com.netferry`
6. Interface: **SwiftUI**
7. Language: **Swift**
8. Save into `netferry-mobile/ios/NetFerry/`

### 2. Add existing source files

1. Delete the auto-generated `ContentView.swift` and `NetFerryApp.swift`
2. Right-click the `NetFerry` group in the navigator, select **Add Files to "NetFerry"**
3. Add all files from `NetFerry/` subdirectory (Models, Stores, Views, NetFerryApp.swift)
4. Ensure "Copy items if needed" is unchecked (files are already in place)

### 3. Add the Network Extension target

1. **File > New > Target**
2. Choose **iOS > Network Extension**
3. Product Name: `PacketTunnel`
4. Provider Type: **Packet Tunnel Provider**
5. Language: **Swift**
6. Delete the auto-generated `PacketTunnelProvider.swift`
7. Add the `PacketTunnel/PacketTunnelProvider.swift` from this project

### 4. Add the Go framework

1. Drag `NetFerryEngine.xcframework` into the Xcode project navigator
2. In the main app target **General** tab, ensure the framework is under **Frameworks, Libraries, and Embedded Content** with **Embed & Sign**
3. In the PacketTunnel extension target **General** tab, add `NetFerryEngine.xcframework` under **Frameworks and Libraries** with **Do Not Embed** (it will be embedded by the app)

### 5. Configure entitlements

Both the main app and the PacketTunnel extension need entitlements:

**NetFerry (main app) entitlements:**
```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>com.apple.developer.networking.networkextension</key>
    <array>
        <string>packet-tunnel-provider</string>
    </array>
    <key>com.apple.security.application-groups</key>
    <array>
        <string>group.com.netferry.app</string>
    </array>
</dict>
</plist>
```

**PacketTunnel extension entitlements:**
```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>com.apple.developer.networking.networkextension</key>
    <array>
        <string>packet-tunnel-provider</string>
    </array>
    <key>com.apple.security.application-groups</key>
    <array>
        <string>group.com.netferry.app</string>
    </array>
</dict>
</plist>
```

### 6. Configure App Groups

1. In both targets (NetFerry and PacketTunnel), go to **Signing & Capabilities**
2. Click **+ Capability** and add **App Groups**
3. Add the group: `group.com.netferry.app`

### 7. Bundle identifiers

- Main app: `com.netferry.app`
- PacketTunnel extension: `com.netferry.app.PacketTunnel`

## Provisioning Profiles

You need provisioning profiles with the **Network Extension** entitlement:

1. Go to [Apple Developer Portal](https://developer.apple.com/account)
2. Under **Certificates, Identifiers & Profiles**, create two App IDs:
   - `com.netferry.app` with Network Extension and App Groups capabilities
   - `com.netferry.app.PacketTunnel` with Network Extension and App Groups capabilities
3. Create provisioning profiles for both identifiers
4. Download and install them in Xcode

The Network Extension entitlement requires explicit approval from Apple. You may need to [request it](https://developer.apple.com/contact/request/network-extension/) if you haven't already.

## Build and Run

1. Select a physical iOS device (Network Extensions do not work in the simulator)
2. Select the `NetFerry` scheme
3. Build and run (Cmd+R)

## Architecture

```
NetFerry (main app)
├── ProfileStore      — Profile CRUD, persisted as JSON in App Group container
├── VPNManager        — Manages NETunnelProviderManager lifecycle
└── SwiftUI Views     — Profile list, editor, connection status, settings

PacketTunnel (extension)
├── PacketTunnelProvider — NEPacketTunnelProvider subclass
├── TunnelCallback       — Implements MobilePlatformCallback for Go engine
└── NetFerryEngine       — Go engine (gomobile xcframework)
```

The main app communicates with the PacketTunnel extension via:
- `NETunnelProviderManager` for start/stop and configuration
- `NETunnelProviderSession.sendProviderMessage()` for stats polling
- App Group shared container for profile data
