import { TestCloudCryptoError } from './errors.js';

export interface EnvelopeJson {
  version: number;
  nonce: string; // Base64
  mac: string; // Base64
  ciphertext: string; // Base64
}

export class EncryptedEnvelope {
  public readonly version: number;
  public readonly nonce: string;
  public readonly mac: string;
  public readonly ciphertext: string;

  constructor(nonce: string, mac: string, ciphertext: string, version: number = 1) {
    this.version = version;
    this.nonce = nonce;
    this.mac = mac;
    this.ciphertext = ciphertext;
  }

  static fromJson(json: EnvelopeJson): EncryptedEnvelope {
    return new EncryptedEnvelope(json.nonce, json.mac, json.ciphertext, json.version ?? 1);
  }

  toJson(): EnvelopeJson {
    return {
      version: this.version,
      nonce: this.nonce,
      mac: this.mac,
      ciphertext: this.ciphertext,
    };
  }

  encode(): string {
    const jsonStr = JSON.stringify(this.toJson());
    const b64 = toBase64Url(new TextEncoder().encode(jsonStr));
    return `tc:enc:v1:${b64}`;
  }

  static tryParse(raw: string): EncryptedEnvelope | null {
    if (!raw.startsWith('tc:enc:v1:')) return null;
    try {
      const b64 = raw.substring('tc:enc:v1:'.length);
      const jsonStr = new TextDecoder().decode(fromBase64Url(b64));
      const obj = JSON.parse(jsonStr) as EnvelopeJson;
      return EncryptedEnvelope.fromJson(obj);
    } catch {
      return null;
    }
  }
}

/**
 * Client-side cryptographic suite for TestCloud
 * Uses native Web Crypto API (SubtleCrypto) compatible with Node.js 18+ and modern browsers.
 */
export class TestCloudCrypto {
  private static get subtle(): SubtleCrypto {
    if (typeof globalThis.crypto?.subtle !== 'undefined') {
      return globalThis.crypto.subtle;
    }
    throw new TestCloudCryptoError('SubtleCrypto is not available in this runtime environment.');
  }

  /**
   * Generates a new cryptographically secure 32-byte (256-bit) Data Encryption Key (DEK)
   */
  static generateDek(): Uint8Array {
    const key = new Uint8Array(32);
    globalThis.crypto.getRandomValues(key);
    return key;
  }

  /**
   * Computes SHA-256 hash of bytes as hex string
   */
  static async sha256Hex(data: Uint8Array): Promise<string> {
    const digest = await TestCloudCrypto.subtle.digest('SHA-256', data as unknown as BufferSource);
    return bytesToHex(new Uint8Array(digest));
  }

  /**
   * Imports raw 32-byte key into SubtleCrypto CryptoKey
   */
  private static async importAesKey(keyBytes: Uint8Array): Promise<CryptoKey> {
    if (keyBytes.length !== 32) {
      throw new TestCloudCryptoError(`Invalid AES key length: expected 32 bytes, got ${keyBytes.length}`);
    }
    return await TestCloudCrypto.subtle.importKey(
      'raw',
      keyBytes as unknown as BufferSource,
      { name: 'AES-GCM', length: 256 },
      false,
      ['encrypt', 'decrypt']
    );
  }

  /**
   * Encrypts plaintext string using AES-256-GCM and returns standard TestCloud EncryptedEnvelope
   */
  static async encryptText(plainText: string, keyBytes: Uint8Array): Promise<EncryptedEnvelope> {
    try {
      const key = await this.importAesKey(keyBytes);
      const iv = new Uint8Array(12);
      globalThis.crypto.getRandomValues(iv);

      const encoded = new TextEncoder().encode(plainText);
      const encryptedBuffer = await this.subtle.encrypt(
        { name: 'AES-GCM', iv: iv as unknown as BufferSource, tagLength: 128 },
        key,
        encoded as unknown as BufferSource
      );

      const encryptedBytes = new Uint8Array(encryptedBuffer);
      // In Web Crypto, AES-GCM appends the 16-byte authentication tag to the ciphertext
      const ciphertext = encryptedBytes.slice(0, encryptedBytes.length - 16);
      const mac = encryptedBytes.slice(encryptedBytes.length - 16);

      return new EncryptedEnvelope(
        toBase64(iv),
        toBase64(mac),
        toBase64(ciphertext)
      );
    } catch (e: any) {
      throw new TestCloudCryptoError(`Failed to encrypt text payload: ${e.message}`);
    }
  }

  /**
   * Decrypts TestCloud EncryptedEnvelope using AES-256-GCM and returns original plaintext string
   */
  static async decryptText(envelope: EncryptedEnvelope, keyBytes: Uint8Array): Promise<string> {
    try {
      const key = await this.importAesKey(keyBytes);
      const iv = fromBase64(envelope.nonce);
      const mac = fromBase64(envelope.mac);
      const ciphertext = fromBase64(envelope.ciphertext);

      // Reconstruct Web Crypto format: ciphertext + tag
      const combined = new Uint8Array(ciphertext.length + mac.length);
      combined.set(ciphertext, 0);
      combined.set(mac, ciphertext.length);

      const decryptedBuffer = await this.subtle.decrypt(
        { name: 'AES-GCM', iv: iv as unknown as BufferSource, tagLength: 128 },
        key,
        combined as unknown as BufferSource
      );

      return new TextDecoder().decode(decryptedBuffer);
    } catch (e: any) {
      throw new TestCloudCryptoError(`Failed to decrypt envelope (invalid key or MAC error): ${e.message}`);
    }
  }

  /**
   * Encrypts arbitrary bytes (files or pictures) with AES-256-GCM
   * Wire format: 12-byte IV + 16-byte MAC tag + ciphertext
   */
  static async encryptBytes(data: Uint8Array, keyBytes: Uint8Array): Promise<Uint8Array> {
    try {
      const key = await this.importAesKey(keyBytes);
      const iv = new Uint8Array(12);
      globalThis.crypto.getRandomValues(iv);

      const encryptedBuffer = await this.subtle.encrypt(
        { name: 'AES-GCM', iv: iv as unknown as BufferSource, tagLength: 128 },
        key,
        data as unknown as BufferSource
      );

      const encryptedBytes = new Uint8Array(encryptedBuffer);
      const ciphertext = encryptedBytes.slice(0, encryptedBytes.length - 16);
      const mac = encryptedBytes.slice(encryptedBytes.length - 16);

      // Output wire format: nonce (12) + mac (16) + ciphertext
      const out = new Uint8Array(12 + 16 + ciphertext.length);
      out.set(iv, 0);
      out.set(mac, 12);
      out.set(ciphertext, 28);
      return out;
    } catch (e: any) {
      throw new TestCloudCryptoError(`Failed to encrypt bytes: ${e.message}`);
    }
  }

  /**
   * Decrypts binary payload created by encryptBytes
   */
  static async decryptBytes(encryptedData: Uint8Array, keyBytes: Uint8Array): Promise<Uint8Array> {
    if (encryptedData.length < 28) {
      throw new TestCloudCryptoError('Encrypted payload too short (must be >= 28 bytes)');
    }
    try {
      const key = await this.importAesKey(keyBytes);
      const iv = encryptedData.slice(0, 12);
      const mac = encryptedData.slice(12, 28);
      const ciphertext = encryptedData.slice(28);

      const combined = new Uint8Array(ciphertext.length + mac.length);
      combined.set(ciphertext, 0);
      combined.set(mac, ciphertext.length);

      const decrypted = await this.subtle.decrypt(
        { name: 'AES-GCM', iv: iv as unknown as BufferSource, tagLength: 128 },
        key,
        combined as unknown as BufferSource
      );

      return new Uint8Array(decrypted);
    } catch (e: any) {
      throw new TestCloudCryptoError(`Failed to decrypt bytes: ${e.message}`);
    }
  }

  /**
   * Generates an Ed25519 signing keypair using Web Crypto (supported in Node.js and modern browsers)
   */
  static async generateKeyPair(): Promise<{ publicKeyHex: string; privateKey: CryptoKey }> {
    try {
      const keyPair = await this.subtle.generateKey(
        { name: 'Ed25519' },
        true,
        ['sign', 'verify']
      );

      const rawPub = await this.subtle.exportKey('raw', keyPair.publicKey);
      const pubHex = bytesToHex(new Uint8Array(rawPub));
      return {
        publicKeyHex: pubHex,
        privateKey: keyPair.privateKey,
      };
    } catch (e: any) {
      throw new TestCloudCryptoError(`Ed25519 key generation not supported in this environment: ${e.message}`);
    }
  }

  /**
   * Signs a message payload with Ed25519 private key
   */
  static async sign(message: Uint8Array, privateKey: CryptoKey): Promise<string> {
    try {
      const sigBuffer = await this.subtle.sign(
        { name: 'Ed25519' },
        privateKey,
        message as unknown as BufferSource
      );
      return bytesToHex(new Uint8Array(sigBuffer));
    } catch (e: any) {
      throw new TestCloudCryptoError(`Failed to sign message: ${e.message}`);
    }
  }

  /**
   * Verifies an Ed25519 signature
   */
  static async verify(message: Uint8Array, signatureHex: string, publicKeyHex: string): Promise<boolean> {
    try {
      const pubBytes = hexToBytes(publicKeyHex);
      const sigBytes = hexToBytes(signatureHex);
      const pubKey = await this.subtle.importKey(
        'raw',
        pubBytes as unknown as BufferSource,
        { name: 'Ed25519' },
        false,
        ['verify']
      );
      return await this.subtle.verify(
        { name: 'Ed25519' },
        pubKey,
        sigBytes as unknown as BufferSource,
        message as unknown as BufferSource
      );
    } catch {
      return false;
    }
  }
}

// Byte conversion helpers
export function bytesToHex(bytes: Uint8Array): string {
  let hex = '';
  for (let i = 0; i < bytes.length; i++) {
    hex += bytes[i].toString(16).padStart(2, '0');
  }
  return hex;
}

export function hexToBytes(hex: string): Uint8Array {
  const clean = hex.trim().replace(/^0x/, '');
  const bytes = new Uint8Array(clean.length / 2);
  for (let i = 0; i < clean.length; i += 2) {
    bytes[i / 2] = parseInt(clean.substring(i, i + 2), 16);
  }
  return bytes;
}

export function toBase64(bytes: Uint8Array): string {
  if (typeof Buffer !== 'undefined') {
    return Buffer.from(bytes).toString('base64');
  }
  let binary = '';
  for (let i = 0; i < bytes.byteLength; i++) {
    binary += String.fromCharCode(bytes[i]);
  }
  return btoa(binary);
}

export function fromBase64(str: string): Uint8Array {
  if (typeof Buffer !== 'undefined') {
    return new Uint8Array(Buffer.from(str, 'base64'));
  }
  const binary = atob(str);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) {
    bytes[i] = binary.charCodeAt(i);
  }
  return bytes;
}

export function toBase64Url(bytes: Uint8Array): string {
  return toBase64(bytes).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

export function fromBase64Url(str: string): Uint8Array {
  let b64 = str.replace(/-/g, '+').replace(/_/g, '/');
  while (b64.length % 4 !== 0) {
    b64 += '=';
  }
  return fromBase64(b64);
}
