#!/usr/bin/env bash
set -e

BASE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$BASE_DIR/testcloud"
DATA_ROOT="$BASE_DIR/data/cluster"
LOG_ROOT="$BASE_DIR/data/logs"

mkdir -p "$DATA_ROOT" "$LOG_ROOT"

echo "=== Building TestCloud Binary ==="
(cd "$BASE_DIR" && go build -o testcloud ./cmd/testcloud)

echo "=== Stopping Any Existing Nodes ==="
pkill -x "testcloud" 2>/dev/null || true
pkill -f "scripts/localhost_proxy.py" 2>/dev/null || true
sleep 0.5

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

PC_IP="192.168.1.100"
PI_DISCOVERY="http://192.168.1.50:8100"

echo "=== Starting 6-Node Multi-Region Swarm with Public WAN IPs ==="

# Node 1: Alice (Dallas, TX - Public Auditor 1)
echo "Starting Node 1: Alice (Dallas, TX | IP: 104.200.134.8) on port 8201 / 8101..."
"$BIN" \
  -host 0.0.0.0 \
  -port 8201 \
  -dashboard-port 8101 \
  -rns-port 4242 \
  -node-id node_auditor_alpha \
  -name "Alice" \
  -location "Dallas, TX" \
  -public-ip "104.200.134.8" \
  -public-addr "http://$PC_IP:8201" \
  -discovery-url "$PI_DISCOVERY" \
  -peers "$PI_DISCOVERY" \
  -data-dir "$DATA_ROOT/node1" \
  -public \
  > "$LOG_ROOT/node1.log" 2>&1 &
PIDS+=($!)
sleep 0.3

# Node 2: Bob (New York, NY - Co-Auditor 2)
echo "Starting Node 2: Bob (New York, NY | IP: 199.195.250.77) on port 8202..."
"$BIN" \
  -host 0.0.0.0 \
  -port 8202 \
  -dashboard-port 8202 \
  -rns-port 4243 \
  -node-id node_auditor_beta \
  -name "Bob" \
  -location "New York, NY" \
  -public-ip "199.195.250.77" \
  -public-addr "http://$PC_IP:8202" \
  -discovery-url "$PI_DISCOVERY" \
  -peers "http://$PC_IP:8201,$PI_DISCOVERY" \
  -data-dir "$DATA_ROOT/node2" \
  > "$LOG_ROOT/node2.log" 2>&1 &
PIDS+=($!)
sleep 0.2

# Node 3: Charlie (London, UK - Co-Auditor 3)
echo "Starting Node 3: Charlie (London, UK | IP: 51.89.151.107) on port 8203..."
"$BIN" \
  -host 0.0.0.0 \
  -port 8203 \
  -dashboard-port 8203 \
  -rns-port 4244 \
  -node-id node_auditor_gamma \
  -name "Charlie" \
  -location "London, UK" \
  -public-ip "51.89.151.107" \
  -public-addr "http://$PC_IP:8203" \
  -discovery-url "$PI_DISCOVERY" \
  -peers "http://$PC_IP:8201,$PI_DISCOVERY" \
  -data-dir "$DATA_ROOT/node3" \
  > "$LOG_ROOT/node3.log" 2>&1 &
PIDS+=($!)
sleep 0.2

# Node 4: Daiki (Tokyo, JP - Storage Node 1)
echo "Starting Node 4: Daiki (Tokyo, JP | IP: 133.242.180.12) on port 8204..."
"$BIN" \
  -host 0.0.0.0 \
  -port 8204 \
  -dashboard-port 8204 \
  -rns-port 4245 \
  -node-id node_storage_delta \
  -name "Daiki" \
  -location "Tokyo, JP" \
  -public-ip "133.242.180.12" \
  -public-addr "http://$PC_IP:8204" \
  -discovery-url "$PI_DISCOVERY" \
  -peers "http://$PC_IP:8201,$PI_DISCOVERY" \
  -data-dir "$DATA_ROOT/node4" \
  > "$LOG_ROOT/node4.log" 2>&1 &
PIDS+=($!)
sleep 0.2

# Node 5: Elena (Frankfurt, DE - Storage Node 2)
echo "Starting Node 5: Elena (Frankfurt, DE | IP: 185.220.100.255) on port 8205..."
"$BIN" \
  -host 0.0.0.0 \
  -port 8205 \
  -dashboard-port 8205 \
  -rns-port 4246 \
  -node-id node_storage_epsilon \
  -name "Elena" \
  -location "Frankfurt, DE" \
  -public-ip "185.220.100.255" \
  -public-addr "http://$PC_IP:8205" \
  -discovery-url "$PI_DISCOVERY" \
  -peers "http://$PC_IP:8201,$PI_DISCOVERY" \
  -data-dir "$DATA_ROOT/node5" \
  > "$LOG_ROOT/node5.log" 2>&1 &
PIDS+=($!)
sleep 0.2

# Node 6: Finn (Sydney, AU - Storage Node 3)
echo "Starting Node 6: Finn (Sydney, AU | IP: 13.70.151.12) on port 8206..."
"$BIN" \
  -host 0.0.0.0 \
  -port 8206 \
  -dashboard-port 8206 \
  -rns-port 4247 \
  -node-id node_storage_zeta \
  -name "Finn" \
  -location "Sydney, AU" \
  -public-ip "13.70.151.12" \
  -public-addr "http://$PC_IP:8206" \
  -discovery-url "$PI_DISCOVERY" \
  -peers "http://$PC_IP:8201,$PI_DISCOVERY" \
  -data-dir "$DATA_ROOT/node6" \
  > "$LOG_ROOT/node6.log" 2>&1 &
PIDS+=($!)

sleep 0.5
echo ""
echo "=== All 6 Multi-Region Nodes Running ==="
echo "Node 1 (Alice   - Dallas, TX):    http://localhost:8101/dashboard  (LAN: http://$PC_IP:8201 | WAN IP: 104.200.134.8)"
echo "Node 2 (Bob     - New York, NY):  http://localhost:8202/dashboard  (LAN: http://$PC_IP:8202 | WAN IP: 199.195.250.77)"
echo "Node 3 (Charlie - London, UK):    http://localhost:8203/dashboard  (LAN: http://$PC_IP:8203 | WAN IP: 51.89.151.107)"
echo "Node 4 (Daiki   - Tokyo, JP):     http://localhost:8204/dashboard  (LAN: http://$PC_IP:8204 | WAN IP: 133.242.180.12)"
echo "Node 5 (Elena   - Frankfurt, DE): http://localhost:8205/dashboard  (LAN: http://$PC_IP:8205 | WAN IP: 185.220.100.255)"
echo "Node 6 (Finn    - Sydney, AU):    http://localhost:8206/dashboard  (LAN: http://$PC_IP:8206 | WAN IP: 13.70.151.12)"
echo ""
echo "Pi Discovery Rendezvous: $PI_DISCOVERY"
echo "Cluster is running in background. Press Ctrl+C or kill to stop."

wait
