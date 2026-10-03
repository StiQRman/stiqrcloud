import 'dart:convert';

/// Access Control Entry for wrapped DEKs
class AclEntry {
  final String pubAddress;
  final String permission; // "view" | "edit"
  final String wrappedDek;

  AclEntry({
    required this.pubAddress,
    required this.permission,
    required this.wrappedDek,
  });

  factory AclEntry.fromJson(Map<String, dynamic> json) {
    return AclEntry(
      pubAddress: json['pub_address'] as String? ?? '',
      permission: json['permission'] as String? ?? 'view',
      wrappedDek: json['wrapped_dek'] as String? ?? '',
    );
  }

  Map<String, dynamic> toJson() => {
        'pub_address': pubAddress,
        'permission': permission,
        'wrapped_dek': wrappedDek,
      };
}

/// Decentralized Record stored in TestCloud
class Record {
  final String dataId;
  final String collection;
  final String userPubAddress;
  final String ownerSignature;
  final int version;
  final String text;
  final String fileName;
  final String pictureName;
  final String fileChunkHash;
  final String pictureChunkHash;
  final int totalBytes;
  final List<AclEntry>? acl;
  final String merkleRoot;
  final List<String> storagePeers;
  final List<String> auditorPeers;
  final DateTime? createdAt;
  final DateTime? updatedAt;

  Record({
    required this.dataId,
    required this.collection,
    this.userPubAddress = '',
    this.ownerSignature = '',
    this.version = 1,
    this.text = '',
    this.fileName = '',
    this.pictureName = '',
    this.fileChunkHash = '',
    this.pictureChunkHash = '',
    this.totalBytes = 0,
    this.acl,
    this.merkleRoot = '',
    this.storagePeers = const [],
    this.auditorPeers = const [],
    this.createdAt,
    this.updatedAt,
  });

  factory Record.fromJson(Map<String, dynamic> json) {
    List<AclEntry>? aclList;
    if (json['acl'] != null && json['acl'] is List) {
      aclList = (json['acl'] as List)
          .map((e) => AclEntry.fromJson(e as Map<String, dynamic>))
          .toList();
    }

    List<String> parsePeers(dynamic list) {
      if (list is List) {
        return list.map((e) => e.toString()).toList();
      }
      return const [];
    }

    DateTime? parseDate(dynamic d) {
      if (d == null) return null;
      if (d is String) return DateTime.tryParse(d);
      return null;
    }

    return Record(
      dataId: json['data_id'] as String? ?? '',
      collection: json['collection'] as String? ?? 'default',
      userPubAddress: json['user_pub_address'] as String? ?? '',
      ownerSignature: json['owner_signature'] as String? ?? '',
      version: json['version'] as int? ?? 1,
      text: json['text'] as String? ?? '',
      fileName: json['file_name'] as String? ?? '',
      pictureName: json['picture_name'] as String? ?? '',
      fileChunkHash: json['file_chunk_hash'] as String? ?? '',
      pictureChunkHash: json['picture_chunk_hash'] as String? ?? '',
      totalBytes: json['total_bytes'] as int? ?? 0,
      acl: aclList,
      merkleRoot: json['merkle_root'] as String? ?? '',
      storagePeers: parsePeers(json['storage_peers']),
      auditorPeers: parsePeers(json['auditor_peers']),
      createdAt: parseDate(json['created_at']),
      updatedAt: parseDate(json['updated_at']),
    );
  }

  Map<String, dynamic> toJson() => {
        'data_id': dataId,
        'collection': collection,
        'user_pub_address': userPubAddress,
        'owner_signature': ownerSignature,
        'version': version,
        'text': text,
        'file_name': fileName,
        'picture_name': pictureName,
        'file_chunk_hash': fileChunkHash,
        'picture_chunk_hash': pictureChunkHash,
        'total_bytes': totalBytes,
        if (acl != null) 'acl': acl!.map((e) => e.toJson()).toList(),
        'merkle_root': merkleRoot,
        'storage_peers': storagePeers,
        'auditor_peers': auditorPeers,
        if (createdAt != null) 'created_at': createdAt!.toIso8601String(),
        if (updatedAt != null) 'updated_at': updatedAt!.toIso8601String(),
      };

  @override
  String toString() =>
      'Record(dataId: $dataId, collection: $collection, version: $version, text: "${text.length > 40 ? '${text.substring(0, 37)}...' : text}")';
}

/// Response returned when creating or updating a record in TestCloud
class CreateRecordResponse {
  final String dataId;
  final Record record;
  final String? lightningInvoice;
  final List<String> auditorPeers;
  final List<String> storagePeers;

  CreateRecordResponse({
    required this.dataId,
    required this.record,
    this.lightningInvoice,
    this.auditorPeers = const [],
    this.storagePeers = const [],
  });

  factory CreateRecordResponse.fromJson(Map<String, dynamic> json) {
    List<String> parsePeers(dynamic list) {
      if (list is List) {
        return list.map((e) => e.toString()).toList();
      }
      return const [];
    }

    final recJson = json['record'] is Map<String, dynamic>
        ? json['record'] as Map<String, dynamic>
        : json;

    return CreateRecordResponse(
      dataId: json['data_id'] as String? ?? recJson['data_id'] as String? ?? '',
      record: Record.fromJson(recJson),
      lightningInvoice: json['lightning_invoice'] as String?,
      auditorPeers: parsePeers(json['auditor_peers']),
      storagePeers: parsePeers(json['storage_peers']),
    );
  }
}

/// Paginated record list
class RecordList {
  final List<Record> records;
  final int total;

  RecordList({required this.records, required this.total});

  factory RecordList.fromJson(Map<String, dynamic> json) {
    final list = json['records'] as List? ?? [];
    return RecordList(
      records: list
          .map((e) => Record.fromJson(e as Map<String, dynamic>))
          .toList(),
      total: json['total'] as int? ?? list.length,
    );
  }
}

/// Health and status of a TestCloud node
class NodeHealth {
  final String status;
  final String appName;
  final String version;
  final String nodeId;
  final String? rnsDest;
  final bool isPublic;
  final bool standalone;

  NodeHealth({
    required this.status,
    required this.appName,
    required this.version,
    required this.nodeId,
    this.rnsDest,
    this.isPublic = false,
    this.standalone = false,
  });

  factory NodeHealth.fromJson(Map<String, dynamic> json) {
    return NodeHealth(
      status: json['status'] as String? ?? 'unknown',
      appName: json['app_name'] as String? ?? 'testcloud',
      version: json['version'] as String? ?? '',
      nodeId: json['node_id'] as String? ?? '',
      rnsDest: json['rns_dest'] as String?,
      isPublic: json['is_public'] as bool? ?? false,
      standalone: json['standalone'] as bool? ?? false,
    );
  }

  @override
  String toString() =>
      'NodeHealth(nodeId: $nodeId, status: $status, version: $version, public: $isPublic)';
}
