#!/usr/bin/env bash
# TestCloud Cluster Helper & Launcher
# Controls the 6-Node Multi-Region Loopback Cluster (Zero Network Impact)

set -e

BASE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONSOLE_PY="$BASE_DIR/scripts/cluster_console.py"

case "$1" in
  ""|"console"|"tui")
    chmod +x "$CONSOLE_PY"
    exec "$CONSOLE_PY"
    ;;
  "ansi")
    chmod +x "$CONSOLE_PY"
    exec "$CONSOLE_PY" --no-curses
    ;;
  "stop"|"down")
    echo "Stopping any running testcloud cluster nodes..."
    pkill -x "testcloud" 2>/dev/null || true
    echo "All nodes stopped."
    ;;
  "status")
    echo "=== TestCloud 6-Node Multi-Region Status ==="
    printf "%-8s %-18s %-16s %-16s %-16s %-10s\n" "NODE" "ROLE" "LOCATION" "SIMULATED WAN IP" "ENDPOINT" "STATUS"
    printf -- "--------------------------------------------------------------------------------------------------\n"
    for item in \
      "Alice:Auditor 1 (GW):Dallas, TX:104.200.134.8:http://127.10.1.1:8101" \
      "Bob:Co-Auditor 2:New York, NY:199.195.250.77:http://127.10.2.1:8101" \
      "Charlie:Co-Auditor 3:London, UK:51.89.151.107:http://127.10.3.1:8101" \
      "Daiki:Storage Node 1:Tokyo, JP:133.242.180.12:http://127.10.4.1:8101" \
      "Elena:Storage Node 2:Frankfurt, DE:185.220.100.255:http://127.10.5.1:8101" \
      "Finn:Storage Node 3:Sydney, AU:13.70.151.12:http://127.10.6.1:8101"
    do
      IFS=":" read -r name role loc ip url <<< "$item"
      status=$(curl -s --connect-timeout 0.4 "$url/api/health" | grep -q "testcloud" && echo -e "\033[92mONLINE\033[0m" || echo -e "\033[91mOFFLINE\033[0m")
      printf "%-8s %-18s %-16s %-16s %-16s %-10b\n" "$name" "$role" "$loc" "$ip" "$url" "$status"
    done
    printf -- "--------------------------------------------------------------------------------------------------\n"
    echo "Dashboard: http://127.10.1.1:8101/dashboard"
    ;;
  "test")
    echo "Injecting test record into Alice (Node 1)..."
    curl -s -X POST "http://127.10.1.1:8101/api/collections/default/records" \
      -H "Content-Type: application/json" \
      -d '{"collection":"default","text":"Test record injected via cluster.sh","user_pub_address":"test_pub_999"}' | jq .
    ;;
  *)
    echo "Usage: $0 [console|ansi|status|test|stop]"
    echo "  (default)  Launch interactive curses TUI console"
    echo "  ansi       Launch console in non-curses ANSI mode"
    echo "  status     Check health status of all 6 nodes"
    echo "  test       Inject a test record into Node 1"
    echo "  stop       Stop all cluster nodes"
    exit 1
    ;;
esac
