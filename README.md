<div align="center">
  <img src="docs/assets/icon.png" width="96" alt="NetFerry icon" />

  # NetFerry

  [![GitHub Release](https://img.shields.io/github/v/release/hoveychen/netferry?style=flat-square&color=F5B932)](https://github.com/hoveychen/netferry/releases/latest)
  [![Build](https://img.shields.io/github/actions/workflow/status/hoveychen/netferry/release.yml?style=flat-square&label=build)](https://github.com/hoveychen/netferry/actions/workflows/release.yml)
  [![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Windows%20%7C%20Linux-2CC4D4?style=flat-square)](https://github.com/hoveychen/netferry/releases/latest)
  [![License](https://img.shields.io/badge/license-Proprietary-slate?style=flat-square)](LICENSE)

  **Secure tunneling for everyone — no terminal required.**
</div>

NetFerry is a desktop tunneling tool built on top of `sshuttle`, with a modern management UI powered by `Tauri + React`.

## What is NetFerry?

![NetFerry in 4 panels](docs/assets/netferry_comic.png)

> **In short:** Got blocked websites at work or abroad? NetFerry creates a secure tunnel through any SSH server you have access to — no technical knowledge needed. Just pick a profile and click Connect.

## Download (Latest Release)

| Platform | Download |
|----------|----------|
| **macOS (Apple Silicon)** | [NetFerry_macos_silicon.dmg](https://github.com/hoveychen/netferry/releases/latest/download/NetFerry_macos_silicon.dmg) |
| **macOS (Intel)** | [NetFerry_macos_intel.dmg](https://github.com/hoveychen/netferry/releases/latest/download/NetFerry_macos_intel.dmg) |
| **Linux (x64)** | [.deb](https://github.com/hoveychen/netferry/releases/latest/download/NetFerry_linux_x64.deb) · [AppImage](https://github.com/hoveychen/netferry/releases/latest/download/NetFerry_linux_x64.AppImage) |
| **Windows (x64)** | [.msi](https://github.com/hoveychen/netferry/releases/latest/download/NetFerry_windows_x64.msi) · [.exe](https://github.com/hoveychen/netferry/releases/latest/download/NetFerry_windows_x64.exe) |

All links point to the **latest** release; older versions are on the [Releases](https://github.com/hoveychen/netferry/releases) page.

## Repository Layout

- `netferry-desktop`: Desktop application (`Tauri + React`)
- `netferry`: Python wrapper entry (`python -m netferry`)
- `third_party/sshuttle`: Upstream `sshuttle` submodule

## Quick Start (Development)

### 1) Initialize submodules

```bash
git submodule update --init --recursive
```

### 2) Run desktop app in dev mode

```bash
cd netferry-desktop
npm install
npm run tauri dev
```

## Build Installers

```bash
cd netferry-desktop
npm run build
npm run build:sidecar
npm run tauri build
```

Build artifacts are generated under `netferry-desktop/src-tauri/target/`.

## Project Status

This project is currently **proprietary**. See `LICENSE` for details.

## Security and Feedback

- Security reports: see `SECURITY.md`
- Bugs / Features: submit via GitHub Issue templates

