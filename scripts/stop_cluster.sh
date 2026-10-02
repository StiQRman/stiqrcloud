#!/usr/bin/env bash
echo "=== Stopping TestCloud Cluster ==="
pkill -x "testcloud" 2>/dev/null || true
echo "All TestCloud nodes stopped."
