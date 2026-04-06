# NetFerry Android

Android client for NetFerry SSH tunnel VPN, built with Kotlin and Jetpack Compose.

## Prerequisites

- Android Studio Hedgehog (2023.1.1) or later
- JDK 17
- Android SDK 34
- Go 1.21+
- gomobile (`go install golang.org/x/mobile/cmd/gomobile@latest && gomobile init`)
- Android NDK (install via Android Studio SDK Manager)

## Build the Go Engine AAR

From the `netferry-relay/` directory:

```bash
export ANDROID_HOME=$HOME/Library/Android/sdk   # macOS
export ANDROID_NDK_HOME=$ANDROID_HOME/ndk/26.1.10909125  # adjust version

gomobile bind -target=android -androidapi 26 -o mobile.aar ./mobile/
```

Then copy the AAR into the Android project:

```bash
cp mobile.aar ../netferry-mobile/android/app/libs/
```

## Build and Run

1. Open `netferry-mobile/android/` in Android Studio
2. Ensure `app/libs/mobile.aar` is present
3. Sync Gradle
4. Run on a physical device (VPN services require a real device or an emulator with VPN support)

### Command Line Build

```bash
cd netferry-mobile/android
./gradlew assembleDebug
```

The APK will be at `app/build/outputs/apk/debug/app-debug.apk`.

## Architecture

- **Model**: `Profile` (connection config), `TunnelStats` (live metrics)
- **Service**: `NetFerryVpnService` - Android VpnService that hosts the Go engine
- **Store**: `ProfileStore` - SharedPreferences-based profile persistence
- **ViewModel**: `ProfileViewModel` (CRUD), `ConnectionViewModel` (VPN state)
- **UI**: Jetpack Compose screens with Material 3

## Traffic Flow

```
Device traffic → TUN (VpnService) → tun2socks → SOCKS5 127.0.0.1:{port} → Go engine → mux → SSH → remote
DNS queries    → TUN (VpnService) → DNS relay 127.0.0.1:{port} → Go engine → mux → SSH → remote
```

## Known Limitation: tun2socks Required

The Go engine starts a local SOCKS5 proxy + DNS relay. The Android VpnService creates a
TUN interface that captures all device traffic. A **tun2socks** bridge is needed to convert
raw IP packets from the TUN fd into SOCKS5 connections to the Go engine.

Recommended options:
- [hev-socks5-tunnel](https://github.com/nicholasgasior/android-tun2socks) — Lightweight C implementation
- [badvpn tun2socks](https://github.com/nicholasgasior/badvpn) — Widely used in Android VPN apps
- Custom Go-based tun2socks using gVisor netstack (requires fixing gVisor build for Go 1.25+)

Without tun2socks, the Go engine and UI are fully functional but the TUN interface does not
forward packets. This is marked as a TODO in `NetFerryVpnService.kt`.

## Key Design Decisions

- The Go engine runs inside the VPN service process. Socket protection (`VpnService.protect()`) is exposed via `PlatformCallback.protectSocket()` to prevent routing loops.
- VPN state is shared via companion-object `StateFlow` fields on the service class, allowing ViewModels to observe state without binding.
- Speed history is kept in memory (last 60 samples) for the live chart on the connection screen.
