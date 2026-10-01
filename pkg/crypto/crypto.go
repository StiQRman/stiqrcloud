package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

// Keypair contains hex-encoded Ed25519 public and private keys.
type Keypair struct {
	PublicKey  string `json:"public_key"`
	PrivateKey string `json:"private_key"`
}

// GenerateKeypair creates a new Ed25519 keypair.
func GenerateKeypair() (*Keypair, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate ed25519 key: %w", err)
	}
	return &Keypair{
		PublicKey:  hex.EncodeToString(pub),
		PrivateKey: hex.EncodeToString(priv),
	}, nil
}

// SignPayload signs data using a hex-encoded Ed25519 private key.
func SignPayload(data []byte, privHex string) (string, error) {
	privBytes, err := hex.DecodeString(privHex)
	if err != nil {
		return "", fmt.Errorf("invalid private key hex: %w", err)
	}
	if len(privBytes) != ed25519.PrivateKeySize {
		return "", fmt.Errorf("invalid private key length: got %d, expected %d", len(privBytes), ed25519.PrivateKeySize)
	}
	sig := ed25519.Sign(privBytes, data)
	return hex.EncodeToString(sig), nil
}

// VerifySignature verifies an Ed25519 signature against a public key.
func VerifySignature(data []byte, pubHex, sigHex string) bool {
	pubBytes, err := hex.DecodeString(pubHex)
	if err != nil || len(pubBytes) != ed25519.PublicKeySize {
		return false
	}
	sigBytes, err := hex.DecodeString(sigHex)
	if err != nil || len(sigBytes) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(pubBytes, data, sigBytes)
}

// HashBytes computes the SHA-256 hash in hex format.
func HashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// GenerateRandomKey creates a random 32-byte symmetric Data Encryption Key (DEK).
func GenerateRandomKey() ([]byte, error) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	return key, nil
}

// EncryptAESGCM encrypts plaintext using AES-256-GCM with a 32-byte key.
func EncryptAESGCM(plaintext, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	return ciphertext, nil
}

// DecryptAESGCM decrypts ciphertext using AES-256-GCM.
func DecryptAESGCM(ciphertext, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, errors.New("ciphertext too short")
	}
	nonce, actualCiphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	return gcm.Open(nil, nonce, actualCiphertext, nil)
}

// MerkleTree implements a binary Merkle tree for cryptographic storage auditing.
type MerkleTree struct {
	Leaves [][]byte
	Root   []byte
}

// BuildMerkleTree builds a Merkle tree from chunk hashes.
func BuildMerkleTree(chunkHashes []string) (*MerkleTree, error) {
	if len(chunkHashes) == 0 {
		return nil, errors.New("cannot build merkle tree with zero chunks")
	}

	leaves := make([][]byte, len(chunkHashes))
	for i, h := range chunkHashes {
		b, err := hex.DecodeString(h)
		if err != nil {
			return nil, fmt.Errorf("invalid chunk hash %q: %w", h, err)
		}
		leaves[i] = b
	}

	root := computeMerkleRoot(leaves)
	return &MerkleTree{
		Leaves: leaves,
		Root:   root,
	}, nil
}

func computeMerkleRoot(nodes [][]byte) []byte {
	if len(nodes) == 1 {
		return nodes[0]
	}

	var nextLevel [][]byte
	for i := 0; i < len(nodes); i += 2 {
		if i+1 < len(nodes) {
			combined := append(nodes[i], nodes[i+1]...)
			h := sha256.Sum256(combined)
			nextLevel = append(nextLevel, h[:])
		} else {
			// Odd element: duplicate and hash
			combined := append(nodes[i], nodes[i]...)
			h := sha256.Sum256(combined)
			nextLevel = append(nextLevel, h[:])
		}
	}
	return computeMerkleRoot(nextLevel)
}

// RootHex returns the hex-encoded Merkle root.
func (m *MerkleTree) RootHex() string {
	return hex.EncodeToString(m.Root)
}

// ComputeAuditResponse answers a challenge: SHA-256(chunk_bytes || nonce).
func ComputeAuditResponse(chunkBytes []byte, nonce string) string {
	h := sha256.New()
	h.Write(chunkBytes)
	h.Write([]byte(nonce))
	return hex.EncodeToString(h.Sum(nil))
}

// VerifyAuditResponse verifies an auditor's challenge against the expected answer.
func VerifyAuditResponse(chunkBytes []byte, nonce string, expectedResponse string) bool {
	return ComputeAuditResponse(chunkBytes, nonce) == expectedResponse
}
