#!/usr/bin/env bash
echo "=== Stopping Reticulum BLE Bridge ==="
killall -9 rnsd 2>/dev/null || true
echo "Reticulum BLE Bridge stopped."
