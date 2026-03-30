#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
BIN="$PROJECT_DIR/bin/whatsapp-bridge"
CONFIG="$PROJECT_DIR/config.toml"

# Build if needed
if [[ ! -f "$BIN" ]]; then
    echo "Building bridge..."
    cd "$PROJECT_DIR/bridge" && CGO_ENABLED=1 go build -o "$BIN" .
fi

# Check config
if [[ ! -f "$CONFIG" ]]; then
    echo "Error: config.toml not found. Copy config.example.toml and edit it."
    exit 1
fi

# Launch bridge in background
"$BIN" --config "$CONFIG" &
BRIDGE_PID=$!
echo "Bridge started (PID: $BRIDGE_PID)"

# Wait for health
for i in $(seq 1 10); do
    if curl -sf http://127.0.0.1:8080/api/status > /dev/null 2>&1; then
        echo "Bridge healthy."
        echo "$BRIDGE_PID" > "$PROJECT_DIR/data/bridge.pid"
        exit 0
    fi
    sleep 2
done

echo "Bridge failed health check after 20 seconds."
kill "$BRIDGE_PID" 2>/dev/null
exit 1
