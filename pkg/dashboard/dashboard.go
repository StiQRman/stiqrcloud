package dashboard

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/stiqrhub/stiqrcloud/pkg/audit"
	"github.com/stiqrhub/stiqrcloud/pkg/escrow"
	"github.com/stiqrhub/stiqrcloud/pkg/p2p"
	"github.com/stiqrhub/stiqrcloud/pkg/reticulum"
	"github.com/stiqrhub/stiqrcloud/pkg/storage"
	"github.com/stiqrhub/stiqrcloud/pkg/version"
)

//go:embed dashboard.html
var DashboardHTML string

// Server provides the local web dashboard.
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
}

// NewServer initializes the dashboard server.
func NewServer(
	nodeID string,
	name string,
	location string,
	publicIP string,
	store *storage.Store,
	swarm *p2p.Swarm,
	escrowMgr *escrow.Manager,
	auditor *audit.Coordinator,
	rnsManager *reticulum.InterfaceManager,
	rnsDest string,
	isPublic bool,
) *Server {
	return &Server{
		nodeID:     nodeID,
		name:       name,
		location:   location,
		publicIP:   publicIP,
		store:      store,
		swarm:      swarm,
		escrowMgr:  escrowMgr,
		auditor:    auditor,
		rnsManager: rnsManager,
		rnsDest:    rnsDest,
		isPublic:   isPublic,
	}
}

// RegisterRoutes hooks the dashboard endpoints into an HTTP mux.
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/dashboard", s.HandleDashboardHTML)
	mux.HandleFunc("/api/dashboard/stats", s.HandleStatsJSON)
	mux.HandleFunc("/api/dashboard/version", s.HandleVersionJSON)
	mux.HandleFunc("/api/dashboard/records", s.HandleRecordsJSON)
	mux.HandleFunc("/api/dashboard/collections", s.HandleCollectionsJSON)
}

func (s *Server) HandleDashboardHTML(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(DashboardHTML))
}

func (s *Server) HandleStatsJSON(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	storageStats := s.store.StorageStats()
	peers := s.swarm.ListPeers()
	audits := s.auditor.GetAuditHistory()
	contracts := s.escrowMgr.ListContracts()

	rnsPeers := 0
	if s.rnsManager != nil {
		rnsPeers = s.rnsManager.GetPeersCount()
	}

	onlinePeers := 0
	for _, p := range peers {
		if p.IsOnline {
			onlinePeers++
		}
	}

	mode := "Standalone Database (Local)"
	if len(peers) > 0 {
		mode = fmt.Sprintf("Decentralized P2P Mesh (%d online / %d total)", onlinePeers, len(peers))
	}

	var totalSatsAccrued int64
	for _, c := range contracts {
		totalSatsAccrued += c.PaidSats
	}

	gw := ""
	if s.rnsManager != nil {
		gw = s.rnsManager.GetActiveGateway()
	}

	var storageSplit storage.StorageSplitConfig
	if s.store != nil {
		storageSplit = s.store.GetStorageSplit()
	}
	var personalNodes []*p2p.PersonalNode
	var airgapStatus map[string]interface{}
	if s.swarm != nil {
		personalNodes = s.swarm.ListPersonalNodes()
		airgapStatus = s.swarm.AirgapStatus()
	}

	res := map[string]interface{}{
		"app_name":           version.AppName,
		"version":            version.CurrentVersion,
		"node_id":            s.nodeID,
		"operator_name":      s.name,
		"operator_location":  s.location,
		"public_ip":          s.publicIP,
		"mode":               mode,
		"is_public":          s.isPublic,
		"reticulum_dest":     s.rnsDest,
		"reticulum_gateway":  gw,
		"reticulum_peers":    rnsPeers,
		"storage":            storageStats,
		"storage_split":      storageSplit,
		"personal_nodes":     personalNodes,
		"airgap_status":      airgapStatus,
		"peers_count":        len(peers),
		"online_peers_count": onlinePeers,
		"peers":              peers,
		"audit_count":        len(audits),
		"recent_audits":      audits,
		"contracts_count":    len(contracts),
		"contracts":          contracts,
		"total_sats_accrued": totalSatsAccrued,
		"pricing_policy":     s.escrowMgr.GetPolicy(),
		"server_time":        time.Now().UTC().Format(time.RFC3339),
	}

	_ = json.NewEncoder(w).Encode(res)
}

func (s *Server) HandleVersionJSON(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	status := version.CheckUpdate()
	_ = json.NewEncoder(w).Encode(status)
}

func (s *Server) HandleRecordsJSON(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	col := r.URL.Query().Get("collection")
	records, total := s.store.ListRecords(col, 100, 0)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"collection": col,
		"records":    records,
		"total":      total,
	})
}

func (s *Server) HandleCollectionsJSON(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case http.MethodGet:
		collections := s.store.ListCollections()
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"collections": collections,
		})

	case http.MethodPost:
		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
			http.Error(w, "invalid collection name", http.StatusBadRequest)
			return
		}
		if err := s.store.CreateCollection(body.Name); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "created",
			"name":   body.Name,
		})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
