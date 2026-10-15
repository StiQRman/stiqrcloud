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

func TestAudit_ComprehensiveHardening(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "stiqrcloud_hardening_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	store, _ := storage.NewStore(tempDir)
	srv := NewServer(Config{
		Store:    store,
		IsPublic: true,
	})
	handler := srv.Handler()

	// 1. SEC-02: Public WAN deletion of unowned/anonymous records MUST be 403 Forbidden!
	unownedRec := &storage.Record{
		DataID:     "unowned-doc-001",
		Collection: "notes",
		Text:       "Sensitive internal document without explicit owner key",
	}
	_, _ = store.CreateRecord(unownedRec, nil, nil)

	// WAN deletion attempt (no local IP)
	reqDelWAN := httptest.NewRequest("DELETE", "/api/records/unowned-doc-001", nil)
	wDelWAN := httptest.NewRecorder()
	handler.ServeHTTP(wDelWAN, reqDelWAN)
	if wDelWAN.Code != http.StatusForbidden {
		t.Fatalf("[SEC-02] SECURITY VIOLATION: Unowned record deleted over WAN! Got status %d, want 403", wDelWAN.Code)
	}

	// Verify unowned record is still intact
	if _, err := store.GetRecord("unowned-doc-001"); err != nil {
		t.Fatalf("[SEC-02] Unowned record was erroneously deleted!")
	}

	// Local deletion attempt MUST succeed (200 OK)
	reqDelLocal := httptest.NewRequest("DELETE", "http://127.0.0.1:8100/api/records/unowned-doc-001", nil)
	reqDelLocal.RemoteAddr = "127.0.0.1:54321"
	wDelLocal := httptest.NewRecorder()
	handler.ServeHTTP(wDelLocal, reqDelLocal)
	if wDelLocal.Code != http.StatusOK {
		t.Fatalf("[SEC-02] Local administrative deletion of unowned record failed: got %d, want 200", wDelLocal.Code)
	}

	// 2. SEC-03: WAN access to /api/reticulum/send MUST be 403 Forbidden!
	reqRnsSend := httptest.NewRequest("POST", "/api/reticulum/send", bytes.NewReader([]byte(`{"target_dest":"1234","text":"forged"}`)))
	wRnsSend := httptest.NewRecorder()
	handler.ServeHTTP(wRnsSend, reqRnsSend)
	if wRnsSend.Code != http.StatusForbidden {
		t.Fatalf("[SEC-03] SECURITY VIOLATION: WAN access to /api/reticulum/send permitted! Got %d, want 403", wRnsSend.Code)
	}

	// 3. SEC-04: Merkle Tree Odd-Leaf Collision Resistance (CVE-2012-2459 prevention)
	leafA := crypto.HashBytes([]byte("chunk A"))
	leafB := crypto.HashBytes([]byte("chunk B"))
	leafC := crypto.HashBytes([]byte("chunk C"))

	tree3, err3 := crypto.BuildMerkleTree([]string{leafA, leafB, leafC})
	tree4, err4 := crypto.BuildMerkleTree([]string{leafA, leafB, leafC, leafC})
	if err3 != nil || err4 != nil {
		t.Fatalf("[SEC-04] Failed to build trees: %v, %v", err3, err4)
	}
	if tree3.RootHex() == tree4.RootHex() {
		t.Fatalf("[SEC-04] SECURITY VIOLATION: Merkle root collision between 3-chunk and 4-chunk trees! Root: %s", tree3.RootHex())
	}

	// 4. SEC-09: PowerShell installer MUST contain SHA256 checksum verification
	reqPS := httptest.NewRequest("GET", "/install.ps1", nil)
	wPS := httptest.NewRecorder()
	handler.ServeHTTP(wPS, reqPS)
	psScript := wPS.Body.String()
	if !bytes.Contains([]byte(psScript), []byte("Get-FileHash")) || !bytes.Contains([]byte(psScript), []byte("SHA256")) {
		t.Fatalf("[SEC-09] SECURITY VIOLATION: install.ps1 lacks SHA256 verification!")
	}

	// 5. SEC-10: File download endpoints MUST include nosniff & DENY headers
	fileRec := &storage.Record{
		DataID:        "file-doc-002",
		Collection:    "files",
		FileName:      "payload.html",
		FileChunkHash: leafA,
	}
	_, _ = store.CreateRecord(fileRec, []byte("<html><script>alert(1)</script></html>"), nil)
	reqFile := httptest.NewRequest("GET", "/api/files/default/file-doc-002/payload.html", nil)
	wFile := httptest.NewRecorder()
	handler.ServeHTTP(wFile, reqFile)
	if wFile.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("[SEC-10] Missing X-Content-Type-Options: nosniff header on file download!")
	}
	if wFile.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("[SEC-10] Missing X-Frame-Options: DENY header on file download!")
	}
}
