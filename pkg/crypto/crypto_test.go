package crypto

import (
	"testing"
)

func TestEd25519SigningAndVerification(t *testing.T) {
	kp, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair failed: %v", err)
	}

	payload := []byte("hello world test payload")
	sig, err := SignPayload(payload, kp.PrivateKey)
	if err != nil {
		t.Fatalf("SignPayload failed: %v", err)
	}

	if !VerifySignature(payload, kp.PublicKey, sig) {
		t.Fatalf("VerifySignature failed for valid signature")
	}

	// Tampered data should fail
	if VerifySignature([]byte("tampered data"), kp.PublicKey, sig) {
		t.Fatalf("VerifySignature succeeded on tampered data")
	}
}

func TestAESGCMEncryptionAndDecryption(t *testing.T) {
	key, err := GenerateRandomKey()
	if err != nil {
		t.Fatalf("GenerateRandomKey failed: %v", err)
	}

	secretMessage := []byte("super secret confidential document")
	ciphertext, err := EncryptAESGCM(secretMessage, key)
	if err != nil {
		t.Fatalf("EncryptAESGCM failed: %v", err)
	}

	plaintext, err := DecryptAESGCM(ciphertext, key)
	if err != nil {
		t.Fatalf("DecryptAESGCM failed: %v", err)
	}

	if string(plaintext) != string(secretMessage) {
		t.Fatalf("Decrypted message does not match original: got %s, want %s", plaintext, secretMessage)
	}
}

func TestMerkleTreeAndAuditVerification(t *testing.T) {
	chunks := [][]byte{
		[]byte("chunk zero data"),
		[]byte("chunk one data"),
		[]byte("chunk two data"),
	}

	var chunkHashes []string
	for _, c := range chunks {
		chunkHashes = append(chunkHashes, HashBytes(c))
	}

	tree, err := BuildMerkleTree(chunkHashes)
	if err != nil {
		t.Fatalf("BuildMerkleTree failed: %v", err)
	}

	if tree.RootHex() == "" {
		t.Fatalf("MerkleRoot should not be empty")
	}

	nonce := "random-challenge-nonce-12345"
	resp := ComputeAuditResponse(chunks[1], nonce)
	if !VerifyAuditResponse(chunks[1], nonce, resp) {
		t.Fatalf("VerifyAuditResponse failed for valid response")
	}
	if VerifyAuditResponse(chunks[0], nonce, resp) {
		t.Fatalf("VerifyAuditResponse succeeded for wrong chunk")
	}
}
