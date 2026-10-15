package storage

import (
	"os"
	"testing"
	"time"

	"github.com/stiqrhub/stiqrcloud/pkg/crypto"
)

func TestStandaloneDatabaseCRUDAndInvariantDataID(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "stiqrcloud_db_*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	store, err := NewStore(tempDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	ownerKP, _ := crypto.GenerateKeypair()

	// 1. Create Record
	rec := &Record{
		UserPubAddress: ownerKP.PublicKey,
		Collection:     "notes",
		Text:           "Version 1 plaintext content",
		FileName:       "sample.txt",
	}

	fileBytes := []byte("hello file content")
	picBytes := []byte("picture binary blob")

	created, err := store.CreateRecord(rec, fileBytes, picBytes)
	if err != nil {
		t.Fatalf("CreateRecord failed: %v", err)
	}

	originalDataID := created.DataID
	if originalDataID == "" {
		t.Fatalf("Expected non-empty DataID")
	}
	if created.Version != 1 {
		t.Fatalf("Expected initial version 1, got %d", created.Version)
	}

	// 2. Fetch chunk and verify
	chunk, err := store.GetChunk(created.FileChunkHash)
	if err != nil || string(chunk) != string(fileBytes) {
		t.Fatalf("GetChunk failed for file attachment")
	}

	// 3. Update Record - CRITICAL REQUIREMENT: Data ID MUST STAY THE SAME!
	updatedText := "Version 2 updated content"
	sig, _ := crypto.SignPayload([]byte(originalDataID+":2:"+updatedText), ownerKP.PrivateKey)

	updated, err := store.UpdateRecord(originalDataID, ownerKP.PublicKey, sig, updatedText, []byte("updated file"), nil)
	if err != nil {
		t.Fatalf("UpdateRecord failed: %v", err)
	}

	if updated.DataID != originalDataID {
		t.Fatalf("CRITICAL FAILURE: Data ID changed! Expected %s, got %s", originalDataID, updated.DataID)
	}
	if updated.Version != 2 {
		t.Fatalf("Expected version 2, got %d", updated.Version)
	}
	if updated.Text != updatedText {
		t.Fatalf("Expected text %s, got %s", updatedText, updated.Text)
	}

	// 4. Test Google Drive style sharing (ACL + Wrapped DEK)
	friendKP, _ := crypto.GenerateKeypair()
	aclEntry := AccessControlEntry{
		UserPubKey: friendKP.PublicKey,
		Permission: PermEdit,
		WrappedDEK: "mock_wrapped_dek_hex_string_32_bytes",
	}

	if err := store.AddCollaborator(originalDataID, ownerKP.PublicKey, aclEntry); err != nil {
		t.Fatalf("AddCollaborator failed: %v", err)
	}

	fetched, err := store.GetRecord(originalDataID)
	if err != nil {
		t.Fatalf("GetRecord failed: %v", err)
	}

	if len(fetched.ACL) != 1 || fetched.ACL[0].UserPubKey != friendKP.PublicKey {
		t.Fatalf("ACL verification failed: %v", fetched.ACL)
	}

	// Friend (editor) can now update
	friendSig, _ := crypto.SignPayload([]byte(originalDataID+":3:Friend edited"), friendKP.PrivateKey)
	friendUpdated, err := store.UpdateRecord(originalDataID, friendKP.PublicKey, friendSig, "Friend edited", nil, nil)
	if err != nil {
		t.Fatalf("Friend update failed: %v", err)
	}
	if friendUpdated.DataID != originalDataID || friendUpdated.Version != 3 {
		t.Fatalf("Friend update data ID or version mismatch")
	}

	// 5. Query and list
	list, total := store.ListRecords("notes", 10, 0)
	if total != 1 || len(list) != 1 {
		t.Fatalf("ListRecords failed: total %d", total)
	}

	// 6. Delete
	if err := store.DeleteRecord(originalDataID, ownerKP.PublicKey, ""); err != nil {
		t.Fatalf("DeleteRecord failed: %v", err)
	}

	_, err = store.GetRecord(originalDataID)
	if err == nil {
		t.Fatalf("Record should have been deleted")
	}
}

func TestExpiredContractEvictionWhenStorageFull(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "stiqrcloud_evict_*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	store, err := NewStore(tempDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	// Set small quota of 300 bytes
	store.SetMaxDiskBytes(300)

	// Simulated contract status: doc_expired is expired, doc_active is active
	expiredTime := time.Now().Add(-2 * time.Hour)
	activeTime := time.Now().Add(30 * 24 * time.Hour)

	store.SetContractChecker(func(dataID string) (bool, time.Time) {
		if dataID == "doc_expired" {
			return true, expiredTime
		}
		return false, activeTime
	})

	// 1. Create expired record with 150 byte payload
	chunk1 := make([]byte, 150)
	for i := range chunk1 {
		chunk1[i] = 'A'
	}
	rec1 := &Record{DataID: "doc_expired", Collection: "test"}
	_, err = store.CreateRecord(rec1, chunk1, nil)
	if err != nil {
		t.Fatalf("CreateRecord doc_expired failed: %v", err)
	}

	// Verify chunk1 exists on disk (NOT automatically deleted upon contract expiry!)
	stats := store.StorageStats()
	if stats.UsedBytes < 150 {
		t.Fatalf("Expected chunk to be preserved, used %d", stats.UsedBytes)
	}
	if stats.GraceRecordCount != 1 {
		t.Fatalf("Expected 1 record in grace retention, got %d", stats.GraceRecordCount)
	}

	// 2. Now write a new active contract chunk with 200 bytes
	// 150 (existing) + 200 (new) = 350 bytes > 300 bytes max limit!
	// Storage engine must evict doc_expired chunk to make room for new contract!
	chunk2 := make([]byte, 200)
	for i := range chunk2 {
		chunk2[i] = 'B'
	}
	rec2 := &Record{DataID: "doc_active", Collection: "test"}
	_, err = store.CreateRecord(rec2, chunk2, nil)
	if err != nil {
		t.Fatalf("CreateRecord doc_active should succeed after evicting expired contract: %v", err)
	}

	// Verify doc_active chunk exists
	newChunk, err := store.GetChunk(rec2.FileChunkHash)
	if err != nil || len(newChunk) != 200 {
		t.Fatalf("Expected active chunk to be present")
	}

	// Verify doc_expired chunk was evicted to make room
	_, err = store.GetChunk(rec1.FileChunkHash)
	if err == nil {
		t.Fatalf("Expected expired chunk to be evicted when storage reached quota")
	}
}
