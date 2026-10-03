#!/usr/bin/env bash
set -e

BASE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$BASE_DIR/testcloud"
DATA_ROOT="$BASE_DIR/data/cluster"
LOG_ROOT="$BASE_DIR/data/logs"

mkdir -p "$DATA_ROOT" "$LOG_ROOT"

if [ ! -f "$BIN" ]; then
  echo "=== Building TestCloud Binary ==="
  (cd "$BASE_DIR" && go build -o testcloud ./cmd/testcloud)
fi

echo "=== Stopping Any Existing Nodes ==="
pkill -x "testcloud" 2>/dev/null || true
sleep 0.2

PIDS=()

cleanup() {
  echo ""
  echo "=== Shutting down 6-Node Cluster ==="
  trap - SIGINT SIGTERM SIGHUP EXIT
  kill "${PIDS[@]}" 2>/dev/null || true
  pkill -x "testcloud" 2>/dev/null || true
  exit 0
}

trap cleanup SIGINT SIGTERM SIGHUP EXIT

echo "=== Starting 6-Node Mesh Network Across Distinct Subnets ==="

# Node 1: Entry Gateway & 1st Auditor (Network 127.10.1.0/24)
echo "Starting Node 1 (Auditor 1) on 127.10.1.1:8101 (RNS: 4242)..."
"$BIN" \
  -host 127.10.1.1 \
  -port 8101 \
  -rns-port 4242 \
  -node-id node_auditor_alpha \
  -data-dir "$DATA_ROOT/node1" \
  -public \
  > "$LOG_ROOT/node1.log" 2>&1 &
PIDS+=($!)
sleep 0.3

# Start localhost proxy so http://localhost:8101/dashboard works in any browser
python3 "$BASE_DIR/scripts/localhost_proxy.py" > "$LOG_ROOT/proxy.log" 2>&1 &
PIDS+=($!)

# Node 2: Co-Auditor 1 (Network 127.10.2.0/24)
echo "Starting Node 2 (Auditor 2) on 127.10.2.1:8101 (RNS: 4242)..."
"$BIN" \
  -host 127.10.2.1 \
  -port 8101 \
  -rns-port 4242 \
  -node-id node_auditor_beta \
  -data-dir "$DATA_ROOT/node2" \
  -peers "http://127.10.1.1:8101" \
  > "$LOG_ROOT/node2.log" 2>&1 &
PIDS+=($!)
sleep 0.2

# Node 3: Co-Auditor 2 (Network 127.10.3.0/24)
echo "Starting Node 3 (Auditor 3) on 127.10.3.1:8101 (RNS: 4242)..."
"$BIN" \
  -host 127.10.3.1 \
  -port 8101 \
  -rns-port 4242 \
  -node-id node_auditor_gamma \
  -data-dir "$DATA_ROOT/node3" \
  -peers "http://127.10.1.1:8101" \
  > "$LOG_ROOT/node3.log" 2>&1 &
PIDS+=($!)
sleep 0.2

# Node 4: Storage Node 1 (Network 127.10.4.0/24)
echo "Starting Node 4 (Storage 1) on 127.10.4.1:8101 (RNS: 4242)..."
"$BIN" \
  -host 127.10.4.1 \
  -port 8101 \
  -rns-port 4242 \
  -node-id node_storage_delta \
  -data-dir "$DATA_ROOT/node4" \
  -peers "http://127.10.1.1:8101" \
  > "$LOG_ROOT/node4.log" 2>&1 &
PIDS+=($!)
sleep 0.2

# Node 5: Storage Node 2 (Network 127.10.5.0/24)
echo "Starting Node 5 (Storage 2) on 127.10.5.1:8101 (RNS: 4242)..."
"$BIN" \
  -host 127.10.5.1 \
  -port 8101 \
  -rns-port 4242 \
  -node-id node_storage_epsilon \
  -data-dir "$DATA_ROOT/node5" \
  -peers "http://127.10.1.1:8101" \
  > "$LOG_ROOT/node5.log" 2>&1 &
PIDS+=($!)
sleep 0.2

# Node 6: Storage Node 3 (Network 127.10.6.0/24)
echo "Starting Node 6 (Storage 3) on 127.10.6.1:8101 (RNS: 4242)..."
"$BIN" \
  -host 127.10.6.1 \
  -port 8101 \
  -rns-port 4242 \
  -node-id node_storage_zeta \
  -data-dir "$DATA_ROOT/node6" \
  -peers "http://127.10.1.1:8101" \
  > "$LOG_ROOT/node6.log" 2>&1 &
PIDS+=($!)

sleep 0.5
echo ""
echo "=== All 6 Nodes Running on Distinct Subnets ==="
echo "Node 1 (Auditor 1): http://127.10.1.1:8101/dashboard (Network: 127.10.1.0/24)"
echo "Node 2 (Auditor 2): http://127.10.2.1:8101/dashboard (Network: 127.10.2.0/24)"
echo "Node 3 (Auditor 3): http://127.10.3.1:8101/dashboard (Network: 127.10.3.0/24)"
echo "Node 4 (Storage 1): http://127.10.4.1:8101/dashboard (Network: 127.10.4.0/24)"
echo "Node 5 (Storage 2): http://127.10.5.1:8101/dashboard (Network: 127.10.5.0/24)"
echo "Node 6 (Storage 3): http://127.10.6.1:8101/dashboard (Network: 127.10.6.0/24)"
echo "Cluster is running in background. Press Ctrl+C or kill to stop."

# Wait for children
wait
