#!/usr/bin/env bash
set -e

BASE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$BASE_DIR/stiqrcloud"
DATA_ROOT="$BASE_DIR/data/cluster"
LOG_ROOT="$BASE_DIR/data/logs"

mkdir -p "$DATA_ROOT" "$LOG_ROOT"

echo "=== Building StiQRcloud Binary ==="
(cd "$BASE_DIR" && go build -o stiqrcloud ./cmd/stiqrcloud)

echo "=== Stopping Any Existing Nodes ==="
pkill -x "stiqrcloud" 2>/dev/null || true
pkill -f "scripts/localhost_proxy.py" 2>/dev/null || true
sleep 0.5

PIDS=()

cleanup() {
  echo ""
  echo "=== Shutting down 6-Node Cluster ==="
  trap - SIGINT SIGTERM SIGHUP EXIT
  kill "${PIDS[@]}" 2>/dev/null || true
  pkill -x "stiqrcloud" 2>/dev/null || true
  exit 0
}

trap cleanup SIGINT SIGTERM SIGHUP EXIT

PC_IP="127.10.1.1"
LOCAL_DISCOVERY="http://127.10.1.1:8101"

echo "=== Starting 6-Node Multi-Region Swarm with Public WAN IPs ==="
echo "Operating mode: 100% Loopback Isolated (0% network/LAN traffic)"
echo "For interactive live monitor with hotkeys, use: ./scripts/cluster.sh"
echo ""

# Node 1: Alice (Dallas, TX - Public Auditor 1)
echo "Starting Node 1: Alice (Dallas, TX | IP: 104.200.134.8) on 127.10.1.1:8101..."
"$BIN" \
  -host 127.10.1.1 \
  -port 8101 \
  -dashboard-port 8101 \
  -rns-port 4242 \
  -node-id node_auditor_alpha \
  -name "Alice" \
  -location "Dallas, TX" \
  -public-ip "104.200.134.8" \
  -public-addr "http://127.10.1.1:8101" \
  -data-dir "$DATA_ROOT/node1" \
  -public \
  > "$LOG_ROOT/node1.log" 2>&1 &
PIDS+=($!)
sleep 0.4

python3 "$BASE_DIR/scripts/localhost_proxy.py" > "$LOG_ROOT/proxy.log" 2>&1 &
PIDS+=($!)

# Node 2: Bob (New York, NY - Co-Auditor 2)
echo "Starting Node 2: Bob (New York, NY | IP: 199.195.250.77) on 127.10.2.1:8101..."
"$BIN" \
  -host 127.10.2.1 \
  -port 8101 \
  -dashboard-port 8101 \
  -rns-port 4243 \
  -node-id node_auditor_beta \
  -name "Bob" \
  -location "New York, NY" \
  -public-ip "199.195.250.77" \
  -public-addr "http://127.10.2.1:8101" \
  -discovery-url "$LOCAL_DISCOVERY" \
  -peers "$LOCAL_DISCOVERY" \
  -data-dir "$DATA_ROOT/node2" \
  > "$LOG_ROOT/node2.log" 2>&1 &
PIDS+=($!)
sleep 0.2

# Node 3: Charlie (London, UK - Co-Auditor 3)
echo "Starting Node 3: Charlie (London, UK | IP: 51.89.151.107) on 127.10.3.1:8101..."
"$BIN" \
  -host 127.10.3.1 \
  -port 8101 \
  -dashboard-port 8101 \
  -rns-port 4244 \
  -node-id node_auditor_gamma \
  -name "Charlie" \
  -location "London, UK" \
  -public-ip "51.89.151.107" \
  -public-addr "http://127.10.3.1:8101" \
  -discovery-url "$LOCAL_DISCOVERY" \
  -peers "$LOCAL_DISCOVERY" \
  -data-dir "$DATA_ROOT/node3" \
  > "$LOG_ROOT/node3.log" 2>&1 &
PIDS+=($!)
sleep 0.2

# Node 4: Daiki (Tokyo, JP - Storage Node 1)
echo "Starting Node 4: Daiki (Tokyo, JP | IP: 133.242.180.12) on 127.10.4.1:8101..."
"$BIN" \
  -host 127.10.4.1 \
  -port 8101 \
  -dashboard-port 8101 \
  -rns-port 4245 \
  -node-id node_storage_delta \
  -name "Daiki" \
  -location "Tokyo, JP" \
  -public-ip "133.242.180.12" \
  -public-addr "http://127.10.4.1:8101" \
  -discovery-url "$LOCAL_DISCOVERY" \
  -peers "$LOCAL_DISCOVERY" \
  -data-dir "$DATA_ROOT/node4" \
  > "$LOG_ROOT/node4.log" 2>&1 &
PIDS+=($!)
sleep 0.2

# Node 5: Elena (Frankfurt, DE - Storage Node 2)
echo "Starting Node 5: Elena (Frankfurt, DE | IP: 185.220.100.255) on 127.10.5.1:8101..."
"$BIN" \
  -host 127.10.5.1 \
  -port 8101 \
  -dashboard-port 8101 \
  -rns-port 4246 \
  -node-id node_storage_epsilon \
  -name "Elena" \
  -location "Frankfurt, DE" \
  -public-ip "185.220.100.255" \
  -public-addr "http://127.10.5.1:8101" \
  -discovery-url "$LOCAL_DISCOVERY" \
  -peers "$LOCAL_DISCOVERY" \
  -data-dir "$DATA_ROOT/node5" \
  > "$LOG_ROOT/node5.log" 2>&1 &
PIDS+=($!)
sleep 0.2

# Node 6: Finn (Sydney, AU - Storage Node 3)
echo "Starting Node 6: Finn (Sydney, AU | IP: 13.70.151.12) on 127.10.6.1:8101..."
"$BIN" \
  -host 127.10.6.1 \
  -port 8101 \
  -dashboard-port 8101 \
  -rns-port 4247 \
  -node-id node_storage_zeta \
  -name "Finn" \
  -location "Sydney, AU" \
  -public-ip "13.70.151.12" \
  -public-addr "http://127.10.6.1:8101" \
  -discovery-url "$LOCAL_DISCOVERY" \
  -peers "$LOCAL_DISCOVERY" \
  -data-dir "$DATA_ROOT/node6" \
  > "$LOG_ROOT/node6.log" 2>&1 &
PIDS+=($!)

sleep 0.5
echo ""
echo "=== All 6 Multi-Region Nodes Running ==="
echo "Node 1 (Alice   - Dallas, TX):    http://127.10.1.1:8101/dashboard  (Simulated WAN: 104.200.134.8)"
echo "Node 2 (Bob     - New York, NY):  http://127.10.2.1:8101/dashboard  (Simulated WAN: 199.195.250.77)"
echo "Node 3 (Charlie - London, UK):    http://127.10.3.1:8101/dashboard  (Simulated WAN: 51.89.151.107)"
echo "Node 4 (Daiki   - Tokyo, JP):     http://127.10.4.1:8101/dashboard  (Simulated WAN: 133.242.180.12)"
echo "Node 5 (Elena   - Frankfurt, DE): http://127.10.5.1:8101/dashboard  (Simulated WAN: 185.220.100.255)"
echo "Node 6 (Finn    - Sydney, AU):    http://127.10.6.1:8101/dashboard  (Simulated WAN: 13.70.151.12)"
echo ""
echo "Local Discovery Rendezvous: $LOCAL_DISCOVERY"
echo "Interactive TUI Console:    ./scripts/cluster.sh"
echo "Cluster is running in background. Press Ctrl+C or kill to stop."

wait
