#!/usr/bin/env bash
# Local macOS build script (no notarization).
#
# Required (fill in .env at the project root):
#   APPLE_CERTIFICATE          - base64-encoded .p12 certificate
#   APPLE_CERTIFICATE_PASSWORD - password for the .p12
#   APPLE_SIGNING_IDENTITY     - e.g. "Developer ID Application: Your Name (TEAMID)"
#
# Optional:
#   RUST_TARGET   - aarch64-apple-darwin (default) or x86_64-apple-darwin
#
# Usage:
#   bash scripts/build_mac_local.sh

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

# ── Default target to native architecture ─────────────────────────────────────
if [[ -z "${RUST_TARGET:-}" ]]; then
  RUST_TARGET=$(rustc -Vv | awk '/^host:/{print $2}')
fi
echo "==> Target: $RUST_TARGET"

# ── Validate required env vars ────────────────────────────────────────────────
for var in APPLE_CERTIFICATE APPLE_CERTIFICATE_PASSWORD APPLE_SIGNING_IDENTITY; do
  if [[ -z "${!var:-}" ]]; then
    echo "ERROR: $var is not set. Fill in netferry-desktop/.env" >&2
    exit 1
  fi
done

# ── Import certificate into a temporary keychain ──────────────────────────────
TMPDIR_CERTS=$(mktemp -d)
CERTIFICATE_PATH="$TMPDIR_CERTS/certificate.p12"
# Use a path inside the temp dir so it's always fresh — no "item already exists" errors
# from leftover keychains created by previous (interrupted) runs.
KEYCHAIN_PATH="$TMPDIR_CERTS/netferry-build.keychain-db"
KEYCHAIN_PASSWORD=$(openssl rand -base64 32)

cleanup() {
  security delete-keychain "$KEYCHAIN_PATH" 2>/dev/null || true
  rm -rf "$TMPDIR_CERTS"
}
trap cleanup EXIT

echo "==> Importing certificate into build keychain"
echo -n "$APPLE_CERTIFICATE" | base64 --decode -o "$CERTIFICATE_PATH"

security create-keychain -p "$KEYCHAIN_PASSWORD" "$KEYCHAIN_PATH"
security set-keychain-settings -lut 21600 "$KEYCHAIN_PATH"
security unlock-keychain -p "$KEYCHAIN_PASSWORD" "$KEYCHAIN_PATH"
# Split the .p12 into private key + certificate so we can import them separately.
# If the cert already lives in the login keychain (from a prior run), importing
# it again would fail and abort the whole import — including the private key.
PRIVKEY_PATH="$TMPDIR_CERTS/signing.key.pem"
CERT_PEM_PATH="$TMPDIR_CERTS/signing.cert.pem"
openssl pkcs12 -in "$CERTIFICATE_PATH" -nocerts -nodes -legacy \
  -passin pass:"$APPLE_CERTIFICATE_PASSWORD" -out "$PRIVKEY_PATH" 2>/dev/null
openssl pkcs12 -in "$CERTIFICATE_PATH" -nokeys -legacy \
  -passin pass:"$APPLE_CERTIFICATE_PASSWORD" -out "$CERT_PEM_PATH" 2>/dev/null

# Import private key into our temp keychain (always succeeds — key is unique)
security import "$PRIVKEY_PATH" -A -t priv -k "$KEYCHAIN_PATH"
# Import certificate — ignore "already exists" (may already be in login keychain)
set +e
security import "$CERT_PEM_PATH" -A -t cert -k "$KEYCHAIN_PATH"
set -e
# apple-tool:,apple:,codesign: — all three are required for codesign to access the key
security set-key-partition-list \
  -S "apple-tool:,apple:,codesign:" \
  -s -k "$KEYCHAIN_PASSWORD" "$KEYCHAIN_PATH"

# Import Apple Developer ID Certification Authority G2 intermediate cert.
# Our Developer ID Application cert is issued by the G2 CA, which is NOT in
# the macOS system roots by default.  Without it codesign can't build the
# trust chain and fails with errSecInternalComponent.
DEVID_G2_DER="$TMPDIR_CERTS/devidg2.der"
curl -fsSL http://certs.apple.com/devidg2.der -o "$DEVID_G2_DER"
security import "$DEVID_G2_DER" -A -t cert -k "$KEYCHAIN_PATH"
# Prepend our keychain while keeping the existing list intact
EXISTING_KEYCHAINS=$(security list-keychains -d user | tr -d '"' | tr '\n' ' ')
# shellcheck disable=SC2086
security list-keychain -d user -s "$KEYCHAIN_PATH" $EXISTING_KEYCHAINS

cd "$DESKTOP_DIR"

# ── Install deps ──────────────────────────────────────────────────────────────
echo "==> Installing npm dependencies"
npm ci

# ── Build sidecar (no signing yet) ───────────────────────────────────────────
echo "==> Building sidecar"
python scripts/build_sidecar.py --target "$RUST_TARGET"

# ── Generate dev version ──────────────────────────────────────────────────────
VERSION="$(date +%y).$((10#$(date +%m))).$((10#$(date +%d)))-$(date +%s)"
echo "==> Version: v$VERSION"

# ── Build Tauri .app bundle WITHOUT signing ───────────────────────────────────
# Tauri auto-signs when APPLE_CERTIFICATE / APPLE_SIGNING_IDENTITY are present
# in the environment. Unset them so Tauri produces an unsigned .app; we sign
# everything manually afterwards (preserving sidecar entitlements).
echo "==> Building Tauri .app (unsigned)"
env -u APPLE_CERTIFICATE \
    -u APPLE_CERTIFICATE_PASSWORD \
    -u APPLE_SIGNING_IDENTITY \
    npm run tauri build -- --target "$RUST_TARGET" --bundles app --config "{\"version\":\"$VERSION\"}"

APP_PATH="$DESKTOP_DIR/src-tauri/target/$RUST_TARGET/release/bundle/macos/NetFerry.app"
if [[ ! -d "$APP_PATH" ]]; then
  echo "ERROR: .app bundle not found at $APP_PATH" >&2
  exit 1
fi

# ── Copy privileged helper + LaunchDaemon plist into the bundle ───────────────
# SMAppService looks for the plist at Contents/Library/LaunchDaemons/ and
# the helper binary at the path given by BundleProgram (relative to app root).
HELPER_DIR="$APP_PATH/Contents/Library/LaunchDaemons"
mkdir -p "$HELPER_DIR"

HELPER_BIN="$DESKTOP_DIR/src-tauri/target/$RUST_TARGET/release/netferry-helper"
if [[ ! -f "$HELPER_BIN" ]]; then
  echo "ERROR: netferry-helper binary not found at $HELPER_BIN" >&2
  echo "       Make sure 'cargo build --release --bin netferry-helper' ran." >&2
  exit 1
fi

cp "$HELPER_BIN" "$HELPER_DIR/com.hoveychen.netferry.helper"
cp "$DESKTOP_DIR/src-tauri/com.hoveychen.netferry.helper.plist" \
   "$HELPER_DIR/com.hoveychen.netferry.helper.plist"

# ── Sign all Frameworks / dylibs first ───────────────────────────────────────
echo "==> Signing frameworks and dylibs"
if [[ -d "$APP_PATH/Contents/Frameworks" ]]; then
  find "$APP_PATH/Contents/Frameworks" \( -name "*.dylib" -o -name "*.framework" \) | sort -r | while read -r lib; do
    codesign --force --options runtime \
      --sign "$APPLE_SIGNING_IDENTITY" \
      "$lib" 2>/dev/null || true
  done
else
  echo "    (no Frameworks dir, skipping)"
fi

# ── Sign privileged helper ────────────────────────────────────────────────────
# The helper runs as root (LaunchDaemon) — sign with its own entitlements.
echo "==> Signing privileged helper"
codesign --force --options runtime \
  --entitlements "$DESKTOP_DIR/src-tauri/helper-entitlements.plist" \
  --sign "$APPLE_SIGNING_IDENTITY" \
  "$HELPER_DIR/com.hoveychen.netferry.helper"

# ── Sign sidecar ──────────────────────────────────────────────────────────────
# Note: Tauri renames the sidecar to "netferry-tunnel" (no target suffix) inside the bundle.
SIDECAR="$APP_PATH/Contents/MacOS/netferry-tunnel"
echo "==> Signing sidecar: $SIDECAR"
codesign --force --options runtime \
  --entitlements "$DESKTOP_DIR/src-tauri/sidecar-entitlements.plist" \
  --sign "$APPLE_SIGNING_IDENTITY" \
  "$SIDECAR"

# ── Sign main executable ──────────────────────────────────────────────────────
# The binary name matches the Rust crate name (netferry-desktop), not productName.
echo "==> Signing main executable"
codesign --force --options runtime \
  --entitlements "$DESKTOP_DIR/src-tauri/entitlements.plist" \
  --sign "$APPLE_SIGNING_IDENTITY" \
  "$APP_PATH/Contents/MacOS/netferry-desktop"

# ── Sign the .app bundle ──────────────────────────────────────────────────────
echo "==> Signing .app bundle"
codesign --force --options runtime \
  --entitlements "$DESKTOP_DIR/src-tauri/entitlements.plist" \
  --sign "$APPLE_SIGNING_IDENTITY" \
  "$APP_PATH"

# ── Create DMG ────────────────────────────────────────────────────────────────
DMG_DIR="$DESKTOP_DIR/src-tauri/target/$RUST_TARGET/release/bundle/dmg"
mkdir -p "$DMG_DIR"

case "$RUST_TARGET" in
  aarch64-apple-darwin) ARCH_LABEL="macos_silicon" ;;
  x86_64-apple-darwin)  ARCH_LABEL="macos_intel" ;;
  *)                    ARCH_LABEL="$RUST_TARGET" ;;
esac

VERSION_US="v${VERSION//./_}"
DMG_PATH="$DMG_DIR/NetFerry_${ARCH_LABEL}_${VERSION_US}.dmg"

echo "==> Creating DMG: $DMG_PATH"
# Create a temporary folder with the .app and an Applications symlink
TMP_DMG_SRC=$(mktemp -d)
cp -R "$APP_PATH" "$TMP_DMG_SRC/"
ln -s /Applications "$TMP_DMG_SRC/Applications"

hdiutil create \
  -volname "NetFerry" \
  -srcfolder "$TMP_DMG_SRC" \
  -ov -format UDZO \
  "$DMG_PATH"
rm -rf "$TMP_DMG_SRC"

# ── Sign DMG ──────────────────────────────────────────────────────────────────
echo "==> Signing DMG"
codesign --force --sign "$APPLE_SIGNING_IDENTITY" "$DMG_PATH"

echo ""
echo "==> Done!"
echo "    DMG: $DMG_PATH"
