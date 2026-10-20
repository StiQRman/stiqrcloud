# StiQRcloud P2P Mesh & Rendezvous System Record

**Date**: October 3, 2026  
**Status**: Production Ready & Fully Verified  
**Public Gateway & Discovery Rendezvous**: `https://stiqrcloud.stiqrnotes.com`  
**Pi Server LAN Address**: `http://192.168.1.50:8100`  
**Repository**: `./stiqrcloud`

---

## 1. Executive Summary & Architecture

StiQRcloud is a database-first, decentralized P2P storage mesh with Reticulum Network Stack (RNS) compatibility and Lightning escrow settlement. 

```mermaid
graph TD
    subgraph WAN [Public Internet]
        Friend["Friend Linux VM / Windows PC<br/>(Edge Node - NAT / Zero Port Forwarding)"]
        CF["Cloudflare Tunnel Edge<br/>stiqrcloud.stiqrnotes.com"]
    end

    subgraph Home_LAN [Home Network (192.168.1.0/24)]
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
curl -sSL https://stiqrcloud.stiqrnotes.com/download/stiqrcloud -o stiqrcloud && chmod +x stiqrcloud && ./stiqrcloud -discovery-url "https://stiqrcloud.stiqrnotes.com" -name "FriendsNode"
```

---

## 3. Raspberry Pi Server Configuration

- **Host**: `192.168.1.50` (`node-operator@192.168.1.50`, user `stiqrcloud`)
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
User=stiqrcloud
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
./stiqrcloud/scripts/start_cluster.sh

# Stop all local nodes
pkill -9 stiqrcloud
```

---

## 5. Critical Protocol & Stability Fixes Applied

### 1. Peer Status Flapping & Loopback Collision
- **Cause**: Nodes running on default settings advertised `http://0.0.0.0:8100`. The Pi probed this address, hitting its own local listener, detecting a NodeID mismatch, and marking the peer offline. Passive gossip and periodic announcements then flipped it back online, creating continuous flapping.
- **Fix**:
  - `cmd/stiqrcloud/main.go`: Default `listenAddr` is empty (`""`) when binding `0.0.0.0` without an explicit `-public-addr` or `-gateway-url`.
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

### 7. User-Controlled Data Placement & Storage Routing Policies
- **Problem Solved**: Stored records were previously always pushed into a standard 3-storage-node mesh quorum. App users had no mechanism to keep private data strictly on their own node, or to designate specific trusted peer nodes for backup.
- **Implementation**:
  - Added `ReplicationPolicy` (`"local"`, `"replicated_2"`, `"distributed"`, `"custom"`) and `TargetReplicas` to `storage.Record`:
    - **`local-only` / `local`**: Kept exclusively on user's node. 0 remote storage nodes assigned; zero network broadcast; airgapped from `/api/sync/records` peer pull sync loops (and dynamic backfill).
    - **`replicated-2` / `distributed_2`**: Data is saved on the user's node and replicated to exactly 2 peer storage nodes (dynamic selection via `SelectQuorumWithCounts(3, 2)`).
    - **`custom`**: User supplies explicit `storage_peers` array; backend replicates strictly to those target nodes without overriding.
    - **`full-mesh` / `distributed`**: Standard 3-node storage quorum.
  - Integration verified via `TestStoragePoliciesAndUserControl` in [`pkg/gateway/server_test.go`](pkg/gateway/server_test.go).

### 8. Official TypeScript & JavaScript Client SDK (`@stiqrhub/stiqrcloud`)
- **Location**: [`sdk/ts/`](sdk/ts)
- **Runtime Support**: Works in Node.js 18+, modern web browsers, React, Vue, Svelte, and React Native with zero external runtime dependencies.
- **Core Modules**:
  - **`NodeManager`** ([`sdk/ts/src/node-manager.ts`](sdk/ts/src/node-manager.ts)): Multi-node management allowing app users to connect their own local node (`http://localhost:8100`), add custom self-hosted nodes (Raspberry Pi, VPS, friend's node), switch primary nodes dynamically, and probe health/latency.
  - **`StiQRcloudClient`** ([`sdk/ts/src/client.ts`](sdk/ts/src/client.ts)): Complete client supporting `storagePolicy` (`local-only`, `replicated-2`, `custom`), attachments, and PocketBase-compatible collection API.
  - **`StiQRcloudCrypto`** ([`sdk/ts/src/crypto.ts`](sdk/ts/src/crypto.ts)): Client-side AES-256-GCM zero-knowledge encryption using Web Crypto API (`crypto.subtle`) and Ed25519 signing.
  - **Test Suite**: 22 unit and live backend integration tests in [`sdk/ts/test/`](sdk/ts/test).

### 9. Post-Quantum 2-of-3 Threshold & PocketBase Collections UI (`v0.2.0`)
- **Deployed Version**: `v0.2.0` (ARM64 static binary)
- **Deployment Target**: Raspberry Pi 4/5 (`192.168.1.50`, `node-operator@192.168.1.50`), service `stiqrcloud.service`
- **Key Capabilities Live on the Pi**:
  - **Post-Quantum Cryptography**: ML-KEM-768 (FIPS 203) key encapsulation and ML-DSA-65 (FIPS 204) hybrid signatures.
  - **Shamir 2-of-3 Threshold**: DEK split into 3 mathematical shares over GF($2^8$), reconstructable from any 2 shares in 0.0017 ms.
  - **FastCDC Chunking**: Content-defined chunking (Gear rolling hash) preventing cascading boundary shifts during middle-file edits.
  - **Signed Merkle Chunk Manifests**: Incremental updates re-encrypt only modified chunks while reusing existing chunks.
  - **Strict Node-Level Authorization**: Edits and deletions verified against owner or `PermEdit` ACL entries with cryptographic signatures.
  - **PocketBase-Style Dashboard**: Collections sidebar, dynamic schema fields bar, and record inspector with live 2-of-3 lockbox visualization.

### 10. Operator Abuse Blacklist & Free Storage Whitelist
- **Free Whitelist API**: `GET /api/escrow/whitelist` & `POST /api/escrow/whitelist` to exempt specific user public addresses or RNode hashes from lightning satoshi charges.
- **Abuse Blacklist API**: `GET /api/escrow/blacklist` & `POST /api/escrow/blacklist` allowing the node operator to ban public keys or identities in case of abuse. Banned identities receive `403 Forbidden` on all write, edit, and delete requests.
- **Node Dashboard Controls**: Dedicated management cards under the Settings & Audits views with active pill badges and instant unban/remove actions.

### 11. Offline Zero-Dependency RNode QR Code Generator
- **Dashboard Integration**: Zero-dependency pure JavaScript SVG QR code generator embedded directly inside `pkg/dashboard/dashboard.html`.
- **TopBar & P2P Mesh Access**: "📱 Node QR" button in topbar and "📱 Show QR Code" in the Reticulum Destination card triggers `modal-rnode-qr`.
- **Interoperability**: Allows mobile apps, users, and peer operators to scan and designate the node for threshold storage.

### 12. SDK App-to-Node Programmatic Schema Handshake (`client.ensureCollection`)
- **Handshake Endpoint**: `POST /api/collections/ensure` accepts `{ name, fields }` and merges new custom fields into `collections_schema.json`.
- **SDK Methods**: `client.ensureCollection({ name, fields })` and `client.listCollections()` allow apps to dynamically initialize their required schema without manual dashboard configuration.
- **Verified**: 24 unit and live backend integration tests in [`sdk/ts/test/`](sdk/ts/test).

### 13. Data Owner Node Designation & Quorum Routing
- **New Record Modal**: Allows data owners to enter specific RNode destination hashes or leave blank to automatically select random 2-of-3 quorum peers.
- **SDK Routing**: Full support in `CreateRecordOptions.storagePeers` for explicit RNode and peer destination arrays.

### 14. Personal Swarm, Airgap Cold Backups & Public WAN Gateway (`v0.3.0`)
- **Deployed Version**: `v0.3.0` (ARM64 static binary on Raspberry Pi, AMD64 on Workstation)
- **Deployment Target**: Raspberry Pi 4/5 (`10.0.0.2:8100`, service `stiqrcloud.service`) & Workstation (`10.0.0.23:8100`)
- **Key Capabilities Live**:
  - **Personal Swarm Mirroring**: Enter peer Reticulum RNode addresses or LAN URLs to pair hardware. All collections and records automatically synchronize 100% across owned devices.
  - **Relaxed 0-Fee Audits**: Personal swarm peers operate under trusted owner audits with zero satoshi micro-escrow overhead.
  - **Airgap Cold Backup Workflow**: Dedicated status card, live readiness indicator, and single-click `Sync & Prepare for Airgap` ensuring safe offline disconnect.
  - **Storage Split Controls**: Interactive slider allocating local disk quota between private Personal Swarm and Public Mesh hosting with configurable satoshi pricing.
  - **Public WAN Gateway Access**: `-allow-public-dashboard` flag enables operator web dashboard access over Cloudflare Tunnel (`stiqrcloud.stiqrnotes.com/dashboard`) without compromising backend zero-trust invariants.
  - **PocketBase API Layer & Dynamic Note Attachments**: Resilient multipart sync preserving invariant Data IDs and handling binary image chunks seamlessly.

---

## 6. Verification Endpoints & Test Commands

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

# Run Go backend test suite
go test -count=1 ./...

# Run TypeScript SDK test suite (Builds + runs all 24 tests)
cd sdk/ts && npm test
```


