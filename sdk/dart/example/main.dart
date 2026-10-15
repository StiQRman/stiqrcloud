import 'package:stiqrcloud/testcloud.dart';

void main() async {
  print('==================================================');
  print('   StiQRcloud Client SDK (Dart & Flutter) Example  ');
  print('==================================================\n');

  // 1. Initialize Client & Ed25519 Identity
  print('[1] Generating client-side Ed25519 identity keypair...');
  final keyPair = await StiQRcloudCrypto.generateKeyPair();
  final pubAddress = await StiQRcloudCrypto.getPublicAddress(keyPair);
  print('    Owner Public Key: $pubAddress\n');

  final client = StiQRcloudClient(
    baseUrl: 'http://localhost:8101',
    keyPair: keyPair,
  );

  try {
    // 2. Health check
    print('[2] Querying cluster health...');
    final health = await client.getHealth();
    print('    Node ID        : ${health.nodeId}');
    print('    Version        : ${health.version}');
    print('    RNS Destination: ${health.rnsDest ?? "none"}');
    print('    Status         : ${health.status}\n');

    // 3. Create Plain Record
    print('[3] Creating unencrypted record in collection "sensor_feed"...');
    final plainRes = await client.createRecord(
      collection: 'sensor_feed',
      text: 'Temperature: 21.4C, Humidity: 45%, Pressure: 1013hPa',
    );
    print('    Record DataID  : ${plainRes.dataId}');
    print('    Auditors Quorum: ${plainRes.auditorPeers.join(", ")}');
    print('    Storage Peers  : ${plainRes.storagePeers.join(", ")}\n');

    // 4. Create End-to-End Encrypted Record (Zero-Knowledge)
    print('[4] Creating End-to-End Encrypted record with AES-256-GCM...');
    final dek = StiQRcloudCrypto.generateDek();
    const confidentialData = 'Patient Records #8812: Blood Type O+, Allergies: Penicillin.';

    final encRes = await client.createRecord(
      collection: 'medical_records',
      text: confidentialData,
      encrypt: true,
      dek: dek,
    );
    print('    Encrypted DataID : ${encRes.dataId}');
    print('    Lightning Invoice: ${encRes.lightningInvoice ?? "n/a"}');

    // 5. Fetch raw vs decrypted record
    print('\n[5] Fetching record back from the decentralized mesh:');
    final rawRecord = await client.getRecord(encRes.dataId, decrypt: false);
    print('    Raw payload on cluster: ${rawRecord.text}');

    final decryptedRecord = await client.getRecord(
      encRes.dataId,
      decrypt: true,
      dek: dek,
    );
    print('    Decrypted with DEK    : ${decryptedRecord.text}\n');

    // 6. Demonstrate Universal Cross-Node Query
    print('[6] Querying from separate subnet peer (Node 5 on 127.10.5.1:8101)...');
    final node5Client = StiQRcloudClient(baseUrl: 'http://127.10.5.1:8101');
    try {
      final crossResolved = await node5Client.getRecord(
        encRes.dataId,
        decrypt: true,
        dek: dek,
      );
      print('    Resolved by Node 5    : ${crossResolved.text}');
      print('    DataID Invariance     : Verified!\n');
    } finally {
      node5Client.close();
    }

    print('==================================================');
    print('   All StiQRcloud SDK operations completed!        ');
    print('==================================================');
  } finally {
    client.close();
  }
}
