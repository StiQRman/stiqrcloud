package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stiqrhub/stiqrcloud/pkg/crypto"
	"github.com/stiqrhub/stiqrcloud/pkg/storage"
)

// TestAudit_AllExternalSecurityIssues tests each of the 8 issues raised in the audit.
func TestAudit_AllExternalSecurityIssues(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "stiqrcloud_audit_all_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	store, err := storage.NewStore(tempDir)
	if err != nil {
		t.Fatal(err)
	}

	srv := NewServer(Config{
		Store:    store,
		IsPublic: true,
	})

	// -------------------------------------------------------------
	// ISSUE 1 & 6: Unauthenticated /api/internal/store & UpsertReplicatedRecord
	// -------------------------------------------------------------
	aliceKP, _ := crypto.GenerateKeypair()
	rec := &storage.Record{
		DataID:         "test-data-id-alice-001",
		Collection:     "default",
		UserPubAddress: aliceKP.PublicKey,
		Text:           "Alice Secret Original Content",
	}
	created, err := store.CreateRecord(rec, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create record: %v", err)
	}

	attackerPayload := map[string]interface{}{
		"record": map[string]interface{}{
			"data_id":          created.DataID,
			"collection":       "default",
			"user_pub_address": "attacker-pubkey",
			"version":          999,
			"text":             "POISONED BY ATTACKER VIA REPLICATION",
		},
	}
	bodyBytes, _ := json.Marshal(attackerPayload)
	req1 := httptest.NewRequest("POST", "/api/internal/store", bytes.NewReader(bodyBytes))
	req1.Header.Set("Content-Type", "application/json")
	w1 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w1, req1)

	currentRec, _ := store.GetRecord(created.DataID)
	issue1and6Vulnerable := (w1.Code == http.StatusOK && currentRec.Text == "POISONED BY ATTACKER VIA REPLICATION")
	t.Logf("[Issue 1 & 6] Unauthenticated Record Overwrite via Replication: Vulnerable=%v (Status: %d, Text: %q)",
		issue1and6Vulnerable, w1.Code, currentRec.Text)

	// -------------------------------------------------------------
	// ISSUE 2: Public Record Creation Without Proof-of-Possession Signature
	// -------------------------------------------------------------
	attackerSquatPayload := map[string]interface{}{
		"data_id":          "test-squat-alice-002",
		"collection":       "default",
		"user_pub_address": aliceKP.PublicKey,
		"signature":        "", // Missing signature
		"text":             "Forged note claiming to be authored by Alice",
	}
	bodySquat, _ := json.Marshal(attackerSquatPayload)
	req2 := httptest.NewRequest("POST", "/api/records", bytes.NewReader(bodySquat))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w2, req2)

	squattedRec, sErr := store.GetRecord("test-squat-alice-002")
	ownerStr := ""
	if squattedRec != nil {
		ownerStr = squattedRec.UserPubAddress
	}
	issue2Vulnerable := (sErr == nil && squattedRec != nil && squattedRec.UserPubAddress == aliceKP.PublicKey)
	t.Logf("[Issue 2] Identity Squatting Without Signature on Creation: Vulnerable=%v (Status: %d, Owner: %q)",
		issue2Vulnerable, w2.Code, ownerStr)

	// -------------------------------------------------------------
	// ISSUE 3: Mock PocketBase JWT Authentication Endpoint
	// -------------------------------------------------------------
	req3 := httptest.NewRequest("POST", "/api/collections/users/auth-with-password", nil)
	w3 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w3, req3)

	var res3 map[string]interface{}
	_ = json.Unmarshal(w3.Body.Bytes(), &res3)
	token3, _ := res3["token"].(string)

	patchPayload := map[string]interface{}{"text": "Bypass attempt"}
	patchBytes, _ := json.Marshal(patchPayload)
	req3Bypass := httptest.NewRequest("PATCH", "/api/collections/default/records/test-data-id-alice-001", bytes.NewReader(patchBytes))
	req3Bypass.Header.Set("Authorization", "Bearer "+token3)
	req3Bypass.Header.Set("Content-Type", "application/json")
	w3Bypass := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w3Bypass, req3Bypass)

	issue3IsBypass := (w3Bypass.Code == http.StatusOK)
	t.Logf("[Issue 3] Mock JWT Token Exists=%v, Token Grants Server Bypass=%v (Status: %d)",
		token3 == "stiqrcloud_mock_jwt_token", issue3IsBypass, w3Bypass.Code)

	// -------------------------------------------------------------
	// ISSUE 4: Unbounded Public Resource Consumption (Rate Limiting & Storage Quotas)
	// -------------------------------------------------------------
	// Test sending multiple unauthenticated writes without any rate limit or payment enforcement
	writeAcceptedCount := 0
	rateLimitedCount := 0
	for i := 0; i < 35; i++ {
		floodPayload := map[string]interface{}{
			"data_id": fmt.Sprintf("flood-test-%d", i),
			"text":    "unauthenticated storage consumption test",
		}
		fb, _ := json.Marshal(floodPayload)
		rf := httptest.NewRequest("POST", "/api/records", bytes.NewReader(fb))
		rf.Header.Set("Content-Type", "application/json")
		rf.RemoteAddr = "198.51.100.25:54321" // External WAN IP
		wf := httptest.NewRecorder()
		srv.Handler().ServeHTTP(wf, rf)
		if wf.Code == http.StatusCreated {
			writeAcceptedCount++
		} else if wf.Code == http.StatusTooManyRequests {
			rateLimitedCount++
		}
	}
	t.Logf("[Issue 4] Unauthenticated Storage Writes Rate-Limiting: Accepted=%d, Throttled(429)=%d (Protected=%v)",
		writeAcceptedCount, rateLimitedCount, rateLimitedCount > 0)

	// -------------------------------------------------------------
	// ISSUE 5: Signature Malleability / Lack of Content Binding in Updates
	// -------------------------------------------------------------
	rec5 := &storage.Record{
		DataID:         "test-malleable-005",
		Collection:     "default",
		UserPubAddress: aliceKP.PublicKey,
		Text:           "Legitimate version 1 text",
	}
	_, _ = store.CreateRecord(rec5, nil, nil)

	// User signs generic message "dataID:update"
	genericMsg := []byte(fmt.Sprintf("%s:update", rec5.DataID))
	sigHex, _ := crypto.SignPayload(genericMsg, aliceKP.PrivateKey)

	// Attacker intercepts signature and supplies tampered text
	tamperedText := "ATTACKER TAMPERED CONTENT"
	updatedRec5, uErr := store.UpdateRecord(rec5.DataID, aliceKP.PublicKey, sigHex, tamperedText, nil, nil)
	updatedText := ""
	if updatedRec5 != nil {
		updatedText = updatedRec5.Text
	}
	issue5Vulnerable := (uErr == nil && updatedRec5 != nil && updatedRec5.Text == tamperedText)
	t.Logf("[Issue 5] Generic Signature Malleability (Content Swapping): Vulnerable=%v (Updated text: %q)",
		issue5Vulnerable, updatedText)

	// -------------------------------------------------------------
	// ISSUE 7: Private Data Plaintext Exposure & Public Listing
	// -------------------------------------------------------------
	req7List := httptest.NewRequest("GET", "/api/records?collection=default", nil)
	w7List := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w7List, req7List)
	listLeaked := bytes.Contains(w7List.Body.Bytes(), []byte(tamperedText))

	t.Logf("[Issue 7] Public Listing Exposes Plaintext Payloads When Client Does Not Encrypt: Leaked=%v", listLeaked)

	// -------------------------------------------------------------
	// ISSUE 8: Installer Script Pipeline Integrity (SHA256 pinning)
	// -------------------------------------------------------------
	req8 := httptest.NewRequest("GET", "/install.sh", nil)
	w8 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w8, req8)
	scriptBody := w8.Body.String()
	hasChecksumVerification := bytes.Contains([]byte(scriptBody), []byte("sha256sum"))
	t.Logf("[Issue 8] Installer Script Verifies SHA256 Checksum: HasChecksum=%v", hasChecksumVerification)
}
