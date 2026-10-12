import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { NodeManager } from '../dist/node-manager.js';

describe('NodeManager', () => {
  it('should initialize with default primary node', () => {
    const manager = new NodeManager({ primaryUrl: 'http://localhost:8100' });
    const primary = manager.getPrimaryNode();
    assert.equal(primary.url, 'http://localhost:8100');
    assert.equal(primary.isPrimary, true);
  });

  it('should allow user to add custom self-hosted nodes', () => {
    const manager = new NodeManager();

    manager.addNode({
      id: 'my-pi-gateway',
      url: 'http://192.168.1.50:8100',
      name: 'Raspberry Pi Node',
    });

    manager.addNode('http://192.168.1.50:8100');

    const nodes = manager.listNodes();
    assert.equal(nodes.length, 3);

    const piNode = manager.getNode('my-pi-gateway');
    assert.ok(piNode !== undefined);
    assert.equal(piNode?.url, 'http://192.168.1.50:8100');
  });

  it('should allow switching primary node dynamically', () => {
    const manager = new NodeManager({ primaryUrl: 'http://localhost:8100' });

    manager.addNode({
      id: 'home-server',
      url: 'http://192.168.1.200:8100',
      name: 'Home Server',
    });

    manager.setPrimaryNode('home-server');

    const currentPrimary = manager.getPrimaryNode();
    assert.equal(currentPrimary.url, 'http://192.168.1.200:8100');
    assert.equal(currentPrimary.isPrimary, true);
  });

  it('should normalize URLs correctly', () => {
    const manager = new NodeManager();
    const node = manager.addNode('10.0.0.5:8100/');
    assert.equal(node.url, 'http://10.0.0.5:8100');
  });

  it('should remove nodes correctly', () => {
    const manager = new NodeManager();
    manager.addNode({ id: 'temp-node', url: 'http://temp.local:8100' });
    assert.ok(manager.getNode('temp-node') !== undefined);

    const removed = manager.removeNode('temp-node');
    assert.equal(removed, true);
    assert.equal(manager.getNode('temp-node'), undefined);
  });
});
