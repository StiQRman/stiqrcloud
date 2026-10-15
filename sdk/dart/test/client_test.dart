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
      expect(health.version, startsWith('v0.'));
      expect(health.nodeId, isNotEmpty);
      expect(health.rnsDest, isNotNull);
    });

    test('getVersion returns version check info', () async {
      final ver = await client.getVersion();
      expect(ver['current_version'], startsWith('v0.'));
      expect(ver['latest_version'], startsWith('v0.'));
      expect(ver['update_available'], isFalse);
    });

    test('createRecord and getRecord (unencrypted)', () async {
      final res = await client.createRecord(
        collection: 'dart_sdk_tests',
        text: 'Hello from Dart SDK unencrypted test',
      );

      final uuidv7Regex = RegExp(r'^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$');
      expect(uuidv7Regex.hasMatch(res.dataId) || res.dataId.startsWith('rec_'), isTrue);
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

      final uuidv7Regex = RegExp(r'^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$');
      expect(uuidv7Regex.hasMatch(res.dataId) || res.dataId.startsWith('rec_'), isTrue);

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

    test('PocketBase-compatible collection API (create, getOne, getList, update, delete)', () async {
      final col = client.collection('dart_pocket_notes');
      final record = await col.create({
        'title': 'Test note via Dart fluent API',
        'body': 'This is a test note',
        'priority': 1,
      }, options: const CreateRecordOptions(storagePolicy: StoragePolicy.localOnly));

      expect(record.id, isNotEmpty);
      expect(record.data['title'], equals('Test note via Dart fluent API'));

      final fetched = await col.getOne(record.id);
      expect(fetched.id, equals(record.id));
      expect(fetched.data['title'], equals('Test note via Dart fluent API'));

      final list = await col.getList(page: 1, perPage: 10);
      expect(list.items.any((r) => r.id == record.id), isTrue);

      final updated = await col.update(record.id, {
        'title': 'Updated note via Dart fluent API',
        'body': 'This is a test note',
        'priority': 2,
      });
      expect(updated.data['title'], equals('Updated note via Dart fluent API'));

      await col.delete(record.id);
    });

    test('ensureCollection declares and handshakes schema fields with node', () async {
      final info = await client.ensureCollection(
        name: 'dart_schema_app',
        fields: [
          const CollectionField(name: 'user_id', type: 'text', required: true),
          const CollectionField(name: 'payload', type: 'json'),
          const CollectionField(name: 'is_active', type: 'bool'),
        ],
      );
      expect(info.name, equals('dart_schema_app'));
      expect(info.fields.map((f) => f.name), containsAll(['user_id', 'payload', 'is_active']));

      final all = await client.listCollections();
      expect(all.any((c) => c.name == 'dart_schema_app'), isTrue);
    });

    test('createRecord with storage policies (local-only, custom routing)', () async {
      final localRes = await client.createRecord(
        collection: 'dart_storage_policy',
        text: 'Stays locally on node',
        storagePolicy: StoragePolicy.localOnly,
      );
      expect(localRes.replicationPolicy, equals('local-only'));

      final customRes = await client.createRecord(
        collection: 'dart_storage_policy',
        text: 'Custom peers routing',
        storagePolicy: StoragePolicy.custom,
        storagePeers: ['node_pi_lan', 'node_backup'],
      );
      expect(customRes.replicationPolicy, equals('custom'));
      expect(customRes.storagePeers, containsAll(['node_pi_lan', 'node_backup']));
    });

    test('NodeManager adds and removes nodes dynamically', () {
      final nm = client.nodeManager;
      nm.addNode(const NodeConfig(url: 'http://10.0.0.99:8100', name: 'Backup Node'));
      expect(nm.nodes.any((n) => n.url == 'http://10.0.0.99:8100'), isTrue);
      nm.removeNode('http://10.0.0.99:8100');
      expect(nm.nodes.any((n) => n.url == 'http://10.0.0.99:8100'), isFalse);
    });
  });
}
