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
- **Phase 3: UI & Design Assets Check (In Progress)**:
  - Created [assets/ui](assets/ui) folder.
  - Querying user for UI/UX wireframes, design mockups, or preferences.
