package audit

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stiqrhub/stiqrcloud/pkg/crypto"
	"github.com/stiqrhub/stiqrcloud/pkg/escrow"
	"github.com/stiqrhub/stiqrcloud/pkg/p2p"
	"github.com/stiqrhub/stiqrcloud/pkg/storage"
)

func TestAuditorChallengeVerificationAndFailover(t *testing.T) {
	tempDir, _ := os.MkdirTemp("", "stiqrcloud_audit_*")
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

	contract, _ := escrowMgr.CreateContract(rec.DataID, "owner_test_pub", 1024*1024, 1, map[string]int64{
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

func TestBatchedAuditCycleAndUserAffinityHealing(t *testing.T) {
	tempDir, _ := os.MkdirTemp("", "stiqrcloud_batched_audit_*")
	defer os.RemoveAll(tempDir)

	store, _ := storage.NewStore(tempDir)
	escrowMgr := escrow.NewManager("auditor_1", 100)
	swarm := p2p.NewSwarm("auditor_1", "http://127.0.0.1:8100", store)

	// Add 5 peers
	swarm.RegisterPeer(&p2p.Peer{NodeID: "node_1", PublicAddr: "http://127.0.0.1:8101", IsPublic: true, IsOnline: true, LastSeen: time.Now()})
	swarm.RegisterPeer(&p2p.Peer{NodeID: "node_2", PublicAddr: "http://127.0.0.1:8102", IsPublic: true, IsOnline: true, LastSeen: time.Now()})
	swarm.RegisterPeer(&p2p.Peer{NodeID: "node_3", PublicAddr: "http://127.0.0.1:8103", IsPublic: true, IsOnline: true, LastSeen: time.Now()})
	swarm.RegisterPeer(&p2p.Peer{NodeID: "node_spare1", PublicAddr: "http://127.0.0.1:8104", IsPublic: true, IsOnline: true, LastSeen: time.Now()})
	swarm.RegisterPeer(&p2p.Peer{NodeID: "node_spare2", PublicAddr: "http://127.0.0.1:8105", IsPublic: true, IsOnline: true, LastSeen: time.Now()})

	coord := NewCoordinator("auditor_1", store, escrowMgr, swarm)

	userPub := "02aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	var contractIDs []string
	// Create 3 records all owned by same user and stored on node_1, node_2
	for i := 1; i <= 3; i++ {
		rec := &storage.Record{
			DataID:         fmt.Sprintf("user_rec_%d", i),
			UserPubAddress: userPub,
			StoragePeers:   []string{"node_1", "node_2"},
		}
		_ = store.SaveRecord(rec)
		c, _ := escrowMgr.CreateContract(rec.DataID, userPub, 1024, 1, map[string]int64{
			"node_1": 100,
			"node_2": 100,
		}, []string{"auditor_1"})
		_, _ = escrowMgr.SettlePayment(c.ContractID)
		contractIDs = append(contractIDs, c.ContractID)
	}

	// Run audit cycle - node probes should be batched and executed
	results := coord.RunAuditCycle()
	if len(results) != 6 { // 3 contracts x 2 storage peers = 6 audit evaluations
		t.Fatalf("Expected 6 audit evaluations, got %d", len(results))
	}

	// Verify failover uses affinity ranking for this user
	rec1, _ := store.GetRecord("user_rec_1")
	repID, err := coord.TriggerSelfHealingReplication(contractIDs[0], "node_1", rec1)
	if err != nil {
		t.Fatalf("TriggerSelfHealingReplication failed: %v", err)
	}

	if repID == "" || repID == "node_1" {
		t.Fatalf("Invalid replacement node ID: %s", repID)
	}

	// Verify another record of the same user also fails over to the same replacement node!
	rec2, _ := store.GetRecord("user_rec_2")
	repID2, err := coord.TriggerSelfHealingReplication(contractIDs[1], "node_1", rec2)
	if err != nil {
		t.Fatalf("TriggerSelfHealingReplication record 2 failed: %v", err)
	}

	if repID2 != repID {
		t.Fatalf("User affinity pod failed: rec1 failed over to %s but rec2 failed over to %s", repID, repID2)
	}
}
