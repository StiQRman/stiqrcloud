package storage

import (
	"os"
	"testing"

	"github.com/stiqrhub/testcloud/pkg/crypto"
)

func TestStandaloneDatabaseCRUDAndInvariantDataID(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "testcloud_db_*")
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
