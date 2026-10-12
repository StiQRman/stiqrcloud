import { NodeConfig, NodeHealth } from './models.js';
import { TestCloudNodeOfflineError } from './errors.js';

export interface NodeManagerOptions {
  /** Primary node URL (e.g. user's own local node 'http://localhost:8100') */
  primaryUrl?: string;
  /** Initial list of custom nodes */
  nodes?: (string | NodeConfig)[];
  /** Whether to automatically fallback to secondary nodes if primary fails */
  autoFallback?: boolean;
}

/**
 * NodeManager allows app users to add, prioritize, and manage their own nodes.
 *
 * An app user can:
 * 1. Register their own local node ('http://localhost:8100').
 * 2. Register external self-hosted nodes (e.g. home Raspberry Pi, VPS, friend's node).
 * 3. Switch their active primary node at runtime.
 * 4. Measure node latency and probe health.
 */
export class NodeManager {
  private nodes: Map<string, NodeConfig> = new Map();
  private primaryUrl: string;
  public autoFallback: boolean;

  constructor(options?: NodeManagerOptions) {
    this.autoFallback = options?.autoFallback ?? true;
    const defaultUrl = options?.primaryUrl || 'http://localhost:8100';
    this.primaryUrl = this.normalizeUrl(defaultUrl);

    // Register primary node
    this.addNode({
      url: this.primaryUrl,
      name: 'Primary Node',
      isPrimary: true,
    });

    // Register any additional nodes
    if (options?.nodes) {
      for (const n of options.nodes) {
        this.addNode(n);
      }
    }
  }

  /**
   * Adds the user's own node or another community gateway to the manager.
   * If isPrimary is true, this node becomes the active target for future requests.
   */
  addNode(node: string | NodeConfig): NodeConfig {
    const config: NodeConfig = typeof node === 'string'
      ? { url: this.normalizeUrl(node) }
      : { ...node, url: this.normalizeUrl(node.url) };

    if (!config.id) {
      config.id = this.generateNodeId(config.url);
    }
    if (!config.name) {
      config.name = config.url;
    }

    if (config.isPrimary) {
      // Unset previous primary
      for (const n of this.nodes.values()) {
        n.isPrimary = false;
      }
      this.primaryUrl = config.url;
    }

    this.nodes.set(config.url, config);
    return config;
  }

  /**
   * Removes a node by its URL or ID
   */
  removeNode(urlOrId: string): boolean {
    const norm = this.normalizeUrl(urlOrId);
    if (this.nodes.has(norm)) {
      if (this.primaryUrl === norm) {
        // Fallback primary to next available
        const remaining = Array.from(this.nodes.values()).filter(n => n.url !== norm);
        if (remaining.length > 0) {
          this.setPrimaryNode(remaining[0].url);
        }
      }
      return this.nodes.delete(norm);
    }

    // Check by ID
    for (const [url, config] of this.nodes.entries()) {
      if (config.id === urlOrId) {
        if (this.primaryUrl === url) {
          const remaining = Array.from(this.nodes.values()).filter(n => n.url !== url);
          if (remaining.length > 0) {
            this.setPrimaryNode(remaining[0].url);
          }
        }
        return this.nodes.delete(url);
      }
    }
    return false;
  }

  /**
   * Sets the specified node as the primary node
   */
  setPrimaryNode(urlOrId: string): NodeConfig {
    let target = this.getNode(urlOrId);
    if (!target) {
      // If node doesn't exist yet, add it as primary
      target = this.addNode({ url: urlOrId, isPrimary: true });
    }

    for (const n of this.nodes.values()) {
      n.isPrimary = false;
    }
    target.isPrimary = true;
    this.primaryUrl = target.url;
    return target;
  }

  /**
   * Returns the current primary node configuration
   */
  getPrimaryNode(): NodeConfig {
    return this.nodes.get(this.primaryUrl) || {
      url: this.primaryUrl,
      isPrimary: true,
    };
  }

  /**
   * Returns a node by URL or ID
   */
  getNode(urlOrId: string): NodeConfig | undefined {
    const norm = this.normalizeUrl(urlOrId);
    if (this.nodes.has(norm)) {
      return this.nodes.get(norm);
    }
    for (const config of this.nodes.values()) {
      if (config.id === urlOrId) {
        return config;
      }
    }
    return undefined;
  }

  /**
   * Lists all configured nodes
   */
  listNodes(): NodeConfig[] {
    return Array.from(this.nodes.values());
  }

  /**
   * Checks the health and measures latency of a specific node
   */
  async checkHealth(nodeUrl?: string): Promise<NodeHealth> {
    const target = nodeUrl ? this.normalizeUrl(nodeUrl) : this.primaryUrl;
    const start = Date.now();

    try {
      const res = await fetch(`${target}/api/health`, {
        headers: { 'Accept': 'application/json' },
      });
      const latency = Date.now() - start;

      if (!res.ok) {
        throw new Error(`HTTP ${res.status}`);
      }

      const health = await res.json() as NodeHealth;
      const config = this.nodes.get(target);
      if (config) {
        config.isOnline = true;
        config.latencyMs = latency;
      }
      return health;
    } catch (err: any) {
      const config = this.nodes.get(target);
      if (config) {
        config.isOnline = false;
      }
      throw new TestCloudNodeOfflineError(target, err);
    }
  }

  /**
   * Resolves the active base URL to use for an API request,
   * supporting per-request override or automatic failover.
   */
  async resolveEndpoint(targetUrl?: string): Promise<string> {
    if (targetUrl) {
      return this.normalizeUrl(targetUrl);
    }

    if (!this.autoFallback) {
      return this.primaryUrl;
    }

    // Try primary first
    const primary = this.getPrimaryNode();
    if (primary.isOnline !== false) {
      return primary.url;
    }

    // If primary was marked offline, find first healthy node
    for (const node of this.nodes.values()) {
      if (node.isOnline) {
        return node.url;
      }
    }

    return this.primaryUrl;
  }

  private normalizeUrl(url: string): string {
    let clean = url.trim();
    if (!clean.startsWith('http://') && !clean.startsWith('https://')) {
      clean = `http://${clean}`;
    }
    return clean.replace(/\/+$/, '');
  }

  private generateNodeId(url: string): string {
    return url.replace(/https?:\/\//, '').replace(/[^a-zA-Z0-9_-]/g, '_');
  }
}
