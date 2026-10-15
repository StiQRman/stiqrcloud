package gateway

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/stiqrhub/stiqrcloud/pkg/audit"
	"github.com/stiqrhub/stiqrcloud/pkg/crypto"
	"github.com/stiqrhub/stiqrcloud/pkg/dashboard"
	"github.com/stiqrhub/stiqrcloud/pkg/escrow"
	"github.com/stiqrhub/stiqrcloud/pkg/p2p"
	"github.com/stiqrhub/stiqrcloud/pkg/reticulum"
	"github.com/stiqrhub/stiqrcloud/pkg/storage"
	"github.com/stiqrhub/stiqrcloud/pkg/version"
)

// Server coordinates HTTP API routing and services.
type Server struct {
	nodeID      string
	name        string
	location    string
	publicIP    string
	gatewayURL  string
	publicAddr  string
	publicHosts []string
	store       *storage.Store
	swarm       *p2p.Swarm
	escrowMgr   *escrow.Manager
	auditor     *audit.Coordinator
	rnsManager  *reticulum.InterfaceManager
	rnsDest     string
	isPublic    bool
	standalone  bool
	mux         *http.ServeMux
	reconciling atomic.Bool
}

// Config holds gateway server options.
type Config struct {
	NodeID     string
	Name       string
	Location   string
	PublicIP   string
	GatewayURL string
	PublicAddr string
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
	var pubHosts []string
	for _, u := range []string{cfg.GatewayURL, cfg.PublicAddr} {
		if u != "" {
			parsed, err := url.Parse(u)
			if err == nil && parsed.Hostname() != "" {
				pubHosts = append(pubHosts, strings.ToLower(parsed.Hostname()))
			} else {
				h := strings.TrimPrefix(u, "http://")
				h = strings.TrimPrefix(h, "https://")
				h = strings.Split(h, "/")[0]
				h = strings.Split(h, ":")[0]
				if h != "" {
					pubHosts = append(pubHosts, strings.ToLower(h))
				}
			}
		}
	}

	s := &Server{
		nodeID:      cfg.NodeID,
		name:        cfg.Name,
		location:    cfg.Location,
		publicIP:    cfg.PublicIP,
		gatewayURL:  cfg.GatewayURL,
		publicAddr:  cfg.PublicAddr,
		publicHosts: pubHosts,
		store:       cfg.Store,
		swarm:       cfg.Swarm,
		escrowMgr:   cfg.EscrowMgr,
		auditor:     cfg.Auditor,
		rnsManager:  cfg.RNSManager,
		rnsDest:     cfg.RNSDest,
		isPublic:    cfg.IsPublic,
		standalone:  cfg.Standalone,
		mux:         http.NewServeMux(),
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

	// Periodic mesh reconciliation loop: ensures any records lacking replicas are backed up to peers
	if !s.standalone && s.swarm != nil && s.store != nil {
		go func() {
			time.Sleep(3 * time.Second)
			s.ReconcileAndReplicateAll()

			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			for range ticker.C {
				s.ReconcileAndReplicateAll()
			}
		}()
	}

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
	s.mux.HandleFunc("/api/collections/ensure", s.handleCollectionsEnsure)
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

	// Edge Node Sync & Content-Addressed Chunk Distribution API
	s.mux.HandleFunc("/api/sync/records", s.handleSyncRecords)
	s.mux.HandleFunc("/api/chunks/", s.handleChunks)

	// Escrow & Storage Monetization API (Admin - Local Only)
	s.mux.HandleFunc("/api/escrow/policy", s.localOnly(s.handleEscrowPolicy))
	s.mux.HandleFunc("/api/escrow/whitelist", s.localOnly(s.handleEscrowWhitelist))
	s.mux.HandleFunc("/api/escrow/blacklist", s.localOnly(s.handleEscrowBlacklist))
	s.mux.HandleFunc("/api/storage/quota", s.localOnly(s.handleStorageQuota))

	// Auditing Trigger API (Admin - Local Only)
	s.mux.HandleFunc("/api/audit/trigger", s.localOnly(s.handleAuditTrigger))

	// Binary Download & One-Line Installer API (Public)
	s.mux.HandleFunc("/download/", s.handleDownload)
	s.mux.HandleFunc("/install.sh", s.handleInstallScript)
	s.mux.HandleFunc("/install.ps1", s.handlePowerShellScript)

	// Dashboard Integration (Admin - Local Only)
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
	s.mux.HandleFunc("/dashboard", s.localOnly(dash.HandleDashboardHTML))
	s.mux.HandleFunc("/api/dashboard/stats", s.localOnly(dash.HandleStatsJSON))
	s.mux.HandleFunc("/api/dashboard/version", s.localOnly(dash.HandleVersionJSON))
	s.mux.HandleFunc("/api/dashboard/records", s.localOnly(dash.HandleRecordsJSON))
	s.mux.HandleFunc("/api/dashboard/collections", s.localOnly(dash.HandleCollectionsJSON))

	// Reticulum Peer-to-Peer Packet API
	s.mux.HandleFunc("/api/reticulum/send", s.handleReticulumSend)
	s.mux.HandleFunc("/api/reticulum/announce", s.handleReticulumAnnounce)
	s.mux.HandleFunc("/api/reticulum/status", s.handleReticulumStatus)

	if s.rnsManager != nil {
		s.rnsManager.SetPacketHandler(func(p *reticulum.Packet, addr net.Addr) {
			if p.Context == 0x01 && len(p.Payload) > 0 {
				payload := p.Payload
				// Transparently decompress if gzip magic header detected
				if len(payload) > 2 && payload[0] == 0x1f && payload[1] == 0x8b {
					if gz, err := gzip.NewReader(bytes.NewReader(payload)); err == nil {
						if decompressed, err := io.ReadAll(gz); err == nil {
							payload = decompressed
						}
						_ = gz.Close()
					}
				}
				var rec storage.Record
				if err := json.Unmarshal(payload, &rec); err == nil && rec.DataID != "" {
					log.Printf("[%s] [Reticulum] Direct record received: %s (collection: %s) from %s (%v)", s.nodeID, rec.DataID, rec.Collection, p.Destination.String(), addr)
					if s.store != nil {
						_, _ = s.store.UpsertReplicatedRecord(&rec, nil, nil)
					}
				}
			}
		})
	}

	// Root redirect: local requests redirect to /dashboard; public WAN requests receive gateway metadata
	s.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			if s.isLocalRequest(r) {
				http.Redirect(w, r, "/dashboard", http.StatusFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"app_name":    version.AppName,
				"version":     version.CurrentVersion,
				"status":      "ok",
				"message":     "StiQRcloud Decentralized Public Gateway & Discovery Rendezvous.",
				"node_id":     s.nodeID,
				"health":      "/api/health",
				"peers":       "/api/peers",
				"collections": "/api/collections/",
			})
			return
		}
		http.NotFound(w, r)
	})
}

// isLocalRequest returns true ONLY if the request originates from localhost or a private LAN IP.
// Any traffic arriving via Cloudflare Tunnel, public reverse proxies, or matching public domain names is rejected.
func (s *Server) isLocalRequest(r *http.Request) bool {
	// 1. Cloudflare Tunnel headers - if present, request came from public WAN via Cloudflare
	if r.Header.Get("CF-Ray") != "" || r.Header.Get("CF-Connecting-IP") != "" || r.Header.Get("CF-Visitor") != "" {
		return false
	}

	// 2. Check Host header: reject if it matches any configured public host (e.g. stiqrcloud.stiqrnotes.com)
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(strings.TrimSpace(host))
	for _, pubHost := range s.publicHosts {
		if pubHost != "" && host == pubHost {
			return false
		}
	}

	// Reject if host is an external domain (not an IP, not localhost, and not a local domain like .local/.lan)
	if parsedIP := net.ParseIP(host); parsedIP == nil {
		if host != "localhost" && !strings.HasSuffix(host, ".local") && !strings.HasSuffix(host, ".lan") && !strings.HasSuffix(host, ".home") {
			return false
		}
	}

	// 3. Check X-Forwarded-For if present: reject if any forwarded IP is public
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		for _, part := range parts {
			ipStr := strings.TrimSpace(part)
			ip := net.ParseIP(ipStr)
			if ip != nil && !isPrivateOrLoopbackIP(ip) {
				return false
			}
		}
	}

	// 4. Check RemoteAddr: must be loopback or private RFC 1918 LAN IP
	remoteHost := r.RemoteAddr
	if h, _, err := net.SplitHostPort(remoteHost); err == nil {
		remoteHost = h
	}
	remoteIP := net.ParseIP(remoteHost)
	if remoteIP == nil {
		return false
	}

	return isPrivateOrLoopbackIP(remoteIP)
}

func isPrivateOrLoopbackIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsPrivate()
}

// localOnly wraps a handler to enforce that only local LAN or localhost requests can access it.
func (s *Server) localOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.isLocalRequest(r) {
			if strings.Contains(r.Header.Get("Accept"), "text/html") || r.URL.Path == "/dashboard" {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <title>403 Forbidden - Local Access Only</title>
  <style>
    body { background: #0d1117; color: #c9d1d9; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; display: flex; align-items: center; justify-content: center; height: 100vh; margin: 0; }
    .card { background: #161b22; border: 1px solid #30363d; border-radius: 8px; padding: 32px; max-width: 520px; text-align: center; }
    h1 { color: #f85149; margin-bottom: 12px; font-size: 1.5rem; }
    p { line-height: 1.6; color: #8b949e; margin-bottom: 16px; font-size: 0.95rem; }
    code { background: #21262d; padding: 3px 8px; border-radius: 4px; color: #58a6ff; font-family: monospace; }
  </style>
</head>
<body>
  <div class="card">
    <h1>403 Forbidden</h1>
    <p>The StiQRcloud Node Dashboard is restricted to local network access only.</p>
    <p>Administrative and telemetry views cannot be accessed over the public internet gateway. To access your dashboard, connect directly via your local LAN address:</p>
    <p><code>http://&lt;local-ip&gt;:&lt;port&gt;/dashboard</code></p>
  </div>
</body>
</html>`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"code":    403,
				"status":  "forbidden",
				"message": "Access denied: This administrative endpoint is restricted to local network access only.",
			})
			return
		}
		next(w, r)
	}
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
		"message":     "StiQRcloud decentralized backend is healthy.",
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
			s.swarm.RegisterDirectPeer(&p)
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

	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	var payload struct {
		Record      *storage.Record `json:"record"`
		FileData    []byte          `json:"file_data"`
		PictureData []byte          `json:"picture_data"`
		DataID      string          `json:"data_id"`
	}

	var rec *storage.Record
	var fileData, picData []byte

	if err := json.Unmarshal(raw, &payload); err == nil && payload.Record != nil && payload.Record.DataID != "" {
		rec = payload.Record
		fileData = payload.FileData
		picData = payload.PictureData
	} else {
		var fallback storage.Record
		if err := json.Unmarshal(raw, &fallback); err == nil && fallback.DataID != "" {
			rec = &fallback
		}
	}

	if rec == nil || rec.DataID == "" {
		http.Error(w, "invalid record payload", http.StatusBadRequest)
		return
	}

	stored, err := s.store.UpsertReplicatedRecord(rec, fileData, picData)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to store replicated record: %v", err), http.StatusInternalServerError)
		return
	}

	// Ensure escrow contract exists locally
	if s.escrowMgr != nil {
		rates := make(map[string]int64)
		for _, st := range stored.StoragePeers {
			rates[st] = 100
		}
		_, _ = s.escrowMgr.CreateContract(stored.DataID, stored.UserPubAddress, stored.TotalBytes, 1, rates, stored.AuditorPeers)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "stored",
		"data_id": stored.DataID,
		"version": stored.Version,
	})
}

func (s *Server) handleSyncRecords(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	peerID := r.URL.Query().Get("peer_id")
	sinceStr := r.URL.Query().Get("since")
	all := r.URL.Query().Get("all") == "true"
	limit := 200
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if parsed, err := strconv.Atoi(lStr); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	var since time.Time
	if sinceStr != "" {
		since, _ = time.Parse(time.RFC3339, sinceStr)
	}

	if s.store == nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"records":   []*storage.Record{},
			"total":     0,
			"timestamp": time.Now().UTC(),
		})
		return
	}

	if peerID != "" && s.swarm != nil {
		s.swarm.TouchPeer(peerID)
	}

	allRecords := s.store.ListAllRecords()
	var assigned []*storage.Record

	for _, rec := range allRecords {
		if rec == nil {
			continue
		}
		// Local-only records never leave this node
		if rec.ReplicationPolicy == "local" || rec.ReplicationPolicy == "local-only" {
			continue
		}
		if !since.IsZero() && !rec.UpdatedAt.After(since) {
			continue
		}

		isAssigned := false
		if all || peerID == "" {
			isAssigned = true
		} else {
			for _, sp := range rec.StoragePeers {
				if sp == peerID {
					isAssigned = true
					break
				}
			}
			if !isAssigned {
				for _, ap := range rec.AuditorPeers {
					if ap == peerID {
						isAssigned = true
						break
					}
				}
			}
		}

		if isAssigned {
			assigned = append(assigned, rec)
		}
	}

	// Dynamic Quorum Backfill:
	// If peerID was specified and has fewer than 20 assigned records,
	// look for records that have < 3 storage peers or auto/stale peers, and assign peerID!
	if peerID != "" && !all && len(assigned) < 20 {
		for _, rec := range allRecords {
			if rec == nil {
				continue
			}
			// Never backfill local-only or custom-routed records
			if rec.ReplicationPolicy == "local" || rec.ReplicationPolicy == "local-only" || rec.ReplicationPolicy == "custom" {
				continue
			}
			if (rec.ReplicationPolicy == "replicated_2" || rec.ReplicationPolicy == "distributed_2" || rec.TargetReplicas == 2) && len(rec.StoragePeers) >= 2 {
				continue
			}
			if len(assigned) >= limit {
				break
			}
			alreadyIn := false
			for _, sp := range rec.StoragePeers {
				if sp == peerID {
					alreadyIn = true
					break
				}
			}
			if alreadyIn {
				continue
			}

			// Check if rec needs a storage peer
			hasAuto := false
			for _, sp := range rec.StoragePeers {
				if strings.Contains(sp, "auto") {
					hasAuto = true
					break
				}
			}
			if len(rec.StoragePeers) < 3 || hasAuto {
				newStorage := make([]string, 0, len(rec.StoragePeers)+1)
				replaced := false
				for _, sp := range rec.StoragePeers {
					if strings.Contains(sp, "auto") && !replaced {
						newStorage = append(newStorage, peerID)
						replaced = true
					} else {
						newStorage = append(newStorage, sp)
					}
				}
				if !replaced && len(newStorage) < 3 {
					newStorage = append(newStorage, peerID)
				}
				rec.StoragePeers = newStorage
				_ = s.store.SaveRecord(rec)
				assigned = append(assigned, rec)
			}
		}
	}

	// If assigned is still empty and peerID is specified, return active records
	// so newly connected edge node immediately receives mesh data to replicate!
	if len(assigned) == 0 && len(allRecords) > 0 {
		for _, rec := range allRecords {
			if rec == nil {
				continue
			}
			if rec.ReplicationPolicy == "local" || rec.ReplicationPolicy == "local-only" {
				continue
			}
			if !since.IsZero() && !rec.UpdatedAt.After(since) {
				continue
			}
			assigned = append(assigned, rec)
			if len(assigned) >= limit {
				break
			}
		}
	}

	if len(assigned) > limit {
		assigned = assigned[:limit]
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"records":   assigned,
		"total":     len(assigned),
		"timestamp": time.Now().UTC(),
	})
}

func (s *Server) handleChunks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	hash := strings.TrimPrefix(r.URL.Path, "/api/chunks/")
	hash = strings.Trim(hash, "/")
	if hash == "" {
		http.Error(w, "missing chunk hash", http.StatusBadRequest)
		return
	}

	if s.store == nil {
		http.NotFound(w, r)
		return
	}

	chunk, err := s.store.GetChunk(hash)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(len(chunk)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(chunk)
}

// replicateAndEngageQuorum selects healthy storage peers if needed, creates/updates the contract, and broadcasts replicas.
func (s *Server) replicateAndEngageQuorum(rec *storage.Record, fileBytes, picBytes []byte) error {
	if s.standalone || s.swarm == nil || rec == nil {
		return nil
	}

	// 1. Local-only policy: Data stays strictly on this node with zero network broadcast
	if rec.ReplicationPolicy == "local" || rec.ReplicationPolicy == "local-only" {
		rec.StoragePeers = nil
		_ = s.store.SaveRecord(rec)
		return nil
	}

	// 2. Custom storage peers explicitly chosen by user
	if len(rec.StoragePeers) > 0 && (rec.ReplicationPolicy == "custom" || rec.ReplicationPolicy == "") {
		if len(rec.AuditorPeers) < 3 {
			auditors, _, qErr := s.swarm.SelectQuorumForUser(rec.UserPubAddress, 3, 0)
			if qErr == nil {
				rec.AuditorPeers = auditors
				_ = s.store.SaveRecord(rec)
			}
		}
	} else if rec.ReplicationPolicy == "replicated_2" || rec.ReplicationPolicy == "distributed_2" || rec.TargetReplicas == 2 {
		// 3. User requested replication to 2 storage peers (biased to user affinity pod)
		peersOnline := 0
		hasAutoNode := false
		if len(rec.StoragePeers) > 0 {
			for _, sp := range rec.StoragePeers {
				if strings.Contains(sp, "auto") {
					hasAutoNode = true
					break
				}
				if p, ok := s.swarm.GetPeer(sp); ok && p.IsOnline && p.PublicAddr != "" {
					peersOnline++
				}
			}
		}
		if hasAutoNode || peersOnline < 1 || len(rec.StoragePeers) < 2 {
			auditors, storageNodes, qErr := s.swarm.SelectQuorumForUser(rec.UserPubAddress, 3, 2)
			if qErr == nil {
				rec.AuditorPeers = auditors
				rec.StoragePeers = storageNodes
				_ = s.store.SaveRecord(rec)
			}
		}
	} else {
		// 4. Default 3-node mesh quorum (biased to user affinity pod)
		peersOnline := 0
		hasAutoNode := false
		if len(rec.StoragePeers) > 0 {
			for _, sp := range rec.StoragePeers {
				if strings.Contains(sp, "auto") {
					hasAutoNode = true
					break
				}
				if p, ok := s.swarm.GetPeer(sp); ok && p.IsOnline && p.PublicAddr != "" {
					peersOnline++
				}
			}
		}

		if hasAutoNode || peersOnline < 2 || len(rec.StoragePeers) < 3 {
			auditors, storageNodes, qErr := s.swarm.SelectQuorumForUser(rec.UserPubAddress, 3, 3)
			if qErr == nil {
				rec.AuditorPeers = auditors
				rec.StoragePeers = storageNodes
				_ = s.store.SaveRecord(rec)
			}
		}
	}

	// Always ensure contract exists / updated in escrow
	rates := make(map[string]int64)
	for _, st := range rec.StoragePeers {
		rates[st] = 100
	}
	if s.escrowMgr != nil {
		_, _ = s.escrowMgr.CreateContract(rec.DataID, rec.UserPubAddress, rec.TotalBytes, 1, rates, rec.AuditorPeers)
	}

	// Fetch chunk bytes from store if not provided in caller
	if len(fileBytes) == 0 && rec.FileChunkHash != "" && s.store != nil {
		if chunk, err := s.store.GetChunk(rec.FileChunkHash); err == nil {
			fileBytes = chunk
		}
	}
	if len(picBytes) == 0 && rec.PictureChunkHash != "" && s.store != nil {
		if chunk, err := s.store.GetChunk(rec.PictureChunkHash); err == nil {
			picBytes = chunk
		}
	}

	// Broadcast backup to all assigned storage peers!
	return s.swarm.BroadcastRecordFull(rec, rec.StoragePeers, fileBytes, picBytes)
}

// ReconcileAndReplicateAll scans all records in the store, assigns live peers if missing, and replicates them.
func (s *Server) ReconcileAndReplicateAll() int {
	if s.standalone || s.swarm == nil || s.store == nil {
		return 0
	}

	if !s.reconciling.CompareAndSwap(false, true) {
		return 0
	}
	defer s.reconciling.Store(false)

	peers := s.swarm.ListPeers()
	onlineCount := 0
	for _, p := range peers {
		if p.IsOnline && p.NodeID != s.nodeID {
			onlineCount++
		}
	}
	if onlineCount == 0 {
		return 0
	}

	all := s.store.ListAllRecords()
	reconciled := 0
	for _, rec := range all {
		needsQuorum := false
		if len(rec.StoragePeers) < 3 {
			needsQuorum = true
		} else {
			for _, sp := range rec.StoragePeers {
				if strings.Contains(sp, "auto") {
					needsQuorum = true
					break
				}
			}
		}

		if needsQuorum {
			auditors, storageNodes, qErr := s.swarm.SelectQuorumForUser(rec.UserPubAddress, 3, 3)
			if qErr == nil {
				rec.AuditorPeers = auditors
				rec.StoragePeers = storageNodes
				_ = s.store.SaveRecord(rec)
				_ = s.replicateAndEngageQuorum(rec, nil, nil)
				reconciled++
				if reconciled >= 10 {
					break // throttle to at most 10 records per cycle to prevent CPU/network exhaustion
				}
			}
		}
	}
	return reconciled
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
	var text, userPubKey, sig, collection, dataID, repPolicy string
	var customStoragePeers []string
	var targetReplicas int
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
		repPolicy = r.FormValue("replication_policy")
		if spVal := r.FormValue("storage_peers"); spVal != "" {
			if strings.HasPrefix(spVal, "[") {
				_ = json.Unmarshal([]byte(spVal), &customStoragePeers)
			} else {
				for _, part := range strings.Split(spVal, ",") {
					if trimmed := strings.TrimSpace(part); trimmed != "" {
						customStoragePeers = append(customStoragePeers, trimmed)
					}
				}
			}
		}
		if trVal := r.FormValue("target_replicas"); trVal != "" {
			targetReplicas, _ = strconv.Atoi(trVal)
		}

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
			DataID            string   `json:"data_id"`
			Collection        string   `json:"collection"`
			UserPubAddress    string   `json:"user_pub_address"`
			Signature         string   `json:"signature"`
			Text              string   `json:"text"`
			ReplicationPolicy string   `json:"replication_policy"`
			StoragePeers      []string `json:"storage_peers"`
			TargetReplicas    int      `json:"target_replicas"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
			text = body.Text
			userPubKey = body.UserPubAddress
			sig = body.Signature
			collection = body.Collection
			dataID = body.DataID
			repPolicy = body.ReplicationPolicy
			customStoragePeers = body.StoragePeers
			targetReplicas = body.TargetReplicas
		}
	}

	if collection == "" {
		collection = "default"
	}

	if s.escrowMgr != nil && s.escrowMgr.IsBlacklisted(userPubKey) {
		http.Error(w, "Forbidden: public key or identity is blacklisted on this node", http.StatusForbidden)
		return
	}

	rec := &storage.Record{
		DataID:            dataID,
		Collection:        collection,
		UserPubAddress:    userPubKey,
		OwnerSignature:    sig,
		Text:              text,
		FileName:          fileName,
		PictureName:       picName,
		ReplicationPolicy: repPolicy,
		StoragePeers:      customStoragePeers,
		TargetReplicas:    targetReplicas,
	}

	created, err := s.store.CreateRecord(rec, fileBytes, picBytes)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to create record: %v", err), http.StatusBadRequest)
		return
	}

	// Engage quorum and storage nodes according to user replication policy
	_ = s.replicateAndEngageQuorum(created, fileBytes, picBytes)

	var invoice string
	if s.escrowMgr != nil {
		if c, err := s.escrowMgr.GetContract(created.DataID); err == nil && c != nil {
			invoice = c.LightningInvoice
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"record":             created,
		"data_id":            created.DataID, // Invariant persistent ID
		"replication_policy": created.ReplicationPolicy,
		"auditor_peers":      created.AuditorPeers,
		"storage_peers":      created.StoragePeers,
		"lightning_invoice":  invoice,
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
			switch r.Method {
			case http.MethodPost:
				var body struct {
					OwnerPubKey    string                     `json:"owner_pub_key"`
					OwnerSignature string                     `json:"signature"`
					Collaborator   storage.AccessControlEntry `json:"collaborator"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					http.Error(w, "invalid request body", http.StatusBadRequest)
					return
				}
				if body.OwnerPubKey == "" {
					body.OwnerPubKey = r.Header.Get("X-User-PubKey")
				}
				if body.OwnerSignature == "" {
					body.OwnerSignature = r.Header.Get("X-User-Signature")
				}

				if s.escrowMgr != nil && s.escrowMgr.IsBlacklisted(body.OwnerPubKey) {
					http.Error(w, "Forbidden: public key or identity is blacklisted on this node", http.StatusForbidden)
					return
				}

				if err := s.store.AddCollaboratorSigned(dataID, body.OwnerPubKey, body.OwnerSignature, body.Collaborator); err != nil {
					http.Error(w, err.Error(), http.StatusForbidden)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]string{"status": "collaborator added"})
				return

			case http.MethodDelete:
				var body struct {
					OwnerPubKey        string `json:"owner_pub_key"`
					OwnerSignature     string `json:"signature"`
					CollaboratorPubKey string `json:"collaborator_pub_key"`
				}
				_ = json.NewDecoder(r.Body).Decode(&body)
				if body.OwnerPubKey == "" {
					body.OwnerPubKey = r.Header.Get("X-User-PubKey")
				}
				if body.OwnerSignature == "" {
					body.OwnerSignature = r.Header.Get("X-User-Signature")
				}
				if body.CollaboratorPubKey == "" {
					body.CollaboratorPubKey = r.URL.Query().Get("collaborator_pub_key")
				}

				if s.escrowMgr != nil && s.escrowMgr.IsBlacklisted(body.OwnerPubKey) {
					http.Error(w, "Forbidden: public key or identity is blacklisted on this node", http.StatusForbidden)
					return
				}

				if err := s.store.RevokeCollaboratorSigned(dataID, body.OwnerPubKey, body.OwnerSignature, body.CollaboratorPubKey); err != nil {
					http.Error(w, err.Error(), http.StatusForbidden)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]string{"status": "collaborator revoked"})
				return

			default:
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}

		case "lockbox":
			switch r.Method {
			case http.MethodPost:
				var box crypto.UserShareLockbox
				if err := json.NewDecoder(r.Body).Decode(&box); err != nil {
					http.Error(w, "invalid lockbox payload", http.StatusBadRequest)
					return
				}
				if s.escrowMgr != nil && (s.escrowMgr.IsBlacklisted(box.FriendID) || s.escrowMgr.IsBlacklisted(box.FriendMLKEMPubKey) || s.escrowMgr.IsBlacklisted(box.FriendSignPubKey)) {
					http.Error(w, "Forbidden: friend public key or identity is blacklisted on this node", http.StatusForbidden)
					return
				}
				if err := s.store.PutShareLockbox(dataID, box); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]string{"status": "lockbox saved"})
				return

			case http.MethodGet:
				friendID := r.URL.Query().Get("friend_id")
				box, err := s.store.GetShareLockbox(dataID, friendID)
				if err != nil {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(box)
				return

			case http.MethodDelete:
				ownerPubKey := r.Header.Get("X-User-PubKey")
				sig := r.Header.Get("X-User-Signature")
				friendID := r.URL.Query().Get("friend_id")
				if s.escrowMgr != nil && s.escrowMgr.IsBlacklisted(ownerPubKey) {
					http.Error(w, "Forbidden: public key or identity is blacklisted on this node", http.StatusForbidden)
					return
				}
				if err := s.store.PurgeShareLockbox(dataID, ownerPubKey, sig, friendID); err != nil {
					http.Error(w, err.Error(), http.StatusForbidden)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]string{"status": "lockbox purged"})
				return

			default:
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
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

		if body.UpdaterPubKey == "" {
			body.UpdaterPubKey = r.Header.Get("X-User-PubKey")
		}
		if body.Signature == "" {
			body.Signature = r.Header.Get("X-User-Signature")
		}

		if s.escrowMgr != nil && s.escrowMgr.IsBlacklisted(body.UpdaterPubKey) {
			http.Error(w, "Forbidden: public key or identity is blacklisted on this node", http.StatusForbidden)
			return
		}

		updated, err := s.store.UpdateRecord(dataID, body.UpdaterPubKey, body.Signature, body.Text, nil, nil)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unauthorized") {
				http.Error(w, err.Error(), http.StatusForbidden)
			} else {
				http.Error(w, err.Error(), http.StatusBadRequest)
			}
			return
		}
		_ = s.replicateAndEngageQuorum(updated, nil, nil)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(updated)

	case http.MethodDelete:
		callerPubKey := r.Header.Get("X-User-PubKey")
		sig := r.Header.Get("X-User-Signature")
		if s.escrowMgr != nil && s.escrowMgr.IsBlacklisted(callerPubKey) {
			http.Error(w, "Forbidden: public key or identity is blacklisted on this node", http.StatusForbidden)
			return
		}
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

	// Ensure PocketBase and StiQRcloud standard fields are present
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
	if rec.ReplicationPolicy != "" {
		res["replication_policy"] = rec.ReplicationPolicy
	}
	if rec.TargetReplicas > 0 {
		res["target_replicas"] = rec.TargetReplicas
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

	if userPubKey == "" {
		userPubKey = r.Header.Get("X-User-PubKey")
	}
	if sig == "" {
		sig = r.Header.Get("X-User-Signature")
	}
	if s.escrowMgr != nil && s.escrowMgr.IsBlacklisted(userPubKey) {
		http.Error(w, "Forbidden: public key or identity is blacklisted on this node", http.StatusForbidden)
		return
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

	var record *storage.Record
	if dataID != "" {
		if existing, err := s.store.GetRecord(dataID); err == nil && existing != nil {
			// Record already exists; perform idempotent update
			record, err = s.store.UpdateRecord(dataID, userPubKey, sig, text, fileBytes, picBytes)
			if err != nil {
				http.Error(w, fmt.Sprintf("failed to update record: %v", err), http.StatusBadRequest)
				return
			}
		}
	}

	if record == nil {
		var repPolicy string
		if rp, ok := rawBodyMap["replication_policy"].(string); ok {
			repPolicy = rp
		}
		var targetReplicas int
		if tr, ok := rawBodyMap["target_replicas"].(float64); ok {
			targetReplicas = int(tr)
		} else if tr, ok := rawBodyMap["target_replicas"].(int); ok {
			targetReplicas = tr
		}
		var customStoragePeers []string
		if sp, ok := rawBodyMap["storage_peers"].([]interface{}); ok {
			for _, item := range sp {
				if sStr, ok := item.(string); ok && sStr != "" {
					customStoragePeers = append(customStoragePeers, sStr)
				}
			}
		}

		rec := &storage.Record{
			DataID:            dataID,
			Collection:        collection,
			UserPubAddress:    userPubKey,
			OwnerSignature:    sig,
			Text:              text,
			FileName:          fileName,
			PictureName:       picName,
			ReplicationPolicy: repPolicy,
			TargetReplicas:    targetReplicas,
			StoragePeers:      customStoragePeers,
		}

		var err error
		record, err = s.store.CreateRecord(rec, fileBytes, picBytes)
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to create record: %v", err), http.StatusBadRequest)
			return
		}
	}

	// Always engage quorum & replicate across mesh
	_ = s.replicateAndEngageQuorum(record, fileBytes, picBytes)

	formatted := formatPBRecord(record)
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
					"token": "stiqrcloud_mock_jwt_token",
					"record": map[string]interface{}{
						"id":       "user_local_owner",
						"email":    "owner@stiqrcloud.mesh",
						"name":     "StiQRcloud Owner",
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
					if updaterPubKey == "" {
						updaterPubKey = r.Header.Get("X-User-PubKey")
					}
					if sig == "" {
						sig = r.Header.Get("X-User-Signature")
					}
					if s.escrowMgr != nil && s.escrowMgr.IsBlacklisted(updaterPubKey) {
						http.Error(w, "Forbidden: public key or identity is blacklisted on this node", http.StatusForbidden)
						return
					}
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
						if strings.Contains(strings.ToLower(err.Error()), "unauthorized") {
							http.Error(w, err.Error(), http.StatusForbidden)
						} else {
							http.Error(w, err.Error(), http.StatusBadRequest)
						}
						return
					}
					_ = s.replicateAndEngageQuorum(updated, nil, nil)
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(formatPBRecord(updated))
					return

				case http.MethodDelete:
					callerPubKey := r.Header.Get("X-User-PubKey")
					sig := r.Header.Get("X-User-Signature")
					if s.escrowMgr != nil && s.escrowMgr.IsBlacklisted(callerPubKey) {
						http.Error(w, "Forbidden: public key or identity is blacklisted on this node", http.StatusForbidden)
						return
					}
					if err := s.store.DeleteRecord(recordID, callerPubKey, sig); err != nil {
						http.Error(w, err.Error(), http.StatusForbidden)
						return
					}
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
			"blacklist":           policy.Blacklist,
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
			Blacklist       []string `json:"blacklist"`
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
		if req.Blacklist != nil {
			current.Blacklist = req.Blacklist
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
	w.Header().Set("Content-Type", "application/json")
	if s.escrowMgr == nil {
		http.Error(w, "escrow manager disabled", http.StatusInternalServerError)
		return
	}

	switch r.Method {
	case http.MethodGet:
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":         "ok",
			"free_whitelist": s.escrowMgr.GetPolicy().FreeWhitelist,
		})

	case http.MethodPost:
		var req struct {
			Action  string `json:"action"` // "add" or "remove"
			Address string `json:"address"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}

		if strings.EqualFold(req.Action, "remove") {
			_ = s.escrowMgr.RemoveWhitelist(req.Address)
		} else {
			_ = s.escrowMgr.AddWhitelist(req.Address)
		}

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":         "ok",
			"free_whitelist": s.escrowMgr.GetPolicy().FreeWhitelist,
		})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleEscrowBlacklist(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if s.escrowMgr == nil {
		http.Error(w, "escrow manager disabled", http.StatusInternalServerError)
		return
	}

	switch r.Method {
	case http.MethodGet:
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":    "ok",
			"blacklist": s.escrowMgr.GetPolicy().Blacklist,
		})

	case http.MethodPost:
		var req struct {
			Action  string `json:"action"` // "add" or "remove"
			Address string `json:"address"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}

		if strings.EqualFold(req.Action, "remove") {
			_ = s.escrowMgr.RemoveBlacklist(req.Address)
		} else {
			_ = s.escrowMgr.AddBlacklist(req.Address)
		}

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":    "ok",
			"blacklist": s.escrowMgr.GetPolicy().Blacklist,
		})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleCollectionsEnsure provides an idempotent handshake for apps using the SDK
// to declare their collections and schema fields upon connecting to a node.
func (s *Server) handleCollectionsEnsure(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	callerPubKey := r.Header.Get("X-User-PubKey")
	if s.escrowMgr != nil && s.escrowMgr.IsBlacklisted(callerPubKey) {
		http.Error(w, "Forbidden: public key or identity is blacklisted on this node", http.StatusForbidden)
		return
	}

	var req struct {
		Name   string                   `json:"name"`
		Fields []storage.CollectionField `json:"fields"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if s.store == nil {
		http.Error(w, "store not available", http.StatusInternalServerError)
		return
	}

	info, err := s.store.EnsureCollection(req.Name, req.Fields)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(info)
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

	if s.escrowMgr != nil && s.escrowMgr.IsBlacklisted(body.OwnerPubKey) {
		http.Error(w, "Forbidden: public key or identity is blacklisted on this node", http.StatusForbidden)
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

	// Replicate across mesh & engage quorum
	_ = s.replicateAndEngageQuorum(created, nil, nil)

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

	if s.escrowMgr != nil && s.escrowMgr.IsBlacklisted(body.OwnerPubKey) {
		http.Error(w, "Forbidden: public key or identity is blacklisted on this node", http.StatusForbidden)
		return
	}

	updated, err := s.store.UpdateRecord(dataID, body.OwnerPubKey, body.Signature, body.Content, nil, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = s.replicateAndEngageQuorum(updated, nil, nil)

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

	if s.escrowMgr != nil && s.escrowMgr.IsBlacklisted(body.OwnerPubKey) {
		http.Error(w, "Forbidden: public key or identity is blacklisted on this node", http.StatusForbidden)
		return
	}

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
		pubIP := req.PublicIP
		if pubIP == "" {
			if cfIP := r.Header.Get("CF-Connecting-IP"); cfIP != "" {
				pubIP = cfIP
			} else if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
				parts := strings.Split(xff, ",")
				pubIP = strings.TrimSpace(parts[0])
			} else {
				if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
					pubIP = h
				}
			}
		}
		pubAddr := req.PublicAddr
		if strings.Contains(pubAddr, "0.0.0.0") || strings.Contains(pubAddr, "127.0.0.1") || strings.Contains(pubAddr, "localhost") {
			pubAddr = ""
		}
		s.swarm.RegisterDirectPeer(&p2p.Peer{
			NodeID:          id,
			Name:            name,
			Location:        req.Location,
			PublicIP:        pubIP,
			PublicAddr:      pubAddr,
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

	// Ensure the discovery rendezvous host itself is always visible to registering peers
	if s.nodeID != "" {
		hasSelf := false
		for _, p := range peers {
			if p.NodeID == s.nodeID {
				hasSelf = true
				break
			}
		}
		if !hasSelf {
			selfPeer := &p2p.Peer{
				NodeID:          s.nodeID,
				Name:            s.name,
				Location:        s.location,
				PublicIP:        s.publicIP,
				PublicAddr:      s.publicAddr,
				ReticulumDest:   s.rnsDest,
				IsPublic:        s.isPublic,
				PricePerGBMonth: 100,
				IsOnline:        true,
				LastSeen:        time.Now().UTC(),
			}
			peers = append([]*p2p.Peer{selfPeer}, peers...)
		}
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

	// Ensure discovery rendezvous host itself is visible
	if s.nodeID != "" {
		hasSelf := false
		for _, p := range peers {
			if p.NodeID == s.nodeID {
				hasSelf = true
				break
			}
		}
		if !hasSelf {
			selfPeer := &p2p.Peer{
				NodeID:          s.nodeID,
				Name:            s.name,
				Location:        s.location,
				PublicIP:        s.publicIP,
				PublicAddr:      s.publicAddr,
				ReticulumDest:   s.rnsDest,
				IsPublic:        s.isPublic,
				PricePerGBMonth: 100,
				IsOnline:        true,
				LastSeen:        time.Now().UTC(),
			}
			peers = append([]*p2p.Peer{selfPeer}, peers...)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"total_online": len(peers),
		"peers":        peers,
	})
}

func (s *Server) handleReticulumSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.rnsManager == nil {
		http.Error(w, "Reticulum stack is not initialized on this node", http.StatusBadRequest)
		return
	}

	var req struct {
		TargetDest string `json:"target_dest"`
		Collection string `json:"collection"`
		Text       string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.TargetDest == "" {
		http.Error(w, "target_dest is required (hex format or <hex>)", http.StatusBadRequest)
		return
	}
	if req.Collection == "" {
		req.Collection = "reticulum_direct"
	}

	dest, err := reticulum.DestinationFromHex(req.TargetDest)
	if err != nil {
		http.Error(w, fmt.Sprintf("invalid target_dest: %v", err), http.StatusBadRequest)
		return
	}

	dataID := fmt.Sprintf("rec_%s_%d", crypto.HashBytes([]byte(req.Text))[:12], time.Now().UnixNano()%100000)
	rec := &storage.Record{
		DataID:         dataID,
		Collection:     req.Collection,
		UserPubAddress: "stiqr_admin_operator_pubkey",
		Version:        1,
		Text:           req.Text,
		TotalBytes:     int64(len(req.Text)),
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}

	if s.store != nil {
		_ = s.store.SaveRecord(rec)
	}

	payload, err := json.Marshal(rec)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to serialize record: %v", err), http.StatusInternalServerError)
		return
	}

	// Transparently compress if approaching Reticulum MTU (500 bytes)
	if len(payload) > 350 {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		if _, err := gz.Write(payload); err == nil {
			_ = gz.Close()
			if buf.Len() < len(payload) {
				payload = buf.Bytes()
			}
		}
	}

	if err := s.rnsManager.SendPacketToDest(dest, 0x01, payload); err != nil {
		http.Error(w, fmt.Sprintf("failed to send Reticulum packet: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":      "sent",
		"transport":   "reticulum_mesh",
		"target_dest": req.TargetDest,
		"data_id":     rec.DataID,
		"record":      rec,
	})
}

func (s *Server) handleReticulumAnnounce(w http.ResponseWriter, r *http.Request) {
	if s.rnsManager == nil {
		http.Error(w, "Reticulum stack is not initialized", http.StatusBadRequest)
		return
	}
	if err := s.rnsManager.Announce([]byte(reticulum.DefaultRNSAspect)); err != nil {
		http.Error(w, fmt.Sprintf("announce failed: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":   "announced",
		"rns_dest": s.rnsDest,
	})
}

func (s *Server) handleReticulumStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	gw := ""
	peers := 0
	if s.rnsManager != nil {
		gw = s.rnsManager.GetActiveGateway()
		peers = s.rnsManager.GetPeersCount()
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"rns_dest":       s.rnsDest,
		"active_gateway": gw,
		"peers_seen":     peers,
	})
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	filename := strings.TrimPrefix(r.URL.Path, "/download/")
	filename = strings.Trim(filename, "/")
	if filename == "" || filename == "stiqrcloud" || filename == "testcloud" {
		filename = "stiqrcloud-linux-amd64"
	}

	// Search locations for the requested binary
	searchPaths := []string{
		filename,
		filepath.Join(".", filename),
		filepath.Join("releases", filename),
		filepath.Join("releases", "v0.2.0", filename),
		filepath.Join("releases_storage", filename),
		filepath.Join("/opt/stiqrhub/releases_storage", filename),
		filepath.Join("/opt/stiqrcloud", filename),
		filepath.Join("./stiqrcloud", filename),
		filepath.Join("./stiqrcloud/releases/v0.2.0", filename),
		filepath.Join("./testcloud", filename),
		filepath.Join("./StiQRhub/releases_storage", filename),
	}

	var foundPath string
	for _, p := range searchPaths {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			foundPath = p
			break
		}
	}

	if foundPath == "" {
		http.Error(w, fmt.Sprintf("binary %q not found", filename), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	http.ServeFile(w, r, foundPath)
}

func (s *Server) handleInstallScript(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	proto := "https"
	if r.TLS == nil && !strings.Contains(host, "stiqrnotes.com") {
		proto = "http"
	}

	baseURL := fmt.Sprintf("%s://%s", proto, host)

	script := fmt.Sprintf(`#!/usr/bin/env bash
# ==============================================================================
# StiQRcloud Universal One-Line Installer
# Runs on Linux VMs (Windows WSL, VirtualBox, VMware), PCs, and servers.
# Zero port forwarding required - connects outbound via Discovery Rendezvous.
# ==============================================================================
set -e

echo "=================================================================="
echo "          Installing StiQRcloud Decentralized Node                 "
echo "=================================================================="

ARCH="$(uname -m)"
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"

if [ "$OS" != "linux" ]; then
  echo "Error: Linux required. Detected: $OS"
  echo "If you are on Windows, run this in PowerShell instead:"
  echo "  iwr -useb %s/install.ps1 | iex"
  exit 1
fi

BIN_FILE="stiqrcloud-linux-amd64"
case "$ARCH" in
  x86_64|amd64)
    BIN_FILE="stiqrcloud-linux-amd64"
    echo "-> Architecture: Linux x86_64"
    ;;
  aarch64|arm64)
    BIN_FILE="stiqrcloud-linux-arm64"
    echo "-> Architecture: Linux ARM64"
    ;;
  *)
    echo "Warning: Architecture $ARCH, defaulting to x86_64"
    ;;
esac

DEST_DIR="$HOME/.stiqrcloud"
mkdir -p "$DEST_DIR"
TARGET_BIN="$DEST_DIR/stiqrcloud"

echo "-> Downloading static binary from %s..."
if command -v curl >/dev/null 2>&1; then
  curl -sSLf "%s/download/$BIN_FILE" -o "$TARGET_BIN" || curl -sSLf "%s/download/stiqrcloud" -o "$TARGET_BIN"
elif command -v wget >/dev/null 2>&1; then
  wget -qO "$TARGET_BIN" "%s/download/$BIN_FILE" || wget -qO "$TARGET_BIN" "%s/download/stiqrcloud"
else
  echo "Error: curl or wget is required to download the binary."
  exit 1
fi
chmod +x "$TARGET_BIN"

# Automatically find available ports to prevent conflicts
PORT=8100
while command -v ss >/dev/null 2>&1 && ss -tlpn 2>/dev/null | grep -q ":$PORT "; do
  PORT=$((PORT+1))
done

RNS_PORT=4242
while command -v ss >/dev/null 2>&1 && ss -ulpn 2>/dev/null | grep -q ":$RNS_PORT "; do
  RNS_PORT=$((RNS_PORT+1))
done

NODE_NAME="${STIQRCLOUD_NAME:-FriendNode-$(hostname -s 2>/dev/null || echo "VM")-$RANDOM}"
DISCOVERY_URL="%s"

echo "=================================================================="
echo "-> Starting StiQRcloud node '$NODE_NAME'..."
echo "-> Connecting to discovery rendezvous: $DISCOVERY_URL"
echo "-> Private Dashboard active at: http://localhost:$PORT/dashboard"
echo "=================================================================="

exec "$TARGET_BIN" \
  -host 0.0.0.0 \
  -port "$PORT" \
  -rns-port "$RNS_PORT" \
  -rns-gateway "176.84.102.17:4242" \
  -discovery-url "$DISCOVERY_URL" \
  -peers "$DISCOVERY_URL" \
  -data-dir "$DEST_DIR/data" \
  -name "$NODE_NAME"
`, baseURL, baseURL, baseURL, baseURL, baseURL, baseURL, baseURL)

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(script))
}

func (s *Server) handlePowerShellScript(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	proto := "https"
	if r.TLS == nil && !strings.Contains(host, "stiqrnotes.com") {
		proto = "http"
	}
	baseURL := fmt.Sprintf("%s://%s", proto, host)

	script := fmt.Sprintf(`# StiQRcloud Windows PowerShell Installer & Launcher
$destDir = "$HOME\.stiqrcloud"
New-Item -ItemType Directory -Force -Path $destDir | Out-Null
$binPath = "$destDir\stiqrcloud.exe"

Write-Host "=================================================================="
Write-Host "          Installing StiQRcloud Decentralized Node (Windows)       "
Write-Host "=================================================================="

Get-Process -Name "stiqrcloud*" -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
Start-Sleep -Milliseconds 600
try {
    $conn = Get-NetTCPConnection -LocalPort 8100 -ErrorAction SilentlyContinue
    if ($conn -and $conn.OwningProcess) {
        Stop-Process -Id $conn.OwningProcess -Force -ErrorAction SilentlyContinue
        Start-Sleep -Milliseconds 500
    }
} catch {}
Remove-Item -Path $binPath -Force -ErrorAction SilentlyContinue

$randomId = Get-Random -Minimum 1000 -Maximum 9999

Write-Host "-> Downloading latest StiQRcloud for Windows..."
Invoke-WebRequest -Uri "%s/download/stiqrcloud-windows-amd64.exe?v=$randomId" -OutFile $binPath

$nodeName = "WindowsNode-$randomId"
$discoveryUrl = "%s"

Write-Host "-> Starting StiQRcloud node '$nodeName'..."
Write-Host "-> Connecting to discovery rendezvous: $discoveryUrl"
Write-Host "-> Reticulum Mesh Gateway: 176.84.102.17:4242 (Air Barcelona)"
Write-Host "-> Private Dashboard active at: http://localhost:8100/dashboard"
Write-Host "=================================================================="

& $binPath -host 0.0.0.0 -port 8100 -rns-gateway "176.84.102.17:4242" -discovery-url "$discoveryUrl" -peers "$discoveryUrl" -data-dir "$destDir\data" -name "$nodeName"
`, baseURL, baseURL)

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(script))
}
