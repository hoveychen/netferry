#!/bin/bash
# Integration test: build server, upload to bt-container, verify it starts.
# Usage: ./test_integration.sh

set -e
cd "$(dirname "$0")"

SSH_KEY=~/.ssh/id_rsa
SSH_PORT=22007
SSH_USER=yuheng
SSH_HOST=10.7.0.7
SSH="ssh -i $SSH_KEY -o StrictHostKeyChecking=no -p $SSH_PORT"
SCP="scp -i $SSH_KEY -o StrictHostKeyChecking=no -P $SSH_PORT"
REMOTE_DIR=".cache/netferry"
REMOTE_BIN="$REMOTE_DIR/server-test"
LOCAL_BIN=/tmp/netferry-server-test

PASS=0
FAIL=0

ok()   { echo "OK:   $*"; PASS=$((PASS+1)); }
fail() { echo "FAIL: $*"; FAIL=$((FAIL+1)); }

echo "=== Building server binary (linux/amd64) ==="
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o "$LOCAL_BIN" ./cmd/server/
echo "built $(wc -c < "$LOCAL_BIN" | tr -d ' ') bytes"

echo "=== Checking SSH connectivity ==="
if ! $SSH $SSH_USER@$SSH_HOST "echo connected" >/dev/null 2>&1; then
    echo "FAIL: cannot reach $SSH_HOST:$SSH_PORT as $SSH_USER"
    exit 1
fi
ok "SSH connectivity"

echo "=== Uploading server binary ==="
$SSH $SSH_USER@$SSH_HOST "mkdir -p $REMOTE_DIR"
$SCP "$LOCAL_BIN" "$SSH_USER@$SSH_HOST:$REMOTE_BIN"
$SSH $SSH_USER@$SSH_HOST "chmod +x $REMOTE_BIN"
ok "binary uploaded"

echo "=== Testing server: --version ==="
VERSION=$($SSH $SSH_USER@$SSH_HOST "$REMOTE_BIN --version" 2>/dev/null)
echo "  version: '$VERSION'"
if [ -n "$VERSION" ]; then
    ok "--version outputs: $VERSION"
else
    fail "--version produced no output"
fi

echo "=== Testing server: sync header + CMD_ROUTES ==="
# Keep stdin open via 'sleep 5' so the server's reader blocks (not EOF),
# giving the writer goroutine time to flush the CMD_ROUTES frame.
# Server writes: \x00\x00SSHUTTLE0001 (14 bytes) + CMD_ROUTES frame (8 bytes) = 22 bytes min.
(sleep 5) | $SSH $SSH_USER@$SSH_HOST "$REMOTE_BIN" > /tmp/srv_out.bin 2>/dev/null &
SSH_PID=$!
sleep 2
kill $SSH_PID 2>/dev/null || true
wait $SSH_PID 2>/dev/null || true

STDOUT_BYTES=$(wc -c < /tmp/srv_out.bin | tr -d ' ')
echo "  stdout bytes: $STDOUT_BYTES"

if [ "$STDOUT_BYTES" -ge 22 ]; then
    ok "server wrote $STDOUT_BYTES bytes (sync header + routes)"
else
    fail "server only wrote $STDOUT_BYTES bytes (expected >= 22)"
fi

# Verify sync header bytes: \x00\x00SSHUTTLE0001 = 000053534855544c453030303131
SYNC_HEX=$(head -c 14 /tmp/srv_out.bin | xxd -p | tr -d '\n')
EXPECTED="00005353485554544c4530303031"
echo "  sync header hex: $SYNC_HEX"
echo "  expected:        $EXPECTED"
if [ "$SYNC_HEX" = "$EXPECTED" ]; then
    ok "sync header matches"
else
    fail "sync header mismatch"
fi

# Check frame magic bytes SS at offset 14
FRAME_MAGIC=$(dd if=/tmp/srv_out.bin bs=1 skip=14 count=2 2>/dev/null | xxd -p)
echo "  frame magic at offset 14: $FRAME_MAGIC"
if [ "$FRAME_MAGIC" = "5353" ]; then
    ok "CMD_ROUTES frame has correct SS magic"
else
    fail "CMD_ROUTES frame missing SS magic (got: $FRAME_MAGIC)"
fi

echo "=== Cleaning up ==="
$SSH $SSH_USER@$SSH_HOST "rm -f $REMOTE_BIN" 2>/dev/null || true
rm -f "$LOCAL_BIN" /tmp/srv_out.bin

echo ""
echo "=== Results: $PASS passed, $FAIL failed ==="
[ "$FAIL" -eq 0 ]
