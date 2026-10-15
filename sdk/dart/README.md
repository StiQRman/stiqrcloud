# StiQRcloud Client SDK for Dart & Flutter

Official Dart and Flutter client SDK for **StiQRcloud** — a decentralized, local-first database engine with client-side end-to-end encryption, invariant data routing, and Reticulum mesh network integration.

---

## Features

- 🔒 **Zero-Knowledge End-to-End Encryption**: Built-in **AES-256-GCM** authenticated encryption with automatic nonce and MAC verification.
- 🔑 **Ed25519 Cryptographic Identity**: Client-side keypair generation, public key addressing, and request signature verification.
- 🌐 **Universal Data ID Resolution**: Query any node in the cluster by invariant `dataId` and automatically receive the record, even if stored across separate subnets or mesh links.
- ⚡ **Lightning Network Micropayments**: Native inspection of generated Lightning invoices (`lightning_invoice`) for storage contracts.
- 📦 **Multi-part Attachments**: Seamlessly store and retrieve encrypted files and picture blobs.
- 🛰️ **Reticulum Mesh Ready**: Direct integration with Reticulum Network Stack (RNS) destination addresses.
- 📱 **Cross-Platform**: 100% compatible with Flutter on Android, iOS, macOS, Windows, Linux, and pure Dart servers.

---

## Installation

Add `stiqrcloud` to your `pubspec.yaml`:

```yaml
dependencies:
  stiqrcloud:
    path: /path/to/stiqrcloud/sdk/dart
```

Or when published:

```yaml
dependencies:
  stiqrcloud: ^0.1.0
```

---

## Quick Start

### 1. Initialize Client & Keypair

```dart
import 'package:stiqrcloud/stiqrcloud.dart';

void main() async {
  // Generate client Ed25519 keypair for cryptographic identity and signing
  final keyPair = await StiQRcloudCrypto.generateKeyPair();
  final pubAddress = await StiQRcloudCrypto.getPublicAddress(keyPair);
  print('Owner Address: $pubAddress');

  // Initialize client pointing to local node or gateway
  final client = StiQRcloudClient(
    baseUrl: 'http://localhost:8101',
    keyPair: keyPair,
  );
}
```

---

### 2. Create an End-to-End Encrypted Record

```dart
// 1. Generate a random 256-bit Data Encryption Key (DEK)
final dek = StiQRcloudCrypto.generateDek();

// 2. Store encrypted record in cluster
final response = await client.createRecord(
  collection: 'notes',
  text: 'Confidential client note stored with Zero-Knowledge encryption.',
  encrypt: true,
  dek: dek,
);

print('Record DataID: ${response.dataId}');
print('Auditors: ${response.auditorPeers}');
print('Storage Nodes: ${response.storagePeers}');
print('Lightning Invoice: ${response.lightningInvoice}');
```

---

### 3. Retrieve and Decrypt Records

```dart
// Fetch and decrypt automatically with your DEK
final record = await client.getRecord(
  response.dataId,
  decrypt: true,
  dek: dek,
);

print('Decrypted text: ${record.text}');
```

---

### 4. Upload Files and Pictures

```dart
import 'dart:io';

final fileBytes = await File('report.pdf').readAsBytes();

final res = await client.createRecord(
  collection: 'documents',
  text: 'Q3 Financial Report',
  fileBytes: fileBytes,
  fileName: 'report.pdf',
  encrypt: true,
  dek: dek,
);

// Download file later from any cluster node
final downloadedBytes = await client.downloadFile(
  res.dataId,
  decrypt: true,
  dek: dek,
);
```

---

### 5. Universal Invariant Routing Across Subnets

Because StiQRcloud guarantees Data ID invariance, any node in the cluster will resolve a record:

```dart
// Created on Node 1 (127.10.1.1)
final client1 = StiQRcloudClient(baseUrl: 'http://127.10.1.1:8101');
final created = await client1.createRecord(collection: 'feed', text: 'Universal Data');

// Queried directly from Node 5 (127.10.5.1 on a completely different subnet)
final client5 = StiQRcloudClient(baseUrl: 'http://127.10.5.1:8101');
final resolved = await client5.getRecord(created.dataId);
print(resolved.text); // 'Universal Data'
```

---

## Running the Example & Tests

Run the full interactive example:

```bash
cd sdk/dart
dart run example/main.dart
```

Run unit and integration tests:

```bash
dart test
```
