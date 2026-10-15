import 'dart:convert';
import 'package:http/http.dart' as http;
import 'client.dart';
import 'crypto.dart';
import 'exceptions.dart';
import 'models.dart';

/// PocketBase-compatible fluent collection record accessor.
///
/// Example:
/// ```dart
/// final client = StiQRcloudClient(baseUrl: 'http://192.168.1.50:8100');
/// final record = await client.collection('notes').create({
///   'title': 'My First Decentralized Note',
///   'body': 'Encrypted end-to-end!',
/// });
/// ```
class RecordService {
  final StiQRcloudClient _client;
  final String collectionName;

  RecordService(this._client, this.collectionName);

  /// Creates a record in this collection.
  ///
  /// Automatically generates an offline UUIDv7 if [id] is omitted.
  /// If [encrypt] is true, text/attachments are encrypted client-side with AES-256-GCM.
  Future<RecordModel> create(
    Map<String, dynamic> data, {
    CreateRecordOptions? options,
    String? storagePolicy,
    List<String>? storagePeers,
    int? targetReplicas,
    String? targetNodeUrl,
    bool? encrypt,
    List<int>? dek,
    List<int>? fileBytes,
    String? fileName,
    List<int>? pictureBytes,
    String? pictureName,
    String? id,
  }) async {
    final effPolicy = storagePolicy ?? options?.storagePolicy ?? _client.defaultStoragePolicy;
    final effPeers = storagePeers ?? options?.storagePeers;
    final effReplicas = targetReplicas ?? options?.targetReplicas;
    final effNodeUrl = targetNodeUrl ?? options?.targetNodeUrl;
    final effEncrypt = encrypt ?? options?.encrypt ?? false;
    final effDek = dek ?? options?.dek;
    final effFileBytes = fileBytes ?? options?.fileBytes;
    final effFileName = fileName ?? options?.fileName;
    final effPicBytes = pictureBytes ?? options?.pictureBytes;
    final effPicName = pictureName ?? options?.pictureName;
    final effId = id ?? options?.id ?? data['data_id'] ?? data['id'] ?? StiQRcloudCrypto.generateUuidV7();

    final endpoint = await _client.nodeManager.resolveEndpoint(effNodeUrl);

    final body = Map<String, dynamic>.from(data);
    body['id'] = effId;
    body['data_id'] = effId;
    body['collection'] = collectionName;

    if (effPolicy != null) body['replication_policy'] = effPolicy;
    if (effPeers != null && effPeers.isNotEmpty) {
      body['storage_peers'] = effPeers;
    }
    if (effReplicas != null) body['target_replicas'] = effReplicas;

    // Client-side encryption if requested
    if (effEncrypt) {
      final activeDek = effDek != null ? effDek : StiQRcloudCrypto.generateDek();
      if (body.containsKey('text') && body['text'] is String) {
        final env = await StiQRcloudCrypto.encryptText(body['text'] as String, activeDek);
        body['text'] = env.encode();
      } else if (body.containsKey('content') && body['content'] is String) {
        final env = await StiQRcloudCrypto.encryptText(body['content'] as String, activeDek);
        body['content'] = env.encode();
      }
    }

    final pubAddr = await _client.publicAddress ?? '';
    body['user_pub_address'] = pubAddr;

    final uri = Uri.parse('$endpoint/api/collections/${Uri.encodeComponent(collectionName)}/records');

    // If attachments present, send multipart
    if (effFileBytes != null || effPicBytes != null) {
      final req = http.MultipartRequest('POST', uri);
      for (final entry in body.entries) {
        if (entry.value != null) {
          req.fields[entry.key] = entry.value is String
              ? entry.value as String
              : jsonEncode(entry.value);
        }
      }
      if (effFileBytes != null) {
        req.files.add(http.MultipartFile.fromBytes(
          'file',
          effFileBytes,
          filename: effFileName ?? 'attachment.bin',
        ));
      }
      if (effPicBytes != null) {
        req.files.add(http.MultipartFile.fromBytes(
          'picture',
          effPicBytes,
          filename: effPicName ?? 'picture.bin',
        ));
      }
      final streamed = await _client.httpClient.send(req);
      final resp = await http.Response.fromStream(streamed);
      _client.checkResponse(resp, 'collection.create');
      final resMap = jsonDecode(resp.body) as Map<String, dynamic>;
      return RecordModel.fromJson(resMap);
    } else {
      final resp = await _client.httpClient.post(
        uri,
        headers: {'Content-Type': 'application/json'},
        body: jsonEncode(body),
      );
      _client.checkResponse(resp, 'collection.create');
      final resMap = jsonDecode(resp.body) as Map<String, dynamic>;
      return RecordModel.fromJson(resMap);
    }
  }

  /// Fetches a single record by ID
  Future<RecordModel> getOne(String id, {String? targetNodeUrl}) async {
    final endpoint = await _client.nodeManager.resolveEndpoint(targetNodeUrl);
    final uri = Uri.parse(
      '$endpoint/api/collections/${Uri.encodeComponent(collectionName)}/records/${Uri.encodeComponent(id)}',
    );
    final resp = await _client.httpClient.get(uri);
    _client.checkResponse(resp, 'collection.getOne');
    final resMap = jsonDecode(resp.body) as Map<String, dynamic>;
    return RecordModel.fromJson(resMap);
  }

  /// Alias for getOne()
  Future<RecordModel> get(String id, {String? targetNodeUrl}) =>
      getOne(id, targetNodeUrl: targetNodeUrl);

  /// Lists records in this collection with pagination
  Future<ListResult<RecordModel>> getList({
    int page = 1,
    int perPage = 30,
    String? targetNodeUrl,
  }) async {
    final endpoint = await _client.nodeManager.resolveEndpoint(targetNodeUrl);
    final offset = (page - 1) * perPage;
    final uri = Uri.parse(
      '$endpoint/api/collections/${Uri.encodeComponent(collectionName)}/records?limit=$perPage&offset=$offset',
    );
    final resp = await _client.httpClient.get(uri);
    _client.checkResponse(resp, 'collection.getList');
    final map = jsonDecode(resp.body) as Map<String, dynamic>;
    return ListResult<RecordModel>.fromJson(map, (item) => RecordModel.fromJson(item));
  }

  /// Alias for getList()
  Future<ListResult<RecordModel>> list({
    int page = 1,
    int perPage = 30,
    String? targetNodeUrl,
  }) =>
      getList(page: page, perPage: perPage, targetNodeUrl: targetNodeUrl);

  /// Updates an existing record
  Future<RecordModel> update(
    String id,
    Map<String, dynamic> data, {
    String? targetNodeUrl,
  }) async {
    final endpoint = await _client.nodeManager.resolveEndpoint(targetNodeUrl);
    final uri = Uri.parse(
      '$endpoint/api/collections/${Uri.encodeComponent(collectionName)}/records/${Uri.encodeComponent(id)}',
    );

    final headers = <String, String>{'Content-Type': 'application/json'};
    final body = Map<String, dynamic>.from(data);

    final pubAddr = await _client.publicAddress;
    if (pubAddr != null && pubAddr.isNotEmpty) {
      headers['X-User-PubKey'] = pubAddr;
      body['updater_pub_key'] = pubAddr;

      if (_client.keyPair != null) {
        final signBytes = utf8.encode('$id:update');
        final sig = await StiQRcloudCrypto.sign(signBytes, _client.keyPair!);
        headers['X-User-Signature'] = sig;
        body['signature'] = sig;
      }
    }

    final resp = await _client.httpClient.patch(
      uri,
      headers: headers,
      body: jsonEncode(body),
    );
    _client.checkResponse(resp, 'collection.update');
    final resMap = jsonDecode(resp.body) as Map<String, dynamic>;
    return RecordModel.fromJson(resMap);
  }

  /// Deletes a record by ID
  Future<bool> delete(String id, {String? targetNodeUrl}) async {
    final endpoint = await _client.nodeManager.resolveEndpoint(targetNodeUrl);
    final uri = Uri.parse(
      '$endpoint/api/collections/${Uri.encodeComponent(collectionName)}/records/${Uri.encodeComponent(id)}',
    );

    final headers = <String, String>{};
    final pubAddr = await _client.publicAddress;
    if (pubAddr != null && pubAddr.isNotEmpty) {
      headers['X-User-PubKey'] = pubAddr;

      if (_client.keyPair != null) {
        final signBytes = utf8.encode('delete:$id');
        final sig = await StiQRcloudCrypto.sign(signBytes, _client.keyPair!);
        headers['X-User-Signature'] = sig;
      }
    }

    final resp = await _client.httpClient.delete(uri, headers: headers);
    _client.checkResponse(resp, 'collection.delete');
    return true;
  }

  /// Declares or merges schema fields for this collection on the node
  Future<CollectionInfo> ensureSchema(
    List<CollectionField> fields, {
    String? targetNodeUrl,
  }) async {
    return _client.ensureCollection(
      name: collectionName,
      fields: fields,
      targetNodeUrl: targetNodeUrl,
    );
  }
}
