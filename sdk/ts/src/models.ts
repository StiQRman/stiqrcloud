/**
 * TestCloud Decentralized Storage Mesh & Reticulum Client Models
 */

/**
 * Storage replication policy allowing the user full control of data placement:
 * - 'local-only' / 'local': Data stays exclusively on the user's own node (0 external replicas).
 * - 'replicated-2' / 'distributed_2': Data replicates from user's node to exactly 2 other storage nodes.
 * - 'full-mesh' / 'distributed': Data is distributed across standard 3-storage node quorum.
 * - 'custom': Data is routed explicitly to user-selected storage peers.
 */
export type StoragePolicy =
  | 'local-only'
  | 'local'
  | 'replicated-2'
  | 'distributed_2'
  | 'full-mesh'
  | 'distributed'
  | 'custom';

export type AccessPermission = 'view' | 'edit';

export interface AccessControlEntry {
  user_pub_key: string;
  permission: AccessPermission;
  wrapped_dek: string;
  added_at: string;
}

export interface Record {
  data_id: string;
  collection: string;
  user_pub_address?: string;
  owner_signature?: string;
  version: number;
  text: string;
  file_chunk_hash?: string;
  file_name?: string;
  picture_chunk_hash?: string;
  picture_name?: string;
  total_bytes: number;
  acl?: AccessControlEntry[];
  merkle_root?: string;
  replication_policy?: string;
  target_replicas?: number;
  storage_peers?: string[];
  auditor_peers?: string[];
  created_at: string;
  updated_at: string;
}

export interface NodeHealth {
  status: string;
  app_name?: string;
  version?: string;
  node_id: string;
  rns_dest?: string;
  is_public: boolean;
  standalone?: boolean;
  message?: string;
  data?: {
    canBackup?: boolean;
    peers?: number;
  };
}

export interface Peer {
  node_id: string;
  name: string;
  location: string;
  public_ip: string;
  public_addr: string;
  reticulum_dest: string;
  is_public: boolean;
  is_online: boolean;
  price_per_gb_month: number;
  audits_passed?: number;
  audits_performed?: number;
  avg_latency_ms?: number;
  last_seen: string;
  social_score: number;
}

export interface NodeConfig {
  /** Unique ID or label for the node */
  id?: string;
  /** HTTP or HTTPS base URL of the node (e.g. 'http://localhost:8100' or 'http://192.168.1.100:8100') */
  url: string;
  /** Friendly display name */
  name?: string;
  /** Whether this node is designated as the primary gateway */
  isPrimary?: boolean;
  /** Measured round-trip latency in milliseconds */
  latencyMs?: number;
  /** Last health status of the node */
  isOnline?: boolean;
}

export interface CreateRecordOptions {
  /** Collection / table name (e.g. 'notes', 'photos', 'app_state') */
  collection: string;
  /** Main text or JSON string payload */
  text?: string;
  /** Binary file attachment */
  file?: Uint8Array | ArrayBuffer | Blob | { data: Uint8Array; name: string };
  /** Optional file name */
  fileName?: string;
  /** Binary picture attachment */
  picture?: Uint8Array | ArrayBuffer | Blob | { data: Uint8Array; name: string };
  /** Optional picture name */
  pictureName?: string;
  /** Optional specific invariant DataID (auto-generated if omitted) */
  dataId?: string;
  /** Client-side AES-256-GCM encryption */
  encrypt?: boolean;
  /** Explicit 32-byte Data Encryption Key (auto-generated if encrypt=true and dek omitted) */
  dek?: Uint8Array;
  /**
   * Routing & Storage policy:
   * - 'local-only': Kept exclusively on user's node; zero remote replicas.
   * - 'replicated-2': Kept on user's node and replicated to 2 peer nodes.
   * - 'full-mesh': Standard distributed quorum across 3 storage nodes.
   * - 'custom': Replicated strictly to target peers in storagePeers array.
   */
  storagePolicy?: StoragePolicy;
  /** Explicit target storage peer IDs for 'custom' policy */
  storagePeers?: string[];
  /** Optional explicit replica count (e.g. 2) */
  targetReplicas?: number;
  /** Optional specific node endpoint URL to dispatch this request to */
  targetNodeUrl?: string;
}

export interface CreateRecordResponse {
  record: Record;
  data_id: string;
  replication_policy?: string;
  auditor_peers?: string[];
  storage_peers?: string[];
  lightning_invoice?: string;
  dek?: Uint8Array;
}

export interface RecordList {
  records: Record[];
  total: number;
  limit: number;
  offset: number;
}

export interface CollectionField {
  name: string;
  type: 'text' | 'number' | 'file' | 'json' | 'date' | 'bool' | string;
  required?: boolean;
}

export interface CollectionInfo {
  name: string;
  record_count: number;
  fields: CollectionField[];
  system: boolean;
  updated_at: string;
}

export interface EnsureCollectionOptions {
  name: string;
  fields?: CollectionField[];
  targetNodeUrl?: string;
}

