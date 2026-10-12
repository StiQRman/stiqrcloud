package storage

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/stiqrhub/testcloud/pkg/crypto"
)

type AccessPermission string

const (
	PermView AccessPermission = "view"
	PermEdit AccessPermission = "edit"
)

// AccessControlEntry manages Google Drive-style sharing.
type AccessControlEntry struct {
	UserPubKey string           `json:"user_pub_key"`
	Permission AccessPermission `json:"permission"` // "view" or "edit"
	WrappedDEK string           `json:"wrapped_dek"` // Encrypted DEK for this collaborator
	AddedAt    time.Time        `json:"added_at"`
}

// Record is the core persistent database entity.
type Record struct {
	DataID           string               `json:"data_id"`                      // INVARIANT: Never changes across edits or node migration
	Collection       string               `json:"collection"`                   // Collection/table name
	UserPubAddress   string               `json:"user_pub_address,omitempty"`   // Owner public key
	OwnerSignature   string               `json:"owner_signature,omitempty"`   // Signature over payload
	Version          int64                `json:"version"`                      // Monotonic revision counter
	Text             string               `json:"text"`                         // Encrypted or plaintext payload
	FileChunkHash    string               `json:"file_chunk_hash,omitempty"`    // Content hash of file
	FileName         string               `json:"file_name,omitempty"`
	PictureChunkHash string               `json:"picture_chunk_hash,omitempty"` // Content hash of picture
	PictureName      string               `json:"picture_name,omitempty"`
	TotalBytes       int64                `json:"total_bytes"`
	ACL              []AccessControlEntry `json:"acl,omitempty"`                // Authorized collaborators
	MerkleRoot       string               `json:"merkle_root,omitempty"`        // Cryptographic audit root
	ReplicationPolicy string              `json:"replication_policy,omitempty"` // "local", "replicated_2", "distributed", "custom"
	TargetReplicas   int                  `json:"target_replicas,omitempty"`    // Desired replica count (e.g. 2)
	StoragePeers     []string             `json:"storage_peers,omitempty"`      // Assigned storage peers
	AuditorPeers     []string             `json:"auditor_peers,omitempty"`      // Assigned auditor peers
	CreatedAt        time.Time            `json:"created_at"`
	UpdatedAt        time.Time            `json:"updated_at"`
}

// ContractCheckerFunc returns whether a record's contract is expired/unpaid and its expiration timestamp.
type ContractCheckerFunc func(dataID string) (isExpired bool, expiresAt time.Time)

// StorageStats reports disk, record metrics, and active vs grace retention breakdown.
type StorageStats struct {
	UsedBytes        int64   `json:"used_bytes"`
	MaxBytes         int64   `json:"max_bytes"`
	ActiveBytes      int64   `json:"active_bytes"`
	GraceBytes       int64   `json:"grace_bytes"`
	GraceRecordCount int     `json:"grace_record_count"`
	RecordCount      int     `json:"record_count"`
	ChunkCount       int     `json:"chunk_count"`
	QuotaPercent     float64 `json:"quota_percent"`
}

// Store handles standalone local database persistence and chunk storage.
type Store struct {
	mu              sync.RWMutex
	baseDir         string
	chunksDir       string
	recordsDir      string
	indexFile       string
	records         map[string]*Record // data_id -> Record
	maxDiskBytes    int64
	contractChecker ContractCheckerFunc
}

// NewStore initializes a local database store.
func NewStore(baseDir string) (*Store, error) {
	chunksDir := filepath.Join(baseDir, "chunks")
	recordsDir := filepath.Join(baseDir, "records")

	if err := os.MkdirAll(chunksDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create chunks dir: %w", err)
	}
	if err := os.MkdirAll(recordsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create records dir: %w", err)
	}

	s := &Store{
		baseDir:      baseDir,
		chunksDir:    chunksDir,
		recordsDir:   recordsDir,
		indexFile:    filepath.Join(baseDir, "index.json"),
		records:      make(map[string]*Record),
		maxDiskBytes: 50 * 1024 * 1024 * 1024, // 50 GB default limit
	}

	if err := s.loadIndex(); err != nil {
		return nil, fmt.Errorf("failed to load database index: %w", err)
	}

	return s, nil
}

// SetMaxDiskBytes dynamically configures maximum local storage limit.
func (s *Store) SetMaxDiskBytes(maxBytes int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if maxBytes > 0 {
		s.maxDiskBytes = maxBytes
	}
}

// GetMaxDiskBytes returns the configured disk limit in bytes.
func (s *Store) GetMaxDiskBytes() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.maxDiskBytes
}

// SetContractChecker wires in the escrow status callback.
func (s *Store) SetContractChecker(fn ContractCheckerFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.contractChecker = fn
}

func (s *Store) loadIndex() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(s.recordsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(s.recordsDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var rec Record
		if err := json.Unmarshal(data, &rec); err == nil && rec.DataID != "" {
			s.records[rec.DataID] = &rec
		}
	}
	return nil
}

func generateDataID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return fmt.Sprintf("rec_%s_%d", hex.EncodeToString(b), time.Now().UnixNano()%100000)
}

type evictionCandidate struct {
	dataID    string
	expiresAt time.Time
	rec       *Record
}

func (s *Store) getUsedDiskBytesLocked() int64 {
	var totalUsed int64
	chunkEntries, _ := os.ReadDir(s.chunksDir)
	for _, c := range chunkEntries {
		if info, err := c.Info(); err == nil {
			totalUsed += info.Size()
		}
	}
	return totalUsed
}

// EvictExpiredData evicts stored chunks belonging to expired or unpaid contracts.
// Oldest expired contracts are pruned first. Active/paid and whitelisted records are protected.
func (s *Store) EvictExpiredData(neededBytes int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.evictExpiredDataLocked(neededBytes)
}

func (s *Store) evictExpiredDataLocked(neededBytes int64) (int64, error) {
	if s.contractChecker == nil {
		return 0, nil
	}

	var candidates []evictionCandidate
	for dataID, rec := range s.records {
		isExpired, expiresAt := s.contractChecker(dataID)
		if isExpired && (rec.FileChunkHash != "" || rec.PictureChunkHash != "") {
			candidates = append(candidates, evictionCandidate{
				dataID:    dataID,
				expiresAt: expiresAt,
				rec:       rec,
			})
		}
	}

	if len(candidates) == 0 {
		return 0, nil
	}

	// Sort oldest expired first
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].expiresAt.Before(candidates[j].expiresAt)
	})

	var freedTotal int64
	for _, cand := range candidates {
		rec := cand.rec
		var freedForRec int64

		if rec.FileChunkHash != "" {
			chunkPath := filepath.Join(s.chunksDir, rec.FileChunkHash)
			if info, err := os.Stat(chunkPath); err == nil {
				freedForRec += info.Size()
				_ = os.Remove(chunkPath)
			}
			rec.FileChunkHash = ""
		}

		if rec.PictureChunkHash != "" {
			chunkPath := filepath.Join(s.chunksDir, rec.PictureChunkHash)
			if info, err := os.Stat(chunkPath); err == nil {
				freedForRec += info.Size()
				_ = os.Remove(chunkPath)
			}
			rec.PictureChunkHash = ""
		}

		rec.TotalBytes = int64(len(rec.Text))
		_ = s.saveRecordDisk(rec)

		freedTotal += freedForRec
		if freedTotal >= neededBytes {
			break
		}
	}

	return freedTotal, nil
}

// PutChunk stores a binary blob addressed by its SHA-256 hash.
// If disk quota is reached, it will first evict expired/unpaid data before rejecting.
func (s *Store) PutChunk(data []byte) (string, error) {
	hash := crypto.HashBytes(data)
	chunkPath := filepath.Join(s.chunksDir, hash)

	// Idempotent write
	if _, err := os.Stat(chunkPath); err == nil {
		return hash, nil
	}

	// Check storage quota and evict expired contracts if full
	chunkSize := int64(len(data))
	currentUsed := s.getUsedDiskBytesLocked()
	if s.maxDiskBytes > 0 && (currentUsed+chunkSize) > s.maxDiskBytes {
		needed := (currentUsed + chunkSize) - s.maxDiskBytes
		freed, _ := s.evictExpiredDataLocked(needed)
		currentUsed -= freed
		if (currentUsed + chunkSize) > s.maxDiskBytes {
			return "", fmt.Errorf("storage quota exceeded (%d bytes max): all retained data belongs to active paid contracts", s.maxDiskBytes)
		}
	}

	if err := os.WriteFile(chunkPath, data, 0644); err != nil {
		return "", fmt.Errorf("failed to write chunk: %w", err)
	}
	return hash, nil
}

// GetChunk retrieves chunk bytes by hash.
func (s *Store) GetChunk(hash string) ([]byte, error) {
	chunkPath := filepath.Join(s.chunksDir, hash)
	data, err := os.ReadFile(chunkPath)
	if err != nil {
		return nil, fmt.Errorf("chunk not found: %w", err)
	}
	return data, nil
}

// HasChunk checks whether a chunk exists on disk without loading it into memory.
func (s *Store) HasChunk(hash string) bool {
	if hash == "" {
		return false
	}
	chunkPath := filepath.Join(s.chunksDir, hash)
	fi, err := os.Stat(chunkPath)
	return err == nil && !fi.IsDir()
}

// CreateRecord persists a new record. If DataID is empty, it generates a permanent DataID.
func (s *Store) CreateRecord(rec *Record, fileData []byte, picData []byte) (*Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if rec.DataID == "" {
		rec.DataID = generateDataID()
	} else if _, exists := s.records[rec.DataID]; exists {
		return nil, fmt.Errorf("record with data_id %q already exists; use UpdateRecord", rec.DataID)
	}

	if rec.Collection == "" {
		rec.Collection = "default"
	}

	rec.Version = 1
	rec.CreatedAt = time.Now().UTC()
	rec.UpdatedAt = rec.CreatedAt

	// Store attachments
	var chunkHashes []string
	if len(fileData) > 0 {
		h, err := s.PutChunk(fileData)
		if err != nil {
			return nil, err
		}
		rec.FileChunkHash = h
		chunkHashes = append(chunkHashes, h)
	}

	if len(picData) > 0 {
		h, err := s.PutChunk(picData)
		if err != nil {
			return nil, err
		}
		rec.PictureChunkHash = h
		chunkHashes = append(chunkHashes, h)
	}

	// Also hash text into merkle calculation
	textHash := crypto.HashBytes([]byte(rec.Text))
	chunkHashes = append(chunkHashes, textHash)

	tree, err := crypto.BuildMerkleTree(chunkHashes)
	if err == nil {
		rec.MerkleRoot = tree.RootHex()
	}

	rec.TotalBytes = int64(len(rec.Text) + len(fileData) + len(picData))

	if err := s.saveRecordDisk(rec); err != nil {
		return nil, err
	}

	s.records[rec.DataID] = rec
	return rec, nil
}

// UpdateRecord updates an existing record while guaranteeing that the DataID remains unchanged.
func (s *Store) UpdateRecord(dataID string, updaterPubKey string, signature string, newText string, newFileData []byte, newPicData []byte) (*Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, exists := s.records[dataID]
	if !exists {
		return nil, fmt.Errorf("record %q not found", dataID)
	}

	// Verify update permission: must be owner or have PermEdit in ACL
	allowed := (rec.UserPubAddress == "" || updaterPubKey == "" || rec.UserPubAddress == updaterPubKey)
	if !allowed {
		for _, acl := range rec.ACL {
			if acl.UserPubKey == updaterPubKey && acl.Permission == PermEdit {
				allowed = true
				break
			}
		}
	}
	if !allowed {
		return nil, errors.New("unauthorized: caller does not possess edit rights")
	}

	// Validate signature if full ed25519 hex provided
	if signature != "" && len(updaterPubKey) == 64 && len(signature) == 128 {
		toSign1 := []byte(fmt.Sprintf("%s:%d:%s", dataID, rec.Version+1, newText))
		toSign2 := []byte(fmt.Sprintf("%s%s%d", dataID, newText, rec.Version+1))
		toSign3 := []byte(fmt.Sprintf("%s%s%d", dataID, newText, rec.Version))
		if !crypto.VerifySignature(toSign1, updaterPubKey, signature) &&
			!crypto.VerifySignature(toSign2, updaterPubKey, signature) &&
			!crypto.VerifySignature(toSign3, updaterPubKey, signature) {
			return nil, errors.New("invalid signature for record update")
		}
	}

	// Monotonically increment revision counter while KEEPING invariant DataID
	rec.Version++
	rec.UpdatedAt = time.Now().UTC()
	rec.Text = newText

	var chunkHashes []string
	if len(newFileData) > 0 {
		h, err := s.PutChunk(newFileData)
		if err != nil {
			return nil, err
		}
		rec.FileChunkHash = h
		chunkHashes = append(chunkHashes, h)
	} else if rec.FileChunkHash != "" {
		chunkHashes = append(chunkHashes, rec.FileChunkHash)
	}

	if len(newPicData) > 0 {
		h, err := s.PutChunk(newPicData)
		if err != nil {
			return nil, err
		}
		rec.PictureChunkHash = h
		chunkHashes = append(chunkHashes, h)
	} else if rec.PictureChunkHash != "" {
		chunkHashes = append(chunkHashes, rec.PictureChunkHash)
	}

	chunkHashes = append(chunkHashes, crypto.HashBytes([]byte(newText)))
	tree, err := crypto.BuildMerkleTree(chunkHashes)
	if err == nil {
		rec.MerkleRoot = tree.RootHex()
	}

	rec.TotalBytes = int64(len(rec.Text) + len(newFileData) + len(newPicData))
	rec.OwnerSignature = signature

	if err := s.saveRecordDisk(rec); err != nil {
		return nil, err
	}

	return rec, nil
}

// GetRecord returns a record by invariant DataID.
func (s *Store) GetRecord(dataID string) (*Record, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rec, exists := s.records[dataID]
	if !exists {
		return nil, fmt.Errorf("record %q not found", dataID)
	}
	return rec, nil
}

// DeleteRecord deletes a record.
func (s *Store) DeleteRecord(dataID string, callerPubKey string, signature string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, exists := s.records[dataID]
	if !exists {
		return fmt.Errorf("record %q not found", dataID)
	}

	// Only owner or editor can delete
	allowed := (rec.UserPubAddress == "" || callerPubKey == "" || rec.UserPubAddress == callerPubKey)
	if !allowed {
		for _, acl := range rec.ACL {
			if acl.UserPubKey == callerPubKey && acl.Permission == PermEdit {
				allowed = true
				break
			}
		}
	}
	if !allowed {
		return errors.New("unauthorized: caller cannot delete this record")
	}

	path := filepath.Join(s.recordsDir, dataID+".json")
	_ = os.Remove(path)
	delete(s.records, dataID)
	return nil
}

// AddCollaborator adds or updates Google Drive-style permissions with Wrapped DEK.
func (s *Store) AddCollaborator(dataID string, ownerPubKey string, entry AccessControlEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, exists := s.records[dataID]
	if !exists {
		return fmt.Errorf("record %q not found", dataID)
	}

	if rec.UserPubAddress != ownerPubKey {
		return errors.New("only data owner can manage collaborators")
	}

	entry.AddedAt = time.Now().UTC()

	// Update existing or append
	found := false
	for i, existing := range rec.ACL {
		if existing.UserPubKey == entry.UserPubKey {
			rec.ACL[i] = entry
			found = true
			break
		}
	}
	if !found {
		rec.ACL = append(rec.ACL, entry)
	}

	return s.saveRecordDisk(rec)
}

// ListRecords lists records optionally filtered by collection.
func (s *Store) ListRecords(collection string, limit, offset int) ([]*Record, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var matched []*Record
	for _, r := range s.records {
		if collection == "" || r.Collection == collection {
			matched = append(matched, r)
		}
	}

	// Sort newest first
	sort.Slice(matched, func(i, j int) bool {
		return matched[i].UpdatedAt.After(matched[j].UpdatedAt)
	})

	total := len(matched)
	if offset > total {
		return []*Record{}, total
	}
	end := offset + limit
	if limit <= 0 || end > total {
		end = total
	}

	return matched[offset:end], total
}

func (s *Store) saveRecordDisk(rec *Record) error {
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(s.recordsDir, rec.DataID+".json")
	return os.WriteFile(path, data, 0644)
}

// SaveRecord updates the in-memory cache and persists the record to disk.
func (s *Store) SaveRecord(rec *Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[rec.DataID] = rec
	return s.saveRecordDisk(rec)
}

// UpsertReplicatedRecord stores or updates a replicated record from another peer.
// If the record exists and incoming version is older, existing is kept.
// Attachments (fileData / picData) are saved to the chunk store if provided.
func (s *Store) UpsertReplicatedRecord(rec *Record, fileData, picData []byte) (*Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if rec == nil || rec.DataID == "" {
		return nil, errors.New("empty data_id in replicated record")
	}

	if len(fileData) > 0 {
		h, err := s.PutChunk(fileData)
		if err == nil {
			rec.FileChunkHash = h
		}
	}
	if len(picData) > 0 {
		h, err := s.PutChunk(picData)
		if err == nil {
			rec.PictureChunkHash = h
		}
	}

	existing, exists := s.records[rec.DataID]
	if exists {
		if rec.Version < existing.Version {
			return existing, nil
		}
		if rec.FileChunkHash == "" && existing.FileChunkHash != "" {
			rec.FileChunkHash = existing.FileChunkHash
		}
		if rec.PictureChunkHash == "" && existing.PictureChunkHash != "" {
			rec.PictureChunkHash = existing.PictureChunkHash
		}
	}

	if err := s.saveRecordDisk(rec); err != nil {
		return nil, err
	}
	s.records[rec.DataID] = rec
	return rec, nil
}

// ListAllRecords returns a slice containing all in-memory records.
func (s *Store) ListAllRecords() []*Record {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var all []*Record
	for _, r := range s.records {
		all = append(all, r)
	}
	return all
}

// StorageStats calculates disk usage, counts, and active vs grace breakdown.
func (s *Store) StorageStats() StorageStats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var totalUsed int64
	chunkEntries, _ := os.ReadDir(s.chunksDir)
	for _, c := range chunkEntries {
		if info, err := c.Info(); err == nil {
			totalUsed += info.Size()
		}
	}

	pct := 0.0
	if s.maxDiskBytes > 0 {
		pct = float64(totalUsed) / float64(s.maxDiskBytes) * 100.0
	}

	var activeBytes, graceBytes int64
	var graceCount int
	if s.contractChecker != nil {
		for dataID, rec := range s.records {
			isExpired, _ := s.contractChecker(dataID)
			if isExpired {
				graceBytes += rec.TotalBytes
				graceCount++
			} else {
				activeBytes += rec.TotalBytes
			}
		}
	} else {
		activeBytes = totalUsed
	}

	return StorageStats{
		UsedBytes:        totalUsed,
		MaxBytes:         s.maxDiskBytes,
		ActiveBytes:      activeBytes,
		GraceBytes:       graceBytes,
		GraceRecordCount: graceCount,
		RecordCount:      len(s.records),
		ChunkCount:       len(chunkEntries),
		QuotaPercent:     pct,
	}
}
