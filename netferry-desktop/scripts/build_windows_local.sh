#!/usr/bin/env bash
# Build NetFerry for Windows.
#
# Two modes:
#   1. Run on macOS/Linux → cross-compiles only the Go sidecar (no Tauri bundle).
#   2. Run on Windows (Git Bash / MSYS2) → full build including NSIS/MSI installer.
#
# Prerequisites:
#   - Go, Python 3, Node.js + npm (in PATH)
#   - (Windows only) Rust + cargo, NSIS
#
# Optional (fill in .env at the project root):
#   WINDOWS_CERTIFICATE          - base64-encoded .pfx certificate
#   WINDOWS_CERTIFICATE_PASSWORD - password for the .pfx
#
# Usage:
#   bash scripts/build_windows_local.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DESKTOP_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

# Load .env if present
if [[ -f "$DESKTOP_DIR/.env" ]]; then
  set -o allexport
  # shellcheck disable=SC1091
  source "$DESKTOP_DIR/.env"
  set +o allexport
fi

RUST_TARGET="x86_64-pc-windows-msvc"
echo "==> Target: $RUST_TARGET"

cd "$DESKTOP_DIR"

# ── Generate dev version ──────────────────────────────────────────────────────
VERSION="$(date +%y).$((10#$(date +%m))).$((10#$(date +%d)))-$(date +%s)"
echo "==> Version: v$VERSION"

# ── Build sidecar (Go cross-compilation — works on any OS) ───────────────────
echo "==> Building sidecar"
python3 scripts/build_sidecar.py --target "$RUST_TARGET" --version "v$VERSION"

SIDECAR_PATH="$DESKTOP_DIR/src-tauri/binaries/netferry-tunnel-${RUST_TARGET}.exe"
echo "    Sidecar: $SIDECAR_PATH"

# ── Check if we can do a full Tauri build ─────────────────────────────────────
# Tauri for Windows requires MSVC linker + NSIS, which are only available on
# Windows. On macOS/Linux we stop after the sidecar.
if [[ "$(uname -s)" != MINGW* && "$(uname -s)" != MSYS* && "$(uname -s)" != CYGWIN* && "$(uname -o 2>/dev/null || true)" != "Msys" ]]; then
  echo ""
  echo "==> Not running on Windows — skipping Tauri bundle."
  echo "    The Go sidecar has been cross-compiled successfully."
  echo "    To build the full installer, run this script on a Windows machine."
  exit 0
fi

# ── (Windows only) Full Tauri build ──────────────────────────────────────────
echo "==> Installing npm dependencies"
npm ci

echo "==> Building Tauri bundle"
if [[ -n "${WINDOWS_CERTIFICATE:-}" && -n "${WINDOWS_CERTIFICATE_PASSWORD:-}" ]]; then
  echo "    (code signing enabled)"
  export TAURI_SIGNING_PRIVATE_KEY="$WINDOWS_CERTIFICATE"
  export TAURI_SIGNING_PRIVATE_KEY_PASSWORD="$WINDOWS_CERTIFICATE_PASSWORD"
fi

npm run tauri build -- --target "$RUST_TARGET" --config "{\"version\":\"$VERSION\"}"

# ── Locate outputs ────────────────────────────────────────────────────────────
BUNDLE_DIR="$DESKTOP_DIR/src-tauri/target/$RUST_TARGET/release/bundle"

echo ""
echo "==> Done!"
if [[ -d "$BUNDLE_DIR/nsis" ]]; then
  echo "    NSIS: $(ls "$BUNDLE_DIR/nsis/"*.exe 2>/dev/null || echo "(not found)")"
fi
if [[ -d "$BUNDLE_DIR/msi" ]]; then
  echo "    MSI:  $(ls "$BUNDLE_DIR/msi/"*.msi 2>/dev/null || echo "(not found)")"
fi
