#!/usr/bin/env bash
#
# Build NetFerry Android APK from source.
#
# Usage:
#   ./scripts/build-android.sh              # debug APK
#   ./scripts/build-android.sh --release    # release APK (unsigned)
#
# Prerequisites:
#   - Go 1.22+
#   - Android SDK (ANDROID_HOME set)
#   - Android NDK (any version under $ANDROID_HOME/ndk/)
#   - gomobile + gobind:  go install golang.org/x/mobile/cmd/{gomobile,gobind}@latest
#
set -euo pipefail
cd "$(dirname "$0")/.."

# Load .env if present (shared with desktop build).
if [[ -f .env ]]; then
    set -a
    source .env
    set +a
fi

RELAY_DIR="netferry-relay"
MOBILE_DIR="netferry-mobile/android"
AAR_OUT="$MOBILE_DIR/app/libs/netferry-engine.aar"

VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TYPE="Debug"
GRADLE_TASK="assembleDebug"

if [[ "${1:-}" == "--release" ]]; then
    BUILD_TYPE="Release"
    GRADLE_TASK="assembleRelease"
fi

# ── Resolve tools ────────────────────────────────────────────────────────────

GOBIN=$(go env GOPATH)/bin
GOMOBILE="$GOBIN/gomobile"
GOBIND="$GOBIN/gobind"

if [[ ! -x "$GOMOBILE" ]] || [[ ! -x "$GOBIND" ]]; then
    echo "Installing gomobile + gobind..."
    go install golang.org/x/mobile/cmd/gomobile@latest
    go install golang.org/x/mobile/cmd/gobind@latest
fi

# Resolve Android NDK — pick the latest installed version.
ANDROID_HOME="${ANDROID_HOME:-$HOME/Library/Android/sdk}"
if [[ -z "${ANDROID_NDK_HOME:-}" ]]; then
    NDK_DIR=$(ls -d "$ANDROID_HOME/ndk/"*/ 2>/dev/null | sort -V | tail -1)
    if [[ -z "$NDK_DIR" ]]; then
        echo "Error: No Android NDK found under $ANDROID_HOME/ndk/"
        echo "Install one via: sdkmanager --install 'ndk;27.1.12297006'"
        exit 1
    fi
    export ANDROID_NDK_HOME="${NDK_DIR%/}"
fi
echo "NDK: $ANDROID_NDK_HOME"

# ── Step 1: Build server binaries (embedded in Go engine) ────────────────────

echo ""
echo "==> Building server binaries..."
cd "$RELAY_DIR"
make build-servers
cd ..

# Copy server binaries for mobile embed.
mkdir -p "$RELAY_DIR/mobile/binaries"
cp "$RELAY_DIR/cmd/tunnel/binaries/server-linux-amd64" "$RELAY_DIR/mobile/binaries/"
cp "$RELAY_DIR/cmd/tunnel/binaries/server-linux-arm64" "$RELAY_DIR/mobile/binaries/"

# ── Step 2: Build Go mobile AAR ─────────────────────────────────────────────

echo ""
echo "==> Building Go mobile AAR (arm64)..."
mkdir -p "$(dirname "$AAR_OUT")"

MOBILE_LDFLAGS="-X github.com/hoveychen/netferry/relay/mobile.Version=$VERSION -s -w"
if [[ -n "${NETFERRY_EXPORT_KEY:-}" ]]; then
    MOBILE_LDFLAGS="$MOBILE_LDFLAGS -X github.com/hoveychen/netferry/relay/mobile.ExportKey=$NETFERRY_EXPORT_KEY"
    echo "Export key: embedded"
else
    echo "Warning: NETFERRY_EXPORT_KEY not set — QR/file import will be disabled"
fi

( cd "$RELAY_DIR" && PATH="$GOBIN:$PATH" "$GOMOBILE" bind \
    -target=android/arm64 \
    -androidapi 26 \
    -ldflags="$MOBILE_LDFLAGS" \
    -o "../$AAR_OUT" \
    ./mobile/ )

echo "AAR: $AAR_OUT ($(du -h "$AAR_OUT" | cut -f1))"

# ── Step 3: Build APK via Gradle ────────────────────────────────────────────

echo ""
echo "==> Building Android APK ($BUILD_TYPE)..."
cd "$MOBILE_DIR"
./gradlew "$GRADLE_TASK" --no-daemon -q

# Find the output APK.
if [[ "$BUILD_TYPE" == "Release" ]]; then
    APK=$(find app/build/outputs/apk/release -name "*.apk" 2>/dev/null | head -1)
else
    APK=$(find app/build/outputs/apk/debug -name "*.apk" 2>/dev/null | head -1)
fi

echo ""
echo "Done! APK: $MOBILE_DIR/$APK ($(du -h "$APK" | cut -f1))"
