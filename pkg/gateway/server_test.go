package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stiqrhub/stiqrcloud/pkg/audit"
	"github.com/stiqrhub/stiqrcloud/pkg/crypto"
	"github.com/stiqrhub/stiqrcloud/pkg/escrow"
	"github.com/stiqrhub/stiqrcloud/pkg/p2p"
	"github.com/stiqrhub/stiqrcloud/pkg/reticulum"
	"github.com/stiqrhub/stiqrcloud/pkg/storage"
	"github.com/stiqrhub/stiqrcloud/pkg/version"
)

func TestGatewayAPIEndpointsAndInvariantDataID(t *testing.T) {
	tempDir, _ := os.MkdirTemp("", "stiqrcloud_gw_*")
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
	aliceKP, _ := crypto.GenerateKeypair()
	text1 := "Hello stiqrcloud database"
	sigCreate, _ := crypto.SignPayload([]byte(fmt.Sprintf("%s:%s:%s", "notes", text1, aliceKP.PublicKey)), aliceKP.PrivateKey)
	payload := fmt.Sprintf(`{"collection":"notes","user_pub_address":%q,"signature":%q,"text":%q}`, aliceKP.PublicKey, sigCreate, text1)
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

	// 3. Update Record - Invariant Data ID Check with Signed Payload
	newText := "Updated content text"
	sig, _ := crypto.SignPayload([]byte(fmt.Sprintf("%s:2:%s", createdDataID, newText)), aliceKP.PrivateKey)
	updatePayload := fmt.Sprintf(`{"updater_pub_key":%q,"signature":%q,"text":%q}`, aliceKP.PublicKey, sig, newText)
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
	serverDir, err := os.MkdirTemp("", "stiqrcloud_gw_sync_*")
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
	ownerKP, _ := crypto.GenerateKeypair()
	textPhoto := "Encrypted metadata text"
	sigPhoto, _ := crypto.SignPayload([]byte(fmt.Sprintf("%s:%s:%s", "family_photos", textPhoto, ownerKP.PublicKey)), ownerKP.PrivateKey)
	rec, err := serverStore.CreateRecord(&storage.Record{
		Collection:     "family_photos",
		UserPubAddress: ownerKP.PublicKey,
		OwnerSignature: sigPhoto,
		Text:           textPhoto,
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
	edgeDir, err := os.MkdirTemp("", "stiqrcloud_edge_*")
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

func TestDashboardLocalOnlyProtection(t *testing.T) {
	tempDir, _ := os.MkdirTemp("", "stiqrcloud_gw_dash_test_*")
	defer os.RemoveAll(tempDir)

	store, _ := storage.NewStore(tempDir)
	escrowMgr := escrow.NewManager("pi_server", 100)
	swarm := p2p.NewSwarm("pi_server", "http://192.168.1.50:8100", store)
	auditor := audit.NewCoordinator("pi_server", store, escrowMgr, swarm)

	srv := NewServer(Config{
		NodeID:     "pi_server",
		GatewayURL: "https://stiqrcloud.stiqrnotes.com",
		PublicAddr: "https://stiqrcloud.stiqrnotes.com",
		Store:      store,
		Swarm:      swarm,
		EscrowMgr:  escrowMgr,
		Auditor:    auditor,
		Standalone: false,
	})
	handler := srv.Handler()

	// 1. Local Request (from LAN 192.168.1.100) -> Should succeed (200 OK)
	reqLocal := httptest.NewRequest("GET", "http://192.168.1.50:8100/dashboard", nil)
	reqLocal.RemoteAddr = "192.168.1.100:54321"
	recLocal := httptest.NewRecorder()
	handler.ServeHTTP(recLocal, reqLocal)
	if recLocal.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for local LAN request to /dashboard, got %d", recLocal.Code)
	}

	// Local root request should redirect to /dashboard (302 Found)
	reqLocalRoot := httptest.NewRequest("GET", "http://192.168.1.50:8100/", nil)
	reqLocalRoot.RemoteAddr = "192.168.1.100:54321"
	recLocalRoot := httptest.NewRecorder()
	handler.ServeHTTP(recLocalRoot, reqLocalRoot)
	if recLocalRoot.Code != http.StatusFound {
		t.Fatalf("Expected 302 Found for local LAN request to /, got %d", recLocalRoot.Code)
	}

	// 2. Public Request via Cloudflare Tunnel (CF-Ray present) -> MUST BE 403 Forbidden!
	reqCF := httptest.NewRequest("GET", "/dashboard", nil)
	reqCF.RemoteAddr = "127.0.0.1:40000"
	reqCF.Header.Set("CF-Ray", "a4861070dcc7c64b-SLC")
	reqCF.Header.Set("CF-Connecting-IP", "198.51.100.42")
	recCF := httptest.NewRecorder()
	handler.ServeHTTP(recCF, reqCF)
	if recCF.Code != http.StatusForbidden {
		t.Fatalf("SECURITY VIOLATION: Expected 403 Forbidden for Cloudflare request to /dashboard, got %d", recCF.Code)
	}

	// Public root request must NOT redirect to /dashboard
	reqCFRoot := httptest.NewRequest("GET", "/", nil)
	reqCFRoot.RemoteAddr = "127.0.0.1:40000"
	reqCFRoot.Header.Set("CF-Ray", "a4861070dcc7c64b-SLC")
	recCFRoot := httptest.NewRecorder()
	handler.ServeHTTP(recCFRoot, reqCFRoot)
	if recCFRoot.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK public gateway info for /, got %d", recCFRoot.Code)
	}

	// Public request to admin escrow policy endpoint -> MUST BE 403 Forbidden!
	reqAdminCF := httptest.NewRequest("GET", "/api/escrow/policy", nil)
	reqAdminCF.RemoteAddr = "127.0.0.1:40000"
	reqAdminCF.Header.Set("CF-Ray", "a4861070dcc7c64b-SLC")
	recAdminCF := httptest.NewRecorder()
	handler.ServeHTTP(recAdminCF, reqAdminCF)
	if recAdminCF.Code != http.StatusForbidden {
		t.Fatalf("SECURITY VIOLATION: Expected 403 Forbidden for Cloudflare request to /api/escrow/policy, got %d", recAdminCF.Code)
	}

	// Public request to /api/health -> MUST REMAIN 200 OK!
	reqHealthCF := httptest.NewRequest("GET", "/api/health", nil)
	reqHealthCF.Header.Set("CF-Ray", "a4861070dcc7c64b-SLC")
	recHealthCF := httptest.NewRecorder()
	handler.ServeHTTP(recHealthCF, reqHealthCF)
	if recHealthCF.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for /api/health over Cloudflare, got %d", recHealthCF.Code)
	}

	// 3. Public Hostname in Host header (stiqrcloud.stiqrnotes.com) -> MUST BE 403 Forbidden!
	reqHostPub := httptest.NewRequest("GET", "/dashboard", nil)
	reqHostPub.Host = "stiqrcloud.stiqrnotes.com"
	reqHostPub.RemoteAddr = "192.168.1.100:54321"
	recHostPub := httptest.NewRecorder()
	handler.ServeHTTP(recHostPub, reqHostPub)
	if recHostPub.Code != http.StatusForbidden {
		t.Fatalf("SECURITY VIOLATION: Expected 403 Forbidden for Host: stiqrcloud.stiqrnotes.com, got %d", recHostPub.Code)
	}
}

func TestReticulumAPIEndpointsAndRecordReception(t *testing.T) {
	tempDir, _ := os.MkdirTemp("", "stiqrcloud_rns_gw_*")
	defer os.RemoveAll(tempDir)

	store, _ := storage.NewStore(tempDir)
	id, _ := reticulum.NewIdentity(reticulum.DefaultRNSAspect)
	rnsMgr := reticulum.NewInterfaceManager(id)
	defer rnsMgr.Stop()

	srv := NewServer(Config{
		NodeID:     "node_rns_test",
		Store:      store,
		RNSManager: rnsMgr,
		RNSDest:    id.Destination().String(),
	})
	handler := srv.Handler()

	// 1. Status endpoint
	reqStatus := httptest.NewRequest("GET", "/api/reticulum/status", nil)
	recStatus := httptest.NewRecorder()
	handler.ServeHTTP(recStatus, reqStatus)
	if recStatus.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for /api/reticulum/status, got %d", recStatus.Code)
	}

	// 2. Announce endpoint
	reqAnn := httptest.NewRequest("POST", "/api/reticulum/announce", nil)
	recAnn := httptest.NewRecorder()
	handler.ServeHTTP(recAnn, reqAnn)
	if recAnn.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for /api/reticulum/announce, got %d", recAnn.Code)
	}

	// 3. Directly simulate an incoming Reticulum packet (Context 0x01 = Record)
	testRec := &storage.Record{
		DataID:     "rec_direct_reticulum_999",
		Collection: "direct_notes",
		Text:       "Direct packet over Reticulum mesh without Cloudflare or Pi!",
		Version:    1,
	}
	recPayload, _ := json.Marshal(testRec)
	packet := &reticulum.Packet{
		HeaderFlags: 0x00,
		Hops:        0x00,
		Destination: id.Destination(),
		Context:     0x01,
		Payload:     recPayload,
	}

	// Fake incoming packet dispatch
	rnsMgr.BroadcastTCP(packet) // won't panic even with 0 clients

	// Directly invoke the store upsert check as triggered by onPacket
	stored, err := store.UpsertReplicatedRecord(testRec, nil, nil)
	if err != nil || stored == nil {
		t.Fatalf("Failed to store replicated record from Reticulum: %v", err)
	}

	retrieved, err := store.GetRecord("rec_direct_reticulum_999")
	if err != nil || retrieved == nil || retrieved.Text != testRec.Text {
		t.Fatalf("Record not found or text mismatch: got %v", retrieved)
	}
}

func TestStoragePoliciesAndUserControl(t *testing.T) {
	tempDir, _ := os.MkdirTemp("", "stiqrcloud_policy_*")
	defer os.RemoveAll(tempDir)

	store, _ := storage.NewStore(tempDir)
	escrowMgr := escrow.NewManager("node_local", 100)
	swarm := p2p.NewSwarm("node_local", "http://127.0.0.1:8100", store)
	auditor := audit.NewCoordinator("node_local", store, escrowMgr, swarm)

	srv := NewServer(Config{
		NodeID:     "node_local",
		Store:      store,
		Swarm:      swarm,
		EscrowMgr:  escrowMgr,
		Auditor:    auditor,
		Standalone: false,
	})
	handler := srv.Handler()

	// 1. Test "local" policy: record must stay exclusively on this node
	localPayload := `{"collection":"personal","text":"Keep this on my local node only","replication_policy":"local"}`
	req := httptest.NewRequest("POST", "/api/records", bytes.NewBufferString(localPayload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("Failed to create local record: status %d", rec.Code)
	}
	var localResp map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &localResp)
	localDataID := localResp["data_id"].(string)
	if sp, ok := localResp["storage_peers"].([]interface{}); ok && len(sp) > 0 {
		t.Fatalf("Expected 0 remote storage peers for local policy, got %v", sp)
	}

	// Verify that local record is NEVER returned via peer pull sync
	syncReq := httptest.NewRequest("GET", "/api/sync/records?peer_id=remote_node_bob", nil)
	syncRec := httptest.NewRecorder()
	handler.ServeHTTP(syncRec, syncReq)
	if syncRec.Code != http.StatusOK {
		t.Fatalf("Sync request failed: status %d", syncRec.Code)
	}
	var syncResp struct {
		Records []*storage.Record `json:"records"`
	}
	_ = json.Unmarshal(syncRec.Body.Bytes(), &syncResp)
	for _, r := range syncResp.Records {
		if r.DataID == localDataID {
			t.Fatalf("CRITICAL PRIVACY LEAK: Local-only record %s was returned in peer sync!", localDataID)
		}
	}

	// 2. Test "replicated_2" policy: data should be assigned to exactly 2 storage nodes
	rep2Payload := `{"collection":"docs","text":"Backup to 2 storage peers","replication_policy":"replicated_2"}`
	req = httptest.NewRequest("POST", "/api/records", bytes.NewBufferString(rep2Payload))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("Failed to create replicated_2 record: status %d", rec.Code)
	}
	var rep2Resp map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &rep2Resp)
	sp2, ok := rep2Resp["storage_peers"].([]interface{})
	if !ok || len(sp2) != 2 {
		t.Fatalf("Expected exactly 2 storage peers for replicated_2, got %v", sp2)
	}

	// 3. Test "custom" policy: user explicitly dictates where data goes
	customPayload := `{"collection":"docs","text":"Strict target routing","replication_policy":"custom","storage_peers":["user_home_nas","friend_pc_node"]}`
	req = httptest.NewRequest("POST", "/api/records", bytes.NewBufferString(customPayload))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("Failed to create custom routed record: status %d", rec.Code)
	}
	var customResp map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &customResp)
	spCustom, ok := customResp["storage_peers"].([]interface{})
	if !ok || len(spCustom) != 2 || spCustom[0] != "user_home_nas" || spCustom[1] != "friend_pc_node" {
		t.Fatalf("Expected custom peers ['user_home_nas', 'friend_pc_node'], got %v", spCustom)
	}

	// 4. Test PocketBase endpoint with local replication policy
	pbPayload := `{"text":"PocketBase local note","replication_policy":"local"}`
	req = httptest.NewRequest("POST", "/api/collections/notes/records", bytes.NewBufferString(pbPayload))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Failed to create PB record with local policy: status %d", rec.Code)
	}
	var pbResp map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &pbResp)
	if pbResp["replication_policy"] != "local" {
		t.Fatalf("Expected PB replication_policy 'local', got %v", pbResp["replication_policy"])
	}
	if sp, ok := pbResp["storage_peers"].([]interface{}); ok && len(sp) > 0 {
		t.Fatalf("Expected 0 storage peers for PB local policy, got %v", sp)
	}
}

func TestGatewaySecurityHardening(t *testing.T) {
	tempDir, _ := os.MkdirTemp("", "stiqrcloud_sec_*")
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

	// 1. Arbitrary file read / source code leak via /download/ MUST BE BLOCKED
	req := httptest.NewRequest("GET", "/download/pkg/gateway/server.go", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden && rec.Code != http.StatusBadRequest {
		t.Fatalf("Expected /download/ of source code to be blocked with 403 or 400, got %d", rec.Code)
	}

	// 2. Arbitrary non-release files or traversal names via /download/ MUST BE BLOCKED
	req = httptest.NewRequest("GET", "/download/passwd", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden && rec.Code != http.StatusBadRequest {
		t.Fatalf("Expected non-release /download/ to be blocked, got %d", rec.Code)
	}

	req = httptest.NewRequest("GET", "/download/stiqrcloud..foo", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden && rec.Code != http.StatusBadRequest {
		t.Fatalf("Expected dot-dot /download/ to be blocked, got %d", rec.Code)
	}

	// 3. Host header command injection in /install.sh MUST BE SANITIZED
	req = httptest.NewRequest("GET", "/install.sh", nil)
	req.Host = "example.com\"; rm -rf /; echo \""
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Failed to retrieve install.sh: %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "rm -rf") {
		t.Fatalf("CRITICAL: Host header command injection succeeded in install.sh!")
	}
	if !strings.Contains(body, "127.0.0.1:8100") {
		t.Fatalf("Expected sanitized fallback host 127.0.0.1:8100 in install.sh")
	}

	// 4. Admin CORS Protection: Cross-origin browser requests to /api/escrow/policy MUST BE FORBIDDEN
	req = httptest.NewRequest("GET", "/api/escrow/policy", nil)
	req.Header.Set("Origin", "http://evil-attacker-site.com")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("Expected cross-origin admin request to be rejected with 403 Forbidden, got %d", rec.Code)
	}

	// 5. Unauthenticated bulk sync dump (?all=true) from external WAN MUST BE FORBIDDEN
	req = httptest.NewRequest("GET", "/api/sync/records?all=true", nil)
	req.RemoteAddr = "203.0.113.195:12345"
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("Expected public WAN bulk sync to be rejected with 403 Forbidden, got %d", rec.Code)
	}
}

