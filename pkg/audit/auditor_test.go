package audit

import (
	"os"
	"testing"
	"time"

	"github.com/stiqrhub/testcloud/pkg/crypto"
	"github.com/stiqrhub/testcloud/pkg/escrow"
	"github.com/stiqrhub/testcloud/pkg/p2p"
	"github.com/stiqrhub/testcloud/pkg/storage"
)

func TestAuditorChallengeVerificationAndFailover(t *testing.T) {
	tempDir, _ := os.MkdirTemp("", "testcloud_audit_*")
	defer os.RemoveAll(tempDir)

	store, _ := storage.NewStore(tempDir)
	escrowMgr := escrow.NewManager("auditor_1", 100)
	swarm := p2p.NewSwarm("auditor_1", "http://127.0.0.1:8100", store)

	// Add peers
	swarm.RegisterPeer(&p2p.Peer{NodeID: "storage_node_a", IsPublic: true, LastSeen: time.Now()})
	swarm.RegisterPeer(&p2p.Peer{NodeID: "storage_node_b", IsPublic: true, LastSeen: time.Now()})
	swarm.RegisterPeer(&p2p.Peer{NodeID: "storage_node_c", IsPublic: true, LastSeen: time.Now()})
	swarm.RegisterPeer(&p2p.Peer{NodeID: "storage_node_spare", IsPublic: true, LastSeen: time.Now()})

	coord := NewCoordinator("auditor_1", store, escrowMgr, swarm)

	// Create test contract & record
	chunkData := []byte("audit payload chunk test")
	chunkHash, _ := store.PutChunk(chunkData)

	rec := &storage.Record{
		DataID:        "doc_audit_test",
		FileChunkHash: chunkHash,
		StoragePeers:  []string{"storage_node_a", "storage_node_b", "storage_node_c"},
	}

	contract, _ := escrowMgr.CreateContract(rec.DataID, 1024*1024, 1, map[string]int64{
		"storage_node_a": 100,
		"storage_node_b": 100,
		"storage_node_c": 100,
	}, []string{"auditor_1"})
	_, _ = escrowMgr.SettlePayment(contract.ContractID)

	// 1. Challenge creation
	ch, err := coord.CreateChallenge(contract, rec, "storage_node_a")
	if err != nil {
		t.Fatalf("CreateChallenge failed: %v", err)
	}

	// 2. Storage node computes response
	answer := crypto.ComputeAuditResponse(chunkData, ch.Nonce)

	// 3. Auditor verifies
	res, err := coord.VerifyChallengeResponse(ch.ChallengeID, answer, chunkData, 42.5)
	if err != nil || !res.Passed {
		t.Fatalf("VerifyChallengeResponse failed: %v, passed=%v", err, res.Passed)
	}

	// 4. Test self-healing failover when node drops offline
	repID, err := coord.TriggerSelfHealingReplication(contract.ContractID, "storage_node_b", rec)
	if err != nil {
		t.Fatalf("TriggerSelfHealingReplication failed: %v", err)
	}

	if repID == "" || rec.StoragePeers[1] != repID {
		t.Fatalf("Storage peer was not replaced: %+v", rec.StoragePeers)
	}

	// Verify invariant DataID remained untouched through failover
	if rec.DataID != "doc_audit_test" {
		t.Fatalf("Data ID was corrupted during failover")
	}
}
