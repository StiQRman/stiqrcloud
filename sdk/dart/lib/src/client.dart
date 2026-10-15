import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';
import 'package:cryptography/cryptography.dart';
import 'package:http/http.dart' as http;
import 'crypto.dart';
import 'exceptions.dart';
import 'models.dart';
import 'node_manager.dart';
import 'record_service.dart';

/// Official client for interacting with a StiQRcloud node or cluster.
/// Supports both Dart/Flutter mobile apps and desktop/web/server backends.
class StiQRcloudClient {
  final NodeManager nodeManager;
  final SimpleKeyPair? keyPair;
  final String? defaultStoragePolicy;
  final http.Client _client;

  String? _cachedPublicAddress;

  StiQRcloudClient({
    String? baseUrl,
    List<dynamic>? nodes,
    this.keyPair,
    this.defaultStoragePolicy,
    http.Client? httpClient,
  })  : _client = httpClient ?? http.Client(),
        nodeManager = NodeManager(
          primaryUrl: baseUrl,
          nodes: nodes,
          httpClient: httpClient,
        );

  /// Primary node URL currently targeted by this client
  String get baseUrl => nodeManager.primaryUrl;

  /// Underlying HTTP client instance
  http.Client get httpClient => _client;

  /// Static helper to generate a collision-free RFC 9562 UUIDv7 offline
  static String generateId() => TestCloudCrypto.generateUuidV7();

  /// Gets the hexadecimal Ed25519 public key address of the configured keyPair
  Future<String?> get publicAddress async {
    if (keyPair == null) return null;
    _cachedPublicAddress ??= await TestCloudCrypto.getPublicAddress(keyPair!);
    return _cachedPublicAddress;
  }

  /// Adds a custom self-hosted or community node to the client's failover pool
  NodeConfig addNode(String url, {String? name, bool isPrimary = false}) {
    return nodeManager.addNode(url, name: name, isPrimary: isPrimary);
  }

  /// Sets the active primary node for all upcoming requests
  void setPrimaryNode(String url) {
    nodeManager.setPrimaryNode(url);
  }

  /// PocketBase-compatible fluent collection accessor:
  /// `client.collection('notes').create({...})`
  RecordService collection(String collectionName) {
    return RecordService(this, collectionName);
  }

  /// Checks node health, status, version, and Reticulum mesh destination
  Future<NodeHealth> getHealth([String? targetNodeUrl]) async {
    final endpoint = await nodeManager.resolveEndpoint(targetNodeUrl);
    final uri = Uri.parse('$endpoint/api/health');
    final response = await _client.get(uri);
    checkResponse(response, 'getHealth');
    final map = jsonDecode(response.body) as Map<String, dynamic>;
    return NodeHealth.fromJson(map);
  }

  /// Checks software update status against GitHub / release repository
  Future<Map<String, dynamic>> getVersion([String? targetNodeUrl]) async {
    final endpoint = await nodeManager.resolveEndpoint(targetNodeUrl);
    final uri = Uri.parse('$endpoint/api/version');
    final response = await _client.get(uri);
    checkResponse(response, 'getVersion');
    return jsonDecode(response.body) as Map<String, dynamic>;
  }

  /// Creates a new decentralized record in the cluster.
  ///
  /// Automatically generates an offline UUIDv7 if [dataId] is omitted.
  /// If [encrypt] is true, [text], [fileBytes], and [pictureBytes] are encrypted
  /// client-side with AES-256-GCM using [dek] (or an auto-generated 256-bit DEK).
  ///
  /// If a [keyPair] is configured, the record is signed with the user's private key.
  Future<CreateRecordResponse> createRecord({
    required String collection,
    String text = '',
    List<int>? fileBytes,
    String? fileName,
    List<int>? pictureBytes,
    String? pictureName,
    String? dataId,
    String? storagePolicy,
    List<String>? storagePeers,
    int? targetReplicas,
    String? targetNodeUrl,
    bool encrypt = false,
    List<int>? dek,
  }) async {
    final endpoint = await nodeManager.resolveEndpoint(targetNodeUrl);
    final uri = Uri.parse('$endpoint/api/records');
    final policy = storagePolicy ?? defaultStoragePolicy;
    final finalDataId = dataId ?? TestCloudCrypto.generateUuidV7();

    // Handle client-side end-to-end encryption if enabled
    String processedText = text;
    List<int>? processedFile = fileBytes;
    List<int>? processedPic = pictureBytes;
    Uint8List? activeDek;

    if (encrypt) {
      activeDek = dek != null ? Uint8List.fromList(dek) : TestCloudCrypto.generateDek();
      if (text.isNotEmpty) {
        final envelope = await TestCloudCrypto.encryptText(text, activeDek);
        processedText = envelope.encode();
      }
      if (fileBytes != null && fileBytes.isNotEmpty) {
        processedFile = await TestCloudCrypto.encryptBytes(fileBytes, activeDek);
      }
      if (pictureBytes != null && pictureBytes.isNotEmpty) {
        processedPic = await TestCloudCrypto.encryptBytes(pictureBytes, activeDek);
      }
    }

    final pubAddr = await publicAddress ?? '';
    String signature = '';
    if (keyPair != null) {
      final signPayload = utf8.encode('$collection:$processedText:$pubAddr');
      signature = await TestCloudCrypto.sign(signPayload, keyPair!);
    }

    // If attachments present, send multipart form; otherwise send JSON
    if (processedFile != null || processedPic != null) {
      final req = http.MultipartRequest('POST', uri);
      req.fields['collection'] = collection;
      req.fields['text'] = processedText;
      req.fields['user_pub_address'] = pubAddr;
      req.fields['signature'] = signature;
      req.fields['data_id'] = finalDataId;

      if (policy != null) req.fields['replication_policy'] = policy;
      if (storagePeers != null && storagePeers.isNotEmpty) {
        req.fields['storage_peers'] = jsonEncode(storagePeers);
      }
      if (targetReplicas != null) {
        req.fields['target_replicas'] = targetReplicas.toString();
      }

      if (processedFile != null) {
        req.files.add(http.MultipartFile.fromBytes(
          'file',
          processedFile,
          filename: fileName ?? 'attachment.bin',
        ));
      }
      if (processedPic != null) {
        req.files.add(http.MultipartFile.fromBytes(
          'picture',
          processedPic,
          filename: pictureName ?? 'picture.bin',
        ));
      }

      final streamed = await _client.send(req);
      final resp = await http.Response.fromStream(streamed);
      checkResponse(resp, 'createRecord');
      final map = jsonDecode(resp.body) as Map<String, dynamic>;
      return CreateRecordResponse.fromJson(map);
    } else {
      final body = <String, dynamic>{
        'collection': collection,
        'text': processedText,
        'user_pub_address': pubAddr,
        'signature': signature,
        'data_id': finalDataId,
      };

      if (policy != null) body['replication_policy'] = policy;
      if (storagePeers != null && storagePeers.isNotEmpty) {
        body['storage_peers'] = storagePeers;
      }
      if (targetReplicas != null) body['target_replicas'] = targetReplicas;

      final resp = await _client.post(
        uri,
        headers: {'Content-Type': 'application/json'},
        body: jsonEncode(body),
      );
      checkResponse(resp, 'createRecord');
      final map = jsonDecode(resp.body) as Map<String, dynamic>;
      return CreateRecordResponse.fromJson(map);
    }
  }

  /// Fetches a record by [dataId]
  Future<Record> getRecord(
    String dataId, {
    bool decrypt = false,
    List<int>? dek,
    String? targetNodeUrl,
  }) async {
    final endpoint = await nodeManager.resolveEndpoint(targetNodeUrl);
    final uri = Uri.parse('$endpoint/api/records/$dataId');
    final res = await _client.get(uri);
    checkResponse(res, 'getRecord');

    final jsonMap = jsonDecode(res.body) as Map<String, dynamic>;
    final record = Record.fromJson(jsonMap);

    if (decrypt && dek != null && record.text.isNotEmpty) {
      final envelope = EncryptedEnvelope.tryParse(record.text);
      if (envelope != null) {
        final plainText = await TestCloudCrypto.decryptText(envelope, dek);
        return Record(
          dataId: record.dataId,
          collection: record.collection,
          userPubAddress: record.userPubAddress,
          ownerSignature: record.ownerSignature,
          version: record.version,
          text: plainText,
          fileName: record.fileName,
          pictureName: record.pictureName,
          fileChunkHash: record.fileChunkHash,
          pictureChunkHash: record.pictureChunkHash,
          totalBytes: record.totalBytes,
          acl: record.acl,
          merkleRoot: record.merkleRoot,
          storagePeers: record.storagePeers,
          auditorPeers: record.auditorPeers,
          createdAt: record.createdAt,
          updatedAt: record.updatedAt,
        );
      }
    }
    return record;
  }

  /// Lists records across collections with pagination
  Future<RecordList> listRecords({
    String? collection,
    int limit = 50,
    int offset = 0,
    String? targetNodeUrl,
  }) async {
    final endpoint = await nodeManager.resolveEndpoint(targetNodeUrl);
    final queryParams = <String, String>{
      'limit': limit.toString(),
      'offset': offset.toString(),
      if (collection != null && collection.isNotEmpty) 'collection': collection,
    };

    final uri = Uri.parse('$endpoint/api/records').replace(queryParameters: queryParams);
    final res = await _client.get(uri);
    checkResponse(res, 'listRecords');

    final jsonMap = jsonDecode(res.body) as Map<String, dynamic>;
    return RecordList.fromJson(jsonMap);
  }

  /// Declares or handshakes a collection and its schema fields with a node
  Future<CollectionInfo> ensureCollection({
    required String name,
    List<CollectionField>? fields,
    String? targetNodeUrl,
  }) async {
    final endpoint = await nodeManager.resolveEndpoint(targetNodeUrl);
    final uri = Uri.parse('$endpoint/api/collections/ensure');
    final headers = <String, String>{'Content-Type': 'application/json'};
    final pub = await publicAddress;
    if (pub != null) headers['X-User-PubKey'] = pub;

    final body = jsonEncode({
      'name': name,
      'fields': fields?.map((f) => f.toJson()).toList() ?? [],
    });

    final resp = await _client.post(uri, headers: headers, body: body);
    checkResponse(resp, 'ensureCollection');
    final map = jsonDecode(resp.body) as Map<String, dynamic>;
    return CollectionInfo.fromJson(map);
  }

  /// Lists all registered collections on the node
  Future<List<CollectionInfo>> listCollections([String? targetNodeUrl]) async {
    final endpoint = await nodeManager.resolveEndpoint(targetNodeUrl);
    final uri = Uri.parse('$endpoint/api/dashboard/collections');
    final resp = await _client.get(uri);
    checkResponse(resp, 'listCollections');
    final map = jsonDecode(resp.body) as Map<String, dynamic>;
    final list = map['collections'] as List? ?? [];
    return list.map((c) => CollectionInfo.fromJson(c as Map<String, dynamic>)).toList();
  }

  /// Lists all known swarm peers in the cluster
  Future<List<Peer>> listPeers([String? targetNodeUrl]) async {
    final endpoint = await nodeManager.resolveEndpoint(targetNodeUrl);
    final uri = Uri.parse('$endpoint/api/peers');
    final res = await _client.get(uri);
    checkResponse(res, 'listPeers');
    final list = jsonDecode(res.body) as List? ?? [];
    return list.map((p) => Peer.fromJson(p as Map<String, dynamic>)).toList();
  }

  /// Downloads the file binary blob associated with [dataId]
  Future<Uint8List> downloadFile(
    String dataId, {
    bool decrypt = false,
    List<int>? dek,
    String? targetNodeUrl,
  }) async {
    final endpoint = await nodeManager.resolveEndpoint(targetNodeUrl);
    final uri = Uri.parse('$endpoint/api/records/$dataId/file');
    final res = await _client.get(uri);
    checkResponse(res, 'downloadFile');

    if (decrypt && dek != null) {
      final plain = await TestCloudCrypto.decryptBytes(res.bodyBytes, dek);
      return Uint8List.fromList(plain);
    }
    return res.bodyBytes;
  }

  /// Downloads the picture binary blob associated with [dataId]
  Future<Uint8List> downloadPicture(
    String dataId, {
    bool decrypt = false,
    List<int>? dek,
    String? targetNodeUrl,
  }) async {
    final endpoint = await nodeManager.resolveEndpoint(targetNodeUrl);
    final uri = Uri.parse('$endpoint/api/records/$dataId/picture');
    final res = await _client.get(uri);
    checkResponse(res, 'downloadPicture');

    if (decrypt && dek != null) {
      final plain = await TestCloudCrypto.decryptBytes(res.bodyBytes, dek);
      return Uint8List.fromList(plain);
    }
    return res.bodyBytes;
  }

  /// Realtime stream that polls the cluster for newly arrived or updated records
  Stream<Record> pollRecords({
    String? collection,
    Duration interval = const Duration(seconds: 3),
    String? targetNodeUrl,
  }) async* {
    final seenDataIds = <String>{};
    try {
      final initial = await listRecords(collection: collection, limit: 100, targetNodeUrl: targetNodeUrl);
      for (final rec in initial.records) {
        seenDataIds.add(rec.dataId);
      }
    } catch (_) {}

    while (true) {
      await Future.delayed(interval);
      try {
        final current = await listRecords(collection: collection, limit: 50, targetNodeUrl: targetNodeUrl);
        for (final rec in current.records) {
          if (!seenDataIds.contains(rec.dataId)) {
            seenDataIds.add(rec.dataId);
            yield rec;
          }
        }
      } catch (_) {}
    }
  }

  void checkResponse(http.Response response, String operation) {
    if (response.statusCode >= 200 && response.statusCode < 300) {
      return;
    }
    throw StiQRcloudApiException(
      response.statusCode,
      'Operation "$operation" failed with status code ${response.statusCode}',
      response.body,
    );
  }

  void close() {
    _client.close();
  }
}

// Backwards-compatible alias
typedef TestCloudClient = StiQRcloudClient;
