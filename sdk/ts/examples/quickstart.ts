/**
 * TestCloud TypeScript SDK Quickstart Example
 *
 * Demonstrates:
 * 1. Adding user's own node and managing multi-node topology
 * 2. Creating records with different storage policies (Local-Only, 2-Peer Replication, Custom Routing)
 * 3. Client-side end-to-end encryption (AES-256-GCM)
 * 4. PocketBase compatible collection usage
 */

import { TestCloudClient, StoragePolicy } from '../dist/index.js';

async function main() {
  console.log('=== TestCloud TypeScript SDK Quickstart ===\n');

  // 1. Initialize client with user's own local node
  const client = new TestCloudClient({
    baseUrl: 'http://localhost:8100', // User's own local node
    defaultStoragePolicy: 'local-only', // Default to staying strictly on their node
  });

  // 2. Add custom nodes (e.g. Home Raspberry Pi, VPS, Friend's node)
  client.addNode({
    id: 'home-pi',
    url: 'http://192.168.1.50:8100',
    name: 'Home Raspberry Pi Gateway',
  });

  client.addNode({
    id: 'cloud-gateway',
    url: 'https://stiqrcloud.stiqrnotes.com',
    name: 'Public Clearnet Gateway',
  });

  console.log('Registered nodes:');
  for (const node of client.nodeManager.listNodes()) {
    console.log(`  - [${node.isPrimary ? 'PRIMARY' : 'PEER'}] ${node.name} (${node.url})`);
  }
  console.log();

  // -------------------------------------------------------------
  // Scenario A: Data stays ONLY on user's own node (Local-Only)
  // -------------------------------------------------------------
  console.log('-> Creating local-only private record (0 external replicas)...');
  const localRecord = await client.createRecord({
    collection: 'private_notes',
    text: 'My private journal entry. Never leaves my computer.',
    storagePolicy: 'local-only',
    encrypt: true, // End-to-end encrypted client-side
  });

  console.log(`   DataID: ${localRecord.data_id}`);
  console.log(`   Replication Policy: ${localRecord.replication_policy}`);
  console.log(`   Assigned Storage Peers: ${localRecord.storage_peers?.length || 0}`);
  console.log();

  // -------------------------------------------------------------
  // Scenario B: Replicate to 2 other storage nodes
  // -------------------------------------------------------------
  console.log('-> Creating record replicated to 2 peer nodes in the mesh...');
  const rep2Record = await client.createRecord({
    collection: 'documents',
    text: 'Important document backed up across 2 network storage peers.',
    storagePolicy: 'replicated-2',
  });

  console.log(`   DataID: ${rep2Record.data_id}`);
  console.log(`   Replication Policy: ${rep2Record.replication_policy}`);
  console.log(`   Assigned Storage Peers:`, rep2Record.storage_peers);
  console.log();

  // -------------------------------------------------------------
  // Scenario C: Custom routing - user explicitly chooses target nodes
  // -------------------------------------------------------------
  console.log('-> Creating record with custom routing to user-chosen nodes...');
  const customRecord = await client.createRecord({
    collection: 'family_vault',
    text: 'Shared strictly between specific trusted peers.',
    storagePolicy: 'custom',
    storagePeers: ['node_storage_delta', 'node_storage_epsilon'],
  });

  console.log(`   DataID: ${customRecord.data_id}`);
  console.log(`   Replication Policy: ${customRecord.replication_policy}`);
  console.log(`   Target Peers:`, customRecord.storage_peers);
  console.log();

  // -------------------------------------------------------------
  // Scenario D: PocketBase-compatible Collection API
  // -------------------------------------------------------------
  console.log('-> PocketBase-compatible collection API...');
  const pb = client.collection('tasks');
  const task = await pb.create({
    title: 'Deploy TestCloud node on home server',
    completed: true,
  }, {
    storagePolicy: 'local-only', // Full control in PB mode too!
  });

  console.log(`   PB Record ID: ${task.id}`);
  console.log(`   PB Replication Policy: ${task.replication_policy}`);
  console.log('\n=== Quickstart Completed Successfully ===');
}

// Run if executed directly
if (import.meta.url === `file://${process.argv[1]}`) {
  main().catch(console.error);
}
