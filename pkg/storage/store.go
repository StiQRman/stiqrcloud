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

	"github.com/stiqrhub/stiqrcloud/pkg/crypto"
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
	Lockboxes        []crypto.UserShareLockbox `json:"lockboxes,omitempty"`     // Post-quantum encrypted share slots
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
	records                map[string]*Record // data_id -> Record
	customCollections      map[string]bool    // registered collections
	customCollectionFields map[string][]CollectionField // collection -> custom schema fields
	schemaFile             string
	maxDiskBytes           int64
	contractChecker        ContractCheckerFunc
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
		baseDir:                baseDir,
		chunksDir:              chunksDir,
		recordsDir:             recordsDir,
		indexFile:              filepath.Join(baseDir, "index.json"),
		schemaFile:             filepath.Join(baseDir, "collections_schema.json"),
		records:                make(map[string]*Record),
		customCollections:      make(map[string]bool),
		customCollectionFields: make(map[string][]CollectionField),
		maxDiskBytes:           50 * 1024 * 1024 * 1024, // 50 GB default limit
	}

	s.loadSchemaFile()

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

func isValidDataID(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.') {
			return false
		}
	}
	if strings.Contains(id, "..") || id == "." {
		return false
	}
	return true
}

func isValidHexHash(h string) bool {
	if len(h) < 16 || len(h) > 128 {
		return false
	}
	for _, r := range h {
		if !((r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
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
	if !isValidHexHash(hash) {
		return nil, fmt.Errorf("invalid chunk hash: %q", hash)
	}
	chunkPath := filepath.Clean(filepath.Join(s.chunksDir, hash))
	if !strings.HasPrefix(chunkPath, filepath.Clean(s.chunksDir)+string(filepath.Separator)) {
		return nil, fmt.Errorf("security violation: path traversal in chunk hash %q", hash)
	}
	data, err := os.ReadFile(chunkPath)
	if err != nil {
		return nil, fmt.Errorf("chunk not found: %w", err)
	}
	return data, nil
}

// HasChunk checks whether a chunk exists on disk without loading it into memory.
func (s *Store) HasChunk(hash string) bool {
	if !isValidHexHash(hash) {
		return false
	}
	chunkPath := filepath.Clean(filepath.Join(s.chunksDir, hash))
	if !strings.HasPrefix(chunkPath, filepath.Clean(s.chunksDir)+string(filepath.Separator)) {
		return false
	}
	fi, err := os.Stat(chunkPath)
	return err == nil && !fi.IsDir()
}

// syncACLFromText parses allowedUsers from JSON text payload and adds them to rec.ACL
func syncACLFromText(rec *Record) {
	if rec == nil || rec.Text == "" {
		return
	}
	var data struct {
		AllowedUsers      []string `json:"allowedUsers"`
		AllowedUsersSnake []string `json:"allowed_users"`
	}
	if err := json.Unmarshal([]byte(rec.Text), &data); err == nil {
		users := data.AllowedUsers
		if len(users) == 0 {
			users = data.AllowedUsersSnake
		}
		existing := make(map[string]bool)
		for _, entry := range rec.ACL {
			existing[entry.UserPubKey] = true
		}
		for _, u := range users {
			u = strings.TrimSpace(u)
			if u != "" && u != rec.UserPubAddress && !existing[u] {
				rec.ACL = append(rec.ACL, AccessControlEntry{
					UserPubKey: u,
					Permission: PermEdit,
					AddedAt:    time.Now().UTC(),
				})
				existing[u] = true
			}
		}
	}
}

// CreateRecord persists a new record or performs an idempotent update if the record already exists.
func (s *Store) CreateRecord(rec *Record, fileData []byte, picData []byte) (*Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if rec.DataID == "" {
		rec.DataID = generateDataID()
	} else {
		if !isValidDataID(rec.DataID) {
			return nil, fmt.Errorf("invalid data_id: %q contains forbidden characters or path traversal", rec.DataID)
		}
		if existing, exists := s.records[rec.DataID]; exists {
			// Idempotent upsert: if the record exists, update it if caller is authorized
			if existing.UserPubAddress != "" && rec.UserPubAddress != "" && existing.UserPubAddress != rec.UserPubAddress {
				hasPerm := false
				for _, acl := range existing.ACL {
					if acl.UserPubKey == rec.UserPubAddress && (acl.Permission == PermEdit || acl.Permission == "edit") {
						hasPerm = true
						break
					}
				}
				if !hasPerm {
					return nil, errors.New("unauthorized: caller does not possess rights to update this record")
				}
			}

			existing.Version++
			existing.UpdatedAt = time.Now().UTC()
			existing.Text = rec.Text
			if rec.UserPubAddress != "" && existing.UserPubAddress == "" {
				existing.UserPubAddress = rec.UserPubAddress
			}
			if rec.OwnerSignature != "" {
				existing.OwnerSignature = rec.OwnerSignature
			}
			if rec.ReplicationPolicy != "" {
				existing.ReplicationPolicy = rec.ReplicationPolicy
			}

			var chunkHashes []string
			if len(fileData) > 0 {
				h, err := s.PutChunk(fileData)
				if err != nil {
					return nil, err
				}
				existing.FileChunkHash = h
				existing.FileName = rec.FileName
				chunkHashes = append(chunkHashes, h)
			} else if existing.FileChunkHash != "" {
				chunkHashes = append(chunkHashes, existing.FileChunkHash)
			}

			if len(picData) > 0 {
				h, err := s.PutChunk(picData)
				if err != nil {
					return nil, err
				}
				existing.PictureChunkHash = h
				existing.PictureName = rec.PictureName
				chunkHashes = append(chunkHashes, h)
			} else if existing.PictureChunkHash != "" {
				chunkHashes = append(chunkHashes, existing.PictureChunkHash)
			}

			if existing.FileName == "" && existing.PictureName != "" {
				existing.FileName = existing.PictureName
			}

			textHash := crypto.HashBytes([]byte(existing.Text))
			chunkHashes = append(chunkHashes, textHash)
			tree, err := crypto.BuildMerkleTree(chunkHashes)
			if err == nil {
				existing.MerkleRoot = tree.RootHex()
			}
			existing.TotalBytes = int64(len(existing.Text) + len(fileData) + len(picData))
			syncACLFromText(existing)

			if err := s.saveRecordDisk(existing); err != nil {
				return nil, err
			}
			return existing, nil
		}
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

	if rec.FileName == "" && rec.PictureName != "" {
		rec.FileName = rec.PictureName
	}

	// Also hash text into merkle calculation
	textHash := crypto.HashBytes([]byte(rec.Text))
	chunkHashes = append(chunkHashes, textHash)

	// Verify owner signature if supplied on creation
	if rec.UserPubAddress != "" && rec.OwnerSignature != "" {
		toSign1 := []byte(fmt.Sprintf("%s:%s:%s", rec.Collection, rec.Text, rec.UserPubAddress))
		toSign2 := []byte(fmt.Sprintf("%s:%d:%s", rec.DataID, 1, rec.Text))
		toSign3 := []byte(fmt.Sprintf("%s:create:%s", rec.DataID, rec.Text))
		toSign4 := []byte(fmt.Sprintf("%s:%d:%s", rec.DataID, 1, textHash))
		toSign5 := []byte(fmt.Sprintf("%s:%s:%s", rec.Collection, textHash, rec.UserPubAddress))
		if !crypto.VerifyHybridSignature(toSign1, rec.UserPubAddress, rec.OwnerSignature) &&
			!crypto.VerifyHybridSignature(toSign2, rec.UserPubAddress, rec.OwnerSignature) &&
			!crypto.VerifyHybridSignature(toSign3, rec.UserPubAddress, rec.OwnerSignature) &&
			!crypto.VerifyHybridSignature(toSign4, rec.UserPubAddress, rec.OwnerSignature) &&
			!crypto.VerifyHybridSignature(toSign5, rec.UserPubAddress, rec.OwnerSignature) {
			return nil, errors.New("unauthorized: invalid owner signature for record creation")
		}
	}

	tree, err := crypto.BuildMerkleTree(chunkHashes)
	if err == nil {
		rec.MerkleRoot = tree.RootHex()
	}

	rec.TotalBytes = int64(len(rec.Text) + len(fileData) + len(picData))
	syncACLFromText(rec)

	if err := s.saveRecordDisk(rec); err != nil {
		return nil, err
	}

	s.records[rec.DataID] = rec
	return rec, nil
}

// UpdateRecord updates an existing record while guaranteeing that the DataID remains unchanged.
func (s *Store) UpdateRecord(dataID string, updaterPubKey string, signature string, newText string, newFileData []byte, newPicData []byte) (*Record, error) {
	return s.UpdateRecordWithNames(dataID, updaterPubKey, signature, newText, newFileData, "", newPicData, "")
}

// UpdateRecordWithNames updates an existing record with optional file and picture names.
func (s *Store) UpdateRecordWithNames(dataID string, updaterPubKey string, signature string, newText string, newFileData []byte, newFileName string, newPicData []byte, newPicName string) (*Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, exists := s.records[dataID]
	if !exists {
		return nil, fmt.Errorf("record %q not found", dataID)
	}

	// Verify update permission: must be owner or have PermEdit in ACL
	allowed := false
	if rec.UserPubAddress == "" {
		allowed = true // Unowned record
	} else if updaterPubKey != "" {
		if rec.UserPubAddress == updaterPubKey {
			allowed = true
		} else {
			for _, acl := range rec.ACL {
				if acl.UserPubKey == updaterPubKey && acl.Permission == PermEdit {
					allowed = true
					break
				}
			}
		}
	}
	if !allowed {
		return nil, errors.New("unauthorized: caller does not possess edit rights")
	}

	// Enforce signature verification if record has an owner or if signature is supplied
	if rec.UserPubAddress != "" {
		if signature == "" {
			return nil, errors.New("unauthorized: missing cryptographic signature for record update")
		}

		textHash := crypto.HashBytes([]byte(newText))
		toSign1 := []byte(fmt.Sprintf("%s:%d:%s", dataID, rec.Version+1, newText))
		toSign2 := []byte(fmt.Sprintf("%s:%d:%s", dataID, rec.Version+1, textHash))
		toSign3 := []byte(fmt.Sprintf("%s%s%d", dataID, newText, rec.Version+1))
		toSign4 := []byte(fmt.Sprintf("%s%s%d", dataID, newText, rec.Version))
		toSign5 := []byte(fmt.Sprintf("%s:%s", dataID, newText))

		valid := crypto.VerifyHybridSignature(toSign1, updaterPubKey, signature) ||
			crypto.VerifyHybridSignature(toSign2, updaterPubKey, signature) ||
			crypto.VerifyHybridSignature(toSign3, updaterPubKey, signature) ||
			crypto.VerifyHybridSignature(toSign4, updaterPubKey, signature) ||
			crypto.VerifyHybridSignature(toSign5, updaterPubKey, signature)

		if !valid {
			return nil, errors.New("unauthorized: invalid signature for record update")
		}
	} else if signature != "" {
		textHash := crypto.HashBytes([]byte(newText))
		toSign1 := []byte(fmt.Sprintf("%s:%d:%s", dataID, rec.Version+1, newText))
		toSign2 := []byte(fmt.Sprintf("%s:%d:%s", dataID, rec.Version+1, textHash))
		toSign5 := []byte(fmt.Sprintf("%s:%s", dataID, newText))
		valid := crypto.VerifyHybridSignature(toSign1, updaterPubKey, signature) ||
			crypto.VerifyHybridSignature(toSign2, updaterPubKey, signature) ||
			crypto.VerifyHybridSignature(toSign5, updaterPubKey, signature)
		if !valid {
			return nil, errors.New("unauthorized: invalid signature for record update")
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

	if newFileName != "" {
		rec.FileName = newFileName
	}
	if newPicName != "" {
		rec.PictureName = newPicName
	}
	if rec.FileName == "" && rec.PictureName != "" {
		rec.FileName = rec.PictureName
	}

	chunkHashes = append(chunkHashes, crypto.HashBytes([]byte(newText)))
	tree, err := crypto.BuildMerkleTree(chunkHashes)
	if err == nil {
		rec.MerkleRoot = tree.RootHex()
	}

	rec.TotalBytes = int64(len(rec.Text) + len(newFileData) + len(newPicData))
	rec.OwnerSignature = signature
	syncACLFromText(rec)

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

	if rec.UserPubAddress != "" {
		if callerPubKey == "" {
			return errors.New("unauthorized: missing caller public key for deletion")
		}

		// Only owner or editor can delete
		allowed := (rec.UserPubAddress == callerPubKey)
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

		if signature == "" {
			return errors.New("unauthorized: missing cryptographic signature for record deletion")
		}

		toSign1 := []byte(fmt.Sprintf("delete:%s", dataID))
		toSign2 := []byte(fmt.Sprintf("%s:delete", dataID))
		if !crypto.VerifyHybridSignature(toSign1, callerPubKey, signature) &&
			!crypto.VerifyHybridSignature(toSign2, callerPubKey, signature) {
			return errors.New("unauthorized: invalid signature for record deletion")
		}
	} else if callerPubKey != "" {
		return errors.New("unauthorized: cannot delete unowned record with claimed caller public key")
	}

	if !isValidDataID(dataID) {
		return fmt.Errorf("invalid data_id: %q", dataID)
	}
	path := filepath.Clean(filepath.Join(s.recordsDir, dataID+".json"))
	if !strings.HasPrefix(path, filepath.Clean(s.recordsDir)+string(filepath.Separator)) {
		return fmt.Errorf("security violation: path traversal in data_id %q", dataID)
	}
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

// AddCollaboratorSigned adds or updates Google Drive-style permissions with owner signature verification.
func (s *Store) AddCollaboratorSigned(dataID string, ownerPubKey string, ownerSignature string, entry AccessControlEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, exists := s.records[dataID]
	if !exists {
		return fmt.Errorf("record %q not found", dataID)
	}

	if rec.UserPubAddress != "" {
		if rec.UserPubAddress != ownerPubKey {
			return errors.New("unauthorized: only data owner can manage collaborators")
		}
		if ownerSignature != "" {
			toSign1 := []byte(fmt.Sprintf("add_collaborator:%s:%s:%s", dataID, entry.UserPubKey, entry.Permission))
			toSign2 := []byte(fmt.Sprintf("%s:%s", dataID, entry.UserPubKey))
			if !crypto.VerifyHybridSignature(toSign1, ownerPubKey, ownerSignature) &&
				!crypto.VerifyHybridSignature(toSign2, ownerPubKey, ownerSignature) {
				return errors.New("unauthorized: invalid signature for adding collaborator")
			}
		}
	}

	entry.AddedAt = time.Now().UTC()
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

// RevokeCollaborator removes a collaborator from the ACL.
func (s *Store) RevokeCollaborator(dataID string, ownerPubKey string, collaboratorPubKey string) error {
	return s.RevokeCollaboratorSigned(dataID, ownerPubKey, "", collaboratorPubKey)
}

// RevokeCollaboratorSigned removes a collaborator from the ACL with owner signature verification.
func (s *Store) RevokeCollaboratorSigned(dataID string, ownerPubKey string, ownerSignature string, collaboratorPubKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, exists := s.records[dataID]
	if !exists {
		return fmt.Errorf("record %q not found", dataID)
	}

	if rec.UserPubAddress != "" {
		if rec.UserPubAddress != ownerPubKey {
			return errors.New("unauthorized: only data owner can revoke collaborators")
		}
		if ownerSignature != "" {
			toSign1 := []byte(fmt.Sprintf("revoke_collaborator:%s:%s", dataID, collaboratorPubKey))
			toSign2 := []byte(fmt.Sprintf("%s:revoke:%s", dataID, collaboratorPubKey))
			toSign3 := []byte(fmt.Sprintf("%s:%s", dataID, collaboratorPubKey))
			if !crypto.VerifyHybridSignature(toSign1, ownerPubKey, ownerSignature) &&
				!crypto.VerifyHybridSignature(toSign2, ownerPubKey, ownerSignature) &&
				!crypto.VerifyHybridSignature(toSign3, ownerPubKey, ownerSignature) {
				return errors.New("unauthorized: invalid signature for revoking collaborator")
			}
		}
	}

	var newACL []AccessControlEntry
	for _, entry := range rec.ACL {
		if entry.UserPubKey != collaboratorPubKey {
			newACL = append(newACL, entry)
		}
	}
	rec.ACL = newACL
	return s.saveRecordDisk(rec)
}

// PutShareLockbox stores an encrypted post-quantum share slot for a collaborator.
func (s *Store) PutShareLockbox(dataID string, box crypto.UserShareLockbox) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, exists := s.records[dataID]
	if !exists {
		return fmt.Errorf("record %q not found", dataID)
	}

	updated := false
	for i, existing := range rec.Lockboxes {
		if existing.FriendID == box.FriendID && existing.ShareIndex == box.ShareIndex {
			rec.Lockboxes[i] = box
			updated = true
			break
		}
	}
	if !updated {
		rec.Lockboxes = append(rec.Lockboxes, box)
	}

	return s.saveRecordDisk(rec)
}

// GetShareLockbox retrieves a specific collaborator's lockbox on this node.
func (s *Store) GetShareLockbox(dataID string, friendID string) (*crypto.UserShareLockbox, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rec, exists := s.records[dataID]
	if !exists {
		return nil, fmt.Errorf("record %q not found", dataID)
	}

	for _, box := range rec.Lockboxes {
		if box.FriendID == friendID || box.FriendMLKEMPubKey == friendID || box.FriendSignPubKey == friendID {
			return &box, nil
		}
	}
	return nil, fmt.Errorf("lockbox not found for friend %q", friendID)
}

// PurgeShareLockbox purges the collaborator's share slot from this node (used for threshold revocation).
func (s *Store) PurgeShareLockbox(dataID string, ownerPubKey string, ownerSig string, friendID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, exists := s.records[dataID]
	if !exists {
		return fmt.Errorf("record %q not found", dataID)
	}

	if rec.UserPubAddress != "" {
		if rec.UserPubAddress != ownerPubKey {
			return errors.New("unauthorized: only data owner can purge share lockboxes")
		}
		if ownerSig != "" {
			toSign1 := []byte(fmt.Sprintf("purge_share:%s:%s", dataID, friendID))
			toSign2 := []byte(fmt.Sprintf("%s:%s", dataID, friendID))
			if !crypto.VerifyHybridSignature(toSign1, ownerPubKey, ownerSig) &&
				!crypto.VerifyHybridSignature(toSign2, ownerPubKey, ownerSig) {
				return errors.New("unauthorized: invalid signature for purging share lockbox")
			}
		}
	}

	var remaining []crypto.UserShareLockbox
	for _, box := range rec.Lockboxes {
		if box.FriendID != friendID && box.FriendMLKEMPubKey != friendID && box.FriendSignPubKey != friendID {
			remaining = append(remaining, box)
		}
	}
	rec.Lockboxes = remaining
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

// CollectionField defines a schema field within a collection.
type CollectionField struct {
	Name     string `json:"name"`
	Type     string `json:"type"` // "text", "number", "file", "json", "date", "bool"
	Required bool   `json:"required"`
}

// CollectionInfo provides PocketBase-style metadata for collections and their fields.
type CollectionInfo struct {
	Name        string            `json:"name"`
	RecordCount int               `json:"record_count"`
	Fields      []CollectionField `json:"fields"`
	System      bool              `json:"system"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// ListCollections returns metadata for all collections, including their record counts and schema fields.
func (s *Store) ListCollections() []CollectionInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	standardFields := []CollectionField{
		{Name: "data_id", Type: "text", Required: true},
		{Name: "version", Type: "number", Required: true},
		{Name: "text", Type: "text", Required: false},
		{Name: "file_name", Type: "file", Required: false},
		{Name: "picture_name", Type: "file", Required: false},
		{Name: "acl", Type: "json", Required: false},
		{Name: "lockboxes", Type: "json", Required: false},
		{Name: "merkle_root", Type: "text", Required: false},
		{Name: "updated_at", Type: "date", Required: true},
	}

	colMap := make(map[string]*CollectionInfo)

	// Ensure standard default collections are always present
	defaults := []string{"files", "notes", "default"}
	for _, name := range defaults {
		colMap[name] = &CollectionInfo{
			Name:        name,
			RecordCount: 0,
			Fields:      standardFields,
			System:      name == "default",
			UpdatedAt:   time.Now().UTC(),
		}
	}

	// Helper to merge standard and custom fields
	mergeFields := func(colName string) []CollectionField {
		res := append([]CollectionField{}, standardFields...)
		custom := s.customCollectionFields[colName]
		fieldSet := make(map[string]bool)
		for _, f := range standardFields {
			fieldSet[strings.ToLower(f.Name)] = true
		}
		for _, f := range custom {
			if !fieldSet[strings.ToLower(f.Name)] {
				fieldSet[strings.ToLower(f.Name)] = true
				res = append(res, f)
			}
		}
		return res
	}

	for _, name := range defaults {
		colMap[name].Fields = mergeFields(name)
	}

	// Register any custom collections created by user / SDK
	for name := range s.customCollections {
		if _, exists := colMap[name]; !exists {
			colMap[name] = &CollectionInfo{
				Name:        name,
				RecordCount: 0,
				Fields:      mergeFields(name),
				System:      false,
				UpdatedAt:   time.Now().UTC(),
			}
		} else {
			colMap[name].Fields = mergeFields(name)
		}
	}

	// Count records and collect latest updated timestamps
	for _, rec := range s.records {
		name := rec.Collection
		if name == "" {
			name = "default"
		}

		info, exists := colMap[name]
		if !exists {
			info = &CollectionInfo{
				Name:        name,
				RecordCount: 0,
				Fields:      mergeFields(name),
				System:      false,
				UpdatedAt:   rec.UpdatedAt,
			}
			colMap[name] = info
		}

		info.RecordCount++
		if rec.UpdatedAt.After(info.UpdatedAt) {
			info.UpdatedAt = rec.UpdatedAt
		}
	}

	var result []CollectionInfo
	for _, info := range colMap {
		result = append(result, *info)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})

	return result
}

// CreateCollection registers a new user collection.
func (s *Store) CreateCollection(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cleanName := strings.TrimSpace(strings.ToLower(name))
	if cleanName == "" {
		return errors.New("collection name cannot be empty")
	}

	s.customCollections[cleanName] = true
	_ = s.saveSchemaFileLocked()
	return nil
}

// EnsureCollection idempotently registers a collection and merges schema fields (used by SDK app handshakes).
func (s *Store) EnsureCollection(name string, fields []CollectionField) (*CollectionInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cleanName := strings.TrimSpace(strings.ToLower(name))
	if cleanName == "" {
		return nil, errors.New("collection name cannot be empty")
	}

	s.customCollections[cleanName] = true

	// Merge incoming custom fields
	existing := s.customCollectionFields[cleanName]
	fieldMap := make(map[string]CollectionField)
	for _, f := range existing {
		fieldMap[strings.ToLower(f.Name)] = f
	}

	for _, f := range fields {
		k := strings.ToLower(strings.TrimSpace(f.Name))
		if k == "" {
			continue
		}
		if _, exists := fieldMap[k]; !exists {
			fieldMap[k] = f
			existing = append(existing, f)
		}
	}
	s.customCollectionFields[cleanName] = existing
	_ = s.saveSchemaFileLocked()

	// Return updated CollectionInfo
	recCount := 0
	for _, r := range s.records {
		if strings.EqualFold(r.Collection, cleanName) {
			recCount++
		}
	}

	standardFields := []CollectionField{
		{Name: "data_id", Type: "text", Required: true},
		{Name: "version", Type: "number", Required: true},
		{Name: "text", Type: "text", Required: false},
		{Name: "file_name", Type: "file", Required: false},
		{Name: "picture_name", Type: "file", Required: false},
		{Name: "acl", Type: "json", Required: false},
		{Name: "lockboxes", Type: "json", Required: false},
		{Name: "merkle_root", Type: "text", Required: false},
		{Name: "updated_at", Type: "date", Required: true},
	}
	fieldSet := make(map[string]bool)
	fullFields := append([]CollectionField{}, standardFields...)
	for _, f := range standardFields {
		fieldSet[strings.ToLower(f.Name)] = true
	}
	for _, f := range existing {
		if !fieldSet[strings.ToLower(f.Name)] {
			fieldSet[strings.ToLower(f.Name)] = true
			fullFields = append(fullFields, f)
		}
	}

	return &CollectionInfo{
		Name:        cleanName,
		RecordCount: recCount,
		Fields:      fullFields,
		System:      false,
		UpdatedAt:   time.Now().UTC(),
	}, nil
}

func (s *Store) loadSchemaFile() {
	if s.schemaFile == "" {
		return
	}
	data, err := os.ReadFile(s.schemaFile)
	if err != nil {
		return
	}
	var stored struct {
		Collections []string                     `json:"collections"`
		Fields      map[string][]CollectionField `json:"fields"`
	}
	if err := json.Unmarshal(data, &stored); err == nil {
		for _, name := range stored.Collections {
			s.customCollections[name] = true
		}
		if stored.Fields != nil {
			s.customCollectionFields = stored.Fields
		}
	}
}

func (s *Store) saveSchemaFileLocked() error {
	if s.schemaFile == "" {
		return nil
	}
	var names []string
	for n := range s.customCollections {
		names = append(names, n)
	}
	stored := struct {
		Collections []string                     `json:"collections"`
		Fields      map[string][]CollectionField `json:"fields"`
	}{
		Collections: names,
		Fields:      s.customCollectionFields,
	}
	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.schemaFile, data, 0644)
}

func (s *Store) saveRecordDisk(rec *Record) error {
	if !isValidDataID(rec.DataID) {
		return fmt.Errorf("invalid data_id: %q contains forbidden characters or path traversal", rec.DataID)
	}
	path := filepath.Clean(filepath.Join(s.recordsDir, rec.DataID+".json"))
	if !strings.HasPrefix(path, filepath.Clean(s.recordsDir)+string(filepath.Separator)) {
		return fmt.Errorf("security violation: path traversal detected for data_id %q", rec.DataID)
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
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

	if rec == nil || rec.DataID == "" || !isValidDataID(rec.DataID) {
		return nil, errors.New("empty or invalid data_id in replicated record")
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
		// Security Check 1: Prevent ownership hijacking via replication
		if existing.UserPubAddress != "" && rec.UserPubAddress != existing.UserPubAddress {
			return nil, errors.New("security violation: cannot change owner of an existing record via replication")
		}

		if rec.Version < existing.Version {
			return existing, nil
		}
		if rec.Version == existing.Version && existing.Text == rec.Text {
			return existing, nil
		}

		// Security Check 2: If the existing record is owned, verify owner or editor signature on the incoming update
		if existing.UserPubAddress != "" {
			if rec.OwnerSignature == "" {
				return nil, errors.New("unauthorized: replicated record update is missing required owner signature")
			}

			textHash := crypto.HashBytes([]byte(rec.Text))
			toSign1 := []byte(fmt.Sprintf("%s:%d:%s", rec.DataID, rec.Version, rec.Text))
			toSign2 := []byte(fmt.Sprintf("%s:%d:%s", rec.DataID, rec.Version, textHash))
			toSign3 := []byte(fmt.Sprintf("%s:%d:%s", rec.DataID, rec.Version, rec.MerkleRoot))
			toSign4 := []byte(fmt.Sprintf("%s:%s", rec.DataID, rec.Text))

			valid := crypto.VerifyHybridSignature(toSign1, existing.UserPubAddress, rec.OwnerSignature) ||
				crypto.VerifyHybridSignature(toSign2, existing.UserPubAddress, rec.OwnerSignature) ||
				crypto.VerifyHybridSignature(toSign3, existing.UserPubAddress, rec.OwnerSignature) ||
				crypto.VerifyHybridSignature(toSign4, existing.UserPubAddress, rec.OwnerSignature)

			if !valid {
				for _, acl := range existing.ACL {
					if acl.Permission == PermEdit {
						if crypto.VerifyHybridSignature(toSign1, acl.UserPubKey, rec.OwnerSignature) ||
							crypto.VerifyHybridSignature(toSign2, acl.UserPubKey, rec.OwnerSignature) ||
							crypto.VerifyHybridSignature(toSign3, acl.UserPubKey, rec.OwnerSignature) ||
							crypto.VerifyHybridSignature(toSign4, acl.UserPubKey, rec.OwnerSignature) {
							valid = true
							break
						}
					}
				}
			}

			if !valid {
				return nil, errors.New("unauthorized: invalid owner signature for replicated record update")
			}
		}

		if rec.FileChunkHash == "" && existing.FileChunkHash != "" {
			rec.FileChunkHash = existing.FileChunkHash
		}
		if rec.PictureChunkHash == "" && existing.PictureChunkHash != "" {
			rec.PictureChunkHash = existing.PictureChunkHash
		}
	} else {
		// New record replicated from peer: if it has an owner, verify owner signature
		if rec.UserPubAddress != "" {
			if rec.OwnerSignature == "" {
				return nil, errors.New("unauthorized: new replicated record is missing required owner signature")
			}
			textHash := crypto.HashBytes([]byte(rec.Text))
			toSign1 := []byte(fmt.Sprintf("%s:%s:%s", rec.Collection, rec.Text, rec.UserPubAddress))
			toSign2 := []byte(fmt.Sprintf("%s:%d:%s", rec.DataID, 1, rec.Text))
			toSign3 := []byte(fmt.Sprintf("%s:create:%s", rec.DataID, rec.Text))
			toSign4 := []byte(fmt.Sprintf("%s:%d:%s", rec.DataID, rec.Version, rec.Text))
			toSign5 := []byte(fmt.Sprintf("%s:%d:%s", rec.DataID, rec.Version, textHash))
			toSign6 := []byte(fmt.Sprintf("%s:%s", rec.DataID, rec.Text))

			valid := crypto.VerifyHybridSignature(toSign1, rec.UserPubAddress, rec.OwnerSignature) ||
				crypto.VerifyHybridSignature(toSign2, rec.UserPubAddress, rec.OwnerSignature) ||
				crypto.VerifyHybridSignature(toSign3, rec.UserPubAddress, rec.OwnerSignature) ||
				crypto.VerifyHybridSignature(toSign4, rec.UserPubAddress, rec.OwnerSignature) ||
				crypto.VerifyHybridSignature(toSign5, rec.UserPubAddress, rec.OwnerSignature) ||
				crypto.VerifyHybridSignature(toSign6, rec.UserPubAddress, rec.OwnerSignature)

			if !valid {
				return nil, errors.New("unauthorized: invalid owner signature for new replicated record")
			}
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
