package p2p

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stiqrhub/stiqrcloud/pkg/storage"
)

func TestSwarmSocialScoreAndQuorumSelection(t *testing.T) {
	tempDir, _ := os.MkdirTemp("", "stiqrcloud_swarm_*")
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

	tempDir, _ := os.MkdirTemp("", "stiqrcloud_routing_*")
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

func TestSelectQuorumForUserAffinityAndAdaptation(t *testing.T) {
	tempDir, _ := os.MkdirTemp("", "stiqrcloud_affinity_*")
	defer os.RemoveAll(tempDir)
	store, _ := storage.NewStore(tempDir)

	swarm := NewSwarm("local_auditor", "http://127.0.0.1:8100", store)

	// Register 8 mock peers
	for i := 1; i <= 8; i++ {
		p := &Peer{
			NodeID:          fmt.Sprintf("peer_%d", i),
			PublicAddr:      fmt.Sprintf("http://127.0.0.1:810%d", i),
			IsPublic:        true,
			IsOnline:        true,
			PricePerGBMonth: 100,
			SocialScore:     80.0,
			LastSeen:        time.Now(),
		}
		swarm.RegisterPeer(p)
	}

	userAlice := "02aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	userBob := "03bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	// 1. Conservative Bias: Verify Alice's storage nodes are 100% deterministic and sticky across multiple calls
	_, storage1, err := swarm.SelectQuorumForUser(userAlice, 3, 3)
	if err != nil {
		t.Fatalf("SelectQuorumForUser failed: %v", err)
	}
	if len(storage1) != 3 {
		t.Fatalf("Expected 3 storage nodes, got %d", len(storage1))
	}

	for i := 0; i < 20; i++ {
		_, storageRetry, err := swarm.SelectQuorumForUser(userAlice, 3, 3)
		if err != nil {
			t.Fatalf("SelectQuorumForUser iteration %d failed: %v", i, err)
		}
		for j := range storage1 {
			if storageRetry[j] != storage1[j] {
				t.Fatalf("Sticky affinity violated: run %d got %v, expected %v", i, storageRetry, storage1)
			}
		}
	}

	// 2. Different user gets a deterministic pod
	_, storageBob, err := swarm.SelectQuorumForUser(userBob, 3, 3)
	if err != nil {
		t.Fatalf("SelectQuorumForUser Bob failed: %v", err)
	}
	if len(storageBob) != 3 {
		t.Fatalf("Expected 3 storage nodes for Bob, got %d", len(storageBob))
	}

	// 3. Fast Adaptation: Take one of Alice's storage nodes offline
	failedNode := storage1[0]
	p, ok := swarm.GetPeer(failedNode)
	if !ok {
		t.Fatalf("Failed to get peer %s", failedNode)
	}
	p.IsOnline = false

	_, storageAdapted, err := swarm.SelectQuorumForUser(userAlice, 3, 3)
	if err != nil {
		t.Fatalf("Adaptation call failed: %v", err)
	}

	// Verify failed node was immediately evicted
	for _, node := range storageAdapted {
		if node == failedNode {
			t.Fatalf("Failed node %s still present in adapted storage nodes: %v", failedNode, storageAdapted)
		}
	}

	// Verify the remaining 2 healthy affinity nodes were retained
	retainedCount := 0
	for _, node := range storageAdapted {
		if node == storage1[1] || node == storage1[2] {
			retainedCount++
		}
	}
	if retainedCount != 2 {
		t.Fatalf("Expected the 2 surviving affinity nodes to be preserved, got %v", storageAdapted)
	}

	// 4. Recovery: Bring node back online
	p.IsOnline = true
	p.LastSeen = time.Now()
	_, storageRecovered, err := swarm.SelectQuorumForUser(userAlice, 3, 3)
	if err != nil {
		t.Fatalf("Recovery call failed: %v", err)
	}
	foundRecovered := false
	for _, node := range storageRecovered {
		if node == failedNode {
			foundRecovered = true
			break
		}
	}
	if !foundRecovered {
		t.Fatalf("Expected recovered node %s to resume affinity spot, got %v", failedNode, storageRecovered)
	}
}
