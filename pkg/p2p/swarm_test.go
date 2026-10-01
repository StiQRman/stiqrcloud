package p2p

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stiqrhub/testcloud/pkg/storage"
)

func TestSwarmSocialScoreAndQuorumSelection(t *testing.T) {
	tempDir, _ := os.MkdirTemp("", "testcloud_swarm_*")
	defer os.RemoveAll(tempDir)
	store, _ := storage.NewStore(tempDir)

	swarm := NewSwarm("local_auditor", "http://127.0.0.1:8100", store)

	// Register 6 mock peers
	for i := 1; i <= 6; i++ {
		p := &Peer{
			NodeID:          fmt.Sprintf("peer_%d", i),
			PublicAddr:      fmt.Sprintf("http://127.0.0.1:810%d", i),
			IsPublic:        true,
			PricePerGBMonth: int64(100 + i*10),
			AuditsPassed:    int64(i * 3),
			AuditsPerformed: int64(i * 2),
			AvgLatencyMs:    float64(100 - i*10), // peer 6 has 40ms (<50ms -> 20pts)
			LastSeen:        time.Now(),
		}
		swarm.RegisterPeer(p)
	}

	peers := swarm.ListPeers()
	if len(peers) != 6 {
		t.Fatalf("Expected 6 peers, got %d", len(peers))
	}

	// Peer 6 should have highest social score
	if peers[0].NodeID != "peer_6" {
		t.Fatalf("Expected peer_6 to lead leaderboard, got %s (score %.2f)", peers[0].NodeID, peers[0].SocialScore)
	}

	// Select Quorum (3 auditors including local, 3 storage nodes)
	auditors, storageNodes, err := swarm.SelectQuorum()
	if err != nil {
		t.Fatalf("SelectQuorum failed: %v", err)
	}

	if len(auditors) != 3 {
		t.Fatalf("Expected exactly 3 auditors, got %d", len(auditors))
	}
	if len(storageNodes) != 3 {
		t.Fatalf("Expected exactly 3 storage nodes, got %d", len(storageNodes))
	}
	if auditors[0] != "local_auditor" {
		t.Fatalf("1st Auditor must be local node, got %s", auditors[0])
	}
}

func TestUniversalDataIDRouting(t *testing.T) {
	// Setup a mock remote peer server holding record "rec_remote_999"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/records/rec_remote_999" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"data_id":"rec_remote_999","text":"Content from remote node","version":1}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	tempDir, _ := os.MkdirTemp("", "testcloud_routing_*")
	defer os.RemoveAll(tempDir)
	store, _ := storage.NewStore(tempDir)

	swarm := NewSwarm("local_node", "http://127.0.0.1:8100", store)
	swarm.RegisterPeer(&Peer{
		NodeID:     "remote_peer_1",
		PublicAddr: server.URL,
		IsPublic:   true,
		LastSeen:   time.Now(),
	})

	// Local store does NOT have rec_remote_999, so swarm resolves across network
	rec, err := swarm.ResolveDataID("rec_remote_999")
	if err != nil {
		t.Fatalf("ResolveDataID failed to query peer: %v", err)
	}

	if rec.DataID != "rec_remote_999" || rec.Text != "Content from remote node" {
		t.Fatalf("Unexpected resolved record data: %+v", rec)
	}
}
