import { describe, it, before, after } from 'node:test';
import assert from 'node:assert/strict';
import { spawn, ChildProcess } from 'node:child_process';
import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';
import { TestCloudClient } from '../dist/index.js';

describe('TypeScript SDK Live End-to-End Integration', () => {
  let nodeProcess: ChildProcess | null = null;
  let testDataDir: string = '';
  const port = 8399;
  const baseUrl = `http://127.0.0.1:${port}`;
  let client: TestCloudClient;

  before(async () => {
    testDataDir = fs.mkdtempSync(path.join(os.tmpdir(), 'tc_ts_e2e_'));
    const binPath = fs.existsSync(path.resolve(process.cwd(), '../../stiqrcloud'))
      ? path.resolve(process.cwd(), '../../stiqrcloud')
      : fs.existsSync(path.resolve(process.cwd(), '../../releases/v0.2.0/stiqrcloud-linux-amd64'))
      ? path.resolve(process.cwd(), '../../releases/v0.2.0/stiqrcloud-linux-amd64')
      : path.resolve(process.cwd(), '../../testcloud');

    // Spawn real TestCloud backend daemon
    nodeProcess = spawn(binPath, [
      '-host', '127.0.0.1',
      '-port', port.toString(),
      '-dashboard-port', (port + 1).toString(),
      '-data-dir', testDataDir,
      '-node-id', 'node_ts_e2e_tester',
      '-standalone',
    ], { stdio: 'ignore' });

    // Wait for server to bind port
    client = new TestCloudClient({ baseUrl });
    let ready = false;
    for (let i = 0; i < 20; i++) {
      try {
        const health = await client.getHealth();
        if (health.status === 'ok') {
          ready = true;
          break;
        }
      } catch {
        await new Promise(r => setTimeout(r, 100));
      }
    }
    assert.ok(ready, 'TestCloud backend server failed to become ready');
  });

  after(() => {
    if (nodeProcess) {
      nodeProcess.kill();
    }
    if (testDataDir && fs.existsSync(testDataDir)) {
      fs.rmSync(testDataDir, { recursive: true, force: true });
    }
  });

  it('should query live node health and node ID', async () => {
    const health = await client.getHealth();
    assert.equal(health.status, 'ok');
    assert.equal(health.node_id, 'node_ts_e2e_tester');
  });

  it('should create and retrieve a local-only record (stays exclusively on user node)', async () => {
    const res = await client.createRecord({
      collection: 'e2e_notes',
      text: 'My private data stays on my node!',
      storagePolicy: 'local-only',
    });

    assert.ok(
      /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(res.data_id) ||
      res.data_id.startsWith('rec_')
    );
    assert.equal(res.replication_policy, 'local-only');
    assert.equal(res.storage_peers?.length || 0, 0);

    // Verify record retrieval
    const record = await client.getRecord(res.data_id);
    assert.equal(record.data_id, res.data_id);
    assert.equal(record.text, 'My private data stays on my node!');
    assert.equal(record.replication_policy, 'local-only');
    assert.equal(record.storage_peers?.length || 0, 0);
  });

  it('should create and retrieve an end-to-end encrypted record', async () => {
    const secretText = 'Zero-Knowledge Encrypted Payload across decentralized mesh';
    const res = await client.createRecord({
      collection: 'e2e_secrets',
      text: secretText,
      encrypt: true,
      storagePolicy: 'local-only',
    });

    assert.ok(res.dek !== undefined);

    // Raw record on server is encrypted envelope
    const raw = await client.getRecord(res.data_id);
    assert.ok(raw.text.startsWith('tc:enc:v1:'));

    // Record retrieved with decrypt: true returns original plaintext
    const decrypted = await client.getRecord(res.data_id, {
      decrypt: true,
      dek: res.dek,
    });
    assert.equal(decrypted.text, secretText);
  });

  it('should create a custom-routed record strictly to user-chosen nodes', async () => {
    const chosenNodes = ['friend_pi_node', 'office_server_node'];
    const res = await client.createRecord({
      collection: 'e2e_vault',
      text: 'Routed strictly to my chosen storage nodes',
      storagePolicy: 'custom',
      storagePeers: chosenNodes,
    });

    assert.equal(res.replication_policy, 'custom');
    assert.deepEqual(res.storage_peers, chosenNodes);
  });

  it('should support PocketBase-compatible operations on live server', async () => {
    const col = client.collection('todo_list');
    const item = await col.create({
      title: 'Finish SDK testing',
      status: 'pending',
    }, {
      storagePolicy: 'local-only',
    });

    assert.ok(item.id !== undefined);
    assert.equal(item.title, 'Finish SDK testing');
    assert.equal(item.replication_policy, 'local-only');

    const fetched = await col.getOne(item.id);
    assert.equal(fetched.id, item.id);
  });

  it('should handshake and ensure collection schema and fields on live server', async () => {
    const info = await client.ensureCollection({
      name: 'live_app_config',
      fields: [
        { name: 'app_id', type: 'text', required: true },
        { name: 'settings_json', type: 'json', required: false },
        { name: 'is_active', type: 'bool', required: false },
      ],
    });

    assert.equal(info.name, 'live_app_config');
    const fieldNames = info.fields.map(f => f.name);
    assert.ok(fieldNames.includes('app_id'));
    assert.ok(fieldNames.includes('settings_json'));
    assert.ok(fieldNames.includes('is_active'));

    // Verify collection is returned in listCollections
    const allCollections = await client.listCollections();
    const found = allCollections.find(c => c.name === 'live_app_config');
    assert.ok(found !== undefined);
  });
});
