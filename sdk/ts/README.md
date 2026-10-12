# TestCloud TypeScript & JavaScript SDK (`@stiqrhub/testcloud`)

The official TypeScript and JavaScript client SDK for **TestCloud** decentralized storage mesh and Reticulum backbone.

Works seamlessly in **Node.js (18+)**, **modern web browsers**, **React**, **Vue**, **Svelte**, and **React Native** with zero external runtime dependencies.

---

## Features

* **User-Owned Node Support**: Easily connect the client to your own local node (`http://localhost:8100`) or custom self-hosted nodes with automatic multi-node failover.
* **Full Data Placement Control (Storage Policies)**:
  * **`local-only`**: Data stays strictly on the user's own node (0 external replicas, excluded from peer sync).
  * **`replicated-2`**: Data is stored locally and replicated to exactly 2 peer storage nodes in the mesh.
  * **`full-mesh`**: Standard 3-node storage quorum.
  * **`custom`**: The user specifies the exact target storage nodes by ID.
* **Client-Side Zero-Knowledge Encryption**: End-to-end AES-256-GCM encryption with auto-generated 32-byte Data Encryption Keys (DEKs) using the native Web Crypto API.
* **PocketBase-Compatible API**: Drop-in compatible collection operations (`client.collection('notes').create(...)`, `.getOne()`, `.getList()`).
* **Cryptographic Signatures**: Ed25519 signing for record provenance and decentralized access control.
* **Realtime Polling**: Stream live record creations and updates across the mesh.

---

## Installation

```bash
npm install @stiqrhub/testcloud
```

---

## Quickstart

### 1. Connect to Your Own Node

```typescript
import { TestCloudClient } from '@stiqrhub/testcloud';

// Connect to the user's local node
const client = new TestCloudClient({
  baseUrl: 'http://localhost:8100',
  defaultStoragePolicy: 'local-only', // Default to keeping data strictly on their node
});

// Optionally add additional self-hosted or community nodes
client.addNode({
  id: 'home-pi',
  url: 'http://192.168.1.50:8100',
  name: 'Raspberry Pi Gateway',
});
```

---

### 2. Full Control Over Where Data Goes

You have full control over whether data remains on your node or replicates across the mesh:

#### Option A: Local-Only (Stays Strictly on Your Node)
```typescript
const record = await client.createRecord({
  collection: 'notes',
  text: 'Private journal entry. Never leaves this machine.',
  storagePolicy: 'local-only',
});

console.log(record.data_id);            // e.g. "rec_a48e0dbd..."
console.log(record.replication_policy); // "local-only"
console.log(record.storage_peers);      // [] (Zero external peers)
```

#### Option B: Replicate to 2 Other Storage Nodes
```typescript
const record = await client.createRecord({
  collection: 'documents',
  text: 'Important document backed up across 2 peer nodes.',
  storagePolicy: 'replicated-2',
});

console.log(record.storage_peers); // ['node_storage_delta', 'node_storage_epsilon']
```

#### Option C: Custom Target Routing (User Chooses Exact Nodes)
```typescript
const record = await client.createRecord({
  collection: 'vault',
  text: 'Shared strictly with trusted nodes.',
  storagePolicy: 'custom',
  storagePeers: ['node_auditor_beta', 'node_storage_zeta'],
});
```

---

### 3. End-to-End Client-Side Encryption (AES-256-GCM)

```typescript
// Enable client-side encryption
const res = await client.createRecord({
  collection: 'passwords',
  text: 'SecretPass123!',
  encrypt: true,
  storagePolicy: 'local-only',
});

// Save the 32-byte DEK returned
const dek = res.dek;

// Retrieve and automatically decrypt
const record = await client.getRecord(res.data_id, {
  decrypt: true,
  dek: dek,
});

console.log(record.text); // "SecretPass123!"
```

---

### 4. PocketBase Compatible API

If your app uses PocketBase conventions, use the `collection` interface:

```typescript
// Create
const task = await client.collection('tasks').create({
  title: 'Setup decentralized backup',
  done: false,
}, {
  storagePolicy: 'local-only', // Or 'replicated-2'
});

// Get one
const item = await client.collection('tasks').getOne(task.id);

// List
const list = await client.collection('tasks').getList(1, 20);

// Update
await client.collection('tasks').update(task.id, { done: true });

// Delete
await client.collection('tasks').delete(task.id);
```

---

### 5. Managing User Nodes Dynamically

The `nodeManager` gives users full control over their node connections:

```typescript
// Add user's node
client.nodeManager.addNode({
  id: 'my-laptop',
  url: 'http://localhost:8100',
  name: 'Workstation Node',
  isPrimary: true, // Set as primary
});

// List all configured nodes
const nodes = client.nodeManager.listNodes();

// Check node health & measure latency
const health = await client.nodeManager.checkHealth('http://localhost:8100');
console.log(`Latency: ${health.data?.peers} peers online`);

// Switch primary node at runtime
client.setPrimaryNode('http://192.168.1.50:8100');
```

---

### 6. Attachments & Binary Files

```typescript
import * as fs from 'node:fs';

const fileBuffer = fs.readFileSync('document.pdf');

const res = await client.createRecord({
  collection: 'files',
  text: 'Project specification',
  file: fileBuffer,
  fileName: 'document.pdf',
  storagePolicy: 'replicated-2',
});

// Download attachment
const downloadedBytes = await client.downloadFile(res.data_id);
```

---

## Storage Policy Reference

| Policy | Description | External Peers | Peer Sync Syncable |
| :--- | :--- | :--- | :--- |
| `'local-only'` / `'local'` | Stays exclusively on user's node | 0 | ❌ No (airgapped from mesh sync) |
| `'replicated-2'` | Stored locally and on 2 peer nodes | 2 | ✅ Yes (assigned to 2 peers) |
| `'full-mesh'` / `'distributed'` | Standard 3-node storage quorum | 3 | ✅ Yes |
| `'custom'` | Stored on user's chosen `storagePeers` | Custom list | ✅ Yes (assigned to custom list) |

---

## License

MIT © StiQRhub Developer
