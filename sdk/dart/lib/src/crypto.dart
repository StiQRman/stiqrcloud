import 'dart:convert';
import 'dart:math';
import 'dart:typed_data';
import 'package:crypto/crypto.dart' as crypto;
import 'package:cryptography/cryptography.dart';
import 'exceptions.dart';

/// Container for AES-256-GCM encrypted data
class EncryptedEnvelope {
  final int version;
  final String nonce;      // Base64
  final String mac;        // Base64
  final String ciphertext; // Base64

  EncryptedEnvelope({
    this.version = 1,
    required this.nonce,
    required this.mac,
    required this.ciphertext,
  });

  factory EncryptedEnvelope.fromJson(Map<String, dynamic> json) {
    return EncryptedEnvelope(
      version: json['version'] as int? ?? 1,
      nonce: json['nonce'] as String? ?? '',
      mac: json['mac'] as String? ?? '',
      ciphertext: json['ciphertext'] as String? ?? '',
    );
  }

  Map<String, dynamic> toJson() => {
        'version': version,
        'nonce': nonce,
        'mac': mac,
        'ciphertext': ciphertext,
      };

  /// Encodes envelope to standard TestCloud header string: "tc:enc:v1:<base64-json>"
  String encode() {
    final jsonStr = jsonEncode(toJson());
    final b64 = base64Url.encode(utf8.encode(jsonStr));
    return 'tc:enc:v1:$b64';
  }

  /// Attempts to parse envelope from string, returns null if not an encrypted envelope
  static EncryptedEnvelope? tryParse(String raw) {
    if (!raw.startsWith('tc:enc:v1:')) return null;
    try {
      final b64 = raw.substring('tc:enc:v1:'.length);
      final jsonStr = utf8.decode(base64Url.decode(b64));
      final map = jsonDecode(jsonStr) as Map<String, dynamic>;
      return EncryptedEnvelope.fromJson(map);
    } catch (_) {
      return null;
    }
  }
}

/// Client-side cryptographic suite for TestCloud
class TestCloudCrypto {
  static final _aesGcm = AesGcm.with256bits();
  static final _ed25519 = Ed25519();
  static final _random = Random.secure();

  /// Generates a new 32-byte (256-bit) Data Encryption Key (DEK)
  static Uint8List generateDek() {
    final bytes = Uint8List(32);
    for (int i = 0; i < 32; i++) {
      bytes[i] = _random.nextInt(256);
    }
    return bytes;
  }

  /// Generates a new Ed25519 keypair for identity and record signing
  static Future<SimpleKeyPair> generateKeyPair() async {
    return await _ed25519.newKeyPair();
  }

  /// Generates a keypair from 32-byte seed
  static Future<SimpleKeyPair> keyPairFromSeed(List<int> seed) async {
    return await _ed25519.newKeyPairFromSeed(seed);
  }

  /// Extracts the public key in lowercase hexadecimal format (user_pub_address)
  static Future<String> getPublicAddress(SimpleKeyPair keyPair) async {
    final pubKey = await keyPair.extractPublicKey();
    return pubKey.bytes
        .map((b) => b.toRadixString(16).padLeft(2, '0'))
        .join('');
  }

  /// Signs raw data with Ed25519 keypair and returns signature as hex
  static Future<String> sign(List<int> message, SimpleKeyPair keyPair) async {
    final sig = await _ed25519.sign(message, keyPair: keyPair);
    return sig.bytes.map((b) => b.toRadixString(16).padLeft(2, '0')).join('');
  }

  /// Verifies an Ed25519 signature
  static Future<bool> verify(
    List<int> message,
    String signatureHex,
    String pubAddressHex,
  ) async {
    try {
      final sigBytes = _hexToBytes(signatureHex);
      final pubBytes = _hexToBytes(pubAddressHex);
      final pubKey = SimplePublicKey(pubBytes, type: KeyPairType.ed25519);
      final sig = Signature(sigBytes, publicKey: pubKey);
      return await _ed25519.verify(message, signature: sig);
    } catch (_) {
      return false;
    }
  }

  /// Encrypts text payload using AES-256-GCM and returns standard envelope
  static Future<EncryptedEnvelope> encryptText(
    String plainText,
    List<int> keyBytes,
  ) async {
    try {
      final secretKey = SecretKey(keyBytes);
      final plainBytes = utf8.encode(plainText);
      final box = await _aesGcm.encrypt(plainBytes, secretKey: secretKey);

      return EncryptedEnvelope(
        nonce: base64.encode(box.nonce),
        mac: base64.encode(box.mac.bytes),
        ciphertext: base64.encode(box.cipherText),
      );
    } catch (e, st) {
      throw TestCloudCryptoException('Failed to encrypt text payload', e);
    }
  }

  /// Decrypts envelope using AES-256-GCM and returns original plainText
  static Future<String> decryptText(
    EncryptedEnvelope envelope,
    List<int> keyBytes,
  ) async {
    try {
      final secretKey = SecretKey(keyBytes);
      final box = SecretBox(
        base64.decode(envelope.ciphertext),
        nonce: base64.decode(envelope.nonce),
        mac: Mac(base64.decode(envelope.mac)),
      );
      final decryptedBytes = await _aesGcm.decrypt(box, secretKey: secretKey);
      return utf8.decode(decryptedBytes);
    } catch (e, st) {
      throw TestCloudCryptoException('Failed to decrypt envelope (invalid key or MAC error)', e);
    }
  }

  /// Encrypts arbitrary bytes (files or pictures) with AES-256-GCM
  static Future<List<int>> encryptBytes(
    List<int> data,
    List<int> keyBytes,
  ) async {
    try {
      final secretKey = SecretKey(keyBytes);
      final box = await _aesGcm.encrypt(data, secretKey: secretKey);
      // Prepend 12-byte nonce + 16-byte mac to ciphertext
      final out = Uint8List(box.nonce.length + box.mac.bytes.length + box.cipherText.length);
      out.setAll(0, box.nonce);
      out.setAll(box.nonce.length, box.mac.bytes);
      out.setAll(box.nonce.length + box.mac.bytes.length, box.cipherText);
      return out;
    } catch (e) {
      throw TestCloudCryptoException('Failed to encrypt byte payload', e);
    }
  }

  /// Decrypts binary payload created by encryptBytes
  static Future<List<int>> decryptBytes(
    List<int> encryptedData,
    List<int> keyBytes,
  ) async {
    if (encryptedData.length < 28) {
      throw TestCloudCryptoException('Invalid encrypted payload length');
    }
    try {
      final nonce = encryptedData.sublist(0, 12);
      final mac = Mac(encryptedData.sublist(12, 28));
      final cipherText = encryptedData.sublist(28);
      final secretKey = SecretKey(keyBytes);
      final box = SecretBox(cipherText, nonce: nonce, mac: mac);
      return await _aesGcm.decrypt(box, secretKey: secretKey);
    } catch (e) {
      throw TestCloudCryptoException('Failed to decrypt byte payload', e);
    }
  }

  /// Computes SHA-256 hash of bytes as hex string
  static String sha256Hex(List<int> bytes) {
    return crypto.sha256.convert(bytes).toString();
  }

  /// Generates a standard RFC 9562 UUIDv7 (Time-Ordered Universally Unique Identifier).
  ///
  /// Structure: 48-bit millisecond timestamp + 12-bit rand_a + 62-bit rand_b.
  /// Formatted as 36-char lowercase hyphenated hex string:
  /// xxxxxxxx-xxxx-7xxx-axxx-xxxxxxxxxxxx
  static String generateUuidV7() {
    final nowMs = DateTime.now().millisecondsSinceEpoch;
    final bytes = Uint8List(16);

    // 48-bit timestamp (bytes 0..5)
    bytes[0] = (nowMs >> 40) & 0xFF;
    bytes[1] = (nowMs >> 32) & 0xFF;
    bytes[2] = (nowMs >> 24) & 0xFF;
    bytes[3] = (nowMs >> 16) & 0xFF;
    bytes[4] = (nowMs >> 8) & 0xFF;
    bytes[5] = nowMs & 0xFF;

    for (int i = 6; i < 16; i++) {
      bytes[i] = _random.nextInt(256);
    }

    // Version 7 in byte 6
    bytes[6] = (bytes[6] & 0x0F) | 0x70;
    // Variant 1 (10xx) in byte 8
    bytes[8] = (bytes[8] & 0x3F) | 0x80;

    const hexChars = '0123456789abcdef';
    final sb = StringBuffer();
    for (int i = 0; i < 16; i++) {
      if (i == 4 || i == 6 || i == 8 || i == 10) {
        sb.write('-');
      }
      final b = bytes[i];
      sb.write(hexChars[(b >> 4) & 0x0F]);
      sb.write(hexChars[b & 0x0F]);
    }
    return sb.toString();
  }

  /// Alias for generateUuidV7()
  static String generateId() => generateUuidV7();

  static Uint8List _hexToBytes(String hex) {
    final clean = hex.replaceAll(' ', '');
    final result = Uint8List(clean.length ~/ 2);
    for (int i = 0; i < clean.length; i += 2) {
      result[i ~/ 2] = int.parse(clean.substring(i, i + 2), radix: 16);
    }
    return result;
  }
}
