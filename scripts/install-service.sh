#!/bin/bash
# Install or uninstall the WhatsApp bridge as a macOS launchd service.
# Usage:
#   ./scripts/install-service.sh install   - Build, install, and start
#   ./scripts/install-service.sh uninstall - Stop and uninstall
#   ./scripts/install-service.sh status    - Check service status
#   ./scripts/install-service.sh restart   - Restart the service
#   ./scripts/install-service.sh logs      - Tail the service logs

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
PLIST_NAME="com.eva.whatsapp-bridge"
PLIST_SRC="$PROJECT_DIR/$PLIST_NAME.plist"
PLIST_DST="$HOME/Library/LaunchAgents/$PLIST_NAME.plist"
BINARY="$PROJECT_DIR/bin/whatsapp-bridge"

case "${1:-status}" in
    install)
        echo "Building bridge binary..."
        cd "$PROJECT_DIR" && make build-bridge

        if [ ! -f "$BINARY" ]; then
            echo "ERROR: Binary not found at $BINARY"
            exit 1
        fi

        # Stop existing service if running
        launchctl bootout "gui/$(id -u)/$PLIST_NAME" 2>/dev/null || true

        # Create log directory
        mkdir -p "$PROJECT_DIR/logs"

        # Copy plist to LaunchAgents
        cp "$PLIST_SRC" "$PLIST_DST"
        echo "Installed plist to $PLIST_DST"

        # Load and start
        launchctl bootstrap "gui/$(id -u)" "$PLIST_DST"
        echo "Service started."

        # Wait and check
        sleep 3
        if launchctl print "gui/$(id -u)/$PLIST_NAME" 2>/dev/null | grep -q "state = running"; then
            echo "Bridge is running."
        else
            echo "Checking health..."
            sleep 5
            if curl -sf http://127.0.0.1:8080/api/status > /dev/null 2>&1; then
                echo "Bridge is healthy."
            else
                echo "WARNING: Bridge may not be healthy yet. Check logs:"
                echo "  tail -f $PROJECT_DIR/logs/bridge-launchd-err.log"
            fi
        fi

        echo ""
        echo "The bridge will now:"
        echo "  - Start automatically on login"
        echo "  - Restart automatically on crash"
        echo "  - Survive terminal closures"
        echo "  - Capture all messages, images, links, and reactions 24/7"
        echo ""
        echo "Use './scripts/install-service.sh status' to check."
        echo "Use './scripts/install-service.sh logs' to view logs."
        ;;

    uninstall)
        echo "Stopping and removing service..."
        launchctl bootout "gui/$(id -u)/$PLIST_NAME" 2>/dev/null || true
        rm -f "$PLIST_DST"
        echo "Service uninstalled."
        ;;

    restart)
        echo "Restarting service..."
        launchctl kickstart -k "gui/$(id -u)/$PLIST_NAME" 2>/dev/null || {
            echo "Service not loaded. Run 'install' first."
            exit 1
        }
        echo "Service restarted."
        sleep 3
        if curl -sf http://127.0.0.1:8080/api/status > /dev/null 2>&1; then
            echo "Bridge is healthy."
        else
            echo "Bridge starting up... check logs if it doesn't respond soon."
        fi
        ;;

    status)
        echo "=== WhatsApp Bridge Service ==="
        if launchctl print "gui/$(id -u)/$PLIST_NAME" 2>/dev/null | head -5; then
            echo ""
            echo "API Status:"
            curl -sf http://127.0.0.1:8080/api/status 2>/dev/null | python3 -m json.tool || echo "  API not responding"
        else
            echo "Service is NOT loaded."
            echo "Run './scripts/install-service.sh install' to install."
        fi
        ;;

    logs)
        echo "=== Bridge Logs (Ctrl+C to stop) ==="
        tail -f "$PROJECT_DIR/logs/bridge-launchd.log" "$PROJECT_DIR/logs/bridge-launchd-err.log"
        ;;

    *)
        echo "Usage: $0 {install|uninstall|restart|status|logs}"
        exit 1
        ;;
esac
