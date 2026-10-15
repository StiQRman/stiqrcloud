# StiQRcloud

[![Version: v0.2.0](https://img.shields.io/badge/Version-v0.2.0-blue.svg)](releases/version.json)
[![Go: 1.23+](https://img.shields.io/badge/Go-1.23+-00ADD8.svg?style=flat&logo=go)](https://golang.org)
[![Reticulum Network Stack](https://img.shields.io/badge/Network-Reticulum%20RNS%20Compatible-purple.svg)](https://reticulum.network)
[![Bitcoin Lightning](https://img.shields.io/badge/Payments-Lightning%20Network-F7931A.svg?logo=bitcoin)](https://lightning.network)
[![PocketBase Compatible](https://img.shields.io/badge/API-PocketBase%20REST-b827fc.svg)](https://pocketbase.io)

> **Lightweight, standalone-first distributed database node compatible with the Reticulum Network Stack (RNS), invariant Data IDs, Merkle proof-of-storage audits, and Bitcoin Lightning Network micropayments.**

---

## Key Architectural Principles

### 1. Database First, Network Second
- **Runs Standalone**: Functions offline as a fast, zero-dependency local database engine for edge devices, Raspberry Pis, Windows, and macOS.
- **On-Demand P2P Mesh**: Activates mesh discovery and 3-Auditor + 3-Storage quorum when networked.

### 2. Reticulum Network Stack (RNS) 100% Compatibility
- Native 16-byte (128-bit) cryptographic destination hashing (`<hex>`).
- Reticulum packet wire framing, announces, and UDP/TCP interface listeners.
- Resilient against internet outages; capable of operating over packet radio, LoRa, or local mesh transports.

### 3. Invariant Persistent Data IDs
- The `data_id` primary key **never changes** when records are updated, edited, or when storage nodes are replaced during self-healing failovers.
- Updates monotonically increment `version` while preserving cryptographic integrity.

### 4. Universal Content Routing
- Any node receiving an API query for a `data_id` will transparently resolve and proxy the data across the mesh network even if that node never held the record locally.

### 5. Google Drive-Style Encrypted Access Control (Wrapped DEK)
- Encrypts payloads once with a symmetric Data Encryption Key (DEK).
- Wraps the 32-byte DEK under each authorized collaborator's public key for view or edit permissions without duplicating storage.

### 6. 3-Auditor + 3-Storage Quorum with 75/25 Reputation Scoring
- **1st Auditor**: Public entry node that receives the client request.
- **2 Co-Auditors**: Selected by the 1st Auditor.
- **3 Storage Nodes**: Replicate chunks and answer Merkle proof-of-retrievability challenges.
- **Social Scoring**: Peers graded on:
  1. Audits passed
  2. Audits performed
  3. Network / retrieval latency
- **Selection Formula**: 75% weighted by social score, 25% random (fair entry for new nodes).

### 7. Bitcoin Lightning Network Escrow & Micropayments
- Pluggable backends: Lightning Address (LNURL-pay), LNbits, LND, and built-in deterministic Mock LN Sandbox for zero-cost automated testing.
- Random audit verification prior to payout disbursement.

---

## Quick Start

### 🚀 One-Line Automated Node Deployment
Any node operator can start a StiQRcloud node in seconds with zero configuration or port forwarding:

#### Linux / Raspberry Pi / macOS / Windows WSL:
```bash
curl -sSL https://stiqrcloud.stiqrnotes.com/install.sh | bash
```
*(Or from local network: `curl -sSL http://192.168.1.50:8100/install.sh | bash`)*

#### Windows (PowerShell):
```powershell
iwr -useb https://stiqrcloud.stiqrnotes.com/install.ps1 | iex
```

---

### Running Precompiled Binaries Manually

#### Running in P2P Mesh Mode with Reticulum & Discovery Rendezvous
```bash
./stiqrcloud -port 8100 -dashboard-port 8101 -discovery-url "https://stiqrcloud.stiqrnotes.com" -name "MyNode"
```

#### Running Locally (Standalone Mode - Offline Zero-Network)
```bash
./stiqrcloud -standalone -port 8101 -data-dir ./data
```

Access your private local operator dashboard at:
👉 **`http://localhost:8101/dashboard`**

---

## Settings Update Card & Version Management

Adhering strictly to **Semantic Versioning (`vMAJOR.MINOR.PATCH`)**:
- Current Version: `v0.2.0`
- The built-in dashboard includes a dedicated **Settings & Updates** card that checks version status, queries release history, displays changelogs, and supports direct downloads.

---

## REST API Reference

| Endpoint | Method | Description |
| :--- | :--- | :--- |
| `/api/health` | `GET` | Node health, mode, version, and Reticulum destination |
| `/api/version` | `GET` | Semantic version history and update availability |
| `/api/records` | `GET` | List all records (supports `?collection=name`) |
| `/api/records` | `POST` | Create new record (returns invariant `data_id`, auditors, storage nodes, and Lightning invoice) |
| `/api/records/:data_id` | `GET` | Universal retrieval by invariant `data_id` |
| `/api/records/:data_id` | `PATCH` | Update record (preserves `data_id`, increments `version`) |
| `/api/records/:data_id` | `DELETE` | Delete record (verified by owner signature) |
| `/api/records/:data_id/collaborators` | `POST` | Add view/edit permission with Wrapped DEK |
| `/api/collections/:col/records` | `GET` | PocketBase-compatible collection listing |
| `/dashboard` | `GET` | Dark-mode live operator dashboard |
