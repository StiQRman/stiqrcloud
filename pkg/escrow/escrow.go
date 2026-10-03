package escrow

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// PricingPolicy configures node storage pricing, free modes, and whitelisting.
type PricingPolicy struct {
	FreeForAll      bool     `json:"free_for_all"`       // If true, storage is 100% free for all clients
	PricePerGBMonth int64    `json:"price_per_gb_month"` // Sats per GB per month (e.g., 100)
	FreeWhitelist   []string `json:"free_whitelist"`     // List of user_pub_address / Reticulum hashes that are always free
}

// StorageContract models the time-locked storage agreement funded via Lightning.
type StorageContract struct {
	ContractID       string               `json:"contract_id"`
	DataID           string               `json:"data_id"`
	UserPubAddress   string               `json:"user_pub_address"`
	TotalBytes       int64                `json:"total_bytes"`
	DurationMonths   int                  `json:"duration_months"`
	TotalSats        int64                `json:"total_sats"`
	PaidSats         int64                `json:"paid_sats"`
	IsFree           bool                 `json:"is_free"` // True if whitelisted or free_for_all
	LightningInvoice string               `json:"lightning_invoice"`
	PaymentHash      string               `json:"payment_hash"`
	IsSettled        bool                 `json:"is_settled"`
	StorageNodes     map[string]int64     `json:"storage_nodes"` // NodeID -> allocated sats
	DisbursedSats    map[string]int64     `json:"disbursed_sats"` // NodeID -> already disbursed sats
	AuditorNodes     []string             `json:"auditor_nodes"`
	CreatedAt        time.Time            `json:"created_at"`
	ExpiresAt        time.Time            `json:"expires_at"`
	LastAuditedAt    map[string]time.Time `json:"last_audited_at"`
}

// Manager coordinates contract creation, Lightning invoice generation, and payouts.
type Manager struct {
	mu           sync.RWMutex
	nodeID       string
	policy       PricingPolicy
	policyFile   string
	contracts    map[string]*StorageContract
	lightningURL string
}

// NewManager initializes the escrow manager with optional persistence.
func NewManager(nodeID string, pricePerGB int64, dataDir ...string) *Manager {
	if pricePerGB <= 0 {
		pricePerGB = 100 // default 100 sats / GB / month
	}

	var pFile string
	if len(dataDir) > 0 && dataDir[0] != "" {
		pFile = filepath.Join(dataDir[0], "pricing_policy.json")
	}

	m := &Manager{
		nodeID: nodeID,
		policy: PricingPolicy{
			FreeForAll:      true,
			PricePerGBMonth: pricePerGB,
			FreeWhitelist:   []string{},
		},
		policyFile: pFile,
		contracts:  make(map[string]*StorageContract),
	}

	if pFile != "" {
		m.loadPolicy()
	}

	return m
}

func (m *Manager) loadPolicy() {
	if m.policyFile == "" {
		return
	}
	data, err := os.ReadFile(m.policyFile)
	if err == nil {
		var p PricingPolicy
		if err := json.Unmarshal(data, &p); err == nil {
			if p.PricePerGBMonth <= 0 {
				p.PricePerGBMonth = 100
			}
			m.policy = p
		}
	}
}

func (m *Manager) savePolicyLocked() error {
	if m.policyFile == "" {
		return nil
	}
	data, err := json.MarshalIndent(m.policy, "", "  ")
	if err != nil {
		return err
	}
	_ = os.MkdirAll(filepath.Dir(m.policyFile), 0755)
	return os.WriteFile(m.policyFile, data, 0644)
}

// GetPolicy returns the current pricing and whitelist policy.
func (m *Manager) GetPolicy() PricingPolicy {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.policy
}

// UpdatePolicy updates and persists the pricing policy.
func (m *Manager) UpdatePolicy(p PricingPolicy) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if p.PricePerGBMonth <= 0 {
		p.PricePerGBMonth = 100
	}
	m.policy = p
	return m.savePolicyLocked()
}

// SetFreeForAll toggles whether storage is 100% free for everyone.
func (m *Manager) SetFreeForAll(free bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.policy.FreeForAll = free
	return m.savePolicyLocked()
}

// SetPricePerGBMonth updates the price in sats per GB per month.
func (m *Manager) SetPricePerGBMonth(price int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if price <= 0 {
		price = 100
	}
	m.policy.PricePerGBMonth = price
	return m.savePolicyLocked()
}

// AddWhitelist adds a public address to the free whitelist.
func (m *Manager) AddWhitelist(pubAddress string) error {
	pubAddress = strings.TrimSpace(pubAddress)
	if pubAddress == "" {
		return errors.New("empty address")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, a := range m.policy.FreeWhitelist {
		if strings.EqualFold(a, pubAddress) {
			return nil // already exists
		}
	}
	m.policy.FreeWhitelist = append(m.policy.FreeWhitelist, pubAddress)
	return m.savePolicyLocked()
}

// RemoveWhitelist removes a public address from the free whitelist.
func (m *Manager) RemoveWhitelist(pubAddress string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var updated []string
	for _, a := range m.policy.FreeWhitelist {
		if !strings.EqualFold(a, strings.TrimSpace(pubAddress)) {
			updated = append(updated, a)
		}
	}
	m.policy.FreeWhitelist = updated
	return m.savePolicyLocked()
}

// IsWhitelisted checks if an address is in the free whitelist.
func (m *Manager) IsWhitelisted(pubAddress string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	clean := strings.TrimSpace(pubAddress)
	for _, a := range m.policy.FreeWhitelist {
		if strings.EqualFold(a, clean) {
			return true
		}
	}
	return false
}

// IsFree evaluates whether a given client is exempt from payment.
func (m *Manager) IsFree(pubAddress string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.policy.FreeForAll {
		return true
	}
	clean := strings.TrimSpace(pubAddress)
	if clean != "" {
		for _, a := range m.policy.FreeWhitelist {
			if strings.EqualFold(a, clean) {
				return true
			}
		}
	}
	return false
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
// If the user's public address is whitelisted or FreeForAll is active, invoice is free (0 sats).
func (m *Manager) CreateContract(dataID string, userPubAddress string, bytes int64, months int, storageNodeRates map[string]int64, auditors []string) (*StorageContract, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var rates []int64
	storageMap := make(map[string]int64)
	for id, rate := range storageNodeRates {
		rates = append(rates, rate)
		storageMap[id] = 0 // Will split total
	}

	isFree := false
	if m.policy.FreeForAll {
		isFree = true
	} else if userPubAddress != "" {
		for _, a := range m.policy.FreeWhitelist {
			if strings.EqualFold(a, strings.TrimSpace(userPubAddress)) {
				isFree = true
				break
			}
		}
	}

	var totalSats int64
	var invoice string
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	hash := hex.EncodeToString(b)
	cID := fmt.Sprintf("ctr_%s", hash[:12])
	now := time.Now().UTC()

	if isFree {
		totalSats = 0
		invoice = "FREE_STORAGE_AGREEMENT"
	} else {
		// Use node's current rate if rates map is empty
		if len(rates) == 0 {
			rates = []int64{m.policy.PricePerGBMonth, m.policy.PricePerGBMonth, m.policy.PricePerGBMonth}
		}
		totalSats = CalculatePrice(bytes, months, rates)
		invoice = fmt.Sprintf("lnbc%du1p%s", totalSats*10, hash[:20])

		if len(storageNodeRates) > 0 {
			perNode := totalSats / int64(len(storageNodeRates))
			for id := range storageNodeRates {
				storageMap[id] = perNode
			}
		}
	}

	// For free contracts, grant generous multi-year duration
	duration := months
	expires := now.AddDate(0, months, 0)
	if isFree {
		duration = 120 // 10 years free agreement
		expires = now.AddDate(10, 0, 0)
	}

	paidSats := int64(0)
	c := &StorageContract{
		ContractID:       cID,
		DataID:           dataID,
		UserPubAddress:   userPubAddress,
		TotalBytes:       bytes,
		DurationMonths:   duration,
		TotalSats:        totalSats,
		PaidSats:         paidSats,
		IsFree:           isFree,
		LightningInvoice: invoice,
		PaymentHash:      hash,
		IsSettled:        isFree, // Free agreements are automatically settled
		StorageNodes:     storageMap,
		DisbursedSats:    make(map[string]int64),
		AuditorNodes:     auditors,
		CreatedAt:        now,
		ExpiresAt:        expires,
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

	allocated := c.StorageNodes[storageNodeID]
	alreadyPaid := c.DisbursedSats[storageNodeID]
	remaining := allocated - alreadyPaid

	if remaining <= 0 {
		return 0, nil
	}

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

// IsContractExpired checks if a contract's paid period has ended or is unpaid.
// Free agreements do not expire during their active period.
func (m *Manager) IsContractExpired(contractID string) (bool, time.Time) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	c, exists := m.contracts[contractID]
	if !exists {
		return true, time.Time{}
	}
	if c.IsFree {
		return false, c.ExpiresAt
	}
	if !c.IsSettled {
		return true, c.CreatedAt
	}
	if time.Now().UTC().After(c.ExpiresAt) {
		return true, c.ExpiresAt
	}
	return false, c.ExpiresAt
}

// GetContractForDataID finds the contract associated with a data_id.
func (m *Manager) GetContractForDataID(dataID string) (*StorageContract, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, c := range m.contracts {
		if c.DataID == dataID {
			return c, true
		}
	}
	return nil, false
}
