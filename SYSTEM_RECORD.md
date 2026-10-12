# TestCloud P2P Mesh & Rendezvous System Record

**Date**: October 3, 2026  
**Status**: Production Ready & Fully Verified  
**Public Gateway & Discovery Rendezvous**: `https://stiqrcloud.stiqrnotes.com`  
**Pi Server LAN Address**: `http://192.168.1.50:8100`  
**Repository**: `./testcloud`

---

## 1. Executive Summary & Architecture

TestCloud is a database-first, decentralized P2P storage mesh with Reticulum Network Stack (RNS) compatibility and Lightning escrow settlement. 

```mermaid
graph TD
    subgraph WAN [Public Internet]
        Friend["Friend Linux VM / Windows PC<br/>(Edge Node - NAT / Zero Port Forwarding)"]
        CF["Cloudflare Tunnel Edge<br/>stiqrcloud.stiqrnotes.com"]
    end

    subgraph Home_LAN [Home Network (10.0.0.0/24)]
        Pi["Raspberry Pi Server (192.168.1.50)<br/>stiqrcloud.service (Port 8100)<br/>Discovery Rendezvous & Public Gateway"]
        WS["Workstation PC (192.168.1.100)<br/>6-Node Multi-Region Quorum Cluster<br/>Alice, Bob, Charlie, Daiki, Elena, Finn"]
    end

    Friend -->|Outbound HTTPS / Discovery| CF
    CF -->|Cloudflared Tunnel| Pi
    WS -->|Local Peering & Discovery| Pi
    Friend -.->|RNS Mesh Gossip / Discovery| Pi
```

---

## 2. One-Line Install & Run Commands for Friends

Zero port forwarding is required. Edge nodes connect outbound to the discovery rendezvous at `stiqrcloud.stiqrnotes.com`.

### Linux (VM, WSL, PC, Server)
```bash
curl -sSL https://stiqrcloud.stiqrnotes.com/install.sh | bash
```

### Windows (PowerShell)
```powershell
iwr -useb https://stiqrcloud.stiqrnotes.com/install.ps1 | iex
```

### Standalone Binary Execution
```bash
curl -sSL https://stiqrcloud.stiqrnotes.com/download/testcloud -o testcloud && chmod +x testcloud && ./testcloud -discovery-url "https://stiqrcloud.stiqrnotes.com" -name "FriendsNode"
```

---

## 3. Raspberry Pi Server Configuration

- **Host**: `192.168.1.50` (`node-operator@192.168.1.50`, user `stiqr`)
- **Systemd Service**: `/etc/systemd/system/stiqrcloud.service`
- **Application Directory**: `/opt/stiqrcloud`
- **Binary**: `/opt/stiqrcloud/stiqrcloud` (Linux ARM64 static binary)
- **Data Store**: `/opt/stiqrcloud/data`
- **Releases Mirror**: `/opt/stiqrhub/releases_storage`

### Service Unit Definition
```ini
[Unit]
Description=StiQRcloud Decentralized Node Daemon
After=network.target

[Service]
Type=simple
User=stiqr
WorkingDirectory=/opt/stiqrcloud
ExecStart=/opt/stiqrcloud/stiqrcloud \
  -port 8100 \
  -dashboard-port 8101 \
  -p2p-port 9100 \
  -public-addr "https://stiqrcloud.stiqrnotes.com" \
  -gateway-url "https://stiqrcloud.stiqrnotes.com" \
  -name "Pi-Server" \
  -location "Living Room" \
  -discovery-url "https://stiqrcloud.stiqrnotes.com" \
  -data-dir "/opt/stiqrcloud/data"
Restart=always
RestartSec=5
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
```

### Management Commands
```bash
# Check service status
ssh node-operator@192.168.1.50 "sudo systemctl status stiqrcloud"

# Restart node service
ssh node-operator@192.168.1.50 "sudo systemctl restart stiqrcloud"

# View real-time logs
ssh node-operator@192.168.1.50 "journalctl -u stiqrcloud -f"
```

---

## 4. Workstation Multi-Region Cluster (`192.168.1.100`)

Simulates a 6-node geographically dispersed quorum across 6 continents:

| Node | Name | Simulated WAN IP | Role | LAN HTTP Port | Reticulum Port | Data Directory |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| **Node 1** | Alice | `104.200.134.8` (Dallas, TX) | Public Auditor 1 | `8201` (Dash: `8101`) | `4242` | `data/cluster/node1` |
| **Node 2** | Bob | `199.195.250.77` (New York, NY) | Auditor 2 | `8202` | `4243` | `data/cluster/node2` |
| **Node 3** | Charlie | `51.89.151.107` (London, UK) | Auditor 3 | `8203` | `4244` | `data/cluster/node3` |
| **Node 4** | Daiki | `133.242.180.12` (Tokyo, JP) | Storage Node 1 | `8204` | `4245` | `data/cluster/node4` |
| **Node 5** | Elena | `185.220.100.255` (Frankfurt, DE)| Storage Node 2 | `8205` | `4246` | `data/cluster/node5` |
| **Node 6** | Finn | `13.70.151.12` (Sydney, AU) | Storage Node 3 | `8206` | `4247` | `data/cluster/node6` |

### Cluster Operations
```bash
# Start 6-node cluster
./testcloud/scripts/start_cluster.sh

# Stop all local nodes
pkill -9 testcloud
```

---

## 5. Critical Protocol & Stability Fixes Applied

### 1. Peer Status Flapping & Loopback Collision
- **Cause**: Nodes running on default settings advertised `http://0.0.0.0:8100`. The Pi probed this address, hitting its own local listener, detecting a NodeID mismatch, and marking the peer offline. Passive gossip and periodic announcements then flipped it back online, creating continuous flapping.
- **Fix**:
  - `cmd/testcloud/main.go`: Default `listenAddr` is empty (`""`) when binding `0.0.0.0` without an explicit `-public-addr` or `-gateway-url`.
  - `pkg/gateway/server.go` & `pkg/p2p/swarm.go`: Loopback/zero addresses (`0.0.0.0`, `127.0.0.1`, `localhost`) are automatically stripped.
  - `ProbePeers()`: If a probe detects a NodeID mismatch (self-loopback or port collision), it immediately clears `p.PublicAddr = ""` and marks the peer offline.

### 2. Edge Node Heartbeat Support (NAT / Zero Port Forwarding)
- For nodes behind NAT with `PublicAddr == ""`, direct HTTP probing is skipped.
- Online status is maintained via periodic outbound discovery announcements (every 3s).
- If announcements cease for > 15s, the node cleanly transitions to `is_online: false`.
- If offline for > 30s, ephemeral edge nodes are automatically pruned from the peer table.

### 3. Separation of Direct Peering vs. Passive Gossip
- Added `RegisterDirectPeer(p)` for direct peer-to-peer contacts and discovery announcements.
- Passive gossip (`RegisterPeer(p)`) from third-party peers updates metadata but is prevented from resurrecting dead or offline nodes.

### 4. Runaway Replication & Resource Exhaustion Fix
- **Cause**: Every 3-second discovery announce was triggering `go s.ReconcileAndReplicateAll()`, scanning thousands of records and spawning concurrent HTTP backups, causing 200% CPU spikes and freezing the Pi.
- **Fix**:
  - Added atomic single-flight locking (`reconciling atomic.Bool`).
  - Throttled reconciliation to a maximum of 10 records per cycle.
  - Removed reconciliation invocation from the 3-second heartbeat handler.

### 5. Edge Node NAT Pull Sync & Content-Addressed Chunk Distribution
- **Cause**: Edge nodes behind residential NAT/firewalls (such as friend VMs or laptops without port forwarding) advertised empty `PublicAddr = ""` to prevent port conflicts. Push-only replication (`POST /api/internal/store`) skipped them, leaving newly joined friend nodes with 0 records.
- **Fix**:
  - `GET /api/sync/records`: Gateway endpoint supporting `peer_id`, `since`, and `limit`, with dynamic quorum backfill for new edge nodes.
  - `GET /api/chunks/{hash}`: Direct content-addressed chunk retrieval for raw file and picture blobs.
  - `SyncRecordsFromPeers()` & `StartSyncLoop()`: Autonomous client sync routine in `pkg/p2p/swarm.go` running on boot and every 10s over outbound HTTPS.
  - Idempotent chunk caching: Nodes verify local chunk hashes (`store.HasChunk`) to skip redundant downloads.
### 6. Local-Only Operator Dashboard & Administrative API Protection
- **Cause**: Previously, the web dashboard (`/dashboard`), telemetry (`/api/dashboard/*`), and policy controls (`/api/escrow/*`, `/api/storage/quota`) were served over the same HTTP handler as public gateway routes. When tunneled via Cloudflare (`stiqrcloud.stiqrnotes.com`), the operator dashboard was exposed to anyone on the public internet, posing an authorization and configuration tampering hazard.
- **Fix**:
  - Implemented `isLocalRequest(r *http.Request)` and `localOnly` access control in [`pkg/gateway/server.go`](pkg/gateway/server.go).
  - Explicitly rejects any request with Cloudflare headers (`CF-Ray`, `CF-Connecting-IP`, `CF-Visitor`), public `Host` headers (`stiqrcloud.stiqrnotes.com`), or non-private IP addresses with `403 Forbidden`.
  - Root path (`/`) now returns public gateway metadata JSON over public WAN and only redirects to `/dashboard` for local LAN requests.
  - Operator dashboard remains 100% accessible locally via `http://192.168.1.50:8100/dashboard` or `http://localhost:8100/dashboard`.
  - Unit tests verified in `TestDashboardLocalOnlyProtection` and deployed live to Raspberry Pi `192.168.1.50`.

---

## 6. Verification Endpoints

```bash
# Public Gateway health check (Open to WAN)
curl -sSL https://stiqrcloud.stiqrnotes.com/api/health

# Connected peer list (Open to WAN)
curl -sSL https://stiqrcloud.stiqrnotes.com/api/peers

# Public Gateway root (Returns JSON metadata, NO redirect)
curl -sSL https://stiqrcloud.stiqrnotes.com/

# Public dashboard access check (Returns 403 Forbidden as expected)
curl -I https://stiqrcloud.stiqrnotes.com/dashboard

# Private Operator Dashboard (Local LAN ONLY)
http://192.168.1.50:8100/dashboard
```
