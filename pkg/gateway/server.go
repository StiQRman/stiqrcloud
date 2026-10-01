package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

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

	s.setupRoutes()
	return s
}

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler {
	return s.mux
}

func (s *Server) setupRoutes() {
	// Health & Version
	s.mux.HandleFunc("/api/health", s.handleHealth)
	s.mux.HandleFunc("/api/version", s.handleVersion)

	// Records & Collections API
	s.mux.HandleFunc("/api/records", s.handleRecordsRoot)
	s.mux.HandleFunc("/api/records/", s.handleRecordDetail)
	s.mux.HandleFunc("/api/collections/", s.handleCollections)

	// Dashboard Integration
	dash := dashboard.NewServer(
		s.nodeID,
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
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":      "ok",
		"app_name":    version.AppName,
		"version":     version.CurrentVersion,
		"node_id":     s.nodeID,
		"standalone":  s.standalone,
		"is_public":   s.isPublic,
		"rns_dest":    s.rnsDest,
	})
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	status := version.CheckUpdate()
	_ = json.NewEncoder(w).Encode(status)
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
			contract, err := s.escrowMgr.CreateContract(created.DataID, created.TotalBytes, 1, rates, auditors)
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

// PocketBase compatibility endpoints: /api/collections/:col/records
func (s *Server) handleCollections(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) >= 4 && parts[1] == "collections" && parts[3] == "records" {
		col := parts[2]
		if r.Method == http.MethodGet {
			records, total := s.store.ListRecords(col, 50, 0)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"items":      records,
				"totalItems": total,
				"page":       1,
				"perPage":    50,
			})
			return
		}
	}
	s.handleRecordsRoot(w, r)
}
