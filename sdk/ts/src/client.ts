import {
  CollectionField,
  CollectionInfo,
  CreateRecordOptions,
  CreateRecordResponse,
  EnsureCollectionOptions,
  NodeConfig,
  NodeHealth,
  Peer,
  Record,
  RecordList,
  StoragePolicy,
} from './models.js';
import { NodeManager } from './node-manager.js';
import { TestCloudApiException, TestCloudNotFoundError } from './errors.js';
import { EncryptedEnvelope, TestCloudCrypto } from './crypto.js';

export interface TestCloudClientOptions {
  /**
   * Primary node URL. Can be the user's own local node ('http://localhost:8100'),
   * a home LAN Raspberry Pi ('http://192.168.1.50:8100'), or a public community gateway.
   */
  baseUrl?: string;
  /**
   * Additional custom or backup nodes to register in the client
   */
  nodes?: (string | NodeConfig)[];
  /**
   * Ed25519 keypair for record ownership signing and identity
   */
  keyPair?: { publicKeyHex: string; privateKey: CryptoKey };
  /**
   * Default storage policy for records created through this client:
   * - 'local-only': Kept strictly on the user's node; 0 external replicas.
   * - 'replicated-2': Kept on user's node and replicated to 2 peer nodes.
   * - 'full-mesh': Standard 3-node storage quorum.
   * - 'custom': User-specified target nodes.
   */
  defaultStoragePolicy?: StoragePolicy;
  /**
   * Optional custom fetch implementation
   */
  fetch?: typeof fetch;
}

export class TestCloudClient {
  public readonly nodeManager: NodeManager;
  public keyPair?: { publicKeyHex: string; privateKey: CryptoKey };
  public defaultStoragePolicy?: StoragePolicy;
  private readonly _fetch: typeof fetch;

  constructor(options?: TestCloudClientOptions) {
    this._fetch = options?.fetch ?? globalThis.fetch.bind(globalThis);
    this.keyPair = options?.keyPair;
    this.defaultStoragePolicy = options?.defaultStoragePolicy;

    this.nodeManager = new NodeManager({
      primaryUrl: options?.baseUrl,
      nodes: options?.nodes,
    });
  }

  /**
   * Returns the user's hex Ed25519 public address (if keyPair configured)
   */
  get publicAddress(): string | undefined {
    return this.keyPair?.publicKeyHex;
  }

  /**
   * Adds the user's own node or another node to the client
   */
  addNode(node: string | NodeConfig): NodeConfig {
    return this.nodeManager.addNode(node);
  }

  /**
   * Sets the user's preferred active node
   */
  setPrimaryNode(urlOrId: string): NodeConfig {
    return this.nodeManager.setPrimaryNode(urlOrId);
  }

  /**
   * Checks the health and connectivity of the active node (or a specific node)
   */
  async getHealth(targetNodeUrl?: string): Promise<NodeHealth> {
    const endpoint = await this.nodeManager.resolveEndpoint(targetNodeUrl);
    return await this.nodeManager.checkHealth(endpoint);
  }

  /**
   * Lists known P2P swarm peers connected to the node
   */
  async listPeers(targetNodeUrl?: string): Promise<Peer[]> {
    const endpoint = await this.nodeManager.resolveEndpoint(targetNodeUrl);
    const res = await this._fetch(`${endpoint}/api/peers`);
    await this.checkResponse(res, 'listPeers');
    return await res.json() as Peer[];
  }

  /**
   * Creates a new decentralized record in the cluster with full user control over placement:
   * - storagePolicy: 'local-only' keeps data strictly on the user's node.
   * - storagePolicy: 'replicated-2' replicates to user's node + 2 peer nodes.
   * - storagePolicy: 'custom' replicates strictly to user's chosen storagePeers.
   * - encrypt: true provides client-side AES-256-GCM encryption before data leaves the device.
   */
  async createRecord(options: CreateRecordOptions): Promise<CreateRecordResponse> {
    const endpoint = await this.nodeManager.resolveEndpoint(options.targetNodeUrl);
    const policy = options.storagePolicy ?? this.defaultStoragePolicy;

    let processedText = options.text || '';
    let processedFile: Uint8Array | undefined = this.extractBytes(options.file);
    let processedPic: Uint8Array | undefined = this.extractBytes(options.picture);
    let activeDek = options.dek;

    // 1. Client-Side End-to-End Encryption
    if (options.encrypt) {
      activeDek = activeDek || TestCloudCrypto.generateDek();

      if (processedText.length > 0) {
        const envelope = await TestCloudCrypto.encryptText(processedText, activeDek);
        processedText = envelope.encode();
      }
      if (processedFile && processedFile.length > 0) {
        processedFile = await TestCloudCrypto.encryptBytes(processedFile, activeDek);
      }
      if (processedPic && processedPic.length > 0) {
        processedPic = await TestCloudCrypto.encryptBytes(processedPic, activeDek);
      }
    }

    // 2. Cryptographic signature over payload if keyPair is present
    const pubAddr = this.publicAddress || '';
    let signature = '';
    if (this.keyPair) {
      const signMessage = new TextEncoder().encode(`${options.collection}:${processedText}:${pubAddr}`);
      signature = await TestCloudCrypto.sign(signMessage, this.keyPair.privateKey);
    }

    // 3. Dispatch: multipart/form-data if binary attachments present; otherwise JSON
    let response: Response;
    if (processedFile || processedPic) {
      const formData = new FormData();
      formData.append('collection', options.collection);
      formData.append('text', processedText);
      formData.append('user_pub_address', pubAddr);
      formData.append('signature', signature);

      if (options.dataId) {
        formData.append('data_id', options.dataId);
      }
      if (policy) {
        formData.append('replication_policy', policy);
      }
      if (options.storagePeers && options.storagePeers.length > 0) {
        formData.append('storage_peers', JSON.stringify(options.storagePeers));
      }
      if (options.targetReplicas) {
        formData.append('target_replicas', options.targetReplicas.toString());
      }

      if (processedFile) {
        const blob = new Blob([processedFile as unknown as BlobPart]);
        formData.append('file', blob, options.fileName || 'attachment.bin');
      }
      if (processedPic) {
        const blob = new Blob([processedPic as unknown as BlobPart]);
        formData.append('picture', blob, options.pictureName || 'picture.bin');
      }

      response = await this._fetch(`${endpoint}/api/records`, {
        method: 'POST',
        body: formData,
      });
    } else {
      const body: any = {
        collection: options.collection,
        text: processedText,
        user_pub_address: pubAddr,
        signature: signature,
      };

      if (options.dataId) body.data_id = options.dataId;
      if (policy) body.replication_policy = policy;
      if (options.storagePeers && options.storagePeers.length > 0) {
        body.storage_peers = options.storagePeers;
      }
      if (options.targetReplicas) body.target_replicas = options.targetReplicas;

      response = await this._fetch(`${endpoint}/api/records`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      });
    }

    await this.checkResponse(response, 'createRecord');
    const result = await response.json() as any;

    return {
      record: result.record || result,
      data_id: result.data_id || result.id,
      replication_policy: result.replication_policy,
      auditor_peers: result.auditor_peers,
      storage_peers: result.storage_peers,
      lightning_invoice: result.lightning_invoice,
      dek: activeDek,
    };
  }

  /**
   * Retrieves a record by its invariant persistent DataID.
   * If decrypt=true and dek provided, automatically decrypts encrypted text payloads.
   */
  async getRecord(
    dataId: string,
    options?: { decrypt?: boolean; dek?: Uint8Array; targetNodeUrl?: string }
  ): Promise<Record> {
    const endpoint = await this.nodeManager.resolveEndpoint(options?.targetNodeUrl);
    const res = await this._fetch(`${endpoint}/api/records/${encodeURIComponent(dataId)}`);

    if (res.status === 404) {
      const body = await res.text();
      throw new TestCloudNotFoundError(dataId, body);
    }
    await this.checkResponse(res, 'getRecord');
    const record = await res.json() as Record;

    if (options?.decrypt && options.dek && record.text) {
      const envelope = EncryptedEnvelope.tryParse(record.text);
      if (envelope) {
        const decryptedText = await TestCloudCrypto.decryptText(envelope, options.dek);
        return {
          ...record,
          text: decryptedText,
        };
      }
    }

    return record;
  }

  /**
   * Lists records, optionally filtered by collection name
   */
  async listRecords(options?: {
    collection?: string;
    limit?: number;
    offset?: number;
    targetNodeUrl?: string;
  }): Promise<RecordList> {
    const endpoint = await this.nodeManager.resolveEndpoint(options?.targetNodeUrl);
    const params = new URLSearchParams();
    params.set('limit', (options?.limit ?? 50).toString());
    params.set('offset', (options?.offset ?? 0).toString());
    if (options?.collection) {
      params.set('collection', options.collection);
    }

    const res = await this._fetch(`${endpoint}/api/records?${params.toString()}`);
    await this.checkResponse(res, 'listRecords');
    return await res.json() as RecordList;
  }

  /**
   * Downloads raw file attachment associated with dataId
   */
  async downloadFile(
    dataId: string,
    options?: { decrypt?: boolean; dek?: Uint8Array; targetNodeUrl?: string }
  ): Promise<Uint8Array> {
    const endpoint = await this.nodeManager.resolveEndpoint(options?.targetNodeUrl);
    const res = await this._fetch(`${endpoint}/api/records/${encodeURIComponent(dataId)}/file`);
    await this.checkResponse(res, 'downloadFile');
    const arrayBuffer = await res.arrayBuffer();
    const bytes = new Uint8Array(arrayBuffer);

    if (options?.decrypt && options.dek) {
      return await TestCloudCrypto.decryptBytes(bytes, options.dek);
    }
    return bytes;
  }

  /**
   * Downloads raw picture attachment associated with dataId
   */
  async downloadPicture(
    dataId: string,
    options?: { decrypt?: boolean; dek?: Uint8Array; targetNodeUrl?: string }
  ): Promise<Uint8Array> {
    const endpoint = await this.nodeManager.resolveEndpoint(options?.targetNodeUrl);
    const res = await this._fetch(`${endpoint}/api/records/${encodeURIComponent(dataId)}/picture`);
    await this.checkResponse(res, 'downloadPicture');
    const arrayBuffer = await res.arrayBuffer();
    const bytes = new Uint8Array(arrayBuffer);

    if (options?.decrypt && options.dek) {
      return await TestCloudCrypto.decryptBytes(bytes, options.dek);
    }
    return bytes;
  }

  /**
   * Realtime polling stream that yields new and updated records
   */
  pollRecords(options: {
    collection?: string;
    intervalMs?: number;
    callback: (rec: Record) => void;
    targetNodeUrl?: string;
  }): () => void {
    const interval = options.intervalMs ?? 3000;
    const seenDataIds = new Set<string>();
    let isCancelled = false;

    // Initial prime
    this.listRecords({ collection: options.collection, limit: 100, targetNodeUrl: options.targetNodeUrl })
      .then(initial => {
        for (const r of initial.records) {
          seenDataIds.add(r.data_id);
        }
      })
      .catch(() => {});

    const timer = setInterval(async () => {
      if (isCancelled) return;
      try {
        const current = await this.listRecords({
          collection: options.collection,
          limit: 50,
          targetNodeUrl: options.targetNodeUrl,
        });
        for (const rec of current.records) {
          if (!seenDataIds.has(rec.data_id)) {
            seenDataIds.add(rec.data_id);
            options.callback(rec);
          }
        }
      } catch {}
    }, interval);

    return () => {
      isCancelled = true;
      clearInterval(timer);
    };
  }

  /**
   * PocketBase-compatible collection accessor
   */
  collection(collectionName: string) {
    const client = this;
    return {
      async create(
        data: globalThis.Record<string, any>,
        options?: {
          storagePolicy?: StoragePolicy;
          storagePeers?: string[];
          targetReplicas?: number;
          targetNodeUrl?: string;
        }
      ) {
        const endpoint = await client.nodeManager.resolveEndpoint(options?.targetNodeUrl);
        const policy = options?.storagePolicy ?? client.defaultStoragePolicy;

        const body: globalThis.Record<string, any> = { ...data };
        if (policy) body.replication_policy = policy;
        if (options?.storagePeers) body.storage_peers = options.storagePeers;
        if (options?.targetReplicas) body.target_replicas = options.targetReplicas;

        const res = await client._fetch(`${endpoint}/api/collections/${encodeURIComponent(collectionName)}/records`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(body),
        });
        await client.checkResponse(res, 'collection.create');
        return await res.json();
      },

      async getOne(id: string, targetNodeUrl?: string) {
        const endpoint = await client.nodeManager.resolveEndpoint(targetNodeUrl);
        const res = await client._fetch(`${endpoint}/api/collections/${encodeURIComponent(collectionName)}/records/${encodeURIComponent(id)}`);
        await client.checkResponse(res, 'collection.getOne');
        return await res.json();
      },

      async getList(page: number = 1, perPage: number = 30, targetNodeUrl?: string) {
        const endpoint = await client.nodeManager.resolveEndpoint(targetNodeUrl);
        const offset = (page - 1) * perPage;
        const res = await client._fetch(
          `${endpoint}/api/collections/${encodeURIComponent(collectionName)}/records?limit=${perPage}&offset=${offset}`
        );
        await client.checkResponse(res, 'collection.getList');
        return await res.json();
      },

      async update(id: string, data: globalThis.Record<string, any>, targetNodeUrl?: string) {
        const endpoint = await client.nodeManager.resolveEndpoint(targetNodeUrl);
        const res = await client._fetch(
          `${endpoint}/api/collections/${encodeURIComponent(collectionName)}/records/${encodeURIComponent(id)}`,
          {
            method: 'PATCH',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(data),
          }
        );
        await client.checkResponse(res, 'collection.update');
        return await res.json();
      },

      async delete(id: string, targetNodeUrl?: string) {
        const endpoint = await client.nodeManager.resolveEndpoint(targetNodeUrl);
        const res = await client._fetch(
          `${endpoint}/api/collections/${encodeURIComponent(collectionName)}/records/${encodeURIComponent(id)}`,
          {
            method: 'DELETE',
          }
        );
        await client.checkResponse(res, 'collection.delete');
        return await res.json();
      },
    };
  }

  /**
   * Programmatic handshake to declare or merge an app collection and its schema fields.
   * Tells a new or existing node about the collection and fields the app requires.
   */
  async ensureCollection(options: EnsureCollectionOptions): Promise<CollectionInfo> {
    const endpoint = await this.nodeManager.resolveEndpoint(options.targetNodeUrl);
    const headers: { [key: string]: string } = {
      'Content-Type': 'application/json',
    };
    if (this.keyPair) {
      headers['X-User-PubKey'] = this.keyPair.publicKeyHex;
    }
    const res = await this._fetch(`${endpoint}/api/collections/ensure`, {
      method: 'POST',
      headers,
      body: JSON.stringify({
        name: options.name,
        fields: options.fields ?? [],
      }),
    });
    await this.checkResponse(res, 'ensureCollection');
    return (await res.json()) as CollectionInfo;
  }

  /**
   * Lists all collections and their schema fields registered on the node.
   */
  async listCollections(targetNodeUrl?: string): Promise<CollectionInfo[]> {
    const endpoint = await this.nodeManager.resolveEndpoint(targetNodeUrl);
    const res = await this._fetch(`${endpoint}/api/dashboard/collections`);
    await this.checkResponse(res, 'listCollections');
    const data = (await res.json()) as { collections: CollectionInfo[] };
    return data.collections;
  }

  private extractBytes(input: any): Uint8Array | undefined {
    if (!input) return undefined;
    if (input instanceof Uint8Array) return input;
    if (input instanceof ArrayBuffer) return new Uint8Array(input);
    if (input.data instanceof Uint8Array) return input.data;
    if (typeof input === 'object' && input.buffer instanceof ArrayBuffer) {
      return new Uint8Array(input.buffer, input.byteOffset, input.byteLength);
    }
    return undefined;
  }

  private async checkResponse(response: Response, operation: string): Promise<void> {
    if (response.ok) return;
    const body = await response.text();
    throw new TestCloudApiException(
      response.status,
      `Operation "${operation}" failed`,
      body
    );
  }
}
