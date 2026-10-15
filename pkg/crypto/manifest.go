package crypto

import (
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// ManifestChunkEntry tracks metadata for an encrypted chunk in the manifest.
type ManifestChunkEntry struct {
	Index            int    `json:"index"`
	Offset           int64  `json:"offset"`
	PlaintextLength  int    `json:"plaintext_length"`
	CiphertextLength int    `json:"ciphertext_length"`
	PlaintextHash    string `json:"plaintext_hash"`    // SHA-256 hex of unencrypted data
	CiphertextHash   string `json:"ciphertext_hash"`   // SHA-256 hex of AES-GCM ciphertext
}

// EncryptedChunk contains both metadata and the encrypted payload for storage or transfer.
type EncryptedChunk struct {
	Entry      ManifestChunkEntry `json:"entry"`
	Ciphertext []byte             `json:"-"`
}

// ChunkManifest represents the signed cryptographic tracking document for a file's chunks.
type ChunkManifest struct {
	DataID               string               `json:"data_id"`
	Version              int64                `json:"version"`
	FileName             string               `json:"file_name"`
	TotalPlaintextBytes  int64                `json:"total_plaintext_bytes"`
	TotalCiphertextBytes int64                `json:"total_ciphertext_bytes"`
	ChunkCount           int                  `json:"chunk_count"`
	Chunks               []ManifestChunkEntry `json:"chunks"`
	MerkleRoot           string               `json:"merkle_root"`
	AuthorPubKey         string               `json:"author_pub_key"`
	AuthorSignature      string               `json:"author_signature"`
	UpdatedAt            time.Time            `json:"updated_at"`
}

// CanonicalSignBytes generates the deterministic byte sequence used for signing the manifest.
func (m *ChunkManifest) CanonicalSignBytes() []byte {
	return []byte(fmt.Sprintf(
		"stiqrcloud:manifest:v1:%s:%d:%s:%d:%s:%s",
		m.DataID,
		m.Version,
		m.FileName,
		m.TotalPlaintextBytes,
		m.MerkleRoot,
		m.AuthorPubKey,
	))
}

// VerifySignature verifies the manifest author's hybrid signature (Ed25519 or ML-DSA-65).
func (m *ChunkManifest) VerifySignature() bool {
	if m.AuthorPubKey == "" || m.AuthorSignature == "" {
		return false
	}
	canonical := m.CanonicalSignBytes()
	return VerifyHybridSignature(canonical, m.AuthorPubKey, m.AuthorSignature)
}

// EncryptAndBuildManifest slices a file with FastCDC, encrypts chunks with the DEK,
// builds the Merkle tree over ciphertext hashes, and signs the manifest.
func EncryptAndBuildManifest(
	dataID string,
	fileName string,
	version int64,
	fileData []byte,
	dek []byte,
	authorPrivKey string,
	cfg ChunkConfig,
) (*ChunkManifest, []*EncryptedChunk, error) {
	if len(dek) != 32 {
		return nil, nil, errors.New("DEK must be 32 bytes")
	}

	rawChunks, err := FastCDC(fileData, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("FastCDC failed: %w", err)
	}

	var encChunks []*EncryptedChunk
	var chunkEntries []ManifestChunkEntry
	var ctHashes []string
	var totalCiphertextBytes int64

	for _, raw := range rawChunks {
		encBytes, err := EncryptAESGCM(raw.Data, dek)
		if err != nil {
			return nil, nil, fmt.Errorf("chunk %d encryption failed: %w", raw.Index, err)
		}

		ctHash := HashBytes(encBytes)
		ctHashes = append(ctHashes, ctHash)
		totalCiphertextBytes += int64(len(encBytes))

		entry := ManifestChunkEntry{
			Index:            raw.Index,
			Offset:           raw.Offset,
			PlaintextLength:  raw.Length,
			CiphertextLength: len(encBytes),
			PlaintextHash:    raw.Hash,
			CiphertextHash:   ctHash,
		}

		chunkEntries = append(chunkEntries, entry)
		encChunks = append(encChunks, &EncryptedChunk{
			Entry:      entry,
			Ciphertext: encBytes,
		})
	}

	merkleRoot := ""
	if len(ctHashes) > 0 {
		tree, err := BuildMerkleTree(ctHashes)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to build merkle tree: %w", err)
		}
		merkleRoot = tree.RootHex()
	}

	// Derive public key from private key to record in manifest
	var authorPubKey string
	privBytes, err := hex.DecodeString(authorPrivKey)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid author private key hex: %w", err)
	}

	if len(privBytes) == 64 {
		// Ed25519 private key contains public key in last 32 bytes
		authorPubKey = hex.EncodeToString(privBytes[32:])
	} else {
		// For ML-DSA-65 or other, signature will be verified against provided key
		authorPubKey = hex.EncodeToString(privBytes[:32]) // Fallback tag
	}

	manifest := &ChunkManifest{
		DataID:               dataID,
		Version:              version,
		FileName:             fileName,
		TotalPlaintextBytes:  int64(len(fileData)),
		TotalCiphertextBytes: totalCiphertextBytes,
		ChunkCount:           len(chunkEntries),
		Chunks:               chunkEntries,
		MerkleRoot:           merkleRoot,
		AuthorPubKey:         authorPubKey,
		UpdatedAt:            time.Now().UTC(),
	}

	sig, err := SignHybrid(manifest.CanonicalSignBytes(), authorPrivKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to sign manifest: %w", err)
	}
	manifest.AuthorSignature = sig

	return manifest, encChunks, nil
}

// IncrementalUpdateManifest takes the previous manifest and updated file content,
// uses FastCDC to identify only changed chunks, re-encrypts ONLY modified chunks,
// reuses unchanged chunks, and produces an updated signed manifest.
func IncrementalUpdateManifest(
	prevManifest *ChunkManifest,
	updatedFileData []byte,
	dek []byte,
	authorPubKey string,
	authorPrivKey string,
	cfg ChunkConfig,
) (*ChunkManifest, []*EncryptedChunk, error) {
	if prevManifest == nil {
		return nil, nil, errors.New("nil previous manifest")
	}
	if len(dek) != 32 {
		return nil, nil, errors.New("DEK must be 32 bytes")
	}

	// Index existing chunks by PlaintextHash -> ManifestChunkEntry
	existingMap := make(map[string]ManifestChunkEntry)
	for _, entry := range prevManifest.Chunks {
		existingMap[entry.PlaintextHash] = entry
	}

	// Sice updated file with FastCDC
	newRawChunks, err := FastCDC(updatedFileData, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("FastCDC failed: %w", err)
	}

	var newEncChunks []*EncryptedChunk
	var updatedEntries []ManifestChunkEntry
	var ctHashes []string
	var totalCiphertextBytes int64

	for i, raw := range newRawChunks {
		if prevEntry, exists := existingMap[raw.Hash]; exists {
			// Chunk content is identical: reuse existing encrypted chunk and ciphertext hash!
			reused := ManifestChunkEntry{
				Index:            i,
				Offset:           raw.Offset,
				PlaintextLength:  raw.Length,
				CiphertextLength: prevEntry.CiphertextLength,
				PlaintextHash:    raw.Hash,
				CiphertextHash:   prevEntry.CiphertextHash,
			}
			updatedEntries = append(updatedEntries, reused)
			ctHashes = append(ctHashes, prevEntry.CiphertextHash)
			totalCiphertextBytes += int64(prevEntry.CiphertextLength)
		} else {
			// Chunk was modified or newly created: encrypt only this chunk!
			encBytes, err := EncryptAESGCM(raw.Data, dek)
			if err != nil {
				return nil, nil, fmt.Errorf("chunk %d encryption failed: %w", i, err)
			}

			ctHash := HashBytes(encBytes)
			ctHashes = append(ctHashes, ctHash)
			totalCiphertextBytes += int64(len(encBytes))

			newEntry := ManifestChunkEntry{
				Index:            i,
				Offset:           raw.Offset,
				PlaintextLength:  raw.Length,
				CiphertextLength: len(encBytes),
				PlaintextHash:    raw.Hash,
				CiphertextHash:   ctHash,
			}

			updatedEntries = append(updatedEntries, newEntry)
			newEncChunks = append(newEncChunks, &EncryptedChunk{
				Entry:      newEntry,
				Ciphertext: encBytes,
			})
		}
	}

	merkleRoot := ""
	if len(ctHashes) > 0 {
		tree, err := BuildMerkleTree(ctHashes)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to build merkle tree: %w", err)
		}
		merkleRoot = tree.RootHex()
	}

	updatedManifest := &ChunkManifest{
		DataID:               prevManifest.DataID,
		Version:              prevManifest.Version + 1,
		FileName:             prevManifest.FileName,
		TotalPlaintextBytes:  int64(len(updatedFileData)),
		TotalCiphertextBytes: totalCiphertextBytes,
		ChunkCount:           len(updatedEntries),
		Chunks:               updatedEntries,
		MerkleRoot:           merkleRoot,
		AuthorPubKey:         authorPubKey,
		UpdatedAt:            time.Now().UTC(),
	}

	sig, err := SignHybrid(updatedManifest.CanonicalSignBytes(), authorPrivKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to sign updated manifest: %w", err)
	}
	updatedManifest.AuthorSignature = sig

	return updatedManifest, newEncChunks, nil
}
