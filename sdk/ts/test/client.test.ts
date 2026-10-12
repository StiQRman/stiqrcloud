import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { TestCloudClient } from '../dist/client.js';
import { TestCloudCrypto, EncryptedEnvelope } from '../dist/crypto.js';

describe('TestCloudClient', () => {
  it('should dispatch local-only storage policy correctly', async () => {
    let capturedUrl = '';
    let capturedBody: any = null;

    const mockFetch: typeof fetch = async (input, init) => {
      capturedUrl = input.toString();
      if (init?.body) {
        capturedBody = JSON.parse(init.body.toString());
      }
      return new Response(JSON.stringify({
        data_id: 'rec_local_test_123',
        replication_policy: 'local-only',
        storage_peers: [],
        record: {
          data_id: 'rec_local_test_123',
          collection: 'notes',
          text: capturedBody?.text,
          replication_policy: 'local-only',
        },
      }), { status: 201 });
    };

    const client = new TestCloudClient({
      baseUrl: 'http://localhost:8100',
      fetch: mockFetch,
    });

    const res = await client.createRecord({
      collection: 'notes',
      text: 'Stays on my node only',
      storagePolicy: 'local-only',
    });

    assert.equal(capturedUrl, 'http://localhost:8100/api/records');
    assert.equal(capturedBody.collection, 'notes');
    assert.equal(capturedBody.replication_policy, 'local-only');
    assert.equal(res.data_id, 'rec_local_test_123');
    assert.equal(res.replication_policy, 'local-only');
  });

  it('should dispatch replicated-2 storage policy correctly', async () => {
    let capturedBody: any = null;

    const mockFetch: typeof fetch = async (input, init) => {
      capturedBody = JSON.parse(init?.body?.toString() || '{}');
      return new Response(JSON.stringify({
        data_id: 'rec_rep2_123',
        replication_policy: 'replicated-2',
        storage_peers: ['node_storage_alpha', 'node_storage_beta'],
      }), { status: 201 });
    };

    const client = new TestCloudClient({
      baseUrl: 'http://localhost:8100',
      fetch: mockFetch,
    });

    const res = await client.createRecord({
      collection: 'docs',
      text: 'Replicated to 2 peer nodes',
      storagePolicy: 'replicated-2',
    });

    assert.equal(capturedBody.replication_policy, 'replicated-2');
    assert.equal(res.storage_peers?.length, 2);
  });

  it('should dispatch custom storage peers allowing user full routing control', async () => {
    let capturedBody: any = null;

    const mockFetch: typeof fetch = async (input, init) => {
      capturedBody = JSON.parse(init?.body?.toString() || '{}');
      return new Response(JSON.stringify({
        data_id: 'rec_custom_456',
        replication_policy: 'custom',
        storage_peers: capturedBody.storage_peers,
      }), { status: 201 });
    };

    const client = new TestCloudClient({
      baseUrl: 'http://localhost:8100',
      fetch: mockFetch,
    });

    const targetPeers = ['home_nas_tokyo', 'friend_vps_london'];
    const res = await client.createRecord({
      collection: 'vault',
      text: 'Custom routing strictly to trusted nodes',
      storagePolicy: 'custom',
      storagePeers: targetPeers,
    });

    assert.equal(capturedBody.replication_policy, 'custom');
    assert.deepEqual(capturedBody.storage_peers, targetPeers);
    assert.deepEqual(res.storage_peers, targetPeers);
  });

  it('should allow user to route request to their own specific node', async () => {
    let capturedUrl = '';

    const mockFetch: typeof fetch = async (input) => {
      capturedUrl = input.toString();
      return new Response(JSON.stringify({
        data_id: 'rec_own_node',
        record: { data_id: 'rec_own_node' },
      }), { status: 201 });
    };

    const client = new TestCloudClient({
      baseUrl: 'http://default-gateway:8100',
      fetch: mockFetch,
    });

    // Add user's own home server node
    client.addNode({
      id: 'my-home-server',
      url: 'http://192.168.1.100:8100',
    });

    // Explicitly target user's node
    await client.createRecord({
      collection: 'private',
      text: 'Direct to home server',
      targetNodeUrl: 'http://192.168.1.100:8100',
      storagePolicy: 'local-only',
    });

    assert.equal(capturedUrl, 'http://192.168.1.100:8100/api/records');
  });

  it('should encrypt text on client-side before dispatching', async () => {
    let capturedBody: any = null;

    const mockFetch: typeof fetch = async (input, init) => {
      capturedBody = JSON.parse(init?.body?.toString() || '{}');
      return new Response(JSON.stringify({
        data_id: 'rec_enc_789',
        record: { data_id: 'rec_enc_789', text: capturedBody.text },
      }), { status: 201 });
    };

    const client = new TestCloudClient({
      baseUrl: 'http://localhost:8100',
      fetch: mockFetch,
    });

    const secretText = 'Extremely sensitive confidential note';
    const res = await client.createRecord({
      collection: 'secret',
      text: secretText,
      encrypt: true,
      storagePolicy: 'local-only',
    });

    // Plaintext should NOT have been sent in cleartext
    assert.notEqual(capturedBody.text, secretText);
    assert.ok(capturedBody.text.startsWith('tc:enc:v1:'));
    assert.ok(res.dek !== undefined);

    // Verify it decrypts back with the returned DEK
    const envelope = EncryptedEnvelope.tryParse(capturedBody.text);
    assert.ok(envelope !== null);
    const decrypted = await TestCloudCrypto.decryptText(envelope!, res.dek!);
    assert.equal(decrypted, secretText);
  });

  it('should support PocketBase-compatible collection API with storage policies', async () => {
    let capturedUrl = '';
    let capturedBody: any = null;

    const mockFetch: typeof fetch = async (input, init) => {
      capturedUrl = input.toString();
      capturedBody = JSON.parse(init?.body?.toString() || '{}');
      return new Response(JSON.stringify({
        id: 'pb_rec_999',
        collectionName: 'tasks',
        title: capturedBody.title,
        replication_policy: capturedBody.replication_policy,
      }), { status: 200 });
    };

    const client = new TestCloudClient({
      baseUrl: 'http://localhost:8100',
      fetch: mockFetch,
    });

    const pbRecord = await client.collection('tasks').create({
      title: 'Buy groceries',
      done: false,
    }, {
      storagePolicy: 'local-only',
    });

    assert.equal(capturedUrl, 'http://localhost:8100/api/collections/tasks/records');
    assert.equal(capturedBody.title, 'Buy groceries');
    assert.equal(capturedBody.replication_policy, 'local-only');
    assert.equal(pbRecord.id, 'pb_rec_999');
  });
});
