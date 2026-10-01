package escrow

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

// StorageContract models the time-locked storage agreement funded via Lightning.
type StorageContract struct {
	ContractID       string            `json:"contract_id"`
	DataID           string            `json:"data_id"`
	TotalBytes       int64             `json:"total_bytes"`
	DurationMonths   int               `json:"duration_months"`
	TotalSats        int64             `json:"total_sats"`
	PaidSats         int64             `json:"paid_sats"`
	LightningInvoice string            `json:"lightning_invoice"`
	PaymentHash      string            `json:"payment_hash"`
	IsSettled        bool              `json:"is_settled"`
	StorageNodes     map[string]int64  `json:"storage_nodes"` // NodeID -> allocated sats
	DisbursedSats    map[string]int64  `json:"disbursed_sats"` // NodeID -> already disbursed sats
	AuditorNodes     []string          `json:"auditor_nodes"`
	CreatedAt        time.Time         `json:"created_at"`
	ExpiresAt        time.Time         `json:"expires_at"`
	LastAuditedAt    map[string]time.Time `json:"last_audited_at"`
}

// Manager coordinates contract creation, Lightning invoice generation, and payouts.
type Manager struct {
	mu           sync.RWMutex
	nodeID       string
	pricePerGB   int64 // sats per GB per month for this node
	contracts    map[string]*StorageContract
	lightningURL string // Optional LNbits / node URL
}

// NewManager initializes the escrow manager.
func NewManager(nodeID string, pricePerGB int64) *Manager {
	if pricePerGB <= 0 {
		pricePerGB = 100 // default 100 sats / GB / month
	}
	return &Manager{
		nodeID:     nodeID,
		pricePerGB: pricePerGB,
		contracts:  make(map[string]*StorageContract),
	}
}

// CalculatePrice calculates total sats for 3 storage nodes across contract duration.
func CalculatePrice(bytes int64, months int, storageRates []int64) int64 {
	if months <= 0 {
		months = 1
	}

	var sumRates int64
	for _, r := range storageRates {
		sumRates += r
	}
	if sumRates <= 0 {
		sumRates = 300 // default 100 sats * 3 nodes
	}

	gb := float64(bytes) / (1024 * 1024 * 1024)
	if gb < 0.001 {
		gb = 0.001 // minimum 1 MB base pricing
	}

	total := int64(gb * float64(sumRates) * float64(months))
	if total < 1 {
		total = 1 // minimum 1 sat
	}
	return total
}

// CreateContract generates a contract with a simulated or live Lightning invoice.
func (m *Manager) CreateContract(dataID string, bytes int64, months int, storageNodeRates map[string]int64, auditors []string) (*StorageContract, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var rates []int64
	storageMap := make(map[string]int64)
	for id, rate := range storageNodeRates {
		rates = append(rates, rate)
		storageMap[id] = 0 // Will split total
	}

	totalSats := CalculatePrice(bytes, months, rates)

	// Generate deterministic mock lightning invoice
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	hash := hex.EncodeToString(b)
	invoice := fmt.Sprintf("lnbc%du1p%s", totalSats*10, hash[:20])

	cID := fmt.Sprintf("ctr_%s", hash[:12])
	now := time.Now().UTC()

	// Split payout evenly across storage nodes
	perNode := totalSats / int64(len(storageNodeRates))
	for id := range storageNodeRates {
		storageMap[id] = perNode
	}

	c := &StorageContract{
		ContractID:       cID,
		DataID:           dataID,
		TotalBytes:       bytes,
		DurationMonths:   months,
		TotalSats:        totalSats,
		PaidSats:         0,
		LightningInvoice: invoice,
		PaymentHash:      hash,
		IsSettled:        false,
		StorageNodes:     storageMap,
		DisbursedSats:    make(map[string]int64),
		AuditorNodes:     auditors,
		CreatedAt:        now,
		ExpiresAt:        now.AddDate(0, months, 0),
		LastAuditedAt:    make(map[string]time.Time),
	}

	m.contracts[cID] = c
	return c, nil
}

// SettlePayment marks the invoice paid (simulated in sandbox or verified via webhook).
func (m *Manager) SettlePayment(contractID string) (*StorageContract, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	c, exists := m.contracts[contractID]
	if !exists {
		return nil, errors.New("contract not found")
	}

	c.IsSettled = true
	c.PaidSats = c.TotalSats
	return c, nil
}

// RecordAuditPass logs a successful audit and disburses scheduled micropayments if due.
func (m *Manager) RecordAuditPass(contractID string, storageNodeID string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	c, exists := m.contracts[contractID]
	if !exists {
		return 0, errors.New("contract not found")
	}
	if !c.IsSettled {
		return 0, errors.New("contract invoice not yet paid")
	}

	c.LastAuditedAt[storageNodeID] = time.Now().UTC()

	// Disburse portion of payout for successful audit
	allocated := c.StorageNodes[storageNodeID]
	alreadyPaid := c.DisbursedSats[storageNodeID]
	remaining := allocated - alreadyPaid

	if remaining <= 0 {
		return 0, nil
	}

	// Disburse an increment (e.g., 20% per audit or remainder)
	disburseAmount := allocated / 5
	if disburseAmount < 1 || disburseAmount > remaining {
		disburseAmount = remaining
	}

	c.DisbursedSats[storageNodeID] += disburseAmount
	return disburseAmount, nil
}

// ReallocateOfflineNode reassigns remaining unaccrued sats to a healthy replacement node.
func (m *Manager) ReallocateOfflineNode(contractID string, failedNodeID string, replacementNodeID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	c, exists := m.contracts[contractID]
	if !exists {
		return errors.New("contract not found")
	}

	allocated := c.StorageNodes[failedNodeID]
	alreadyPaid := c.DisbursedSats[failedNodeID]
	unpaid := allocated - alreadyPaid

	if unpaid < 0 {
		unpaid = 0
	}

	// Remove unpaid balance from failed node and assign to replacement
	c.StorageNodes[failedNodeID] = alreadyPaid
	c.StorageNodes[replacementNodeID] = unpaid
	c.DisbursedSats[replacementNodeID] = 0

	return nil
}

// GetContract fetches contract details.
func (m *Manager) GetContract(contractID string) (*StorageContract, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	c, exists := m.contracts[contractID]
	if !exists {
		return nil, errors.New("contract not found")
	}
	return c, nil
}

// ListContracts returns all tracked contracts.
func (m *Manager) ListContracts() []*StorageContract {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var list []*StorageContract
	for _, c := range m.contracts {
		list = append(list, c)
	}
	return list
}
