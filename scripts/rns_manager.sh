#!/usr/bin/env bash
# Reticulum Global Mesh & Gateway Manager
# Manages rnsd daemon and monitors public failover gateways

BASE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LOG_FILE="$BASE_DIR/data/logs/rnsd.log"
mkdir -p "$BASE_DIR/data/logs"

case "$1" in
  "start")
    echo "=== Starting Reticulum Daemon (rnsd) via systemd user service ==="
    systemctl --user start rnsd.service
    sleep 2
    export PATH="$HOME/.local/bin:$PATH"
    "$HOME/.local/bin/rnstatus"
    ;;
  "stop")
    echo "=== Stopping Reticulum Daemon ==="
    systemctl --user stop rnsd.service
    echo "Reticulum daemon stopped."
    ;;
  "restart")
    echo "=== Restarting Reticulum Daemon ==="
    systemctl --user restart rnsd.service
    sleep 2
    export PATH="$HOME/.local/bin:$PATH"
    "$HOME/.local/bin/rnstatus"
    ;;
  "status"|"")
    export PATH="$HOME/.local/bin:$PATH"
    if systemctl --user is-active --quiet rnsd.service; then
      echo "=== Reticulum Daemon (rnsd) is RUNNING (systemd user unit) ==="
      "$HOME/.local/bin/rnstatus"
    else
      echo "=== Reticulum Daemon (rnsd) is STOPPED ==="
      echo "Run './scripts/rns_manager.sh start' to launch."
    fi
    ;;
  "logs")
    journalctl --user -u rnsd.service -f
    ;;
  *)
    echo "Usage: $0 [start|stop|restart|status|logs]"
    exit 1
    ;;
esac
