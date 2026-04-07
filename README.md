<div align="center">
  <img src="docs/assets/icon.png" width="96" alt="NetFerry icon" />

  # NetFerry

  [![GitHub Release](https://img.shields.io/github/v/release/hoveychen/netferry?style=flat-square&color=F5B932)](https://github.com/hoveychen/netferry/releases/latest)
  [![Build](https://img.shields.io/github/actions/workflow/status/hoveychen/netferry/release.yml?style=flat-square&label=build)](https://github.com/hoveychen/netferry/actions/workflows/release.yml)
  [![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Windows%20%7C%20Linux-2CC4D4?style=flat-square)](https://github.com/hoveychen/netferry/releases/latest)
  [![License](https://img.shields.io/badge/license-Proprietary-slate?style=flat-square)](LICENSE)

  **Secure tunneling for everyone — desktop GUI or CLI.**
</div>

NetFerry is a transparent tunneling tool that routes traffic through any SSH server. It includes a desktop GUI (Tauri + React) and a standalone CLI binary — no Python, no `sshuttle`, no dependencies.

## What is NetFerry?

![NetFerry in 4 panels](docs/assets/netferry_comic.png)

> **In short:** Got blocked websites at work or abroad? NetFerry creates a secure tunnel through any SSH server you have access to — no technical knowledge needed. Use the desktop app for a point-and-click experience, or the CLI for headless / server use.

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

- **`netferry-tunnel`** — Go binary that handles SSH connection, firewall rule setup, transparent TCP/DNS/UDP proxying. Works standalone or as the desktop app's sidecar.
- **`netferry-server`** — Go binary auto-deployed to the remote SSH host. Communicates with the tunnel client over a multiplexed protocol via stdin/stdout.
- **`netferry-desktop`** — Tauri + React GUI that manages profiles and launches the tunnel with privilege elevation.

## Download

### Desktop App

| Platform | Download |
|----------|----------|
| **macOS (Apple Silicon)** | [NetFerry_macos_silicon.dmg](https://github.com/hoveychen/netferry/releases/latest/download/NetFerry_macos_silicon.dmg) |
| **macOS (Intel)** | [NetFerry_macos_intel.dmg](https://github.com/hoveychen/netferry/releases/latest/download/NetFerry_macos_intel.dmg) |
| **Linux (x64)** | [.deb](https://github.com/hoveychen/netferry/releases/latest/download/NetFerry_linux_x64.deb) · [AppImage](https://github.com/hoveychen/netferry/releases/latest/download/NetFerry_linux_x64.AppImage) |
| **Windows (x64)** | [.msi](https://github.com/hoveychen/netferry/releases/latest/download/NetFerry_windows_x64.msi) · [.exe](https://github.com/hoveychen/netferry/releases/latest/download/NetFerry_windows_x64.exe) |

### CLI Only (Linux)

For headless servers or CLI-only usage — no desktop environment required.

| Architecture | Download |
|--------------|----------|
| **x86_64** | [netferry-tunnel-linux-amd64](https://github.com/hoveychen/netferry/releases/latest/download/netferry-tunnel-linux-amd64) |
| **arm64** | [netferry-tunnel-linux-arm64](https://github.com/hoveychen/netferry/releases/latest/download/netferry-tunnel-linux-arm64) |

All links point to the **latest** release; older versions are on the [Releases](https://github.com/hoveychen/netferry/releases) page.

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
netferry-relay/          Go module — tunnel client, server, and proxy logic
  cmd/tunnel/            CLI entry point (netferry-tunnel)
  cmd/server/            Remote server binary (auto-deployed via SSH)
  cmd/probe/             Platform capability probe tool
  internal/              Firewall, mux protocol, proxy, SSH, stats
netferry-desktop/        Tauri + React desktop application
  src/                   React frontend
  src-tauri/             Rust backend (sidecar management, privilege elevation)
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
