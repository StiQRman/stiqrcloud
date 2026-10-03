package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/stiqrhub/testcloud/pkg/audit"
	"github.com/stiqrhub/testcloud/pkg/dashboard"
	"github.com/stiqrhub/testcloud/pkg/escrow"
	"github.com/stiqrhub/testcloud/pkg/p2p"
	"github.com/stiqrhub/testcloud/pkg/reticulum"
	"github.com/stiqrhub/testcloud/pkg/storage"
	"github.com/stiqrhub/testcloud/pkg/version"
)

// Server coordinates HTTP API routing and services.
type Server struct {
	nodeID     string
	name       string
	location   string
	publicIP   string
	store      *storage.Store
	swarm      *p2p.Swarm
	escrowMgr  *escrow.Manager
	auditor    *audit.Coordinator
	rnsManager *reticulum.InterfaceManager
	rnsDest    string
	isPublic   bool
	standalone bool
	mux        *http.ServeMux
}

// Config holds gateway server options.
type Config struct {
	NodeID     string
	Name       string
	Location   string
	PublicIP   string
	Store      *storage.Store
	Swarm      *p2p.Swarm
	EscrowMgr  *escrow.Manager
	Auditor    *audit.Coordinator
	RNSManager *reticulum.InterfaceManager
	RNSDest    string
	IsPublic   bool
	Standalone bool
}

// NewServer builds the HTTP gateway server.
func NewServer(cfg Config) *Server {
	s := &Server{
		nodeID:     cfg.NodeID,
		name:       cfg.Name,
		location:   cfg.Location,
		publicIP:   cfg.PublicIP,
		store:      cfg.Store,
		swarm:      cfg.Swarm,
		escrowMgr:  cfg.EscrowMgr,
		auditor:    cfg.Auditor,
		rnsManager: cfg.RNSManager,
		rnsDest:    cfg.RNSDest,
		isPublic:   cfg.IsPublic,
		standalone: cfg.Standalone,
		mux:        http.NewServeMux(),
	}

	// Wire contract checker for expired/unpaid data eviction
	if s.store != nil && s.escrowMgr != nil {
		s.store.SetContractChecker(func(dataID string) (bool, time.Time) {
			c, exists := s.escrowMgr.GetContractForDataID(dataID)
			if !exists {
				return false, time.Time{}
			}
			return s.escrowMgr.IsContractExpired(c.ContractID)
		})
	}

	s.setupRoutes()
	return s
}

// Handler returns the HTTP handler with CORS support.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		w.Header().Set("Access-Control-Expose-Headers", "*")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		s.mux.ServeHTTP(w, r)
	})
}

func (s *Server) setupRoutes() {
	// Health & Version
	s.mux.HandleFunc("/api/health", s.handleHealth)
	s.mux.HandleFunc("/api/version", s.handleVersion)

	// Records & Collections API
	s.mux.HandleFunc("/api/records", s.handleRecordsRoot)
	s.mux.HandleFunc("/api/records/", s.handleRecordDetail)
	s.mux.HandleFunc("/api/collections/", s.handleCollections)
	s.mux.HandleFunc("/api/files/", s.handleFiles)

	// StiQRcloud SDK Compatibility Endpoints
	s.mux.HandleFunc("/api/store", s.handleSDKStore)
	s.mux.HandleFunc("/api/retrieve/", s.handleSDKRetrieve)
	s.mux.HandleFunc("/api/edit/", s.handleSDKEdit)
	s.mux.HandleFunc("/api/delete/", s.handleSDKDelete)
	s.mux.HandleFunc("/api/status", s.handleSDKStatus)
	s.mux.HandleFunc("/api/contracts", s.handleSDKContracts)
	s.mux.HandleFunc("/api/history", s.handleSDKHistory)

	// Mesh Peers & Discovery API
	s.mux.HandleFunc("/api/peers", s.handlePeers)
	s.mux.HandleFunc("/api/discovery/announce", s.handleDiscoveryAnnounce)
	s.mux.HandleFunc("/api/discovery/peers", s.handleDiscoveryPeers)

	// Internal Replication Storage
	s.mux.HandleFunc("/api/internal/store", s.handleInternalStore)

	// Escrow & Storage Monetization API
	s.mux.HandleFunc("/api/escrow/policy", s.handleEscrowPolicy)
	s.mux.HandleFunc("/api/escrow/whitelist", s.handleEscrowWhitelist)
	s.mux.HandleFunc("/api/storage/quota", s.handleStorageQuota)

	// Auditing Trigger API
	s.mux.HandleFunc("/api/audit/trigger", s.handleAuditTrigger)

	// Dashboard Integration
	dash := dashboard.NewServer(
		s.nodeID,
		s.name,
		s.location,
		s.publicIP,
		s.store,
		s.swarm,
		s.escrowMgr,
		s.auditor,
		s.rnsManager,
		s.rnsDest,
		s.isPublic,
	)
	dash.RegisterRoutes(s.mux)

	// Root redirect to dashboard
	s.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/dashboard", http.StatusFound)
			return
		}
		http.NotFound(w, r)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	peerCount := 0
	if s.swarm != nil {
		peerCount = len(s.swarm.ListPeers())
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"code":        200,
		"status":      "ok",
		"message":     "TestCloud decentralized backend is healthy.",
		"app_name":    version.AppName,
		"version":     version.CurrentVersion,
		"node_id":     s.nodeID,
		"standalone":  s.standalone,
		"is_public":   s.isPublic,
		"rns_dest":    s.rnsDest,
		"data": map[string]interface{}{
			"canBackup": true,
			"peers":     peerCount,
		},
	})
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	status := version.CheckUpdate()
	_ = json.NewEncoder(w).Encode(status)
}

func (s *Server) handlePeers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if s.swarm == nil {
		_ = json.NewEncoder(w).Encode([]*p2p.Peer{})
		return
	}

	switch r.Method {
	case http.MethodGet:
		peers := s.swarm.ListPeers()
		_ = json.NewEncoder(w).Encode(peers)

	case http.MethodPost:
		var p p2p.Peer
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			http.Error(w, "invalid peer payload", http.StatusBadRequest)
			return
		}
		if p.NodeID != "" && p.NodeID != s.nodeID {
			s.swarm.RegisterPeer(&p)
		}
		peers := s.swarm.ListPeers()
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "registered",
			"node_id": p.NodeID,
			"peers":   peers,
		})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleInternalStore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var rec storage.Record
	if err := json.NewDecoder(r.Body).Decode(&rec); err != nil {
		http.Error(w, "invalid record payload", http.StatusBadRequest)
		return
	}

	created, err := s.store.CreateRecord(&rec, nil, nil)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to store replicated record: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "stored",
		"data_id": created.DataID,
	})
}

func (s *Server) handleRecordsRoot(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		collection := r.URL.Query().Get("collection")
		records, total := s.store.ListRecords(collection, 50, 0)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"records": records,
			"total":   total,
		})

	case http.MethodPost:
		s.handleCreateRecord(w, r)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleCreateRecord(w http.ResponseWriter, r *http.Request) {
	// Parse multipart or JSON
	var text, userPubKey, sig, collection, dataID string
	var fileBytes, picBytes []byte
	var fileName, picName string

	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "multipart/form-data") {
		_ = r.ParseMultipartForm(32 << 20) // 32MB max in memory
		text = r.FormValue("text")
		userPubKey = r.FormValue("user_pub_address")
		sig = r.FormValue("signature")
		collection = r.FormValue("collection")
		dataID = r.FormValue("data_id")

		if file, header, err := r.FormFile("file"); err == nil {
			fileBytes, _ = io.ReadAll(file)
			fileName = header.Filename
			_ = file.Close()
		}
		if pic, header, err := r.FormFile("picture"); err == nil {
			picBytes, _ = io.ReadAll(pic)
			picName = header.Filename
			_ = pic.Close()
		}
	} else {
		var body struct {
			DataID         string `json:"data_id"`
			Collection     string `json:"collection"`
			UserPubAddress string `json:"user_pub_address"`
			Signature      string `json:"signature"`
			Text           string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
			text = body.Text
			userPubKey = body.UserPubAddress
			sig = body.Signature
			collection = body.Collection
			dataID = body.DataID
		}
	}

	if collection == "" {
		collection = "default"
	}

	rec := &storage.Record{
		DataID:         dataID,
		Collection:     collection,
		UserPubAddress: userPubKey,
		OwnerSignature: sig,
		Text:           text,
		FileName:       fileName,
		PictureName:    picName,
	}

	created, err := s.store.CreateRecord(rec, fileBytes, picBytes)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to create record: %v", err), http.StatusBadRequest)
		return
	}

	// In network mode: engage 3-auditor quorum and 3-storage nodes
	var auditors, storageNodes []string
	var invoice string
	if !s.standalone && s.swarm != nil {
		var qErr error
		auditors, storageNodes, qErr = s.swarm.SelectQuorum()
		if qErr == nil {
			created.AuditorPeers = auditors
			created.StoragePeers = storageNodes

			// Formulate contract in escrow
			rates := make(map[string]int64)
			for _, st := range storageNodes {
				rates[st] = 100 // default 100 sats
			}
			contract, err := s.escrowMgr.CreateContract(created.DataID, created.UserPubAddress, created.TotalBytes, 1, rates, auditors)
			if err == nil {
				invoice = contract.LightningInvoice
			}

			// Broadcast chunks to storage nodes
			_ = s.swarm.BroadcastRecord(created, storageNodes, fileBytes)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"record":            created,
		"data_id":           created.DataID, // Invariant persistent ID
		"auditor_peers":     created.AuditorPeers,
		"storage_peers":     created.StoragePeers,
		"lightning_invoice": invoice,
	})
}

func (s *Server) handleRecordDetail(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	// e.g. ["api", "records", ":data_id", optional ":sub"]
	if len(parts) < 3 {
		http.NotFound(w, r)
		return
	}

	dataID := parts[2]

	// Handle sub-resources: /file or /picture or /collaborators
	if len(parts) >= 4 {
		sub := parts[3]
		switch sub {
		case "file":
			rec, err := s.store.GetRecord(dataID)
			if err != nil || rec.FileChunkHash == "" {
				http.NotFound(w, r)
				return
			}
			chunk, err := s.store.GetChunk(rec.FileChunkHash)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", rec.FileName))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(chunk)
			return

		case "picture":
			rec, err := s.store.GetRecord(dataID)
			if err != nil || rec.PictureChunkHash == "" {
				http.NotFound(w, r)
				return
			}
			chunk, err := s.store.GetChunk(rec.PictureChunkHash)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "image/png")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(chunk)
			return

		case "collaborators":
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			var entry storage.AccessControlEntry
			var ownerPubKey string
			var body struct {
				OwnerPubKey string                     `json:"owner_pub_key"`
				Collaborator storage.AccessControlEntry `json:"collaborator"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "invalid request body", http.StatusBadRequest)
				return
			}
			entry = body.Collaborator
			ownerPubKey = body.OwnerPubKey

			if err := s.store.AddCollaborator(dataID, ownerPubKey, entry); err != nil {
				http.Error(w, err.Error(), http.StatusForbidden)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "collaborator added"})
			return
		}
	}

	switch r.Method {
	case http.MethodGet:
		// Universal Data ID Resolution:
		// If local node doesn't hold it, resolve across peer mesh!
		var rec *storage.Record
		var err error
		if s.swarm != nil && !s.standalone {
			rec, err = s.swarm.ResolveDataID(dataID)
		} else {
			rec, err = s.store.GetRecord(dataID)
		}

		if err != nil {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rec)

	case http.MethodPatch, http.MethodPut:
		// Update record while GUARANTEEING invariant DataID stays identical
		var body struct {
			UpdaterPubKey string `json:"updater_pub_key"`
			Signature     string `json:"signature"`
			Text          string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		updated, err := s.store.UpdateRecord(dataID, body.UpdaterPubKey, body.Signature, body.Text, nil, nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(updated)

	case http.MethodDelete:
		callerPubKey := r.Header.Get("X-User-PubKey")
		sig := r.Header.Get("X-User-Signature")
		if err := s.store.DeleteRecord(dataID, callerPubKey, sig); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "deleted", "data_id": dataID})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func formatPBRecord(rec *storage.Record) map[string]interface{} {
	res := make(map[string]interface{})
	if rec == nil {
		return res
	}

	// Try unmarshaling Text as JSON map if possible
	trimmed := strings.TrimSpace(rec.Text)
	if strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}") {
		_ = json.Unmarshal([]byte(trimmed), &res)
	}

	// Ensure PocketBase and TestCloud standard fields are present
	res["id"] = rec.DataID
	res["data_id"] = rec.DataID
	res["collectionId"] = rec.Collection
	res["collectionName"] = rec.Collection
	res["created"] = rec.CreatedAt.UTC().Format("2006-01-02 15:04:05.000Z")
	res["updated"] = rec.UpdatedAt.UTC().Format("2006-01-02 15:04:05.000Z")
	res["version"] = rec.Version

	if _, ok := res["text"]; !ok && rec.Text != "" {
		res["text"] = rec.Text
	}
	if _, ok := res["content"]; !ok && rec.Text != "" {
		res["content"] = rec.Text
	}
	if rec.FileName != "" {
		res["file_name"] = rec.FileName
		res["field"] = []string{rec.FileName}
	}
	if len(rec.StoragePeers) > 0 {
		res["storage_peers"] = rec.StoragePeers
	}
	if len(rec.AuditorPeers) > 0 {
		res["auditor_peers"] = rec.AuditorPeers
	}
	return res
}

func (s *Server) handleCreatePBRecord(w http.ResponseWriter, r *http.Request, collection string) {
	var text, userPubKey, sig, dataID string
	var fileBytes, picBytes []byte
	var fileName, picName string
	rawBodyMap := make(map[string]interface{})

	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "multipart/form-data") {
		_ = r.ParseMultipartForm(32 << 20)
		if r.MultipartForm != nil {
			for k, v := range r.MultipartForm.Value {
				if len(v) > 0 {
					var nested interface{}
					if err := json.Unmarshal([]byte(v[0]), &nested); err == nil {
						rawBodyMap[k] = nested
					} else {
						rawBodyMap[k] = v[0]
					}
				}
			}
			for _, fileHeaders := range r.MultipartForm.File {
				for _, fh := range fileHeaders {
					if f, err := fh.Open(); err == nil {
						data, _ := io.ReadAll(f)
						_ = f.Close()
						if len(data) > 0 {
							if strings.HasSuffix(strings.ToLower(fh.Filename), ".png") || strings.HasSuffix(strings.ToLower(fh.Filename), ".jpg") {
								picBytes = data
								picName = fh.Filename
							} else {
								fileBytes = data
								fileName = fh.Filename
							}
						}
					}
				}
			}
		}
	} else {
		rawBytes, _ := io.ReadAll(r.Body)
		if len(rawBytes) > 0 {
			if err := json.Unmarshal(rawBytes, &rawBodyMap); err != nil {
				text = string(rawBytes)
			}
		}
	}

	// Extract standard or custom fields
	if id, ok := rawBodyMap["id"].(string); ok && id != "" {
		dataID = id
	}
	if pk, ok := rawBodyMap["user_pub_address"].(string); ok {
		userPubKey = pk
	} else if pk, ok := rawBodyMap["owner_pubkey"].(string); ok {
		userPubKey = pk
	}
	if sVal, ok := rawBodyMap["signature"].(string); ok {
		sig = sVal
	} else if sVal, ok := rawBodyMap["owner_signature"].(string); ok {
		sig = sVal
	}

	if text == "" {
		if t, ok := rawBodyMap["text"].(string); ok && t != "" {
			text = t
		} else if c, ok := rawBodyMap["content"].(string); ok && c != "" {
			text = c
		} else if len(rawBodyMap) > 0 {
			b, _ := json.Marshal(rawBodyMap)
			text = string(b)
		}
	}

	rec := &storage.Record{
		DataID:         dataID,
		Collection:     collection,
		UserPubAddress: userPubKey,
		OwnerSignature: sig,
		Text:           text,
		FileName:       fileName,
		PictureName:    picName,
	}

	created, err := s.store.CreateRecord(rec, fileBytes, picBytes)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to create record: %v", err), http.StatusBadRequest)
		return
	}

	// In network mode: engage quorum
	if !s.standalone && s.swarm != nil {
		auditors, storageNodes, qErr := s.swarm.SelectQuorum()
		if qErr == nil {
			created.AuditorPeers = auditors
			created.StoragePeers = storageNodes

			rates := make(map[string]int64)
			for _, st := range storageNodes {
				rates[st] = 100
			}
			_, _ = s.escrowMgr.CreateContract(created.DataID, created.UserPubAddress, created.TotalBytes, 1, rates, auditors)
			_ = s.swarm.BroadcastRecord(created, storageNodes, fileBytes)
		}
	}

	formatted := formatPBRecord(created)
	for k, v := range rawBodyMap {
		if _, ok := formatted[k]; !ok {
			formatted[k] = v
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(formatted)
}

// PocketBase compatibility endpoints: /api/collections/:col/records[/:id] and /api/collections/users/...
func (s *Server) handleCollections(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	// e.g. ["api", "collections", ":col", "records", optional ":id"]
	// or ["api", "collections", "users", "auth-with-password"]

	if len(parts) >= 3 && parts[1] == "collections" {
		col := parts[2]

		// Mock Auth for PocketBase client apps
		if col == "users" && len(parts) >= 4 {
			sub := parts[3]
			if sub == "auth-with-password" || sub == "auth-with-oauth2" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"token": "testcloud_mock_jwt_token",
					"record": map[string]interface{}{
						"id":       "user_local_owner",
						"email":    "owner@testcloud.mesh",
						"name":     "TestCloud Owner",
						"verified": true,
					},
				})
				return
			}
		}

		if len(parts) >= 4 && parts[3] == "records" {
			recordID := ""
			if len(parts) >= 5 {
				recordID = parts[4]
			}

			if recordID != "" {
				switch r.Method {
				case http.MethodGet:
					var rec *storage.Record
					var err error
					if s.swarm != nil && !s.standalone {
						rec, err = s.swarm.ResolveDataID(recordID)
					} else {
						rec, err = s.store.GetRecord(recordID)
					}
					if err != nil {
						http.NotFound(w, r)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(formatPBRecord(rec))
					return

				case http.MethodPatch, http.MethodPut:
					var body map[string]interface{}
					_ = json.NewDecoder(r.Body).Decode(&body)

					updaterPubKey, _ := body["updater_pub_key"].(string)
					sig, _ := body["signature"].(string)
					text, _ := body["text"].(string)
					if text == "" {
						if c, ok := body["content"].(string); ok {
							text = c
						} else if len(body) > 0 {
							b, _ := json.Marshal(body)
							text = string(b)
						}
					}

					updated, err := s.store.UpdateRecord(recordID, updaterPubKey, sig, text, nil, nil)
					if err != nil {
						http.Error(w, err.Error(), http.StatusBadRequest)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(formatPBRecord(updated))
					return

				case http.MethodDelete:
					_ = s.store.DeleteRecord(recordID, "", "")
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]string{"status": "deleted", "id": recordID})
					return
				}
			} else {
				if r.Method == http.MethodGet {
					records, total := s.store.ListRecords(col, 50, 0)
					var formattedItems []map[string]interface{}
					for _, rec := range records {
						formattedItems = append(formattedItems, formatPBRecord(rec))
					}
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]interface{}{
						"items":      formattedItems,
						"totalItems": total,
						"page":       1,
						"perPage":    50,
						"totalPages": (total + 49) / 50,
					})
					return
				} else if r.Method == http.MethodPost {
					s.handleCreatePBRecord(w, r, col)
					return
				}
			}
		}
	}
	s.handleRecordsRoot(w, r)
}

// handleFiles handles PocketBase file downloads: /api/files/:col/:id/:filename
func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		http.NotFound(w, r)
		return
	}
	dataID := parts[3]
	var rec *storage.Record
	var err error
	if s.swarm != nil && !s.standalone {
		rec, err = s.swarm.ResolveDataID(dataID)
	} else {
		rec, err = s.store.GetRecord(dataID)
	}
	if err != nil {
		http.NotFound(w, r)
		return
	}

	hash := rec.FileChunkHash
	name := rec.FileName
	contentType := "application/octet-stream"

	if rec.PictureChunkHash != "" && (hash == "" || strings.HasSuffix(strings.ToLower(r.URL.Path), ".png") || strings.HasSuffix(strings.ToLower(r.URL.Path), ".jpg")) {
		hash = rec.PictureChunkHash
		name = rec.PictureName
		contentType = "image/png"
	}

	if hash == "" {
		http.NotFound(w, r)
		return
	}

	chunk, err := s.store.GetChunk(hash)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", name))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(chunk)
}

func (s *Server) handleEscrowPolicy(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if s.escrowMgr == nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": "escrow manager disabled"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		policy := s.escrowMgr.GetPolicy()
		stats := s.store.StorageStats()
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"free_for_all":        policy.FreeForAll,
			"price_per_gb_month": policy.PricePerGBMonth,
			"free_whitelist":      policy.FreeWhitelist,
			"max_bytes":           stats.MaxBytes,
			"used_bytes":          stats.UsedBytes,
			"active_bytes":        stats.ActiveBytes,
			"grace_bytes":         stats.GraceBytes,
			"grace_record_count":  stats.GraceRecordCount,
		})

	case http.MethodPost:
		var req struct {
			FreeForAll      *bool    `json:"free_for_all"`
			PricePerGBMonth *int64   `json:"price_per_gb_month"`
			FreeWhitelist   []string `json:"free_whitelist"`
			MaxBytes        *int64   `json:"max_bytes"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		current := s.escrowMgr.GetPolicy()
		if req.FreeForAll != nil {
			current.FreeForAll = *req.FreeForAll
		}
		if req.PricePerGBMonth != nil && *req.PricePerGBMonth > 0 {
			current.PricePerGBMonth = *req.PricePerGBMonth
		}
		if req.FreeWhitelist != nil {
			current.FreeWhitelist = req.FreeWhitelist
		}
		_ = s.escrowMgr.UpdatePolicy(current)

		if req.MaxBytes != nil && *req.MaxBytes > 0 && s.store != nil {
			s.store.SetMaxDiskBytes(*req.MaxBytes)
		}

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "updated",
			"policy": s.escrowMgr.GetPolicy(),
		})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleEscrowWhitelist(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Action  string `json:"action"` // "add" or "remove"
		Address string `json:"address"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if strings.EqualFold(req.Action, "remove") {
		_ = s.escrowMgr.RemoveWhitelist(req.Address)
	} else {
		_ = s.escrowMgr.AddWhitelist(req.Address)
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":         "ok",
		"free_whitelist": s.escrowMgr.GetPolicy().FreeWhitelist,
	})
}

func (s *Server) handleStorageQuota(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if s.store == nil {
		http.Error(w, "store not available", http.StatusInternalServerError)
		return
	}

	switch r.Method {
	case http.MethodGet:
		stats := s.store.StorageStats()
		_ = json.NewEncoder(w).Encode(stats)

	case http.MethodPost:
		var req struct {
			MaxBytes int64 `json:"max_bytes"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.MaxBytes <= 0 {
			http.Error(w, "invalid max_bytes", http.StatusBadRequest)
			return
		}
		s.store.SetMaxDiskBytes(req.MaxBytes)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":    "updated",
			"max_bytes": req.MaxBytes,
		})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleAuditTrigger(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if s.auditor == nil {
		http.Error(w, "auditor not enabled", http.StatusBadRequest)
		return
	}
	results := s.auditor.RunAuditCycle()
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "completed",
		"results": results,
		"count":   len(results),
	})
}

func (s *Server) handleSDKStore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		OwnerPubKey string `json:"owner_pubkey"`
		Signature   string `json:"signature"`
		Content     string `json:"content"`
		DurationSec int    `json:"duration_sec"`
	}

	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json payload", http.StatusBadRequest)
		return
	}

	rec := &storage.Record{
		Collection:     "default",
		UserPubAddress: body.OwnerPubKey,
		OwnerSignature: body.Signature,
		Text:           body.Content,
	}

	created, err := s.store.CreateRecord(rec, nil, nil)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to store record: %v", err), http.StatusBadRequest)
		return
	}

	// Engage quorum if in network mode
	if !s.standalone && s.swarm != nil {
		auditors, storageNodes, qErr := s.swarm.SelectQuorum()
		if qErr == nil {
			created.AuditorPeers = auditors
			created.StoragePeers = storageNodes
			rates := make(map[string]int64)
			for _, st := range storageNodes {
				rates[st] = 100
			}
			_, _ = s.escrowMgr.CreateContract(created.DataID, created.UserPubAddress, created.TotalBytes, 1, rates, auditors)
			_ = s.swarm.BroadcastRecord(created, storageNodes, nil)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":        "stored",
		"data_id":       created.DataID,
		"id":            created.DataID,
		"version":       created.Version,
		"storage_peers": created.StoragePeers,
		"auditor_peers": created.AuditorPeers,
	})
}

func (s *Server) handleSDKRetrieve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	dataID := strings.TrimPrefix(r.URL.Path, "/api/retrieve/")
	dataID = strings.Trim(dataID, "/")
	if dataID == "" {
		http.Error(w, "data_id required", http.StatusBadRequest)
		return
	}

	var rec *storage.Record
	var err error
	if s.swarm != nil && !s.standalone {
		rec, err = s.swarm.ResolveDataID(dataID)
	} else {
		rec, err = s.store.GetRecord(dataID)
	}

	if err != nil {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(rec.Text))
}

func (s *Server) handleSDKEdit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	dataID := strings.TrimPrefix(r.URL.Path, "/api/edit/")
	dataID = strings.Trim(dataID, "/")
	if dataID == "" {
		http.Error(w, "data_id required", http.StatusBadRequest)
		return
	}

	var body struct {
		OwnerPubKey string `json:"owner_pubkey"`
		Signature   string `json:"signature"`
		Content     string `json:"content"`
		Version     int64  `json:"version"`
	}

	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json payload", http.StatusBadRequest)
		return
	}

	updated, err := s.store.UpdateRecord(dataID, body.OwnerPubKey, body.Signature, body.Content, nil, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "edited",
		"data_id": dataID,
		"version": updated.Version,
	})
}

func (s *Server) handleSDKDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	dataID := strings.TrimPrefix(r.URL.Path, "/api/delete/")
	dataID = strings.Trim(dataID, "/")
	if dataID == "" {
		http.Error(w, "data_id required", http.StatusBadRequest)
		return
	}

	var body struct {
		OwnerPubKey string `json:"owner_pubkey"`
		Signature   string `json:"signature"`
		Version     int64  `json:"version"`
	}

	_ = json.NewDecoder(r.Body).Decode(&body)

	err := s.store.DeleteRecord(dataID, body.OwnerPubKey, body.Signature)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "deleted",
		"data_id": dataID,
	})
}

func (s *Server) handleSDKStatus(w http.ResponseWriter, r *http.Request) {
	peerCount := 0
	if s.swarm != nil {
		peerCount = len(s.swarm.ListPeers())
	}
	contractCount := 0
	if s.escrowMgr != nil {
		contractCount = len(s.escrowMgr.ListContracts())
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"node_id":        s.nodeID,
		"status":         "online",
		"peers":          peerCount,
		"wallet_sats":    100000,
		"wallet_balance": 100000,
		"contracts":      contractCount,
		"version":        "v0.1.0",
		"rns_dest":       s.rnsDest,
	})
}

func (s *Server) handleSDKContracts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if s.escrowMgr == nil {
		_ = json.NewEncoder(w).Encode([]interface{}{})
		return
	}
	contracts := s.escrowMgr.ListContracts()
	if contracts == nil {
		_ = json.NewEncoder(w).Encode([]interface{}{})
		return
	}
	_ = json.NewEncoder(w).Encode(contracts)
}

func (s *Server) handleSDKHistory(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode([]interface{}{})
}

func (s *Server) handleDiscoveryAnnounce(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		PeerID          string `json:"peer_id"`
		NodeID          string `json:"node_id"`
		Name            string `json:"name"`
		Nickname        string `json:"nickname"`
		Location        string `json:"location"`
		PublicIP        string `json:"public_ip"`
		PublicAddr      string `json:"public_addr"`
		ReticulumDest   string `json:"reticulum_dest"`
		IsPublic        bool   `json:"is_public"`
		PricePerGBMonth int64  `json:"price_per_gb_month"`
		Role            string `json:"role"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	id := req.PeerID
	if id == "" {
		id = req.NodeID
	}
	name := req.Name
	if name == "" {
		name = req.Nickname
	}

	if s.swarm != nil && id != "" && id != s.nodeID {
		s.swarm.RegisterPeer(&p2p.Peer{
			NodeID:          id,
			Name:            name,
			Location:        req.Location,
			PublicIP:        req.PublicIP,
			PublicAddr:      req.PublicAddr,
			ReticulumDest:   req.ReticulumDest,
			IsPublic:        req.IsPublic,
			PricePerGBMonth: req.PricePerGBMonth,
			IsOnline:        true,
			LastSeen:        time.Now().UTC(),
		})
	}

	var peers []*p2p.Peer
	if s.swarm != nil {
		peers = s.swarm.ListPeers()
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":       "registered",
		"total_online": len(peers),
		"peers":        peers,
	})
}

func (s *Server) handleDiscoveryPeers(w http.ResponseWriter, r *http.Request) {
	var peers []*p2p.Peer
	if s.swarm != nil {
		peers = s.swarm.ListPeers()
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"total_online": len(peers),
		"peers":        peers,
	})
}
