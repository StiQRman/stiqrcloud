import 'dart:convert';
import 'package:test/test.dart';
import 'package:testcloud/testcloud.dart';

void main() {
  const node1Url = 'http://127.10.1.1:8101';
  const node4Url = 'http://127.10.4.1:8101'; // Storage node on Subnet 4
  const localhostUrl = 'http://localhost:8101';

  group('TestCloudClient Live Cluster Integration', () {
    late TestCloudClient client;
    late SimpleKeyPair keyPair;

    setUpAll(() async {
      keyPair = await TestCloudCrypto.generateKeyPair();
      client = TestCloudClient(baseUrl: localhostUrl, keyPair: keyPair);
    });

    tearDownAll(() {
      client.close();
    });

    test('getHealth returns node metadata and Reticulum address', () async {
      final health = await client.getHealth();
      expect(health.status, equals('ok'));
      expect(health.appName, equals('testcloud'));
      expect(health.version, equals('v0.1.0'));
      expect(health.nodeId, equals('node_auditor_alpha'));
      expect(health.rnsDest, isNotNull);
    });

    test('getVersion returns version check info', () async {
      final ver = await client.getVersion();
      expect(ver['current_version'], equals('v0.1.0'));
      expect(ver['latest_version'], equals('v0.1.0'));
      expect(ver['update_available'], isFalse);
    });

    test('createRecord and getRecord (unencrypted)', () async {
      final res = await client.createRecord(
        collection: 'dart_sdk_tests',
        text: 'Hello from Dart SDK unencrypted test',
      );

      expect(res.dataId, startsWith('rec_'));
      expect(res.auditorPeers, isNotEmpty);
      expect(res.storagePeers, isNotEmpty);

      final fetched = await client.getRecord(res.dataId);
      expect(fetched.dataId, equals(res.dataId));
      expect(fetched.text, equals('Hello from Dart SDK unencrypted test'));
      expect(fetched.collection, equals('dart_sdk_tests'));
    });

    test('createRecord and getRecord with Client-Side AES-256-GCM Encryption', () async {
      final dek = TestCloudCrypto.generateDek();
      const secretMessage = 'Top secret financial records encrypted with AES-256-GCM.';

      final res = await client.createRecord(
        collection: 'encrypted_vault',
        text: secretMessage,
        encrypt: true,
        dek: dek,
      );

      expect(res.dataId, startsWith('rec_'));

      // Fetch raw (without DEK) - should show encrypted envelope string
      final rawRecord = await client.getRecord(res.dataId, decrypt: false);
      expect(rawRecord.text.startsWith('tc:enc:v1:'), isTrue);
      expect(rawRecord.text, isNot(contains(secretMessage)));

      // Fetch with decryption enabled
      final decryptedRecord = await client.getRecord(
        res.dataId,
        decrypt: true,
        dek: dek,
      );
      expect(decryptedRecord.text, equals(secretMessage));
    });

    test('Cross-subnet universal DataID routing (Node 1 -> Node 4)', () async {
      // Create on Node 1
      final res = await client.createRecord(
        collection: 'cross_subnet_tests',
        text: 'Resolvable universally across any cluster node',
      );

      // Create client pointing directly to Node 4 (Storage node on separate subnet 127.10.4.0/24)
      final clientNode4 = TestCloudClient(baseUrl: node4Url);
      try {
        final resolved = await clientNode4.getRecord(res.dataId);
        expect(resolved.dataId, equals(res.dataId));
        expect(resolved.text, equals('Resolvable universally across any cluster node'));
      } finally {
        clientNode4.close();
      }
    });

    test('listRecords returns records in collection', () async {
      final list = await client.listRecords(collection: 'dart_sdk_tests', limit: 10);
      expect(list.total, greaterThanOrEqualTo(1));
      expect(list.records.any((r) => r.collection == 'dart_sdk_tests'), isTrue);
    });
  });
}
