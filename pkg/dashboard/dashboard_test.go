package dashboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stiqrhub/stiqrcloud/pkg/audit"
	"github.com/stiqrhub/stiqrcloud/pkg/escrow"
	"github.com/stiqrhub/stiqrcloud/pkg/p2p"
	"github.com/stiqrhub/stiqrcloud/pkg/storage"
)

func TestPocketBaseStyleDashboardEndpoints(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "stiqrcloud_dash_test_*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	store, err := storage.NewStore(tempDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	escrowMgr := escrow.NewManager("dash_test_node", 100)
	swarm := p2p.NewSwarm("dash_test_node", "http://127.0.0.1:8100", store)
	auditor := audit.NewCoordinator("dash_test_node", store, escrowMgr, swarm)

	dashServer := NewServer(
		"dash_test_node",
		"Test Operator",
		"Test Lab",
		"127.0.0.1",
		store,
		swarm,
		escrowMgr,
		auditor,
		nil,
		"test_dest_1234",
		false,
	)

	mux := http.NewServeMux()
	dashServer.RegisterRoutes(mux)

	// 1. Test Dashboard HTML rendering
	req := httptest.NewRequest("GET", "/dashboard", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Dashboard HTML failed: %d", rec.Code)
	}
	html := rec.Body.String()
	if !strings.Contains(html, "Collections") || !strings.Contains(html, "Fields:") {
		t.Fatalf("Expected dashboard HTML to contain Collections and Fields bar")
	}

	// 2. Test GET /api/dashboard/collections
	req = httptest.NewRequest("GET", "/api/dashboard/collections", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Get collections failed: %d", rec.Code)
	}

	var colResp struct {
		Collections []storage.CollectionInfo `json:"collections"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &colResp); err != nil {
		t.Fatalf("Failed to parse collections JSON: %v", err)
	}

	if len(colResp.Collections) < 2 {
		t.Fatalf("Expected default collections (files, notes), got %d", len(colResp.Collections))
	}

	// Verify fields exist in collections (PocketBase concept)
	hasFields := false
	for _, col := range colResp.Collections {
		if len(col.Fields) > 0 {
			hasFields = true
			break
		}
	}
	if !hasFields {
		t.Fatalf("Expected collections to contain schema fields")
	}

	// 3. Test POST /api/dashboard/collections (Create new collection)
	createColBody := `{"name":"invoices"}`
	req = httptest.NewRequest("POST", "/api/dashboard/collections", strings.NewReader(createColBody))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Create collection failed: %d", rec.Code)
	}

	// Verify newly created collection is listed
	req = httptest.NewRequest("GET", "/api/dashboard/collections", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	var updatedCols struct {
		Collections []storage.CollectionInfo `json:"collections"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &updatedCols)

	foundInvoiceCol := false
	for _, c := range updatedCols.Collections {
		if c.Name == "invoices" {
			foundInvoiceCol = true
			break
		}
	}
	if !foundInvoiceCol {
		t.Fatalf("Expected created collection 'invoices' to be listed")
	}

	// 4. Create a record in 'invoices' and verify filtered GET /api/dashboard/records?collection=invoices
	recToCreate := &storage.Record{
		Collection: "invoices",
		Text:       "Invoice #1001 for 500 sats",
	}
	_, err = store.CreateRecord(recToCreate, nil, nil)
	if err != nil {
		t.Fatalf("CreateRecord failed: %v", err)
	}

	req = httptest.NewRequest("GET", "/api/dashboard/records?collection=invoices", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Get collection records failed: %d", rec.Code)
	}

	var recsResp struct {
		Collection string            `json:"collection"`
		Records    []*storage.Record `json:"records"`
		Total      int               `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &recsResp); err != nil {
		t.Fatalf("Failed to decode records response: %v", err)
	}
	if recsResp.Collection != "invoices" || len(recsResp.Records) != 1 {
		t.Fatalf("Expected 1 record in 'invoices', got %d", len(recsResp.Records))
	}

	// 5. Verify stats
	req = httptest.NewRequest("GET", "/api/dashboard/stats", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Get stats failed: %d", rec.Code)
	}
}
