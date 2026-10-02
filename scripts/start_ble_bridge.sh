#!/usr/bin/env bash
set -e

BASE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LOG_FILE="$BASE_DIR/data/logs/rns_ble_bridge.log"
mkdir -p "$BASE_DIR/data/logs"

echo "=== Starting Reticulum BLE Bridge (Columba Interface) ==="
killall -9 rnsd 2>/dev/null || true
sleep 0.5

export PATH="$HOME/.local/bin:$PATH"
export PYTHONUNBUFFERED=1
exec python3 -u "$HOME/.local/bin/rnsd" -v > "$LOG_FILE" 2>&1
