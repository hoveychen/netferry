<div align="center">
  <img src="docs/assets/icon.png" width="96" alt="NetFerry icon" />

  # NetFerry

  [![GitHub Release](https://img.shields.io/github/v/release/hoveychen/netferry?style=flat-square&color=F5B932)](https://github.com/hoveychen/netferry/releases/latest)
  [![Build](https://img.shields.io/github/actions/workflow/status/hoveychen/netferry/release.yml?style=flat-square&label=build)](https://github.com/hoveychen/netferry/actions/workflows/release.yml)
  [![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Windows%20%7C%20Linux%20%7C%20Android-2CC4D4?style=flat-square)](https://github.com/hoveychen/netferry/releases/latest)
  [![License](https://img.shields.io/badge/license-Proprietary-slate?style=flat-square)](LICENSE)

  **Personal tunneling client for secure remote access to your own SSH server — desktop, mobile, or CLI.**
</div>

NetFerry is a client-side tunneling tool that lets you establish a secure connection to an SSH server **you own and operate**. It does not provide, host, or operate any intermediate servers, and it does not collect, inspect, log, or relay any user traffic. One polished desktop app (macOS, Windows), one Android app, and one single-binary CLI for headless Linux — all driven by the same Go engine. No Python, no `sshuttle`, no extra dependencies on either end.

## What is NetFerry?

> **In short:** NetFerry is a personal tunneling client for users who already have their own SSH server. It establishes a secure connection from your device to **your own server** so you can reach private resources, work remotely, or access your home/office network from anywhere — point-and-click on desktop and Android, or one command on the CLI. The server binary is auto-deployed over SSH to **your** host on first connect; NetFerry never runs any infrastructure of its own and never sees your traffic.

## Architecture

```
┌───────────────────────────┐
│  NetFerry Desktop (GUI)   │  Tauri + React
│  Profile management, UI   │
└────────────┬──────────────┘
             │ launches
             ▼
┌───────────────────────────┐         SSH          ┌──────────────────┐
│  netferry-tunnel (CLI)    │ ───────────────────▶  │  netferry-server │
│  Firewall rules + proxy   │  deploys & connects  │  (auto-deployed) │
└───────────────────────────┘                      └──────────────────┘
```

- **`netferry-tunnel`** — Go binary that handles the SSH connection, firewall rule setup, and transparent TCP/DNS/UDP proxying. Works standalone or as the desktop app's sidecar.
- **`netferry-server`** — Go binary auto-deployed to the remote SSH host on first connect. Communicates with the tunnel client over a multiplexed protocol via stdin/stdout.
- **`netferry-desktop`** — Tauri + React GUI that manages profiles and launches the tunnel with privilege elevation (LaunchDaemon helper on macOS, UAC on Windows).
- **`netferry-mobile`** — Android app (Kotlin Compose) that embeds the same Go engine via gomobile, then plugs it into `VpnService` + tun2socks to capture device traffic. iOS support is in progress.

## Download

### Desktop

| Platform | Download |
|----------|----------|
| **macOS (Apple Silicon)** | [NetFerry_macos_silicon.pkg](https://github.com/hoveychen/netferry/releases/latest/download/NetFerry_macos_silicon.pkg) |
| **macOS (Intel)** | [NetFerry_macos_intel.pkg](https://github.com/hoveychen/netferry/releases/latest/download/NetFerry_macos_intel.pkg) |
| **Windows (x64)** | [NetFerry_windows_x64.msi](https://github.com/hoveychen/netferry/releases/latest/download/NetFerry_windows_x64.msi) |

### Mobile

| Platform | Download |
|----------|----------|
| **Android (arm64)** | [NetFerry_android_arm64.apk](https://github.com/hoveychen/netferry/releases/latest/download/NetFerry_android_arm64.apk) |

### CLI (Linux, headless)

For servers or any environment without a desktop — a single static Go binary, no dependencies.

| Architecture | Download |
|--------------|----------|
| **x86_64** | [netferry-tunnel-linux-amd64](https://github.com/hoveychen/netferry/releases/latest/download/netferry-tunnel-linux-amd64) |
| **arm64** | [netferry-tunnel-linux-arm64](https://github.com/hoveychen/netferry/releases/latest/download/netferry-tunnel-linux-arm64) |

All links resolve to the **latest** release; older versions are on the [Releases](https://github.com/hoveychen/netferry/releases) page.

## CLI Usage

```bash
# Route all traffic through an SSH server
sudo netferry-tunnel --remote user@host 0.0.0.0/0

# Route specific subnets with DNS interception
sudo netferry-tunnel --remote user@host --dns 10.0.0.0/8 172.16.0.0/12

# Auto-discover remote subnets
sudo netferry-tunnel --remote user@host --auto-nets --dns

# Use a specific SSH key
sudo netferry-tunnel --remote user@host --identity ~/.ssh/id_rsa 0.0.0.0/0

# SOCKS5 mode (no root required)
netferry-tunnel --remote user@host --method socks5 0.0.0.0/0
```

### Flags

| Flag | Description |
|------|-------------|
| `--remote <[user@]host[:port]>` | SSH target (required) |
| `--identity <path>` | SSH private key path |
| `--method <name>` | Firewall backend: `auto`, `pf`, `nft`, `tproxy`, `windivert`, `socks5` |
| `--auto-nets` | Auto-discover remote subnets |
| `--dns` | Intercept DNS requests |
| `--dns-target <IP[@port]>` | Remote DNS server |
| `--no-ipv6` | Disable IPv6 handling |
| `--udp` | Enable UDP proxy (tproxy only) |
| `--flow-control` | Enable per-channel flow control |
| `--extra-ssh-opts <string>` | Extra SSH options |
| `-v` | Verbose logging |

Root/sudo is required for all methods except `socks5`, which sets up a local SOCKS5 proxy instead of transparent interception.

## Repository Layout

```
netferry-relay/          Go module — tunnel client, server, proxy, and mobile bindings
  cmd/tunnel/            CLI entry point (netferry-tunnel)
  cmd/server/            Remote server binary (auto-deployed via SSH)
  cmd/probe/             Platform capability probe tool
  internal/              Firewall, mux protocol, proxy, SSH, stats
  mobile/                gomobile bindings shared by Android / iOS
netferry-desktop/        Tauri + React desktop application
  src/                   React frontend
  src-tauri/             Rust backend (sidecar management, privilege elevation)
netferry-mobile/         Mobile apps sharing the Go engine
  android/               Kotlin Compose app + VpnService + tun2socks
  ios/                   SwiftUI app + NEPacketTunnelProvider (in progress)
```

## Quick Start (Development)

### Run desktop app in dev mode

```bash
cd netferry-desktop
npm install
npm run tauri dev
```

### Build tunnel CLI only

```bash
cd netferry-relay
go build -o netferry-tunnel ./cmd/tunnel
```

### Build desktop installers

```bash
cd netferry-desktop
npm install
python ../scripts/build_sidecar.py
npm run tauri build
```

## Project Status

This project is currently **proprietary**. See `LICENSE` for details.

## Security and Feedback

- Security reports: see `SECURITY.md`
- Bugs / Features: submit via GitHub Issue templates
