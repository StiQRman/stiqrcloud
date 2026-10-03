import 'dart:convert';
import 'package:test/test.dart';
import 'package:testcloud/testcloud.dart';

void main() {
  group('TestCloudCrypto', () {
    test('generateDek generates 32 secure bytes', () {
      final dek1 = TestCloudCrypto.generateDek();
      final dek2 = TestCloudCrypto.generateDek();
      expect(dek1.length, equals(32));
      expect(dek2.length, equals(32));
      expect(dek1, isNot(equals(dek2)));
    });

    test('Ed25519 Keypair generation, signing, and verification', () async {
      final keyPair = await TestCloudCrypto.generateKeyPair();
      final pubAddress = await TestCloudCrypto.getPublicAddress(keyPair);

      expect(pubAddress.length, equals(64)); // 32 bytes hex = 64 characters

      final message = utf8.encode('TestCloud decentralized signature test');
      final signature = await TestCloudCrypto.sign(message, keyPair);

      expect(signature.length, equals(128)); // 64 bytes hex = 128 characters

      final isValid = await TestCloudCrypto.verify(message, signature, pubAddress);
      expect(isValid, isTrue);

      final isTamperedValid = await TestCloudCrypto.verify(
        utf8.encode('Tampered message'),
        signature,
        pubAddress,
      );
      expect(isTamperedValid, isFalse);
    });

    test('AES-256-GCM text encryption and decryption', () async {
      final dek = TestCloudCrypto.generateDek();
      const plainText = 'Confidential client payload stored in TestCloud mesh.';

      final envelope = await TestCloudCrypto.encryptText(plainText, dek);
      expect(envelope.ciphertext, isNotEmpty);
      expect(envelope.nonce, isNotEmpty);
      expect(envelope.mac, isNotEmpty);

      // Encode and parse envelope string
      final encodedString = envelope.encode();
      expect(encodedString.startsWith('tc:enc:v1:'), isTrue);

      final parsed = EncryptedEnvelope.tryParse(encodedString);
      expect(parsed, isNotNull);
      expect(parsed!.ciphertext, equals(envelope.ciphertext));

      // Decrypt
      final decrypted = await TestCloudCrypto.decryptText(parsed, dek);
      expect(decrypted, equals(plainText));
    });

    test('AES-256-GCM throws on invalid key or corrupted MAC', () async {
      final dek1 = TestCloudCrypto.generateDek();
      final dek2 = TestCloudCrypto.generateDek();
      const plainText = 'Top secret data';

      final envelope = await TestCloudCrypto.encryptText(plainText, dek1);

      // Decrypt with wrong key
      expect(
        () async => await TestCloudCrypto.decryptText(envelope, dek2),
        throwsA(isA<TestCloudCryptoException>()),
      );
    });

    test('AES-256-GCM binary blob encryption and decryption', () async {
      final dek = TestCloudCrypto.generateDek();
      final binaryData = List<int>.generate(1024, (i) => i % 256);

      final encrypted = await TestCloudCrypto.encryptBytes(binaryData, dek);
      expect(encrypted.length, greaterThan(binaryData.length));

      final decrypted = await TestCloudCrypto.decryptBytes(encrypted, dek);
      expect(decrypted, equals(binaryData));
    });
  });
}
