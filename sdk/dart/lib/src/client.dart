import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';
import 'package:cryptography/cryptography.dart';
import 'package:http/http.dart' as http;
import 'crypto.dart';
import 'exceptions.dart';
import 'models.dart';

/// Official client for interacting with a TestCloud node or cluster.
class TestCloudClient {
  final String baseUrl;
  final SimpleKeyPair? keyPair;
  final http.Client _client;

  String? _cachedPublicAddress;

  TestCloudClient({
    required String baseUrl,
    this.keyPair,
    http.Client? httpClient,
  })  : baseUrl = baseUrl.endsWith('/') ? baseUrl.substring(0, baseUrl.length - 1) : baseUrl,
        _client = httpClient ?? http.Client();

  /// Gets the hexadecimal Ed25519 public key address of the configured keyPair
  Future<String?> get publicAddress async {
    if (keyPair == null) return null;
    _cachedPublicAddress ??= await TestCloudCrypto.getPublicAddress(keyPair!);
    return _cachedPublicAddress;
  }

  /// Checks node health, status, version, and Reticulum mesh destination
  Future<NodeHealth> getHealth() async {
    final uri = Uri.parse('$baseUrl/api/health');
    final response = await _client.get(uri);
    _checkResponse(response, 'getHealth');
    final map = jsonDecode(response.body) as Map<String, dynamic>;
    return NodeHealth.fromJson(map);
  }

  /// Checks software update status against GitHub / release repository
  Future<Map<String, dynamic>> getVersion() async {
    final uri = Uri.parse('$baseUrl/api/version');
    final response = await _client.get(uri);
    _checkResponse(response, 'getVersion');
    return jsonDecode(response.body) as Map<String, dynamic>;
  }

  /// Creates a new decentralized record in the cluster
  ///
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
    bool encrypt = false,
    List<int>? dek,
  }) async {
    final uri = Uri.parse('$baseUrl/api/records');

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
      if (dataId != null && dataId.isNotEmpty) {
        req.fields['data_id'] = dataId;
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

      final streamedResponse = await _client.send(req);
      final res = await http.Response.fromStream(streamedResponse);
      _checkResponse(res, 'createRecord (multipart)');
      final jsonMap = jsonDecode(res.body) as Map<String, dynamic>;
      return CreateRecordResponse.fromJson(jsonMap);
    } else {
      final body = {
        'collection': collection,
        'text': processedText,
        'user_pub_address': pubAddr,
        'signature': signature,
        if (dataId != null && dataId.isNotEmpty) 'data_id': dataId,
      };

      final res = await _client.post(
        uri,
        headers: {'Content-Type': 'application/json'},
        body: jsonEncode(body),
      );
      _checkResponse(res, 'createRecord (json)');
      final jsonMap = jsonDecode(res.body) as Map<String, dynamic>;
      return CreateRecordResponse.fromJson(jsonMap);
    }
  }

  /// Retrieves a record by its invariant persistent DataID
  ///
  /// Any node in the cluster will resolve the record even if stored on other peers.
  /// If [decrypt] is true and a valid [dek] is provided, encrypted text is automatically decrypted.
  Future<Record> getRecord(
    String dataId, {
    bool decrypt = false,
    List<int>? dek,
  }) async {
    final uri = Uri.parse('$baseUrl/api/records/$dataId');
    final res = await _client.get(uri);
    if (res.statusCode == 404) {
      throw TestCloudNotFoundException(dataId, res.body);
    }
    _checkResponse(res, 'getRecord');

    final jsonMap = jsonDecode(res.body) as Map<String, dynamic>;
    final record = Record.fromJson(jsonMap);

    if (decrypt && dek != null && record.text.isNotEmpty) {
      final envelope = EncryptedEnvelope.tryParse(record.text);
      if (envelope != null) {
        final decryptedText = await TestCloudCrypto.decryptText(envelope, dek);
        return Record(
          dataId: record.dataId,
          collection: record.collection,
          userPubAddress: record.userPubAddress,
          ownerSignature: record.ownerSignature,
          version: record.version,
          text: decryptedText,
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

  /// Lists records, optionally filtered by collection
  Future<RecordList> listRecords({
    String? collection,
    int limit = 50,
    int offset = 0,
  }) async {
    final queryParams = <String, String>{
      'limit': limit.toString(),
      'offset': offset.toString(),
      if (collection != null && collection.isNotEmpty) 'collection': collection,
    };

    final uri = Uri.parse('$baseUrl/api/records').replace(queryParameters: queryParams);
    final res = await _client.get(uri);
    _checkResponse(res, 'listRecords');

    final jsonMap = jsonDecode(res.body) as Map<String, dynamic>;
    return RecordList.fromJson(jsonMap);
  }

  /// Downloads the file binary blob associated with [dataId]
  Future<Uint8List> downloadFile(
    String dataId, {
    bool decrypt = false,
    List<int>? dek,
  }) async {
    final uri = Uri.parse('$baseUrl/api/records/$dataId/file');
    final res = await _client.get(uri);
    _checkResponse(res, 'downloadFile');

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
  }) async {
    final uri = Uri.parse('$baseUrl/api/records/$dataId/picture');
    final res = await _client.get(uri);
    _checkResponse(res, 'downloadPicture');

    if (decrypt && dek != null) {
      final plain = await TestCloudCrypto.decryptBytes(res.bodyBytes, dek);
      return Uint8List.fromList(plain);
    }
    return res.bodyBytes;
  }

  /// Lists all known swarm peers in the cluster
  Future<List<dynamic>> listPeers() async {
    final uri = Uri.parse('$baseUrl/api/peers');
    final res = await _client.get(uri);
    _checkResponse(res, 'listPeers');
    return jsonDecode(res.body) as List<dynamic>;
  }

  /// Realtime stream that polls the cluster for newly arrived or updated records
  Stream<Record> pollRecords({
    String? collection,
    Duration interval = const Duration(seconds: 3),
  }) async* {
    final seenDataIds = <String>{};
    // Prime with existing records
    try {
      final initial = await listRecords(collection: collection, limit: 100);
      for (final rec in initial.records) {
        seenDataIds.add(rec.dataId);
      }
    } catch (_) {}

    while (true) {
      await Future.delayed(interval);
      try {
        final current = await listRecords(collection: collection, limit: 50);
        for (final rec in current.records) {
          if (!seenDataIds.contains(rec.dataId)) {
            seenDataIds.add(rec.dataId);
            yield rec;
          }
        }
      } catch (_) {}
    }
  }

  void _checkResponse(http.Response response, String operation) {
    if (response.statusCode >= 200 && response.statusCode < 300) {
      return;
    }
    throw TestCloudApiException(
      response.statusCode,
      'Operation "$operation" failed with status code ${response.statusCode}',
      response.body,
    );
  }

  void close() {
    _client.close();
  }
}
