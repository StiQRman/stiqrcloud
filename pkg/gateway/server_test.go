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

func TestEdgeNodeNATPullSync(t *testing.T) {
	// 1. Setup Gateway Server
	serverDir, err := os.MkdirTemp("", "testcloud_gw_sync_*")
	if err != nil {
		t.Fatalf("failed to create server temp dir: %v", err)
	}
	defer os.RemoveAll(serverDir)

	serverStore, _ := storage.NewStore(serverDir)
	serverEscrow := escrow.NewManager("pi_server", 100)
	serverSwarm := p2p.NewSwarm("pi_server", "http://192.168.1.50:8100", serverStore)
	serverAuditor := audit.NewCoordinator("pi_server", serverStore, serverEscrow, serverSwarm)

	srv := NewServer(Config{
		NodeID:     "pi_server",
		Store:      serverStore,
		Swarm:      serverSwarm,
		EscrowMgr:  serverEscrow,
		Auditor:    serverAuditor,
		Standalone: false,
	})

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// 2. Add record with chunk to Gateway
	chunkContent := []byte("hello decentralized storage chunk from pi gateway")
	rec, err := serverStore.CreateRecord(&storage.Record{
		Collection:     "family_photos",
		UserPubAddress: "ed25519_owner_key",
		Text:           "Encrypted metadata text",
		FileName:       "photo.raw",
		TotalBytes:     int64(len(chunkContent)),
	}, chunkContent, nil)
	if err != nil {
		t.Fatalf("failed to create record on gateway: %v", err)
	}

	// Assign edge node to the record's storage peers
	rec.StoragePeers = []string{"friendnode_levisenvy_4704"}
	_ = serverStore.SaveRecord(rec)

	// 3. Setup Edge Node (behind NAT: localAddr is empty "")
	edgeDir, err := os.MkdirTemp("", "testcloud_edge_*")
	if err != nil {
		t.Fatalf("failed to create edge temp dir: %v", err)
	}
	defer os.RemoveAll(edgeDir)

	edgeStore, _ := storage.NewStore(edgeDir)
	edgeEscrow := escrow.NewManager("friendnode_levisenvy_4704", 100)
	edgeSwarm := p2p.NewSwarm("friendnode_levisenvy_4704", "", edgeStore)
	edgeSwarm.AddBootstrapPeer(ts.URL)

	edgeSyncedContract := false
	edgeSwarm.SetOnRecordSynced(func(r *storage.Record) {
		if r.DataID == rec.DataID {
			edgeSyncedContract = true
			if edgeEscrow != nil {
				_, _ = edgeEscrow.CreateContract(r.DataID, r.UserPubAddress, r.TotalBytes, 1, nil, r.AuditorPeers)
			}
		}
	})

	// Before sync: edge node store must be completely empty
	if _, err := edgeStore.GetRecord(rec.DataID); err == nil {
		t.Fatalf("expected edge store to not have record before sync")
	}
	if edgeStore.HasChunk(rec.FileChunkHash) {
		t.Fatalf("expected edge store to not have chunk before sync")
	}

	// 4. Run Edge Pull Sync
	synced, err := edgeSwarm.SyncRecordsFromPeers()
	if err != nil {
		t.Fatalf("SyncRecordsFromPeers failed: %v", err)
	}
	if synced != 1 {
		t.Fatalf("Expected 1 record synced, got %d", synced)
	}

	// 5. Verify Edge Node now holds record and chunk
	pulledRec, err := edgeStore.GetRecord(rec.DataID)
	if err != nil {
		t.Fatalf("Failed to retrieve synced record from edge store: %v", err)
	}
	if pulledRec.DataID != rec.DataID || pulledRec.Text != rec.Text {
		t.Fatalf("Synced record content mismatch: %+v vs %+v", pulledRec, rec)
	}

	if !edgeStore.HasChunk(rec.FileChunkHash) {
		t.Fatalf("Edge store is missing synced chunk file!")
	}

	downloadedChunk, err := edgeStore.GetChunk(rec.FileChunkHash)
	if err != nil {
		t.Fatalf("Failed to read synced chunk: %v", err)
	}
	if !bytes.Equal(downloadedChunk, chunkContent) {
		t.Fatalf("Chunk bytes mismatch! Expected %q, got %q", string(chunkContent), string(downloadedChunk))
	}

	if !edgeSyncedContract {
		t.Fatalf("Expected onRecordSynced callback to have fired for edge escrow")
	}

	// 6. Test Idempotence: Second sync cycle skips already-cached records
	syncedSecond, err := edgeSwarm.SyncRecordsFromPeers()
	if err != nil {
		t.Fatalf("Second SyncRecordsFromPeers failed: %v", err)
	}
	if syncedSecond != 0 {
		t.Fatalf("Expected 0 records on redundant second sync, got %d", syncedSecond)
	}
}
