package crypto

import (
	"bytes"
	"fmt"
	"testing"
)

func TestFastCDCMiddleEditNoDomino(t *testing.T) {
	// Use small chunk configuration for fast, predictable testing:
	// Min 1KB, Avg 4KB, Max 16KB
	cfg := ChunkConfig{
		MinSize: 1024,
		AvgSize: 4096,
		MaxSize: 16384,
	}

	// Generate 128 KB of synthetic text data
	var buf bytes.Buffer
	for i := 0; i < 2000; i++ {
		buf.WriteString(fmt.Sprintf("Line %05d: the quick brown fox jumps over the lazy dog\n", i))
	}
	originalData := buf.Bytes()

	originalChunks, err := FastCDC(originalData, cfg)
	if err != nil {
		t.Fatalf("FastCDC failed: %v", err)
	}

	if len(originalChunks) < 5 {
		t.Fatalf("Expected at least 5 chunks, got %d", len(originalChunks))
	}

	// Edit in the middle: insert 35 bytes at the end of line 500
	needle := []byte("Line 00500:")
	idx := bytes.Index(originalData, needle)
	if idx == -1 {
		t.Fatalf("Failed to locate needle in original data")
	}

	insertPos := idx + len(needle)
	var modifiedData []byte
	modifiedData = append(modifiedData, originalData[:insertPos]...)
	modifiedData = append(modifiedData, []byte(" [INSERTED COLLABORATIVE EDIT TEXT] ")...)
	modifiedData = append(modifiedData, originalData[insertPos:]...)

	modifiedChunks, err := FastCDC(modifiedData, cfg)
	if err != nil {
		t.Fatalf("FastCDC on modified data failed: %v", err)
	}

	// Compare chunk hashes to verify subsequent chunks did NOT domino:
	// Find which chunk had the edit
	var originalHashes []string
	for _, c := range originalChunks {
		originalHashes = append(originalHashes, c.Hash)
	}

	var modifiedHashes []string
	for _, c := range modifiedChunks {
		modifiedHashes = append(modifiedHashes, c.Hash)
	}

	// Count how many trailing chunks match exactly (preventing domino effect)
	matchingTrailingChunks := 0
	origIdx := len(originalHashes) - 1
	modIdx := len(modifiedHashes) - 1

	for origIdx >= 0 && modIdx >= 0 {
		if originalHashes[origIdx] == modifiedHashes[modIdx] {
			matchingTrailingChunks++
			origIdx--
			modIdx--
		} else {
			break
		}
	}

	if matchingTrailingChunks < 2 {
		t.Fatalf("Expected at least 2 trailing chunks to be preserved without domino effect, got %d", matchingTrailingChunks)
	}
}

func TestManifestCreationAndIncrementalEdit(t *testing.T) {
	dek, _ := GenerateRandomKey()
	authorKeypair, _ := GenerateKeypair()

	cfg := ChunkConfig{
		MinSize: 1024,
		AvgSize: 4096,
		MaxSize: 16384,
	}

	var buf bytes.Buffer
	for i := 0; i < 2000; i++ {
		buf.WriteString(fmt.Sprintf("Document section %04d: confidential proprietary report\n", i))
	}
	originalData := buf.Bytes()

	// 1. Initial creation
	manifest, encChunks, err := EncryptAndBuildManifest(
		"data_doc_1001",
		"report.txt",
		1,
		originalData,
		dek,
		authorKeypair.PrivateKey,
		cfg,
	)
	if err != nil {
		t.Fatalf("EncryptAndBuildManifest failed: %v", err)
	}

	if len(encChunks) != manifest.ChunkCount {
		t.Fatalf("Chunk count mismatch: manifest %d, chunks %d", manifest.ChunkCount, len(encChunks))
	}

	// Verify author's signature on manifest
	if !manifest.VerifySignature() {
		t.Fatalf("Manifest signature failed verification")
	}

	// 2. Incremental middle update
	var updatedData []byte
	updatedData = append(updatedData, originalData[:4000]...)
	updatedData = append(updatedData, []byte(" --- MODIFIED CRITICAL STATEMENT --- ")...)
	updatedData = append(updatedData, originalData[4000:]...)

	updatedManifest, newEncChunks, err := IncrementalUpdateManifest(
		manifest,
		updatedData,
		dek,
		authorKeypair.PublicKey,
		authorKeypair.PrivateKey,
		cfg,
	)
	if err != nil {
		t.Fatalf("IncrementalUpdateManifest failed: %v", err)
	}

	if updatedManifest.Version != manifest.Version+1 {
		t.Fatalf("Expected version %d, got %d", manifest.Version+1, updatedManifest.Version)
	}

	if !updatedManifest.VerifySignature() {
		t.Fatalf("Updated manifest signature failed verification")
	}

	// Verify only a fraction of chunks were encrypted, not all of them
	if len(newEncChunks) >= manifest.ChunkCount {
		t.Fatalf("Expected incremental update to re-encrypt only modified chunks (%d new, total %d)", len(newEncChunks), manifest.ChunkCount)
	}

	// Tampered signature must fail
	updatedManifest.AuthorSignature = "badsignature"
	if updatedManifest.VerifySignature() {
		t.Fatalf("Tampered manifest signature should have failed")
	}
}
