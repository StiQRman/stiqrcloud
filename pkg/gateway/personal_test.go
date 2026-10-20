package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stiqrhub/stiqrcloud/pkg/audit"
	"github.com/stiqrhub/stiqrcloud/pkg/escrow"
	"github.com/stiqrhub/stiqrcloud/pkg/p2p"
	"github.com/stiqrhub/stiqrcloud/pkg/storage"
)

func setupTestServer(t *testing.T) (*Server, func()) {
	tempDir, err := os.MkdirTemp("", "stiqrcloud_gw_personal_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	store, err := storage.NewStore(tempDir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	swarm := p2p.NewSwarm("test-node-1", "http://127.0.0.1:8100", store)
	escrowMgr := escrow.NewManager("test-node-1", 100, tempDir)
	auditor := audit.NewCoordinator("test-node-1", store, escrowMgr, swarm)

	srv := NewServer(Config{
		NodeID:     "test-node-1",
		GatewayURL: "http://127.0.0.1:8100",
		Store:      store,
		Swarm:      swarm,
		EscrowMgr:  escrowMgr,
		Auditor:    auditor,
		RNSDest:    "<13a191bdf8e9c71904e380b06112d88c>",
		IsPublic:   true,
	})

	cleanup := func() {
		os.RemoveAll(tempDir)
	}

	return srv, cleanup
}

func TestPersonalGatewayEndpoints(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()

	// 1. Add Personal Node
	pairPayload := map[string]string{
		"reticulum_dest": "<a5e2992b3adcad7808a44c944f871660>",
		"name":           "Living Room Pi",
		"local_addr":     "http://10.0.0.2:8100",
	}
	bodyBytes, _ := json.Marshal(pairPayload)
	req := httptest.NewRequest("POST", "/api/personal/nodes", bytes.NewReader(bodyBytes))
	req.Host = "127.0.0.1:8100"
	req.RemoteAddr = "127.0.0.1:54321"
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from POST /api/personal/nodes, got %d: %s", rec.Code, rec.Body.String())
	}

	// 2. List Personal Nodes
	req = httptest.NewRequest("GET", "/api/personal/nodes", nil)
	req.Host = "127.0.0.1:8100"
	req.RemoteAddr = "127.0.0.1:54321"
	rec = httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from GET /api/personal/nodes, got %d", rec.Code)
	}
	var listResp struct {
		Nodes []*p2p.PersonalNode `json:"nodes"`
		Total int                 `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil || listResp.Total != 1 {
		t.Fatalf("expected 1 node in response, got %d (err: %v)", listResp.Total, err)
	}
	if listResp.Nodes[0].Name != "Living Room Pi" {
		t.Fatalf("expected 'Living Room Pi', got %s", listResp.Nodes[0].Name)
	}

	// 3. Storage Split Config
	splitPayload := storage.StorageSplitConfig{
		PersonalQuotaBytes: 40 * 1024 * 1024 * 1024,
		PublicQuotaBytes:   10 * 1024 * 1024 * 1024,
		PublicEnabled:      true,
		PublicPriceSats:    50,
	}
	splitBytes, _ := json.Marshal(splitPayload)
	req = httptest.NewRequest("POST", "/api/personal/storage-split", bytes.NewReader(splitBytes))
	req.Host = "127.0.0.1:8100"
	req.RemoteAddr = "127.0.0.1:54321"
	rec = httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from POST /api/personal/storage-split, got %d", rec.Code)
	}

	// 4. Airgap Prep
	req = httptest.NewRequest("POST", "/api/personal/airgap/prep", nil)
	req.Host = "127.0.0.1:8100"
	req.RemoteAddr = "127.0.0.1:54321"
	rec = httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from POST /api/personal/airgap/prep, got %d", rec.Code)
	}
	var prepResp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &prepResp); err != nil {
		t.Fatalf("invalid json from airgap prep: %v", err)
	}
	if safe, ok := prepResp["safe_to_disconnect"].(bool); !ok || !safe {
		t.Fatalf("expected safe_to_disconnect true, got %v", prepResp)
	}

	// 5. Run Personal Parity Audits
	req = httptest.NewRequest("POST", "/api/personal/audits/run", nil)
	req.Host = "127.0.0.1:8100"
	req.RemoteAddr = "127.0.0.1:54321"
	rec = httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from POST /api/personal/audits/run, got %d", rec.Code)
	}

	// 6. Test Public Hosting Disabled Restriction
	// Toggle public hosting off
	splitPayload.PublicEnabled = false
	splitBytes, _ = json.Marshal(splitPayload)
	req = httptest.NewRequest("POST", "/api/personal/storage-split", bytes.NewReader(splitBytes))
	req.Host = "127.0.0.1:8100"
	req.RemoteAddr = "127.0.0.1:54321"
	rec = httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	// Now a public tenant from non-LAN IP attempts to create a record:
	recPayload := map[string]string{
		"collection": "notes",
		"text":       "public tenant data",
	}
	recBytes, _ := json.Marshal(recPayload)
	req = httptest.NewRequest("POST", "/api/records", bytes.NewReader(recBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Host = "stiqrcloud.stiqrnotes.com"
	req.RemoteAddr = "93.184.216.34:54321" // Public WAN IP
	rec = httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden when public hosting is disabled, got %d", rec.Code)
	}

	// But a local/personal request can still create records!
	reqLocal := httptest.NewRequest("POST", "/api/records", bytes.NewReader(recBytes))
	reqLocal.Header.Set("Content-Type", "application/json")
	reqLocal.Host = "127.0.0.1:8100"
	reqLocal.RemoteAddr = "127.0.0.1:54321" // Local IP
	recLocal := httptest.NewRecorder()
	srv.mux.ServeHTTP(recLocal, reqLocal)

	if recLocal.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for local owner record, got %d: %s", recLocal.Code, recLocal.Body.String())
	}
}
