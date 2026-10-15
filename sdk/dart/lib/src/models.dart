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
  final String? replicationPolicy;
  final String? lightningInvoice;
  final List<String> auditorPeers;
  final List<String> storagePeers;

  CreateRecordResponse({
    required this.dataId,
    required this.record,
    this.replicationPolicy,
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
      replicationPolicy: json['replication_policy'] as String? ?? recJson['replication_policy'] as String?,
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

/// Storage policy options for record replication
class StoragePolicy {
  /// Stored strictly on the local node, zero network broadcast
  static const String local = 'local-only';
  static const String localOnly = 'local-only';

  /// Replicated across 2 nodes in the swarm
  static const String replicated2 = 'replicated-2';

  /// Standard 3-node auditor + storage quorum
  static const String meshQuorum = 'mesh-quorum';
  static const String fullMesh = 'mesh-quorum';

  /// User-designated custom storage nodes
  static const String custom = 'custom';
}

/// Schema field definition for collection handshakes
class CollectionField {
  final String name;
  final String type; // 'text' | 'number' | 'boolean' | 'json' | 'vector' | 'file'
  final bool required;
  final dynamic defaultValue;

  const CollectionField({
    required this.name,
    required this.type,
    this.required = false,
    this.defaultValue,
  });

  factory CollectionField.fromJson(Map<String, dynamic> json) {
    return CollectionField(
      name: json['name'] as String? ?? '',
      type: json['type'] as String? ?? 'text',
      required: json['required'] as bool? ?? false,
      defaultValue: json['default_value'],
    );
  }

  Map<String, dynamic> toJson() => {
        'name': name,
        'type': type,
        'required': required,
        if (defaultValue != null) 'default_value': defaultValue,
      };
}

/// Information about a registered collection in TestCloud
class CollectionInfo {
  final String id;
  final String name;
  final String type;
  final List<CollectionField> fields;
  final int recordCount;

  CollectionInfo({
    required this.id,
    required this.name,
    required this.type,
    this.fields = const [],
    this.recordCount = 0,
  });

  factory CollectionInfo.fromJson(Map<String, dynamic> json) {
    final fieldsList = json['fields'] as List? ?? [];
    return CollectionInfo(
      id: json['id'] as String? ?? '',
      name: json['name'] as String? ?? '',
      type: json['type'] as String? ?? 'base',
      fields: fieldsList
          .map((f) => CollectionField.fromJson(f as Map<String, dynamic>))
          .toList(),
      recordCount: json['record_count'] as int? ?? json['records'] as int? ?? 0,
    );
  }

  Map<String, dynamic> toJson() => {
        'id': id,
        'name': name,
        'type': type,
        'fields': fields.map((f) => f.toJson()).toList(),
        'record_count': recordCount,
      };
}

/// Connected peer on the decentralized mesh
class Peer {
  final String nodeId;
  final String name;
  final String location;
  final String publicAddr;
  final String reticulumDest;
  final bool isPublic;
  final bool isOnline;
  final int pricePerGbMonth;
  final double avgLatencyMs;
  final double socialScore;

  Peer({
    required this.nodeId,
    this.name = '',
    this.location = '',
    this.publicAddr = '',
    this.reticulumDest = '',
    this.isPublic = false,
    this.isOnline = false,
    this.pricePerGbMonth = 100,
    this.avgLatencyMs = 0,
    this.socialScore = 0,
  });

  factory Peer.fromJson(Map<String, dynamic> json) {
    return Peer(
      nodeId: json['node_id'] as String? ?? '',
      name: json['name'] as String? ?? '',
      location: json['location'] as String? ?? '',
      publicAddr: json['public_addr'] as String? ?? '',
      reticulumDest: json['reticulum_dest'] as String? ?? '',
      isPublic: json['is_public'] as bool? ?? false,
      isOnline: json['is_online'] as bool? ?? false,
      pricePerGbMonth: json['price_per_gb_month'] as int? ?? 100,
      avgLatencyMs: (json['avg_latency_ms'] as num?)?.toDouble() ?? 0,
      socialScore: (json['social_score'] as num?)?.toDouble() ?? 0,
    );
  }
}

/// Paginated list result matching PocketBase conventions
class ListResult<T> {
  final int page;
  final int perPage;
  final int totalItems;
  final int totalPages;
  final List<T> items;

  ListResult({
    required this.page,
    required this.perPage,
    required this.totalItems,
    required this.totalPages,
    required this.items,
  });

  factory ListResult.fromJson(
    Map<String, dynamic> json,
    T Function(Map<String, dynamic>) itemDecoder,
  ) {
    final list = json['items'] as List? ?? [];
    return ListResult(
      page: json['page'] as int? ?? 1,
      perPage: json['perPage'] as int? ?? 30,
      totalItems: json['totalItems'] as int? ?? list.length,
      totalPages: json['totalPages'] as int? ?? 1,
      items: list.map((item) => itemDecoder(item as Map<String, dynamic>)).toList(),
    );
  }
}

/// Node configuration for multi-node clusters and failover
class NodeConfig {
  final String url;
  final String? name;
  final bool isPrimary;
  final bool isHealthy;
  final DateTime? lastChecked;

  const NodeConfig({
    required this.url,
    this.name,
    this.isPrimary = false,
    this.isHealthy = true,
    this.lastChecked,
  });
}

/// Options when creating or saving a record
class CreateRecordOptions {
  final String? storagePolicy;
  final List<String>? storagePeers;
  final int? targetReplicas;
  final bool encrypt;
  final List<int>? dek;
  final String? id; // Custom or pre-generated UUIDv7
  final String? targetNodeUrl;
  final List<int>? fileBytes;
  final String? fileName;
  final List<int>? pictureBytes;
  final String? pictureName;

  const CreateRecordOptions({
    this.storagePolicy,
    this.storagePeers,
    this.targetReplicas,
    this.encrypt = false,
    this.dek,
    this.id,
    this.targetNodeUrl,
    this.fileBytes,
    this.fileName,
    this.pictureBytes,
    this.pictureName,
  });
}

/// PocketBase-compatible record model returned by collection operations
class RecordModel {
  final String id;
  final String collection;
  final Map<String, dynamic> data;
  final DateTime? created;
  final DateTime? updated;

  RecordModel({
    required this.id,
    required this.collection,
    required this.data,
    this.created,
    this.updated,
  });

  factory RecordModel.fromJson(Map<String, dynamic> json) {
    final id = json['id'] as String? ?? json['data_id'] as String? ?? '';
    final col = json['collection'] as String? ?? '';
    DateTime? parseDate(dynamic val) {
      if (val is String) return DateTime.tryParse(val);
      return null;
    }

    final copy = Map<String, dynamic>.from(json);
    return RecordModel(
      id: id,
      collection: col,
      data: copy,
      created: parseDate(json['created'] ?? json['created_at']),
      updated: parseDate(json['updated'] ?? json['updated_at']),
    );
  }

  dynamic operator [](String key) {
    if (key == 'id') return id;
    if (key == 'collection') return collection;
    return data[key];
  }

  Map<String, dynamic> toJson() => data;

  @override
  String toString() => 'RecordModel(id: $id, collection: $collection, data: $data)';
}
