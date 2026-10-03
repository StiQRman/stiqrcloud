package p2p

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/stiqrhub/testcloud/pkg/storage"
)

// Peer represents a database node on the mesh.
type Peer struct {
	NodeID          string    `json:"node_id"`
	Name            string    `json:"name,omitempty"`
	Location        string    `json:"location,omitempty"`
	PublicIP        string    `json:"public_ip,omitempty"`
	PublicAddr      string    `json:"public_addr"`
	ReticulumDest   string    `json:"reticulum_dest,omitempty"`
	IsPublic        bool      `json:"is_public"`
	IsOnline        bool      `json:"is_online"`
	PricePerGBMonth int64     `json:"price_per_gb_month"`
	AuditsPassed    int64     `json:"audits_passed"`
	AuditsPerformed int64     `json:"audits_performed"`
	AvgLatencyMs    float64   `json:"avg_latency_ms"`
	LastSeen        time.Time `json:"last_seen"`
	SocialScore     float64   `json:"social_score"`
}

// Swarm manages peer discovery, reputation scoring, and distributed content retrieval.
type Swarm struct {
	mu             sync.RWMutex
	localID        string
	localAddr      string
	name           string
	location       string
	publicIP       string
	rnsDest        string
	isPublic       bool
	price          int64
	discoveryURL   string
	bootstrapPeers []string
	peers          map[string]*Peer
	httpClient     *http.Client
	rng            *rand.Rand
	store          *storage.Store
	stopDiscovery  chan struct{}
}

// NewSwarm initializes a peer mesh swarm.
func NewSwarm(localID, localAddr string, store *storage.Store) *Swarm {
	return &Swarm{
		localID:        localID,
		localAddr:      localAddr,
		peers:          make(map[string]*Peer),
		bootstrapPeers: []string{},
		httpClient:     &http.Client{Timeout: 2 * time.Second},
		rng:            rand.New(rand.NewSource(time.Now().UnixNano())),
		store:          store,
	}
}

// SetIdentity configures local metadata for peer announcements.
func (s *Swarm) SetIdentity(rnsDest string, isPublic bool, price int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rnsDest = rnsDest
	s.isPublic = isPublic
	s.price = price
}

// SetMetadata configures friendly display metadata (Name, Location, PublicIP).
func (s *Swarm) SetMetadata(name, location, publicIP string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.name = name
	s.location = location
	s.publicIP = publicIP
}

// StartDiscovery launches a recurring background rendezvous announcement to discoveryURL.
func (s *Swarm) StartDiscovery(discoveryURL string, interval time.Duration) {
	s.mu.Lock()
	s.discoveryURL = strings.TrimRight(discoveryURL, "/")
	if s.stopDiscovery != nil {
		close(s.stopDiscovery)
	}
	s.stopDiscovery = make(chan struct{})
	discURL := s.discoveryURL
	s.mu.Unlock()

	if discURL == "" {
		return
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		// Initial announcement
		s.announceToDiscovery()

		for {
			select {
			case <-ticker.C:
				s.announceToDiscovery()
			case <-s.stopDiscovery:
				return
			}
		}
	}()
}

func (s *Swarm) announceToDiscovery() {
	s.mu.RLock()
	discURL := s.discoveryURL
	localID := s.localID
	localAddr := s.localAddr
	name := s.name
	loc := s.location
	pubIP := s.publicIP
	rnsDest := s.rnsDest
	isPub := s.isPublic
	price := s.price
	s.mu.RUnlock()

	if discURL == "" {
		return
	}

	payload := map[string]interface{}{
		"peer_id":            localID,
		"node_id":            localID,
		"name":               name,
		"nickname":           name,
		"location":           loc,
		"public_ip":          pubIP,
		"public_addr":        localAddr,
		"reticulum_dest":     rnsDest,
		"is_public":          isPub,
		"price_per_gb_month": price,
		"role":               "dual",
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return
	}

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Post(discURL+"/api/discovery/announce", "application/json", bytes.NewReader(data))
	if err != nil {
		return
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}

	var discResp struct {
		TotalOnline int     `json:"total_online"`
		Peers       []*Peer `json:"peers"`
	}

	if err := json.Unmarshal(bodyBytes, &discResp); err == nil && len(discResp.Peers) > 0 {
		for _, p := range discResp.Peers {
			if p.NodeID != localID && p.PublicAddr != localAddr && p.PublicAddr != "" {
				s.RegisterPeer(p)
			}
		}
	} else {
		var peerList []*Peer
		if err := json.Unmarshal(bodyBytes, &peerList); err == nil {
			for _, p := range peerList {
				if p.NodeID != localID && p.PublicAddr != localAddr && p.PublicAddr != "" {
					s.RegisterPeer(p)
				}
			}
		}
	}
}

// AddBootstrapPeer registers a persistent bootstrap peer URL for recurring gossip discovery.
func (s *Swarm) AddBootstrapPeer(addr string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	addr = strings.TrimRight(addr, "/")
	if addr == "" || addr == s.localAddr {
		return
	}
	for _, b := range s.bootstrapPeers {
		if b == addr {
			return
		}
	}
	s.bootstrapPeers = append(s.bootstrapPeers, addr)
}

// RegisterDirectPeer registers or updates a peer that has contacted us directly
// (e.g. through discovery announcement or direct peer greeting).
func (s *Swarm) RegisterDirectPeer(p *Peer) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if p.NodeID == "" || p.NodeID == s.localID {
		return
	}

	// Never accept 0.0.0.0 or loopback as a peer's public address
	if strings.Contains(p.PublicAddr, "0.0.0.0") || strings.Contains(p.PublicAddr, "127.0.0.1") || strings.Contains(p.PublicAddr, "localhost") {
		p.PublicAddr = ""
	}

	existing, exists := s.peers[p.NodeID]
	if exists {
		existing.IsOnline = true
		existing.LastSeen = time.Now().UTC()
		if p.Name != "" {
			existing.Name = p.Name
		}
		if p.Location != "" {
			existing.Location = p.Location
		}
		if p.PublicIP != "" {
			existing.PublicIP = p.PublicIP
		}
		if p.PublicAddr != "" {
			existing.PublicAddr = p.PublicAddr
		}
		if p.ReticulumDest != "" {
			existing.ReticulumDest = p.ReticulumDest
		}
		if p.PricePerGBMonth > 0 {
			existing.PricePerGBMonth = p.PricePerGBMonth
		}
		if p.AvgLatencyMs > 0 {
			existing.AvgLatencyMs = p.AvgLatencyMs
		}
		existing.IsPublic = p.IsPublic
		s.calculateSocialScore(existing)
		return
	}

	p.IsOnline = true
	p.LastSeen = time.Now().UTC()
	s.calculateSocialScore(p)
	s.peers[p.NodeID] = p
}

// RegisterPeer registers or updates a peer in the local routing table via passive gossip.
func (s *Swarm) RegisterPeer(p *Peer) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if p.NodeID == "" || p.NodeID == s.localID {
		return
	}

	// Never accept 0.0.0.0 or loopback as a peer's public address
	if strings.Contains(p.PublicAddr, "0.0.0.0") || strings.Contains(p.PublicAddr, "127.0.0.1") || strings.Contains(p.PublicAddr, "localhost") {
		p.PublicAddr = ""
	}

	existing, exists := s.peers[p.NodeID]
	if exists {
		// Passive gossip MUST NOT resurrect an offline node that failed direct probing!
		if existing.IsOnline {
			existing.LastSeen = time.Now().UTC()
		}
		if p.Name != "" {
			existing.Name = p.Name
		}
		if p.Location != "" {
			existing.Location = p.Location
		}
		if p.PublicIP != "" {
			existing.PublicIP = p.PublicIP
		}
		if p.PublicAddr != "" {
			existing.PublicAddr = p.PublicAddr
		}
		if p.ReticulumDest != "" {
			existing.ReticulumDest = p.ReticulumDest
		}
		if p.PricePerGBMonth > 0 {
			existing.PricePerGBMonth = p.PricePerGBMonth
		}
		if p.AvgLatencyMs > 0 {
			existing.AvgLatencyMs = p.AvgLatencyMs
		}
		existing.IsPublic = p.IsPublic
		s.calculateSocialScore(existing)
		return
	}

	// For a new peer learned passively via gossip:
	// If it has an address, mark offline until ProbePeers verifies reachability directly.
	// If it has no address, only online if LastSeen is very recent.
	if p.PublicAddr != "" {
		p.IsOnline = false
	} else {
		p.IsOnline = time.Since(p.LastSeen) <= 15*time.Second
	}
	s.calculateSocialScore(p)
	s.peers[p.NodeID] = p
}

// RemovePeer deletes a peer from the local routing table.
func (s *Swarm) RemovePeer(nodeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.peers, nodeID)
}

// GetPeer finds a peer by NodeID.
func (s *Swarm) GetPeer(nodeID string) (*Peer, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.peers[nodeID]
	return p, ok
}

// calculateSocialScore grades peer based on:
// #1 Audits passed
// #2 Audits performed
// #3 Network / retrieval speed
func (s *Swarm) calculateSocialScore(p *Peer) {
	// Audits passed weight (up to 50 pts)
	passedScore := float64(p.AuditsPassed) * 5.0
	if passedScore > 50.0 {
		passedScore = 50.0
	}

	// Audits performed weight (up to 30 pts)
	performedScore := float64(p.AuditsPerformed) * 3.0
	if performedScore > 30.0 {
		performedScore = 30.0
	}

	// Speed / Latency score (up to 20 pts)
	latencyScore := 20.0
	if p.AvgLatencyMs > 0 {
		if p.AvgLatencyMs < 50 {
			latencyScore = 20.0
		} else if p.AvgLatencyMs < 200 {
			latencyScore = 15.0
		} else if p.AvgLatencyMs < 500 {
			latencyScore = 10.0
		} else {
			latencyScore = 5.0
		}
	}

	p.SocialScore = passedScore + performedScore + latencyScore
}

// MarkPeerOffline flags a peer as disconnected and slashes its social score.
func (s *Swarm) MarkPeerOffline(nodeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if p, ok := s.peers[nodeID]; ok {
		p.IsOnline = false
		p.SocialScore = p.SocialScore * 0.5
	}
}

// RecordAuditSuccess updates social score when a peer passes an audit.
func (s *Swarm) RecordAuditSuccess(nodeID string, latencyMs float64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if p, ok := s.peers[nodeID]; ok {
		p.IsOnline = true
		p.AuditsPassed++
		if p.AvgLatencyMs == 0 {
			p.AvgLatencyMs = latencyMs
		} else {
			p.AvgLatencyMs = (p.AvgLatencyMs * 0.7) + (latencyMs * 0.3)
		}
		p.LastSeen = time.Now().UTC()
		s.calculateSocialScore(p)
	}
}

// RecordAuditPerformed updates score when an auditor conducts an audit.
func (s *Swarm) RecordAuditPerformed(auditorID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if p, ok := s.peers[auditorID]; ok {
		p.IsOnline = true
		p.AuditsPerformed++
		p.LastSeen = time.Now().UTC()
		s.calculateSocialScore(p)
	}
}

// SelectQuorum selects 2 co-auditors and 3 storage nodes using:
// 75% weighted based on social score, 25% uniform random (giving new nodes a chance).
func (s *Swarm) SelectQuorum() (auditors []string, storageNodes []string, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var eligible []*Peer
	for id, p := range s.peers {
		if id != s.localID && time.Since(p.LastSeen) < 15*time.Minute {
			eligible = append(eligible, p)
		}
	}

	if len(eligible) < 5 {
		// In small local clusters or test environments, allow reuse or mock peers
		return s.fallbackQuorum(eligible)
	}

	// 1st Auditor is this node
	auditors = append(auditors, s.localID)

	// Pick 2 Co-Auditors
	remaining := make([]*Peer, len(eligible))
	copy(remaining, eligible)

	for len(auditors) < 3 && len(remaining) > 0 {
		pickedIdx := s.weightedPickIndex(remaining)
		auditors = append(auditors, remaining[pickedIdx].NodeID)
		remaining = append(remaining[:pickedIdx], remaining[pickedIdx+1:]...)
	}

	// Pick 3 Storage Nodes
	for len(storageNodes) < 3 && len(remaining) > 0 {
		pickedIdx := s.weightedPickIndex(remaining)
		storageNodes = append(storageNodes, remaining[pickedIdx].NodeID)
		remaining = append(remaining[:pickedIdx], remaining[pickedIdx+1:]...)
	}

	if len(auditors) < 3 || len(storageNodes) < 3 {
		return nil, nil, errors.New("insufficient active peers to form 3-auditor / 3-storage quorum")
	}

	return auditors, storageNodes, nil
}

// weightedPickIndex chooses 75% based on social score, 25% random.
func (s *Swarm) weightedPickIndex(pool []*Peer) int {
	if len(pool) == 1 {
		return 0
	}

	roll := s.rng.Float64()
	if roll < 0.25 {
		// 25% random selection for fairness to new nodes
		return s.rng.Intn(len(pool))
	}

	// 75% weighted selection by social score
	var totalScore float64
	for _, p := range pool {
		score := p.SocialScore
		if score < 1.0 {
			score = 1.0
		}
		totalScore += score
	}

	target := s.rng.Float64() * totalScore
	var cumulative float64
	for i, p := range pool {
		score := p.SocialScore
		if score < 1.0 {
			score = 1.0
		}
		cumulative += score
		if cumulative >= target {
			return i
		}
	}
	return len(pool) - 1
}

func (s *Swarm) fallbackQuorum(eligible []*Peer) ([]string, []string, error) {
	auditors := []string{s.localID}
	storageNodes := []string{}

	// Prioritize real peers for storage nodes first so data is genuinely replicated!
	for _, p := range eligible {
		if len(storageNodes) < 3 {
			storageNodes = append(storageNodes, p.NodeID)
		}
		if len(auditors) < 3 && p.NodeID != s.localID {
			auditors = append(auditors, p.NodeID)
		}
	}

	// Synthesize test nodes ONLY if no real peers exist (e.g. standalone test mode)
	padIdx := 1
	for len(storageNodes) < 3 {
		if len(eligible) > 0 {
			// Reuse real peers instead of inventing fake nodes
			storageNodes = append(storageNodes, eligible[(padIdx-1)%len(eligible)].NodeID)
		} else {
			storageNodes = append(storageNodes, fmt.Sprintf("node_storage_auto_%d", padIdx))
		}
		padIdx++
	}
	for len(auditors) < 3 {
		if len(eligible) > 0 {
			auditors = append(auditors, eligible[(padIdx-1)%len(eligible)].NodeID)
		} else {
			auditors = append(auditors, fmt.Sprintf("node_auditor_auto_%d", padIdx))
		}
		padIdx++
	}

	return auditors, storageNodes, nil
}

// ResolveDataID implements Universal Content Routing:
// If local node does not hold dataID, it transparently queries peers across the network.
func (s *Swarm) ResolveDataID(dataID string) (*storage.Record, error) {
	// 1. Check local store first
	if s.store != nil {
		rec, err := s.store.GetRecord(dataID)
		if err == nil {
			return rec, nil
		}
	}

	// 2. Query peer network
	s.mu.RLock()
	var peerAddrs []string
	for _, p := range s.peers {
		if p.PublicAddr != "" {
			peerAddrs = append(peerAddrs, p.PublicAddr)
		}
	}
	s.mu.RUnlock()

	for _, addr := range peerAddrs {
		targetURL := fmt.Sprintf("%s/api/records/%s", addr, dataID)
		resp, err := s.httpClient.Get(targetURL)
		if err == nil && resp.StatusCode == http.StatusOK {
			var rec storage.Record
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err := json.Unmarshal(body, &rec); err == nil && rec.DataID == dataID {
				// Cache locally if store present
				if s.store != nil {
					_, _ = s.store.UpsertReplicatedRecord(&rec, nil, nil)
				}
				return &rec, nil
			}
		}
	}

	return nil, fmt.Errorf("data_id %q could not be resolved across network", dataID)
}

// ListPeers returns all peers with online peers first, sorted by social score.
func (s *Swarm) ListPeers() []*Peer {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var list []*Peer
	for _, p := range s.peers {
		list = append(list, p)
	}

	sort.Slice(list, func(i, j int) bool {
		if list[i].IsOnline != list[j].IsOnline {
			return list[i].IsOnline
		}
		return list[i].SocialScore > list[j].SocialScore
	})
	return list
}

// ReplicationPayload is the wire payload for peer-to-peer data replication.
type ReplicationPayload struct {
	Record      *storage.Record `json:"record"`
	FileData    []byte          `json:"file_data,omitempty"`
	PictureData []byte          `json:"picture_data,omitempty"`
}

// BroadcastRecord sends a replicated record payload to assigned storage peers.
func (s *Swarm) BroadcastRecord(rec *storage.Record, storagePeers []string, chunkBytes []byte) error {
	return s.BroadcastRecordFull(rec, storagePeers, chunkBytes, nil)
}

// BroadcastRecordFull replicates a record and its attachments to target storage peers.
func (s *Swarm) BroadcastRecordFull(rec *storage.Record, storagePeers []string, fileBytes, picBytes []byte) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	payload := ReplicationPayload{
		Record:      rec,
		FileData:    fileBytes,
		PictureData: picBytes,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	for _, peerID := range storagePeers {
		peer, ok := s.peers[peerID]
		if !ok || peer.PublicAddr == "" {
			continue
		}
		go func(addr string) {
			req, err := http.NewRequest("POST", fmt.Sprintf("%s/api/internal/store", addr), bytes.NewReader(payloadBytes))
			if err == nil {
				req.Header.Set("Content-Type", "application/json")
				resp, err := s.httpClient.Do(req)
				if err == nil {
					_ = resp.Body.Close()
				}
			}
		}(peer.PublicAddr)
	}
	return nil
}

// ConnectPeer contacts a remote peer, announces the local node, and learns peers.
func (s *Swarm) ConnectPeer(peerURL string, rnsDest string, isPublic bool, price int64) error {
	peerURL = strings.TrimRight(peerURL, "/")
	if peerURL == "" || strings.Contains(peerURL, "0.0.0.0") || strings.Contains(peerURL, "127.0.0.1") || strings.Contains(peerURL, "localhost") {
		return nil
	}

	// 1. Fetch remote node info
	start := time.Now()
	resp, err := s.httpClient.Get(fmt.Sprintf("%s/api/health", peerURL))
	if err != nil {
		return fmt.Errorf("failed to contact peer at %s: %w", peerURL, err)
	}
	defer resp.Body.Close()
	latencyMs := float64(time.Since(start).Milliseconds())

	var health struct {
		NodeID   string `json:"node_id"`
		RNSDest  string `json:"rns_dest"`
		IsPublic bool   `json:"is_public"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return fmt.Errorf("failed to parse health from %s: %w", peerURL, err)
	}

	// Register remote peer in local swarm
	if health.NodeID != "" && health.NodeID != s.localID {
		s.RegisterDirectPeer(&Peer{
			NodeID:          health.NodeID,
			PublicAddr:      peerURL,
			ReticulumDest:   health.RNSDest,
			IsPublic:        health.IsPublic,
			PricePerGBMonth: 100,
			AvgLatencyMs:    latencyMs,
			LastSeen:        time.Now().UTC(),
		})
	}

	// 2. Announce local node to remote peer
	selfPeer := &Peer{
		NodeID:          s.localID,
		Name:            s.name,
		Location:        s.location,
		PublicIP:        s.publicIP,
		PublicAddr:      s.localAddr,
		ReticulumDest:   rnsDest,
		IsPublic:        isPublic,
		PricePerGBMonth: price,
		LastSeen:        time.Now().UTC(),
	}
	selfPayload, _ := json.Marshal(selfPeer)
	annResp, err := s.httpClient.Post(fmt.Sprintf("%s/api/peers", peerURL), "application/json", bytes.NewReader(selfPayload))
	if err == nil {
		var annData struct {
			Status string  `json:"status"`
			Peers  []*Peer `json:"peers"`
		}
		if err := json.NewDecoder(annResp.Body).Decode(&annData); err == nil && len(annData.Peers) > 0 {
			for _, kp := range annData.Peers {
				if kp.NodeID != s.localID && kp.PublicAddr != s.localAddr && kp.PublicAddr != "" {
					s.RegisterPeer(kp)
				}
			}
		}
		_ = annResp.Body.Close()
	}

	// 3. Query remote peer's routing table to discover other mesh nodes
	peersResp, err := s.httpClient.Get(fmt.Sprintf("%s/api/peers", peerURL))
	if err == nil {
		defer peersResp.Body.Close()
		var knownPeers []*Peer
		if err := json.NewDecoder(peersResp.Body).Decode(&knownPeers); err == nil {
			for _, kp := range knownPeers {
				if kp.NodeID != s.localID && kp.PublicAddr != s.localAddr && kp.PublicAddr != "" {
					s.RegisterPeer(kp)
				}
			}
		}
	}

	return nil
}

// GossipPeers contacts all bootstrap peers and known peers to recursively sync routing tables.
func (s *Swarm) GossipPeers() {
	s.mu.RLock()
	var targets []string
	targets = append(targets, s.bootstrapPeers...)
	for _, p := range s.peers {
		if p.PublicAddr != "" && p.PublicAddr != s.localAddr {
			targets = append(targets, p.PublicAddr)
		}
	}
	rnsDest := s.rnsDest
	isPublic := s.isPublic
	price := s.price
	name := s.name
	location := s.location
	publicIP := s.publicIP
	s.mu.RUnlock()

	seen := make(map[string]bool)
	var uniqueTargets []string
	for _, t := range targets {
		t = strings.TrimRight(t, "/")
		if !seen[t] && t != "" && t != s.localAddr {
			seen[t] = true
			uniqueTargets = append(uniqueTargets, t)
		}
	}

	client := &http.Client{Timeout: 1800 * time.Millisecond}

	for _, targetURL := range uniqueTargets {
		// 1. Announce self to target and receive their peer list
		selfPeer := &Peer{
			NodeID:          s.localID,
			Name:            name,
			Location:        location,
			PublicIP:        publicIP,
			PublicAddr:      s.localAddr,
			ReticulumDest:   rnsDest,
			IsPublic:        isPublic,
			PricePerGBMonth: price,
			LastSeen:        time.Now().UTC(),
		}
		selfPayload, _ := json.Marshal(selfPeer)
		resp, err := client.Post(fmt.Sprintf("%s/api/peers", targetURL), "application/json", bytes.NewReader(selfPayload))
		if err == nil {
			var annResp struct {
				Status string  `json:"status"`
				Peers  []*Peer `json:"peers"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&annResp); err == nil && len(annResp.Peers) > 0 {
				for _, kp := range annResp.Peers {
					if kp.NodeID != s.localID && kp.PublicAddr != s.localAddr && kp.PublicAddr != "" {
						s.mu.RLock()
						_, exists := s.peers[kp.NodeID]
						s.mu.RUnlock()
						if !exists {
							go func(addr string) {
								_ = s.ConnectPeer(addr, rnsDest, isPublic, price)
							}(kp.PublicAddr)
						}
					}
				}
			}
			_ = resp.Body.Close()
		}

		// 2. Fetch target's routing table via GET
		getResp, err := client.Get(fmt.Sprintf("%s/api/peers", targetURL))
		if err == nil {
			var peerList []*Peer
			if err := json.NewDecoder(getResp.Body).Decode(&peerList); err == nil {
				for _, kp := range peerList {
					if kp.NodeID != s.localID && kp.PublicAddr != s.localAddr && kp.PublicAddr != "" {
						s.mu.RLock()
						_, exists := s.peers[kp.NodeID]
						s.mu.RUnlock()
						if !exists {
							go func(addr string) {
								_ = s.ConnectPeer(addr, rnsDest, isPublic, price)
							}(kp.PublicAddr)
						}
					}
				}
			}
			_ = getResp.Body.Close()
		}
	}
}

// ProbePeers pings all known peers to dynamically refresh online/offline status.
func (s *Swarm) ProbePeers() {
	s.mu.Lock()
	type peerCheck struct {
		nodeID string
		addr   string
	}
	var toCheck []peerCheck
	now := time.Now().UTC()
	var toPrune []string

	for id, p := range s.peers {
		// Clean up dead nodes:
		// 1) Ephemeral edge nodes with no PublicAddr that haven't announced in > 30 seconds
		// 2) Nodes marked offline that haven't been seen in > 3 minutes
		if p.PublicAddr == "" && now.Sub(p.LastSeen) > 30*time.Second {
			toPrune = append(toPrune, id)
			continue
		}
		if !p.IsOnline && now.Sub(p.LastSeen) > 3*time.Minute {
			toPrune = append(toPrune, id)
			continue
		}

		// For edge nodes with no inbound public address:
		// Liveness is driven by periodic discovery announcements (within 15s)
		if p.PublicAddr == "" {
			if now.Sub(p.LastSeen) <= 15*time.Second {
				p.IsOnline = true
			} else {
				p.IsOnline = false
			}
			continue
		}

		toCheck = append(toCheck, peerCheck{nodeID: id, addr: p.PublicAddr})
	}

	for _, pruneID := range toPrune {
		delete(s.peers, pruneID)
	}
	s.mu.Unlock()

	client := &http.Client{Timeout: 1200 * time.Millisecond}
	for _, pc := range toCheck {
		start := time.Now()
		resp, err := client.Get(fmt.Sprintf("%s/api/health", pc.addr))
		if err == nil && resp.StatusCode == http.StatusOK {
			var h struct {
				NodeID string `json:"node_id"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&h)
			_ = resp.Body.Close()

			if h.NodeID != "" && h.NodeID != pc.nodeID {
				// Prevent self-loopback or wrong node collision: clear invalid address!
				s.mu.Lock()
				if p, ok := s.peers[pc.nodeID]; ok {
					p.PublicAddr = ""
					p.IsOnline = false
				}
				s.mu.Unlock()
				continue
			}

			latencyMs := float64(time.Since(start).Milliseconds())
			s.mu.Lock()
			if p, ok := s.peers[pc.nodeID]; ok {
				p.IsOnline = true
				p.LastSeen = time.Now().UTC()
				if p.AvgLatencyMs == 0 {
					p.AvgLatencyMs = latencyMs
				} else {
					p.AvgLatencyMs = (p.AvgLatencyMs * 0.8) + (latencyMs * 0.2)
				}
				s.calculateSocialScore(p)
			}
			s.mu.Unlock()
		} else {
			if resp != nil {
				_ = resp.Body.Close()
			}
			s.MarkPeerOffline(pc.nodeID)
		}
	}
}

// StartLivenessLoop starts periodic background peer pinging and gossip discovery.
func (s *Swarm) StartLivenessLoop(interval time.Duration) {
	go func() {
		time.Sleep(500 * time.Millisecond)
		s.GossipPeers()
		s.ProbePeers()

		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			s.GossipPeers()
			s.ProbePeers()
		}
	}()
}

