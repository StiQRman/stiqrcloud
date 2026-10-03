# Project State & Tracking

## 1. Project Requirements & Architecture Overview

### Core Concept & Runtime Targets
- **Core Engine Stack**: **Go (Golang)** - Single static binary, low memory footprint (~20-40MB), first-class concurrency (goroutines), trivial cross-compilation to Raspberry Pi (ARM), Windows (.exe), and macOS.
- **Standalone-First Architecture**:
  - Functions completely offline as an independent, high-performance local database engine (CRUD, indexing, local dashboard, zero network requirements).
  - Can be activated into P2P mesh mode on demand to participate in replication, auditor quorum, and escrow.
- **Local Storage Architecture**: Hybrid Engine — **SQLite** for relational indexing, metadata, social scores, and audit contracts; **Content-Addressed Chunk Store (Blake3/SHA-256)** on local disk for encrypted payload blobs (pictures, files, large texts).
- **Reticulum Network Stack (RNS) Compatibility**:
  - Native compatibility with Reticulum mesh addressing (Destination hashes, Announces, Links, and Resource streaming).
  - Able to operate over Reticulum transport interfaces (LoRa, packet radio, UDP/TCP, I2P) alongside standard TCP/HTTP.
- **Platforms**: Raspberry Pi (Linux ARM), Windows, and macOS.
- **Concurrency**: High concurrency support for multiple simultaneous API calls.
- **Dual Interfaces**:
  - **Public-Facing API**: Industry-standard RESTful JSON API with Server-Sent Events (SSE) for realtime updates (PocketBase-compatible conventions: `/api/collections/:collection/records`, `/api/health`, `/api/realtime`).
  - **Local-Facing Dashboard**: Lightweight responsive web UI for node operators to monitor peer health, audits, escrow, and storage stats.

### Distributed Node Network & Roles
- **Node Network Architecture (Aligned with StiQRcloud & Reticulum)**:
  - **P2P Swarm & Discovery**: HTTP/REST gossip mesh with discovery server bootstrap, plus Reticulum destination announces.
  - **Transparent Global Data ID Routing**:
    - Any node queried for a `data_id` can resolve and return the record, even if that node has never stored it locally, by querying the distributed routing index / DHT / auditors and proxying the encrypted payload.
  - **Dual Node Modes**:
    - *Public Nodes*: Public IP / port-forwarded, serve as ingress gateways and auditors.
    - *Private Nodes*: Behind residential NAT / firewalls, connect outbound via WebSocket reverse relay tunnels (`/ws/relay`) or Reticulum links to receive challenges, chunk replication, and audits.
- **Role Assignment**:
  - **1st Auditor Node**: The public entry node that receives the client's request.
  - **Co-Auditors**: 1st Auditor selects 2 additional Auditor nodes (total 3 auditors).
  - **Storage Nodes**: The 3 auditors coordinate to select 3 Storage nodes to store replicated data.
  - **Feedback**: 1st Auditor returns list of all participating nodes (auditors + storage) to the client.
- **Social Score & Reputation**:
  - Evaluated on:
    1. Audits passed
    2. Audits performed
    3. Network / retrieval speed
  - Node Selection Formula: 75% weighted by social score, 25% random (giving new nodes a path to build reputation).

### Data Model, Cryptography & Permissions
- **Record Fields**:
  - `user_pub_address`: Ed25519 or Secp256k1 public key of data owner.
  - `data_id`: **Invariant Persistent Identifier** (UUIDv7 or user-deterministic ID) that remains permanent across all updates, edits, and storage node replacements.
  - `version`: Monotonically incrementing revision counter.
  - `text`: Encrypted text payload or metadata.
  - `file` / `picture`: Encrypted binary blobs stored in chunk store.
  - `created_at` / `updated_at`: Timestamps.
  - `acl`: List of authorized collaborator public keys, permission levels (`view` | `edit`), and wrapped DEK entries.

### Economics & Auditing
- **Pricing**: Each storage node configures its own rate (`sats_per_gb_month`).
- **Billing & Escrow**: Auditors calculate total contract cost and generate Bitcoin Lightning invoices for client deposits.
- **Lightning Integration**:
  - **Pluggable Backends**: Out-of-the-box support for Lightning Addresses (`LNURL-pay` / Alby / Breez), LNbits API, LND REST/gRPC.
  - **Mock Sandbox Mode**: Built-in deterministic mock Lightning engine for 100% automated local testing without spending real satoshis.
- **Auditing Mechanism**:
  - Cryptographic challenge/response with Merkle tree proofs.
  - Periodic randomized challenges and pre-payout verification checks.
- **Failover & Re-replication**:
  - If a storage node misses an audit or drops offline, co-auditors trigger self-healing replication to a healthy replacement peer.
  - The replacement peer assumes the remainder of the contract duration and accrues remaining satoshi payouts.

### Data Model, Cryptography & Permissions
- **Record Fields**:
  - `user_pub_address`: Ed25519 or Secp256k1 public key of data owner.
  - `data_id`: Unique identifier (UUID or Content Hash).
  - `text`: Encrypted text payload or metadata.
  - `file` / `picture`: Encrypted binary blobs stored in chunk store.
  - `created_at` / `updated_at`: Timestamps.
  - `acl`: List of authorized collaborator public keys, permission levels (`view` | `edit`), and wrapped DEK entries.
- **Authentication**: Requests signed with user's private key (`X-User-PubKey`, `X-User-Signature`); node verifies digital signatures before accepting any edits or deletes.
- **Client-Side Encryption & Access Control**:
  - **Wrapped Data Encryption Key (DEK)** architecture:
    - Payload is encrypted once with a random symmetric key (AES-256-GCM / XChaCha20-Poly1305).
    - The DEK is encrypted using the public key of each authorized collaborator and stored in the record's ACL.
    - *Action Item*: Provide an intuitive, beginner-friendly walkthrough explaining this mechanism to the user after Q&A.

### Multi-Platform Client SDK
- **Primary SDK**: **Flutter / Dart** (`sdk/dart`) — 100% unified cross-platform codebase, zero C/FFI dependencies, compiling directly to Web, iOS, Android, macOS, Windows, and Linux.
- **Web SDK**: **TypeScript / JavaScript** (`sdk/ts`) — Lightweight zero-dependency browser and Node.js/React Native client library.
- **SDK Responsibilities**:
  - Key management (Ed25519 asymmetric keypairs).
  - Client-side encryption/decryption (AES-256-GCM / XChaCha20) and Wrapped DEK management.
  - Request signing (`X-User-PubKey`, `X-User-Signature`).
  - Lightning Network invoice handling & payment flow.
  - Multi-node failover and auditor pricing negotiation.

## 2. Current Constraints & Operational Directives
- Strict adherence to [instructions.md](instructions.md).
- Interactive multiple-choice Q&A discovery (one question at a time) before any code execution.
- Local-first development, testing, and verification.
- Semantic versioning (vMAJOR.MINOR.PATCH) and production build archiving.
- Settings update card integration in dashboard.

## 3. Changelog
- **Phase 1: Initial Discovery (Completed)**:
  - Formulated and completed interactive architectural Q&A:
    - Runtime & Language: Go (Golang) for lightweight single-binary execution across Pi, Windows, and macOS.
    - Local Storage: Hybrid SQLite metadata + Content-Addressed chunk storage.
    - Network Architecture: P2P gossip mesh + WebSocket reverse relay tunnels for NAT traversal (aligned with StiQRcloud).
    - Access Control: Wrapped Data Encryption Keys (DEKs) for client-side zero-knowledge encrypted sharing.
    - Client SDKs: Unified Flutter/Dart SDK + lightweight TypeScript SDK.
    - Lightning Integration: Pluggable Lightning backend (LNURL/Breez/Alby/LNbits) + deterministic Mock LN sandbox.
    - Public API: Industry-standard RESTful JSON (PocketBase convention) + SSE Realtime.
- **Phase 2: Competitor & Open-Source Research (Completed)**:
  - Ecosystem scanned (PocketBase, IPFS/Filecoin, GunDB/OrbitDB, Storj/Sia, StiQRcloud).
  - Clarified differentiation and architecture priorities:
    1. **Database-First & Standalone**: Fully operational zero-network offline local database engine with pluggable P2P activation.
    2. **Reticulum Network Stack (RNS) Compatibility**: 100% interoperability with Reticulum packet mesh addressing, destinations, announces, and transports.
    3. **Invariant Data ID**: Data ID remains persistent across edits, versions, and complete storage node replacements.
    4. **Universal Distributed Retrieval**: Any node can resolve and proxy data by `data_id` across the network.
- **Phase 3: UI & Design Assets Check (Completed)**:
  - Created responsive dark-mode dashboard ([pkg/dashboard/dashboard.html](pkg/dashboard/dashboard.html)) with live metrics for Records, Storage Chunks, Reticulum Destination, 75/25 Mesh Peer Leaderboard, Merkle Audits, and Lightning Escrow.
  - Implemented dedicated **Settings Update Card** component integrating Semantic Versioning (`v0.1.0`), remote feed querying, changelog display, and update checking.
- **Phase 4: Local-First Execution & Deployment Rules (Completed & Verified)**:
  - 100% of unit tests passing across all packages (`pkg/crypto`, `pkg/reticulum`, `pkg/storage`, `pkg/p2p`, `pkg/escrow`, `pkg/audit`, `pkg/gateway`).
  - Verified standalone offline mode and invariant Data ID guarantees via automated tests and live curl operations.
  - Initialized git repository with strict `.gitignore` rules, committed locally, and tagged with semantic version `v0.1.0`.
  - Built and archived production binaries in [releases/v0.1.0/](releases/v0.1.0/):
    - `testcloud-linux-amd64`
    - `testcloud-linux-arm64` (Raspberry Pi ARM)
    - `testcloud-windows-amd64.exe` (Windows)
  - Generated and verified SHA-256 release checksums in [releases/version.json](releases/version.json).
  - Staged and GitHub/stiqrhub ready (local-first; no remote push executed without user approval).
- **Phase 5: Multi-Network 6-Node Mesh Cluster (Completed & Verified)**:
  - Extended CLI with `-host` and `-peers` flags supporting discrete network subnet binding and automated gossip bootstrapping.
  - Implemented `/api/peers` endpoint for live routing table exchange and dynamic peer discovery.
  - Implemented `/api/internal/store` endpoint for cross-node replication and chunk persistence.
  - Extended Reticulum UDP interface manager with `StartUDPAddr` and `SendPacket` for multi-network packet transport.
  - Deployed 6 independent nodes locally across 6 distinct IPv4 network subnets:
    - Node 1 (`127.10.1.1` on `127.10.1.0/24`) — Entry Gateway & 1st Auditor (`node_auditor_alpha`)
    - Node 2 (`127.10.2.1` on `127.10.2.0/24`) — Co-Auditor 1 (`node_auditor_beta`)
    - Node 3 (`127.10.3.1` on `127.10.3.0/24`) — Co-Auditor 2 (`node_auditor_gamma`)
    - Node 4 (`127.10.4.1` on `127.10.4.0/24`) — Storage Node 1 (`node_storage_delta`)
    - Node 5 (`127.10.5.1` on `127.10.5.0/24`) — Storage Node 2 (`node_storage_epsilon`)
    - Node 6 (`127.10.6.1` on `127.10.6.0/24`) — Storage Node 3 (`node_storage_zeta`)
  - Automated cluster lifecycle scripts: [scripts/start_cluster.sh](scripts/start_cluster.sh) and [scripts/stop_cluster.sh](scripts/stop_cluster.sh).
  - Verified live 3-Auditor + 3-Storage quorum negotiation, automated chunk replication to designated storage peers, and universal `data_id` resolution across all 6 separate subnets.
- **Phase 6: Reticulum BLE Bridge for Columba App (Completed & Active)**:
  - Added Reticulum TCP Server listener with standard HDLC framing (`0x7E` flag, `0x7D` escape, `0x20` XOR mask) in [pkg/reticulum/reticulum.go](pkg/reticulum/reticulum.go).
  - Deployed `torlando-tech/ble-reticulum` Protocol v2.2 interface into `~/.reticulum/interfaces/` with GATT service UUID `37145b00-442d-4a94-917f-8f42c5da28e3`.
  - Configured `~/.reticulum/config` with dual-mode `BLEInterface` (peripheral advertising + central scanning) and `TCPClientInterface` bridging into Node 1 (`127.10.1.1:4242`).
  - Implemented daemon management scripts: [scripts/start_ble_bridge.sh](scripts/start_ble_bridge.sh) and [scripts/stop_ble_bridge.sh](scripts/stop_ble_bridge.sh).
  - Verified live Bluetooth LE advertising on host radio `F8:E4:E3:72:8E:85` and active TCP bridge into TestCloud.

## 4. Current State & Resume Guide for Next Session

### Current Running Services
- A **6-node mesh network** is active locally across 6 separate subnets (`127.10.1.0/24` - `127.10.6.0/24`):
  - **Node 1 (Auditor 1)**: `127.10.1.1:8101` — Gateway / Ingress ([Dashboard](http://127.10.1.1:8101/dashboard))
  - **Node 2 (Auditor 2)**: `127.10.2.1:8101` — Co-Auditor ([Dashboard](http://127.10.2.1:8101/dashboard))
  - **Node 3 (Auditor 3)**: `127.10.3.1:8101` — Co-Auditor ([Dashboard](http://127.10.3.1:8101/dashboard))
  - **Node 4 (Storage 1)**: `127.10.4.1:8101` — Storage Peer ([Dashboard](http://127.10.4.1:8101/dashboard))
  - **Node 5 (Storage 2)**: `127.10.5.1:8101` — Storage Peer ([Dashboard](http://127.10.5.1:8101/dashboard))
  - **Node 6 (Storage 3)**: `127.10.6.1:8101` — Storage Peer ([Dashboard](http://127.10.6.1:8101/dashboard))
- Cluster control scripts:
  - Stop cluster: `./scripts/stop_cluster.sh`
  - Start cluster: `./scripts/start_cluster.sh`

### Ingested & Verified Data Records
1. **Record 1 (Telemetry)**:
   - Data ID: `rec_26b1c8324e4a1764b070875c_56724` (Collection: `telemetry`)
   - Revision: Updated to `v2` in-place while keeping `data_id` permanent.
   - Quorum: 1st Auditor `node_auditor_alpha`, Co-Auditors `gamma` & `zeta`, Storage peers `beta`, `delta`, `epsilon`.
   - Invoice: `lnbc10u1pa4f3132bf51e6792bf26`.
2. **Record 2 (Flight Log Attachment)**:
   - Data ID: `rec_0eaa67360f534521bffe7509_81485` (Collection: `flight_logs`)
   - Attachment: [testdata/sample_flight_data.csv](testdata/sample_flight_data.csv) (282 bytes).
   - Content Hash: `3332beed125225eec51ddc57dd1f02fd5908264165d0c038d98a30c8a3b83c85`.
   - Invoice: `lnbc10u1p2b6b7bf2236985a2a794`.
   - Download Endpoint: `/api/records/rec_0eaa67360f534521bffe7509_81485/file`.
3. **Record 3 (Columba / Reticulum BLE Gateway)**:
   - Collection: `columba_messages`
   - Ingress Path: Phone (Seth's S22) -> Bluetooth Low Energy (BLE) GATT -> Reticulum `ble-reticulum` -> Python Shared Instance -> [scripts/lxmf_testcloud_bot.py](scripts/lxmf_testcloud_bot.py) -> REST API (`127.10.1.1:8101`) -> 6-Node Distributed Cluster.

### Bluetooth Low Energy (BLE) & LXMF Gateway Architecture
- **Bluetooth Stack**: Native Reticulum BLE Interface (`ble-reticulum` with `bleak`, `bluezero`, `dbus-fast`).
- **GATT Service UUID**: `37145b00-442d-4a94-917f-8f42c5da28e3`
- **Bridge Script**: [scripts/start_ble_bridge.sh](scripts/start_ble_bridge.sh)
- **LXMF Gateway Bot**: [scripts/lxmf_testcloud_bot.py](scripts/lxmf_testcloud_bot.py)
  - Destination Hash: `<0b0208f35b36830c10b635af6f063bf4>`
  - Display Name: `TestCloud Gateway`
  - Re-announces every 20 seconds over BLE mesh.
  - Automatically stores received messages into collection `columba_messages` and replies with a cluster receipt ACK over Reticulum.

### Client SDK Implementation (Dart & Flutter)
- **Path**: [sdk/dart](sdk/dart)
- **Status**: Completed, tested, and verified against live 6-node cluster (11/11 tests passing).
- **Core Modules**:
  - **`TestCloudCrypto`** ([sdk/dart/lib/src/crypto.dart](sdk/dart/lib/src/crypto.dart)):
    - Ed25519 keypair generation, public address derivation (`user_pub_address`), and cryptographic signing/verification.
    - AES-256-GCM symmetric authenticated encryption for text and binary file/picture blobs.
    - Auto-generation of 256-bit DEKs (Data Encryption Keys).
    - Envelope serialization format: `tc:enc:v1:<base64-json>`.
  - **`TestCloudClient`** ([sdk/dart/lib/src/client.dart](sdk/dart/lib/src/client.dart)):
    - `getHealth()` & `getVersion()`
    - `createRecord()` with client-side Zero-Knowledge encryption and multipart file/picture upload.
    - `getRecord()` with automatic in-place decryption via DEK.
    - `listRecords()`, `downloadFile()`, `downloadPicture()`, `listPeers()`, and `pollRecords()`.
  - **Example**: [sdk/dart/example/main.dart](sdk/dart/example/main.dart) runnable via `dart run example/main.dart`.
  - **Documentation**: [sdk/dart/README.md](sdk/dart/README.md).

### Storage Monetization, Whitelist & Grace Retention Eviction Policy
- **Dynamic Pricing Controls**:
  - **Free For Everyone Toggle**: When enabled, all incoming storage contracts are automatically granted at 0 sats and marked settled with `FREE_STORAGE_AGREEMENT`.
  - **Configurable Rate**: Operators can set their price per GB per month in satoshis (saved to `data/pricing_policy.json`).
  - **Free Whitelist**: Designated public addresses (`user_pub_address` or Reticulum hashes) are permanently exempted from storage charges, even when paid mode is active.
- **Zero-Immediate-Deletion Grace Retention Policy**:
  - When a storage contract's period expires or a client stops paying, the node **never automatically deletes the data**. Data remains preserved, readable, and queryable in grace retention.
  - **Full-Disk Priority Eviction**:
    - If and when the node's local disk quota (`max_bytes`) is reached, the storage engine identifies expired/unpaid contracts and sorts them by expiration timestamp (oldest expired contracts pruned first).
    - Data chunks belonging to expired contracts are evicted until sufficient space is freed for new contracts.
    - Active paid contracts and whitelisted/free contracts are strictly protected and never evicted.
- **Dashboard UI**:
  - Updated "Auditing & Lightning Escrow" tab with real-time toggle switches, price editors, interactive whitelist pill management (add/remove), and visual capacity breakdown meter (Active Paid vs Grace Retention vs Available Headroom).

### Verified Invariant Data Guarantees
- **Data ID Invariance**: Data ID never mutates upon record editing or storage peer migration.
- **Universal Cross-Subnet Proxying**: Every node across all 6 networks successfully resolved and returned records by `data_id`.
- **Reticulum Compatibility**: Destination addresses `<hex>` assigned, packet framing verified, UDP/TCP HDLC framing active on port 4242 per subnet.

### Node Failure & Self-Healing Resilience Verification (Delta Node Knock-Offline Test)
- **Scenario Tested**: Intentionally knocked storage node `node_storage_delta` (`127.10.4.1:8101`) offline to observe network reaction, challenge failure detection, automated peer failover, and data survivability.
- **Observations & Results**:
  1. **Failure Detection**: The background 3-Auditor verification cycle immediately flagged `node_storage_delta` with connection refused (`passed: false`, notes: `Storage node offline: dial tcp 127.10.4.1:8101: connect: connection refused`).
  2. **Automated Self-Healing Replication**:
     - The coordinator (`pkg/audit/auditor.go`) triggered `TriggerSelfHealingReplication`.
     - Automatically selected an unassigned, healthy peer (`node_auditor_beta`) from the swarm using the 75/25 reputation algorithm.
     - Escrow manager immediately reallocated unaccrued payout streams from Delta to Beta (`ReallocateOfflineNode`).
     - Storage peer list dynamically updated from `["node_storage_zeta", "node_storage_delta", "node_storage_epsilon"]` to `["node_storage_zeta", "node_auditor_beta", "node_storage_epsilon"]`.
     - Updated record was automatically broadcast and synced to the new peer.
  3. **Permanent Invariant Data ID**:
     - `data_id` (`rec_b5b51e1d1fe93562beaac716_31031`) and `merkle_root` remained 100% constant and invariant throughout the failover.
     - Universal Content Routing continued to resolve and return the record seamlessly across all surviving subnets (`127.10.1.1`, `127.10.2.1`, `127.10.3.1`, `127.10.5.1`, `127.10.6.1`).
  4. **Subsequent Audit Recovery**: The next audit cycle challenged the newly self-healed quorum (`zeta`, `beta`, `epsilon`), with all 6 challenges passing and latency recorded at <1ms.
  5. **Dashboard Visualization & Peer Liveness Probing**:
     - Added background peer liveness probing loop (`StartLivenessLoop` in [`pkg/p2p/swarm.go`](pkg/p2p/swarm.go)) checking node health every 4 seconds.
     - Dead nodes are immediately flagged with `is_online: false`, slashed in social score, and excluded from new storage quorum allocations.
     - Added dedicated **Status** column in the **P2P & Reticulum Mesh** table:
       - Online peers display `🟢 ONLINE`.
       - Dead/disconnected peers (Delta) sink to the bottom with an explicit **`🔴 OFFLINE`** badge, red row highlight, and `Unreachable` latency.
     - Mesh Peers summary card updated to display `4 / 5 Online`.
     - Database Explorer table now displays active **Storage Quorum** badges for each record, showing that Delta was replaced by Beta on the records.
  6. **Recursive P2P Gossip Peer Discovery (StiQRcloud Alignment)**:
     - Implemented `GossipPeers()` in [`pkg/p2p/swarm.go`](pkg/p2p/swarm.go): nodes periodically exchange full peer routing tables across bootstrap nodes and discovered peers.
     - When any node joins via a bootstrap node (e.g. Node 1 Alpha), it recursively discovers all other peers on the mesh, connects to them directly, and registers bidirectional announcements via `POST /api/peers`.
     - Verified across all nodes (`Beta`, `Gamma`, `Delta`, `Epsilon`, `Zeta`): every dashboard displays the entire 6-node network with live status indicators, even if nodes only booted with a single bootstrap peer flag.

### Planned Next Steps for Tomorrow
1. **Interactive Escrow Settlement & Audit Challenge Loop**:
   - Settle mock Lightning invoices via webhook / API call.
   - Trigger automated periodic Merkle audits against storage peers and log reputation score bumps on the dashboard.
2. **Private Node NAT Traversal**:
   - WebSocket reverse relay tunneling (`/ws/relay`) for nodes behind strict firewalls / residential NAT.
3. **TypeScript / JavaScript Client SDK**:
   - Port SDK conventions to `sdk/ts` for web browser frontends and Node.js.
4. **Remote Staging / Publication**:
   - When authorized by user, push repository to GitHub / stiqrhub with tagged release `v0.1.0`.
