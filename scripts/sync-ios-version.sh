#!/usr/bin/env bash
#
# Sync iOS bundle version strings from git state.
#
# Run this from anywhere inside the repo before archiving the iOS app.
# It rewrites CFBundleShortVersionString (marketing version) and
# CFBundleVersion (build number) in both Info.plist files used by Xcode:
#
#   netferry-mobile/ios/NetFerry/NetFerry/Info.plist        (main app)
#   netferry-mobile/ios/NetFerry/PacketTunnel/Info.plist    (extension)
#
# The marketing version comes from the latest git tag (leading "v" stripped),
# falling back to `git describe` for untagged commits. The build number is
# the total commit count on HEAD — monotonic, which Apple requires for
# TestFlight / App Store updates.

set -euo pipefail

REPO_ROOT=$(git rev-parse --show-toplevel)
PLISTS=(
    "$REPO_ROOT/netferry-mobile/ios/NetFerry/NetFerry/Info.plist"
    "$REPO_ROOT/netferry-mobile/ios/NetFerry/PacketTunnel/Info.plist"
)

TAG=$(git -C "$REPO_ROOT" describe --tags --abbrev=0 2>/dev/null || true)
if [ -n "$TAG" ]; then
    MARKETING_VERSION="${TAG#v}"
else
    MARKETING_VERSION="0.0.0-dev"
fi
BUILD_NUMBER=$(git -C "$REPO_ROOT" rev-list --count HEAD)

PLIST_BUDDY=/usr/libexec/PlistBuddy

for plist in "${PLISTS[@]}"; do
    if [ ! -f "$plist" ]; then
        echo "warn: $plist not found, skipping" >&2
        continue
    fi
    "$PLIST_BUDDY" -c "Set :CFBundleShortVersionString $MARKETING_VERSION" "$plist"
    "$PLIST_BUDDY" -c "Set :CFBundleVersion $BUILD_NUMBER" "$plist"
    echo "updated $(basename "$(dirname "$plist")")/Info.plist -> $MARKETING_VERSION ($BUILD_NUMBER)"
done
