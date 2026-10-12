import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { TestCloudCrypto, EncryptedEnvelope } from '../dist/crypto.js';

describe('TestCloudCrypto', () => {
  it('should generate a 32-byte Data Encryption Key (DEK)', () => {
    const dek1 = TestCloudCrypto.generateDek();
    const dek2 = TestCloudCrypto.generateDek();
    assert.equal(dek1.length, 32);
    assert.equal(dek2.length, 32);
    assert.notDeepEqual(dek1, dek2);
  });

  it('should encrypt and decrypt text payloads via EncryptedEnvelope', async () => {
    const dek = TestCloudCrypto.generateDek();
    const plainText = 'Decentralized mesh storage secret document 12345! 🚀';

    const envelope = await TestCloudCrypto.encryptText(plainText, dek);
    assert.ok(envelope.nonce.length > 0);
    assert.ok(envelope.mac.length > 0);
    assert.ok(envelope.ciphertext.length > 0);

    const encodedStr = envelope.encode();
    assert.ok(encodedStr.startsWith('tc:enc:v1:'));

    const parsedEnvelope = EncryptedEnvelope.tryParse(encodedStr);
    assert.ok(parsedEnvelope !== null);

    const decrypted = await TestCloudCrypto.decryptText(parsedEnvelope!, dek);
    assert.equal(decrypted, plainText);
  });

  it('should encrypt and decrypt arbitrary binary byte payloads', async () => {
    const dek = TestCloudCrypto.generateDek();
    const originalBytes = new Uint8Array([10, 20, 30, 40, 50, 60, 70, 80, 90, 100, 110, 120]);

    const encrypted = await TestCloudCrypto.encryptBytes(originalBytes, dek);
    assert.ok(encrypted.length > originalBytes.length); // 12-byte IV + 16-byte MAC

    const decrypted = await TestCloudCrypto.decryptBytes(encrypted, dek);
    assert.deepEqual(decrypted, originalBytes);
  });

  it('should fail decryption when given wrong DEK', async () => {
    const dekCorrect = TestCloudCrypto.generateDek();
    const dekWrong = TestCloudCrypto.generateDek();
    const plainText = 'Top secret data';

    const envelope = await TestCloudCrypto.encryptText(plainText, dekCorrect);
    await assert.rejects(async () => {
      await TestCloudCrypto.decryptText(envelope, dekWrong);
    }, /Failed to decrypt envelope/);
  });

  it('should compute SHA-256 hashes correctly', async () => {
    const data = new TextEncoder().encode('TestCloud');
    const hash = await TestCloudCrypto.sha256Hex(data);
    assert.equal(hash.length, 64);
  });

  it('should generate Ed25519 keypair and sign/verify messages', async () => {
    const keyPair = await TestCloudCrypto.generateKeyPair();
    assert.equal(keyPair.publicKeyHex.length, 64);

    const message = new TextEncoder().encode('collections:notes:owner_test_123');
    const signature = await TestCloudCrypto.sign(message, keyPair.privateKey);
    assert.equal(signature.length, 128);

    const isValid = await TestCloudCrypto.verify(message, signature, keyPair.publicKeyHex);
    assert.equal(isValid, true);

    const tampered = new TextEncoder().encode('tampered message');
    const isTamperedValid = await TestCloudCrypto.verify(tampered, signature, keyPair.publicKeyHex);
    assert.equal(isTamperedValid, false);
  });
});
