package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stiqrhub/testcloud/pkg/audit"
	"github.com/stiqrhub/testcloud/pkg/crypto"
	"github.com/stiqrhub/testcloud/pkg/escrow"
	"github.com/stiqrhub/testcloud/pkg/p2p"
	"github.com/stiqrhub/testcloud/pkg/storage"
)

func TestNodeEditVerificationAndAccessControl(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "testcloud_auth_test_*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	store, err := storage.NewStore(tempDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	escrowMgr := escrow.NewManager("test_node", 100)
	swarm := p2p.NewSwarm("test_node", "http://127.0.0.1:8100", store)
	auditor := audit.NewCoordinator("test_node", store, escrowMgr, swarm)

	srv := NewServer(Config{
		NodeID:     "test_node",
		Store:      store,
		Swarm:      swarm,
		EscrowMgr:  escrowMgr,
		Auditor:    auditor,
		Standalone: true,
	})
	handler := srv.Handler()

	// 1. Create Data Owner Identity (Ed25519)
	ownerKP, _ := crypto.GenerateKeypair()

	createPayload, _ := json.Marshal(map[string]interface{}{
		"collection":       "secure_docs",
		"user_pub_address": ownerKP.PublicKey,
		"text":             "Original confidential text v1",
	})

	req := httptest.NewRequest("POST", "/api/records", bytes.NewReader(createPayload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("Create record failed: %d, body: %s", rec.Code, rec.Body.String())
	}

	var createResp map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &createResp)
	dataID := createResp["data_id"].(string)

	// 2. Data Owner updates record with valid Ed25519 signature -> 200 OK
	newTextV2 := "Owner updated content v2"
	toSignV2 := []byte(fmt.Sprintf("%s:2:%s", dataID, newTextV2))
	ownerSigV2, _ := crypto.SignPayload(toSignV2, ownerKP.PrivateKey)

	updatePayload, _ := json.Marshal(map[string]interface{}{
		"updater_pub_key": ownerKP.PublicKey,
		"signature":       ownerSigV2,
		"text":            newTextV2,
	})

	req = httptest.NewRequest("PATCH", "/api/records/"+dataID, bytes.NewReader(updatePayload))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Owner update failed: expected 200, got %d, body: %s", rec.Code, rec.Body.String())
	}

	// 3. Attacker with stranger key attempts update -> 403 Forbidden
	strangerKP, _ := crypto.GenerateKeypair()
	strangerSig, _ := crypto.SignPayload([]byte(fmt.Sprintf("%s:3:Hacked text", dataID)), strangerKP.PrivateKey)
	strangerPayload, _ := json.Marshal(map[string]interface{}{
		"updater_pub_key": strangerKP.PublicKey,
		"signature":       strangerSig,
		"text":            "Hacked text",
	})

	req = httptest.NewRequest("PATCH", "/api/records/"+dataID, bytes.NewReader(strangerPayload))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("Stranger update should be rejected with 403 Forbidden, got %d", rec.Code)
	}

	// 4. Attacker attempts update with owner's public key but forged signature -> 403 Forbidden
	forgedPayload, _ := json.Marshal(map[string]interface{}{
		"updater_pub_key": ownerKP.PublicKey,
		"signature":       strangerSig, // forged signature from different private key
		"text":            "Forged text",
	})

	req = httptest.NewRequest("PATCH", "/api/records/"+dataID, bytes.NewReader(forgedPayload))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("Forged signature update should be rejected with 403 Forbidden, got %d", rec.Code)
	}

	// 5. Add Friend #1 with "view" only permission
	viewerKP, _ := crypto.GenerateKeypair()
	addViewerPayload, _ := json.Marshal(map[string]interface{}{
		"owner_pub_key": ownerKP.PublicKey,
		"collaborator": storage.AccessControlEntry{
			UserPubKey: viewerKP.PublicKey,
			Permission: storage.PermView,
		},
	})
	req = httptest.NewRequest("POST", "/api/records/"+dataID+"/collaborators", bytes.NewReader(addViewerPayload))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Add viewer collaborator failed: %d, body: %s", rec.Code, rec.Body.String())
	}

	// Viewer attempts to edit -> 403 Forbidden
	viewerSig, _ := crypto.SignPayload([]byte(fmt.Sprintf("%s:3:Viewer attempt", dataID)), viewerKP.PrivateKey)
	viewerEditPayload, _ := json.Marshal(map[string]interface{}{
		"updater_pub_key": viewerKP.PublicKey,
		"signature":       viewerSig,
		"text":            "Viewer attempt",
	})
	req = httptest.NewRequest("PATCH", "/api/records/"+dataID, bytes.NewReader(viewerEditPayload))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("Viewer edit should be rejected with 403 Forbidden, got %d", rec.Code)
	}

	// 6. Add Friend #2 (Bob) with "edit" permission using Post-Quantum ML-DSA-65 identity
	bobDSA, _ := crypto.GenerateMLDSA65Keypair()
	bobKEM, _ := crypto.GenerateMLKEM768Keypair()

	addEditorPayload, _ := json.Marshal(map[string]interface{}{
		"owner_pub_key": ownerKP.PublicKey,
		"collaborator": storage.AccessControlEntry{
			UserPubKey: bobDSA.PublicKey,
			Permission: storage.PermEdit,
		},
	})
	req = httptest.NewRequest("POST", "/api/records/"+dataID+"/collaborators", bytes.NewReader(addEditorPayload))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Add editor collaborator failed: %d, body: %s", rec.Code, rec.Body.String())
	}

	// Bob edits record with post-quantum ML-DSA-65 signature -> 200 OK!
	bobTextV3 := "Bob edited with ML-DSA-65 post-quantum signature"
	toSignV3 := []byte(fmt.Sprintf("%s:3:%s", dataID, bobTextV3))
	bobSigV3, err := crypto.SignMLDSA65(toSignV3, bobDSA.PrivateKey)
	if err != nil {
		t.Fatalf("SignMLDSA65 failed: %v", err)
	}

	bobEditPayload, _ := json.Marshal(map[string]interface{}{
		"updater_pub_key": bobDSA.PublicKey,
		"signature":       bobSigV3,
		"text":            bobTextV3,
	})
	req = httptest.NewRequest("PATCH", "/api/records/"+dataID, bytes.NewReader(bobEditPayload))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Bob authorized edit failed: expected 200, got %d, body: %s", rec.Code, rec.Body.String())
	}

	// 7. Store Bob's encrypted share lockbox on the node
	dek, _ := crypto.GenerateRandomKey()
	_, bobNodeBoxes, _ := crypto.CreateThresholdEnvelope(dek, "bob", bobKEM.PublicKey, bobDSA.PublicKey)

	boxJSON, _ := json.Marshal(bobNodeBoxes[1])
	req = httptest.NewRequest("POST", "/api/records/"+dataID+"/lockbox", bytes.NewReader(boxJSON))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Save lockbox failed: %d, body: %s", rec.Code, rec.Body.String())
	}

	// Retrieve Bob's lockbox
	req = httptest.NewRequest("GET", "/api/records/"+dataID+"/lockbox?friend_id=bob", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Get lockbox failed: %d", rec.Code)
	}

	// 8. Owner revokes Bob's permission
	revokePayload, _ := json.Marshal(map[string]interface{}{
		"owner_pub_key":        ownerKP.PublicKey,
		"collaborator_pub_key": bobDSA.PublicKey,
	})
	req = httptest.NewRequest("DELETE", "/api/records/"+dataID+"/collaborators", bytes.NewReader(revokePayload))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Revoke collaborator failed: %d, body: %s", rec.Code, rec.Body.String())
	}

	// Purge Bob's lockbox from the node
	req = httptest.NewRequest("DELETE", "/api/records/"+dataID+"/lockbox?friend_id=bob", nil)
	req.Header.Set("X-User-PubKey", ownerKP.PublicKey)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Purge lockbox failed: %d", rec.Code)
	}

	// 9. Bob now attempts to edit after revocation -> 403 Forbidden!
	bobTextV4 := "Bob editing after being revoked"
	toSignV4 := []byte(fmt.Sprintf("%s:4:%s", dataID, bobTextV4))
	bobSigV4, _ := crypto.SignMLDSA65(toSignV4, bobDSA.PrivateKey)

	revokedEditPayload, _ := json.Marshal(map[string]interface{}{
		"updater_pub_key": bobDSA.PublicKey,
		"signature":       bobSigV4,
		"text":            bobTextV4,
	})
	req = httptest.NewRequest("PATCH", "/api/records/"+dataID, bytes.NewReader(revokedEditPayload))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("Revoked collaborator edit should be rejected with 403 Forbidden, got %d", rec.Code)
	}
}

func TestEnsureCollectionAndBlacklist(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "testcloud_col_test_*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	store, err := storage.NewStore(tempDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	escrowMgr := escrow.NewManager("test_node", 100, tempDir)
	swarm := p2p.NewSwarm("test_node", "http://127.0.0.1:8100", store)
	auditor := audit.NewCoordinator("test_node", store, escrowMgr, swarm)

	srv := NewServer(Config{
		NodeID:     "test_node",
		Store:      store,
		Swarm:      swarm,
		EscrowMgr:  escrowMgr,
		Auditor:    auditor,
		Standalone: true,
	})
	handler := srv.Handler()

	// 1. App Handshake: POST /api/collections/ensure
	ensureBody, _ := json.Marshal(map[string]interface{}{
		"name": "chat_messages",
		"fields": []storage.CollectionField{
			{Name: "channel_id", Type: "text", Required: true},
			{Name: "message_body", Type: "text", Required: true},
			{Name: "attachments", Type: "file", Required: false},
		},
	})

	req := httptest.NewRequest("POST", "/api/collections/ensure", bytes.NewReader(ensureBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Ensure collection failed: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var colInfo storage.CollectionInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &colInfo); err != nil {
		t.Fatalf("Failed to parse collection info: %v", err)
	}
	if colInfo.Name != "chat_messages" {
		t.Fatalf("Expected collection name 'chat_messages', got '%s'", colInfo.Name)
	}
	hasChannelField := false
	for _, f := range colInfo.Fields {
		if f.Name == "channel_id" {
			hasChannelField = true
			break
		}
	}
	if !hasChannelField {
		t.Fatalf("Expected custom field 'channel_id' in ensured collection")
	}

	// 2. Blacklist Abuse Testing: Add user to blacklist
	badUserKP, _ := crypto.GenerateKeypair()
	blPayload, _ := json.Marshal(map[string]interface{}{
		"action":  "add",
		"address": badUserKP.PublicKey,
	})
	req = httptest.NewRequest("POST", "/api/escrow/blacklist", bytes.NewReader(blPayload))
	req.RemoteAddr = "127.0.0.1:12345"
	req.Host = "localhost"
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Add blacklist failed: %d: %s", rec.Code, rec.Body.String())
	}
	if !escrowMgr.IsBlacklisted(badUserKP.PublicKey) {
		t.Fatalf("Expected bad user to be blacklisted")
	}

	// 3. Blacklisted user attempts to create record -> 403 Forbidden!
	badCreateBody, _ := json.Marshal(map[string]interface{}{
		"collection":       "chat_messages",
		"user_pub_address": badUserKP.PublicKey,
		"text":             "Abusive spam content",
	})
	req = httptest.NewRequest("POST", "/api/records", bytes.NewReader(badCreateBody))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("Expected blacklisted user record creation to be 403 Forbidden, got %d", rec.Code)
	}

	// 4. Blacklisted user attempts SDK store -> 403 Forbidden!
	badSDKBody, _ := json.Marshal(map[string]interface{}{
		"owner_pubkey": badUserKP.PublicKey,
		"content":      "More spam",
	})
	req = httptest.NewRequest("POST", "/api/store", bytes.NewReader(badSDKBody))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("Expected blacklisted SDK store to be 403 Forbidden, got %d", rec.Code)
	}

	// 5. Unban user from blacklist -> successfully allowed again
	unblPayload, _ := json.Marshal(map[string]interface{}{
		"action":  "remove",
		"address": badUserKP.PublicKey,
	})
	req = httptest.NewRequest("POST", "/api/escrow/blacklist", bytes.NewReader(unblPayload))
	req.RemoteAddr = "127.0.0.1:12345"
	req.Host = "localhost"
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Remove blacklist failed: %d", rec.Code)
	}
	if escrowMgr.IsBlacklisted(badUserKP.PublicKey) {
		t.Fatalf("Expected user to no longer be blacklisted")
	}

	// 6. Good record creation succeeds
	req = httptest.NewRequest("POST", "/api/records", bytes.NewReader(badCreateBody))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("Expected unbanned user record creation to succeed (201 Created), got %d", rec.Code)
	}
}

