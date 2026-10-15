#!/usr/bin/env bash
echo "=== Stopping StiQRcloud Cluster ==="
pkill -x "stiqrcloud" 2>/dev/null || true
pkill -f "scripts/localhost_proxy.py" 2>/dev/null || true
echo "All StiQRcloud nodes stopped."
