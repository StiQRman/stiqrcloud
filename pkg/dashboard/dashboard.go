package dashboard

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/stiqrhub/testcloud/pkg/audit"
	"github.com/stiqrhub/testcloud/pkg/escrow"
	"github.com/stiqrhub/testcloud/pkg/p2p"
	"github.com/stiqrhub/testcloud/pkg/reticulum"
	"github.com/stiqrhub/testcloud/pkg/storage"
	"github.com/stiqrhub/testcloud/pkg/version"
)

//go:embed dashboard.html
var DashboardHTML string

// Server provides the local web dashboard.
type Server struct {
	nodeID     string
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
	mux.HandleFunc("/dashboard", s.handleDashboardHTML)
	mux.HandleFunc("/api/dashboard/stats", s.handleStatsJSON)
	mux.HandleFunc("/api/dashboard/version", s.handleVersionJSON)
	mux.HandleFunc("/api/dashboard/records", s.handleRecordsJSON)
}

func (s *Server) handleDashboardHTML(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(DashboardHTML))
}

func (s *Server) handleStatsJSON(w http.ResponseWriter, r *http.Request) {
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

	res := map[string]interface{}{
		"app_name":           version.AppName,
		"version":            version.CurrentVersion,
		"node_id":            s.nodeID,
		"mode":               mode,
		"is_public":          s.isPublic,
		"reticulum_dest":     s.rnsDest,
		"reticulum_peers":    rnsPeers,
		"storage":            storageStats,
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

func (s *Server) handleVersionJSON(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	status := version.CheckUpdate()
	_ = json.NewEncoder(w).Encode(status)
}

func (s *Server) handleRecordsJSON(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	records, total := s.store.ListRecords("", 50, 0)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"records": records,
		"total":   total,
	})
}
