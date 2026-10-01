package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stiqrhub/testcloud/pkg/audit"
	"github.com/stiqrhub/testcloud/pkg/escrow"
	"github.com/stiqrhub/testcloud/pkg/p2p"
	"github.com/stiqrhub/testcloud/pkg/storage"
	"github.com/stiqrhub/testcloud/pkg/version"
)

func TestGatewayAPIEndpointsAndInvariantDataID(t *testing.T) {
	tempDir, _ := os.MkdirTemp("", "testcloud_gw_*")
	defer os.RemoveAll(tempDir)

	store, _ := storage.NewStore(tempDir)
	escrowMgr := escrow.NewManager("node_test", 100)
	swarm := p2p.NewSwarm("node_test", "http://127.0.0.1:8100", store)
	auditor := audit.NewCoordinator("node_test", store, escrowMgr, swarm)

	srv := NewServer(Config{
		NodeID:     "node_test",
		Store:      store,
		Swarm:      swarm,
		EscrowMgr:  escrowMgr,
		Auditor:    auditor,
		Standalone: true,
	})

	handler := srv.Handler()

	// 1. Health check
	req := httptest.NewRequest("GET", "/api/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Health check failed: status %d", rec.Code)
	}

	var health map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &health)
	if health["version"] != version.CurrentVersion {
		t.Fatalf("Expected version %s, got %v", version.CurrentVersion, health["version"])
	}

	// 2. Create Record
	payload := `{"collection":"notes","user_pub_address":"pubkey_alice","text":"Hello testcloud database"}`
	req = httptest.NewRequest("POST", "/api/records", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("Create record failed: %d, body: %s", rec.Code, rec.Body.String())
	}

	var createResp map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &createResp)
	createdDataID, ok := createResp["data_id"].(string)
	if !ok || createdDataID == "" {
		t.Fatalf("Expected valid data_id in response")
	}

	// 3. Update Record - Invariant Data ID Check
	updatePayload := `{"updater_pub_key":"pubkey_alice","text":"Updated content text"}`
	req = httptest.NewRequest("PATCH", "/api/records/"+createdDataID, bytes.NewBufferString(updatePayload))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Update record failed: %d, body: %s", rec.Code, rec.Body.String())
	}

	var updateResp storage.Record
	_ = json.Unmarshal(rec.Body.Bytes(), &updateResp)
	if updateResp.DataID != createdDataID {
		t.Fatalf("CRITICAL: Data ID changed! Expected %s, got %s", createdDataID, updateResp.DataID)
	}
	if updateResp.Version != 2 {
		t.Fatalf("Expected version 2, got %d", updateResp.Version)
	}

	// 4. PocketBase compatibility endpoint
	req = httptest.NewRequest("GET", "/api/collections/notes/records", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("PocketBase endpoint failed: %d", rec.Code)
	}
}
