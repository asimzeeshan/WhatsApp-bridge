#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
PID_FILE="$PROJECT_DIR/data/bridge.pid"

if [[ -f "$PID_FILE" ]]; then
    PID=$(cat "$PID_FILE")
    if kill -0 "$PID" 2>/dev/null; then
        echo "Stopping bridge (PID: $PID)..."
        kill "$PID"
        rm -f "$PID_FILE"
        echo "Bridge stopped."
    else
        echo "Bridge not running (stale PID file)."
        rm -f "$PID_FILE"
    fi
else
    echo "No PID file found. Bridge may not be running."
fi
