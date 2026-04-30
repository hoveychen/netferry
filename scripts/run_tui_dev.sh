#!/usr/bin/env bash
# Build netferry-tunnel and launch the interactive TUI.
#
# - Sources netferry-desktop/.env to pick up NETFERRY_EXPORT_KEY so the TUI
#   can decrypt .nfprofile imports.
# - Set NETFERRY_DATA_DIR to a scratch path to isolate the run from your real
#   desktop store (otherwise edits land in ~/Library/Application Support/...).
# - Pass extra tunnel flags after a `--` separator.
#
# Examples:
#   bash scripts/run_tui_dev.sh
#   NETFERRY_DATA_DIR=/tmp/nf-tui bash scripts/run_tui_dev.sh
#   bash scripts/run_tui_dev.sh -- -v

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
RELAY_DIR="$REPO_ROOT/netferry-relay"
ENV_FILE="$REPO_ROOT/netferry-desktop/.env"

if [[ -f "$ENV_FILE" ]]; then
  set -a
  # shellcheck disable=SC1090
  source "$ENV_FILE"
  set +a
fi

if [[ -z "${NETFERRY_EXPORT_KEY:-}" ]]; then
  echo "warning: NETFERRY_EXPORT_KEY not set — .nfprofile import will fail" >&2
fi

cd "$RELAY_DIR"
echo "==> Building netferry-tunnel"
make build-tunnel >/dev/null

if [[ -n "${NETFERRY_DATA_DIR:-}" ]]; then
  echo "==> NETFERRY_DATA_DIR=$NETFERRY_DATA_DIR (isolated run)"
  mkdir -p "$NETFERRY_DATA_DIR"
fi

EXTRA=()
if [[ $# -gt 0 && "$1" == "--" ]]; then
  shift
  EXTRA=("$@")
fi

exec ./dist/netferry-tunnel --tui ${EXTRA[@]+"${EXTRA[@]}"}
