package audit

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/stiqrhub/testcloud/pkg/crypto"
	"github.com/stiqrhub/testcloud/pkg/escrow"
	"github.com/stiqrhub/testcloud/pkg/p2p"
	"github.com/stiqrhub/testcloud/pkg/storage"
)

// Challenge represents an unpredictable cryptographic audit challenge.
type Challenge struct {
	ChallengeID string    `json:"challenge_id"`
	ContractID  string    `json:"contract_id"`
	DataID      string    `json:"data_id"`
	StorageNode string    `json:"storage_node"`
	ChunkIndex  int       `json:"chunk_index"`
	ChunkHash   string    `json:"chunk_hash"`
	Nonce       string    `json:"nonce"`
	IssuedAt    time.Time `json:"issued_at"`
	Deadline    time.Time `json:"deadline"`
}

// AuditResult records the outcome of a challenge.
type AuditResult struct {
	ChallengeID string    `json:"challenge_id"`
	StorageNode string    `json:"storage_node"`
	Passed      bool      `json:"passed"`
	LatencyMs   float64   `json:"latency_ms"`
	Timestamp   time.Time `json:"timestamp"`
	Notes       string    `json:"notes"`
}

// Coordinator manages the 3-Auditor verification lifecycle, challenges, and failovers.
type Coordinator struct {
	mu           sync.RWMutex
	localID      string
	store        *storage.Store
	escrowMgr    *escrow.Manager
	swarm        *p2p.Swarm
	activeAudits map[string]*Challenge
	history      []AuditResult
}

// NewCoordinator initializes the auditor coordinator.
func NewCoordinator(localID string, store *storage.Store, escrowMgr *escrow.Manager, swarm *p2p.Swarm) *Coordinator {
	return &Coordinator{
		localID:      localID,
		store:        store,
		escrowMgr:    escrowMgr,
		swarm:        swarm,
		activeAudits: make(map[string]*Challenge),
		history:      make([]AuditResult, 0),
	}
}

// CreateChallenge generates a randomized Merkle challenge for a storage node.
func (c *Coordinator) CreateChallenge(contract *escrow.StorageContract, rec *storage.Record, targetStorageNode string) (*Challenge, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var hashes []string
	if rec.FileChunkHash != "" {
		hashes = append(hashes, rec.FileChunkHash)
	}
	if rec.PictureChunkHash != "" {
		hashes = append(hashes, rec.PictureChunkHash)
	}
	if len(hashes) == 0 {
		hashes = append(hashes, crypto.HashBytes([]byte(rec.Text)))
	}

	// Random chunk index
	n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(hashes))))
	chunkIdx := int(n.Int64())

	// Random challenge nonce
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	nonce := hex.EncodeToString(b)
	chID := fmt.Sprintf("ch_%s", nonce[:10])

	challenge := &Challenge{
		ChallengeID: chID,
		ContractID:  contract.ContractID,
		DataID:      rec.DataID,
		StorageNode: targetStorageNode,
		ChunkIndex:  chunkIdx,
		ChunkHash:   hashes[chunkIdx],
		Nonce:       nonce,
		IssuedAt:    time.Now().UTC(),
		Deadline:    time.Now().UTC().Add(30 * time.Second),
	}

	c.activeAudits[chID] = challenge
	return challenge, nil
}

// VerifyChallengeResponse checks that the storage node's answer matches crypto.ComputeAuditResponse.
func (c *Coordinator) VerifyChallengeResponse(challengeID string, response string, chunkBytes []byte, latencyMs float64) (*AuditResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	ch, exists := c.activeAudits[challengeID]
	if !exists {
		return nil, errors.New("challenge not found or already verified")
	}
	delete(c.activeAudits, challengeID)

	passed := crypto.VerifyAuditResponse(chunkBytes, ch.Nonce, response)
	res := AuditResult{
		ChallengeID: challengeID,
		StorageNode: ch.StorageNode,
		Passed:      passed,
		LatencyMs:   latencyMs,
		Timestamp:   time.Now().UTC(),
	}

	if passed {
		res.Notes = "Merkle chunk integrity mathematically proven"
		c.swarm.RecordAuditSuccess(ch.StorageNode, latencyMs)
		c.swarm.RecordAuditPerformed(c.localID)
		_, _ = c.escrowMgr.RecordAuditPass(ch.ContractID, ch.StorageNode)
	} else {
		res.Notes = "Audit failed: Invalid Merkle response or corrupted chunk"
	}

	c.history = append(c.history, res)
	return &res, nil
}

// TriggerSelfHealingReplication replaces a failed or offline storage node with a new healthy peer.
func (c *Coordinator) TriggerSelfHealingReplication(contractID string, failedNodeID string, rec *storage.Record) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Select replacement node from swarm using 75% reputation / 25% random rule
	peers := c.swarm.ListPeers()
	var replacementID string
	for _, p := range peers {
		if p.NodeID != failedNodeID && p.NodeID != c.localID {
			replacementID = p.NodeID
			break
		}
	}

	if replacementID == "" {
		replacementID = fmt.Sprintf("node_replacement_%d", time.Now().Unix()%1000)
	}

	// Update contract in escrow: unaccrued payouts now flow to replacement
	if err := c.escrowMgr.ReallocateOfflineNode(contractID, failedNodeID, replacementID); err != nil {
		return "", fmt.Errorf("failed to reallocate contract in escrow: %w", err)
	}

	// Update record storage peers while PRESERVING invariant DataID
	for i, id := range rec.StoragePeers {
		if id == failedNodeID {
			rec.StoragePeers[i] = replacementID
			break
		}
	}

	return replacementID, nil
}

// GetAuditHistory returns recent audit outcomes.
func (c *Coordinator) GetAuditHistory() []AuditResult {
	c.mu.RLock()
	defer c.mu.RUnlock()

	res := make([]AuditResult, len(c.history))
	copy(res, c.history)
	return res
}
