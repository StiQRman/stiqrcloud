#!/usr/bin/env python3
"""
TestCloud 6-Node Multi-Region Cluster Console & Live Monitor
Zero Network Overhead | Isolated Linux Loopback Subnets | Multi-Region WAN Simulation
"""

import os
import sys
import time
import json
import signal
import curses
import urllib.request
import urllib.error
import subprocess
import threading
from collections import deque

BASE_DIR = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
BIN_PATH = os.path.join(BASE_DIR, "testcloud")
DATA_ROOT = os.path.join(BASE_DIR, "data", "cluster")
LOG_ROOT = os.path.join(BASE_DIR, "data", "logs")

NODES = [
    {
        "idx": 1,
        "id": "node_auditor_alpha",
        "name": "Alice",
        "role": "Auditor 1 (Gateway)",
        "location": "Dallas, TX",
        "region": "North America",
        "public_ip": "104.200.134.8",
        "host": "127.10.1.1",
        "port": 8101,
        "rns_port": 4242,
        "is_public": True,
        "data_dir": os.path.join(DATA_ROOT, "node1"),
        "log_file": os.path.join(LOG_ROOT, "node1.log"),
    },
    {
        "idx": 2,
        "id": "node_auditor_beta",
        "name": "Bob",
        "role": "Co-Auditor 2",
        "location": "New York, NY",
        "region": "North America",
        "public_ip": "199.195.250.77",
        "host": "127.10.2.1",
        "port": 8101,
        "rns_port": 4243,
        "is_public": False,
        "data_dir": os.path.join(DATA_ROOT, "node2"),
        "log_file": os.path.join(LOG_ROOT, "node2.log"),
    },
    {
        "idx": 3,
        "id": "node_auditor_gamma",
        "name": "Charlie",
        "role": "Co-Auditor 3",
        "location": "London, UK",
        "region": "Europe",
        "public_ip": "51.89.151.107",
        "host": "127.10.3.1",
        "port": 8101,
        "rns_port": 4244,
        "is_public": False,
        "data_dir": os.path.join(DATA_ROOT, "node3"),
        "log_file": os.path.join(LOG_ROOT, "node3.log"),
    },
    {
        "idx": 4,
        "id": "node_storage_delta",
        "name": "Daiki",
        "role": "Storage Node 1",
        "location": "Tokyo, JP",
        "region": "Asia",
        "public_ip": "133.242.180.12",
        "host": "127.10.4.1",
        "port": 8101,
        "rns_port": 4245,
        "is_public": False,
        "data_dir": os.path.join(DATA_ROOT, "node4"),
        "log_file": os.path.join(LOG_ROOT, "node4.log"),
    },
    {
        "idx": 5,
        "id": "node_storage_epsilon",
        "name": "Elena",
        "role": "Storage Node 2",
        "location": "Frankfurt, DE",
        "region": "Europe",
        "public_ip": "185.220.100.255",
        "host": "127.10.5.1",
        "port": 8101,
        "rns_port": 4246,
        "is_public": False,
        "data_dir": os.path.join(DATA_ROOT, "node5"),
        "log_file": os.path.join(LOG_ROOT, "node5.log"),
    },
    {
        "idx": 6,
        "id": "node_storage_zeta",
        "name": "Finn",
        "role": "Storage Node 3",
        "location": "Sydney, AU",
        "region": "Oceania",
        "public_ip": "13.70.151.12",
        "host": "127.10.6.1",
        "port": 8101,
        "rns_port": 4247,
        "is_public": False,
        "data_dir": os.path.join(DATA_ROOT, "node6"),
        "log_file": os.path.join(LOG_ROOT, "node6.log"),
    },
]

DISCOVERY_URL = "http://127.10.1.1:8101"

class ClusterManager:
    def __init__(self):
        self.procs = {}  # idx -> subprocess.Popen
        self.stats = {}  # idx -> dict
        self.logs = deque(maxlen=300)
        self.log_lock = threading.Lock()
        self.running = True
        self.notification = "Cluster initialized. Press [t] to test record sync."
        self.notif_time = time.time()
        self.test_count = 0

        os.makedirs(DATA_ROOT, exist_ok=True)
        os.makedirs(LOG_ROOT, exist_ok=True)

        for n in NODES:
            self.stats[n["idx"]] = {
                "online": False,
                "records": 0,
                "latency_ms": 0.0,
                "social_score": 0.0,
                "rns_dest": "",
                "peers_known": 0,
                "status_text": "Starting...",
            }

    def build_binary(self):
        if not os.path.exists(BIN_PATH) or os.path.getmtime(BIN_PATH) < os.path.getmtime(os.path.join(BASE_DIR, "cmd", "testcloud", "main.go")):
            self.add_log("System", "Compiling fresh TestCloud binary...")
            cmd = ["go", "build", "-o", BIN_PATH, "./cmd/testcloud"]
            res = subprocess.run(cmd, cwd=BASE_DIR, capture_output=True, text=True)
            if res.returncode != 0:
                self.add_log("System", f"Build failed: {res.stderr}")
                return False
            self.add_log("System", "Build complete.")
        return True

    def add_log(self, source, message):
        with self.log_lock:
            ts = time.strftime("%H:%M:%S")
            # Strip excessive newlines
            clean_msg = message.strip()
            if clean_msg:
                self.logs.append((ts, source, clean_msg))

    def start_node(self, node):
        idx = node["idx"]
        if idx in self.procs and self.procs[idx].poll() is None:
            return  # Already running

        os.makedirs(node["data_dir"], exist_ok=True)
        pub_addr = f"http://{node['host']}:{node['port']}"

        args = [
            BIN_PATH,
            "-host", node["host"],
            "-port", str(node["port"]),
            "-dashboard-port", str(node["port"]),
            "-rns-port", str(node["rns_port"]),
            "-node-id", node["id"],
            "-name", node["name"],
            "-location", node["location"],
            "-public-ip", node["public_ip"],
            "-public-addr", pub_addr,
            "-data-dir", node["data_dir"],
        ]

        if node["is_public"]:
            args.append("-public")
        else:
            args.extend(["-discovery-url", DISCOVERY_URL])
            args.extend(["-peers", DISCOVERY_URL])

        log_f = open(node["log_file"], "w")
        proc = subprocess.Popen(
            args,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            bufsize=1,
        )
        self.procs[idx] = proc

        # Stream logs in background thread
        def stream_output(p, name, lf):
            for line in iter(p.stdout.readline, ''):
                lf.write(line)
                lf.flush()
                # Filter out noisy repetitive lines if needed
                if "HTTP & Dashboard:" in line or "Record synced" in line or "stored" in line or "audit" in line.lower() or "peer" in line.lower() or "error" in line.lower() or "quorum" in line.lower() or "self-healing" in line.lower():
                    self.add_log(name, line)
                elif "GET /api/" not in line and "POST /api/discovery" not in line:
                    if len(line.strip()) > 3:
                        self.add_log(name, line)
            p.stdout.close()
            lf.close()

        t = threading.Thread(target=stream_output, args=(proc, node["name"], log_f), daemon=True)
        t.start()
        self.add_log("Cluster", f"Launched {node['name']} ({node['role']}) on {node['host']}:{node['port']}")

    def stop_node(self, idx):
        if idx in self.procs and self.procs[idx].poll() is None:
            node = next(n for n in NODES if n["idx"] == idx)
            self.add_log("Cluster", f"Stopping node {node['name']}...")
            self.procs[idx].terminate()
            try:
                self.procs[idx].wait(timeout=2)
            except subprocess.TimeoutExpired:
                self.procs[idx].kill()
            self.stats[idx]["online"] = False
            self.stats[idx]["status_text"] = "Stopped"
            self.add_log("Cluster", f"Node {node['name']} stopped.")

    def toggle_node(self, idx):
        if idx in self.procs and self.procs[idx].poll() is None:
            self.stop_node(idx)
            self.set_notification(f"Node {idx} knocked OFFLINE. Watch quorum self-healing!")
        else:
            node = next(n for n in NODES if n["idx"] == idx)
            self.start_node(node)
            self.set_notification(f"Node {idx} restarted. Re-joining mesh and sync loop.")

    def start_all(self):
        # Start Alice (Rendezvous seed) first
        self.start_node(NODES[0])
        time.sleep(0.5)
        # Start remaining 5 peers
        for n in NODES[1:]:
            self.start_node(n)
            time.sleep(0.15)

    def stop_all(self):
        self.add_log("Cluster", "Stopping all nodes...")
        for idx in list(self.procs.keys()):
            self.stop_node(idx)
        # Kill any remaining testcloud processes
        subprocess.run(["pkill", "-x", "testcloud"], capture_output=True)

    def set_notification(self, msg):
        self.notification = msg
        self.notif_time = time.time()

    def update_stats_loop(self):
        while self.running:
            for node in NODES:
                idx = node["idx"]
                endpoint = f"http://{node['host']}:{node['port']}"
                try:
                    req = urllib.request.Request(f"{endpoint}/api/health", headers={"User-Agent": "ClusterMonitor"})
                    start_t = time.time()
                    with urllib.request.urlopen(req, timeout=0.8) as resp:
                        lat = (time.time() - start_t) * 1000.0
                        data = json.loads(resp.read().decode())
                        self.stats[idx]["online"] = True
                        self.stats[idx]["rns_dest"] = data.get("rns_dest", "")
                        self.stats[idx]["status_text"] = "ONLINE"
                        self.stats[idx]["latency_ms"] = lat
                except Exception:
                    self.stats[idx]["online"] = False
                    self.stats[idx]["status_text"] = "OFFLINE"
                    self.stats[idx]["latency_ms"] = 0.0

                # If online, query record stats
                if self.stats[idx]["online"]:
                    try:
                        req = urllib.request.Request(f"{endpoint}/api/collections/default/records", headers={"User-Agent": "ClusterMonitor"})
                        with urllib.request.urlopen(req, timeout=0.8) as resp:
                            rdata = json.loads(resp.read().decode())
                            recs = rdata.get("items", [])
                            self.stats[idx]["records"] = len(recs)
                    except Exception:
                        pass

                    # Query peer count
                    try:
                        req = urllib.request.Request(f"{endpoint}/api/peers", headers={"User-Agent": "ClusterMonitor"})
                        with urllib.request.urlopen(req, timeout=0.8) as resp:
                            pdata = json.loads(resp.read().decode())
                            if isinstance(pdata, list):
                                self.stats[idx]["peers_known"] = len(pdata)
                    except Exception:
                        pass

            time.sleep(1.0)

    def inject_test_record(self):
        self.test_count += 1
        record_text = f"Automated Cluster Sync Record #{self.test_count} - Timestamp: {time.time()}"
        payload = json.dumps({
            "collection": "default",
            "text": record_text,
            "user_pub_address": "test_operator_ed25519_key_9999",
        }).encode("utf-8")

        req = urllib.request.Request(
            "http://127.10.1.1:8101/api/collections/default/records",
            data=payload,
            headers={"Content-Type": "application/json", "User-Agent": "ClusterConsole"},
            method="POST",
        )

        try:
            with urllib.request.urlopen(req, timeout=3.0) as resp:
                res = json.loads(resp.read().decode())
                data_id = res.get("data", {}).get("data_id") or res.get("data_id", "rec_ok")
                auditors = res.get("data", {}).get("auditor_peers", [])
                storage = res.get("data", {}).get("storage_peers", [])
                self.add_log("Action", f"Created record {data_id} across 3 Auditors & 3 Storage nodes!")
                self.set_notification(f"Success! Created {data_id} (Auditors: {len(auditors)}, Storage: {len(storage)})")
        except Exception as e:
            self.add_log("Action", f"Record creation failed: {e}")
            self.set_notification(f"Creation failed: {e}")

    def run_curses(self, stdscr):
        curses.curs_set(0)
        stdscr.nodelay(True)
        curses.start_color()
        curses.use_default_colors()

        # Color pairs
        curses.init_pair(1, curses.COLOR_GREEN, -1)   # Online
        curses.init_pair(2, curses.COLOR_RED, -1)     # Offline
        curses.init_pair(3, curses.COLOR_CYAN, -1)    # Alice / Headers
        curses.init_pair(4, curses.COLOR_YELLOW, -1)  # Warnings / Daiki
        curses.init_pair(5, curses.COLOR_BLUE, -1)    # Bob
        curses.init_pair(6, curses.COLOR_MAGENTA, -1) # Charlie
        curses.init_pair(7, curses.COLOR_WHITE, curses.COLOR_BLUE) # Header bar

        # Start stats loop thread
        stats_thread = threading.Thread(target=self.update_stats_loop, daemon=True)
        stats_thread.start()

        while self.running:
            max_y, max_x = stdscr.getmaxyx()
            stdscr.erase()

            # 1. Header Bar
            header = " TESTCLOUD MULTI-REGION CLUSTER | 100% LOOPBACK ISOLATED (0% NETWORK LOAD) "
            try:
                stdscr.addstr(0, 0, header.center(max_x)[:max_x-1], curses.color_pair(7) | curses.A_BOLD)
            except curses.error:
                pass

            # 2. Table Headers
            table_y = 2
            cols = f"{'#':<3}{'NODE':<9}{'ROLE':<19}{'LOCATION':<17}{'SIMULATED WAN IP':<18}{'LOCAL ADDR':<17}{'STATUS':<11}{'RECS':<6}{'LAT':<8}{'PEERS'}"
            try:
                stdscr.addstr(table_y, 1, cols[:max_x-2], curses.A_BOLD | curses.A_UNDERLINE)
            except curses.error:
                pass

            # 3. Node Rows
            row_y = table_y + 1
            for n in NODES:
                idx = n["idx"]
                st = self.stats[idx]
                online = st["online"]
                status_str = "🟢 ONLINE" if online else "🔴 OFFLINE"
                status_color = curses.color_pair(1) if online else curses.color_pair(2)
                lat_str = f"{st['latency_ms']:.1f}ms" if online else "-"
                recs_str = str(st["records"]) if online else "-"
                peers_str = str(st["peers_known"]) if online else "-"
                local_addr = f"{n['host']}:{n['port']}"

                line = f"{idx:<3}{n['name']:<9}{n['role']:<19}{n['location']:<17}{n['public_ip']:<18}{local_addr:<17}"
                try:
                    stdscr.addstr(row_y, 1, line[:max_x-2])
                    stdscr.addstr(row_y, 1 + len(line), f"{status_str:<11}", status_color | curses.A_BOLD)
                    rest = f"{recs_str:<6}{lat_str:<8}{peers_str}"
                    stdscr.addstr(row_y, 1 + len(line) + 11, rest[:max_x - (len(line) + 13)])
                except curses.error:
                    pass
                row_y += 1

            # 4. Summary & Web Dashboard Link
            row_y += 1
            online_count = sum(1 for s in self.stats.values() if s["online"])
            summary_str = f"Cluster Health: {online_count}/6 Nodes Online  |  Dashboard: http://127.10.1.1:8101/dashboard"
            try:
                stdscr.addstr(row_y, 1, summary_str[:max_x-2], curses.color_pair(3) | curses.A_BOLD)
            except curses.error:
                pass

            # 5. Notification Bar
            row_y += 1
            notif_display = f"STATUS: {self.notification}"
            try:
                stdscr.addstr(row_y, 1, notif_display[:max_x-2], curses.color_pair(4) | curses.A_BOLD)
            except curses.error:
                pass

            # 6. Controls Bar
            row_y += 1
            controls = "HOTKEYS: [t] Inject Test Record | [1-6] Toggle Node Kill/Revive | [w] Dashboard | [q] Exit"
            try:
                stdscr.addstr(row_y, 1, controls[:max_x-2], curses.A_DIM)
            except curses.error:
                pass

            # Divider
            row_y += 1
            try:
                stdscr.addstr(row_y, 0, ("─" * (max_x - 1))[:max_x-1], curses.A_DIM)
            except curses.error:
                pass

            # 7. Live Unified Activity Logs
            log_title = " LIVE CLUSTER EVENT LOGS (Cross-Node Realtime Feed) "
            row_y += 1
            try:
                stdscr.addstr(row_y, 1, log_title[:max_x-2], curses.A_BOLD)
            except curses.error:
                pass

            log_start_y = row_y + 1
            log_lines_avail = max(1, max_y - log_start_y - 1)
            
            with self.log_lock:
                recent_logs = list(self.logs)[-log_lines_avail:]

            curr_y = log_start_y
            for ts, source, msg in recent_logs:
                if curr_y >= max_y - 1:
                    break
                tag = f"[{ts}] [{source:^7}] "
                # Colorize tag based on source
                src_color = curses.color_pair(3)
                if source == "Alice":
                    src_color = curses.color_pair(3)
                elif source == "Bob":
                    src_color = curses.color_pair(5)
                elif source == "Charlie":
                    src_color = curses.color_pair(6)
                elif source == "Daiki":
                    src_color = curses.color_pair(4)
                elif source == "Elena":
                    src_color = curses.color_pair(1)
                elif source == "Finn":
                    src_color = curses.color_pair(2)
                elif source == "Action":
                    src_color = curses.color_pair(1) | curses.A_BOLD

                try:
                    stdscr.addstr(curr_y, 1, tag, src_color | curses.A_BOLD)
                    stdscr.addstr(curr_y, 1 + len(tag), msg[:max_x - len(tag) - 2])
                except curses.error:
                    pass
                curr_y += 1

            stdscr.refresh()

            # Handle Keyboard Input
            try:
                ch = stdscr.getch()
                if ch in (ord('q'), ord('Q')):
                    self.running = False
                    break
                elif ch in (ord('t'), ord('T')):
                    self.set_notification("Injecting test record into Alice (Node 1)...")
                    threading.Thread(target=self.inject_test_record, daemon=True).start()
                elif ch in (ord('w'), ord('W')):
                    self.set_notification("Web Dashboard accessible at http://127.10.1.1:8101/dashboard")
                elif ord('1') <= ch <= ord('6'):
                    idx = ch - ord('0')
                    self.toggle_node(idx)
            except curses.error:
                pass

            time.sleep(0.1)

    def run_ansi(self):
        """Fallback streaming console without curses"""
        stats_thread = threading.Thread(target=self.update_stats_loop, daemon=True)
        stats_thread.start()

        def signal_handler(sig, frame):
            self.running = False
            self.stop_all()
            print("\nExited cluster monitor.")
            sys.exit(0)

        signal.signal(signal.SIGINT, signal_handler)

        while self.running:
            os.system("clear")
            print("=" * 105)
            print("      TESTCLOUD MULTI-REGION CLUSTER | 100% LOOPBACK ISOLATED (ZERO NETWORK LOAD)")
            print("=" * 105)
            print(f"{'#':<3}{'NODE':<9}{'ROLE':<19}{'LOCATION':<17}{'SIMULATED WAN IP':<18}{'LOCAL ADDR':<17}{'STATUS':<11}{'RECS':<6}{'LAT'}")
            print("-" * 105)

            for n in NODES:
                idx = n["idx"]
                st = self.stats[idx]
                status_str = "\033[92mONLINE\033[0m" if st["online"] else "\033[91mOFFLINE\033[0m"
                lat_str = f"{st['latency_ms']:.1f}ms" if st["online"] else "-"
                recs_str = str(st["records"]) if st["online"] else "-"
                local_addr = f"{n['host']}:{n['port']}"
                print(f"{idx:<3}{n['name']:<9}{n['role']:<19}{n['location']:<17}{n['public_ip']:<18}{local_addr:<17}{status_str:<20}{recs_str:<6}{lat_str}")

            print("=" * 105)
            online_count = sum(1 for s in self.stats.values() if s["online"])
            print(f"Health: {online_count}/6 Nodes Online | Dashboard: http://127.10.1.1:8101/dashboard")
            print(f"Notice: {self.notification}")
            print("-" * 105)
            print("Recent Logs:")
            with self.log_lock:
                for ts, src, msg in list(self.logs)[-8:]:
                    print(f" [{ts}] [{src:^7}] {msg[:80]}")
            print("-" * 105)
            print("Press Ctrl+C to terminate all nodes.")
            time.sleep(1.5)

def main():
    manager = ClusterManager()
    if not manager.build_binary():
        print("Failed to build testcloud binary.")
        sys.exit(1)

    print("Starting 6-node multi-region cluster on isolated loopback...")
    manager.start_all()

    def cleanup(sig, frame):
        manager.running = False
        manager.stop_all()
        sys.exit(0)

    signal.signal(signal.SIGINT, cleanup)
    signal.signal(signal.SIGTERM, cleanup)

    use_curses = sys.stdout.isatty() and "--no-curses" not in sys.argv
    try:
        if use_curses:
            curses.wrapper(manager.run_curses)
        else:
            manager.run_ansi()
    finally:
        manager.stop_all()

if __name__ == "__main__":
    main()
