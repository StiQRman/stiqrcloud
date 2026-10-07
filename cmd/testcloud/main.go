package main

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/stiqrhub/testcloud/pkg/audit"
	"github.com/stiqrhub/testcloud/pkg/escrow"
	"github.com/stiqrhub/testcloud/pkg/gateway"
	"github.com/stiqrhub/testcloud/pkg/p2p"
	"github.com/stiqrhub/testcloud/pkg/reticulum"
	"github.com/stiqrhub/testcloud/pkg/storage"
	"github.com/stiqrhub/testcloud/pkg/version"
)

func main() {
	host := flag.String("host", "0.0.0.0", "Host IP to bind HTTP and Reticulum interfaces")
	port := flag.Int("port", 8100, "Port for HTTP REST API and Dashboard")
	dashPort := flag.Int("dashboard-port", 8101, "Secondary port for Dashboard (if different from port)")
	dataDir := flag.String("data-dir", "./data", "Local directory for database records and chunks")
	standalone := flag.Bool("standalone", false, "Run in pure standalone database mode (offline/local only)")
	nodeID := flag.String("node-id", "", "Custom node identifier (auto-generated if empty)")
	isPublic := flag.Bool("public", false, "Expose node as a public gateway")
	rnsPort := flag.Int("rns-port", 4242, "Reticulum UDP interface port")
	price := flag.Int64("price", 100, "Price per GB per month in sats")
	peers := flag.String("peers", "", "Comma-separated list of peer bootstrap URLs (e.g. http://127.10.1.1:8101)")
	showVersion := flag.Bool("version", false, "Print version and exit")

	// Compatibility flags for StiQRcloud drop-in replacement
	name := flag.String("name", "Node Operator", "Operator display nickname")
	location := flag.String("location", "Home Network", "Operator geographic location")
	publicIP := flag.String("public-ip", "", "Explicit public WAN IP")
	publicAddr := flag.String("public-addr", "", "Public reachable IP and port for P2P traffic")
	gatewayURL := flag.String("gateway-url", "", "Public HTTP/HTTPS Gateway URL for mobile apps")
	discoveryURL := flag.String("discovery-url", "", "Public internet discovery URL")
	_ = flag.Int("p2p-port", 9100, "P2P swarm communication port")
	flag.Parse()

	if *showVersion {
		fmt.Println(version.GetVersionSummary())
		return
	}

	if *nodeID == "" {
		if *name != "" && *name != "Node Operator" {
			*nodeID = strings.ToLower(strings.ReplaceAll(*name, " ", "_"))
		} else {
			b := make([]byte, 6)
			_, _ = rand.Read(b)
			*nodeID = "node_" + hex.EncodeToString(b)
		}
	}

	absDataDir, err := filepath.Abs(*dataDir)
	if err != nil {
		log.Fatalf("Invalid data directory: %v", err)
	}

	// 1. Initialize Storage Engine (Database-first)
	store, err := storage.NewStore(absDataDir)
	if err != nil {
		log.Fatalf("Failed to initialize database store: %v", err)
	}

	// 2. Initialize Reticulum Network Stack (RNS) Identity
	rnsIdentity, err := reticulum.NewIdentity("testcloud.node")
	if err != nil {
		log.Fatalf("Failed to initialize Reticulum identity: %v", err)
	}
	rnsDest := rnsIdentity.Destination().String()

	rnsManager := reticulum.NewInterfaceManager(rnsIdentity)
	if !*standalone {
		if err := rnsManager.StartUDPAddr(*host, *rnsPort); err != nil {
			log.Printf("Warning: Failed to bind Reticulum UDP interface on %s:%d: %v", *host, *rnsPort, err)
		}
		if err := rnsManager.StartTCPAddr(*host, *rnsPort); err != nil {
			log.Printf("Warning: Failed to bind Reticulum TCP interface on %s:%d: %v", *host, *rnsPort, err)
		}
	}

	// 3. Initialize Swarm, Escrow, and Auditor
	listenAddr := ""
	if *gatewayURL != "" {
		listenAddr = strings.TrimRight(*gatewayURL, "/")
	} else if *publicAddr != "" {
		addr := strings.TrimRight(*publicAddr, "/")
		if !strings.HasPrefix(addr, "http://") && !strings.HasPrefix(addr, "https://") {
			addr = "http://" + addr
		}
		listenAddr = addr
	} else if *host != "0.0.0.0" && *host != "127.0.0.1" && *host != "localhost" {
		listenAddr = fmt.Sprintf("http://%s:%d", *host, *port)
	}
	swarm := p2p.NewSwarm(*nodeID, listenAddr, store)
	swarm.SetIdentity(rnsDest, *isPublic, *price)
	swarm.SetMetadata(*name, *location, *publicIP)

	if *peers != "" && !*standalone {
		for _, pURL := range strings.Split(*peers, ",") {
			pURL = strings.TrimSpace(pURL)
			if pURL != "" && pURL != listenAddr {
				if !strings.HasPrefix(pURL, "http://") && !strings.HasPrefix(pURL, "https://") {
					pURL = "http://" + pURL
				}
				swarm.AddBootstrapPeer(pURL)
			}
		}
	}

	if *discoveryURL != "" && !*standalone {
		swarm.StartDiscovery(*discoveryURL, 3*time.Second)
	}

	escrowMgr := escrow.NewManager(*nodeID, *price, absDataDir)
	auditorCoord := audit.NewCoordinator(*nodeID, store, escrowMgr, swarm)

	// Hook to ensure locally pulled replica records register in local escrow manager
	swarm.SetOnRecordSynced(func(rec *storage.Record) {
		if escrowMgr != nil && rec != nil {
			rates := make(map[string]int64)
			for _, st := range rec.StoragePeers {
				rates[st] = 100
			}
			_, _ = escrowMgr.CreateContract(rec.DataID, rec.UserPubAddress, rec.TotalBytes, 1, rates, rec.AuditorPeers)
		}
	})

	// 4. Initialize Gateway & Dashboard Server
	if !*standalone {
		swarm.StartLivenessLoop(3 * time.Second)
		swarm.StartSyncLoop(10 * time.Second)
	}
	if *isPublic && !*standalone {
		auditorCoord.StartAuditLoop(6 * time.Second)
	}

	gw := gateway.NewServer(gateway.Config{
		NodeID:     *nodeID,
		Name:       *name,
		Location:   *location,
		PublicIP:   *publicIP,
		Store:      store,
		Swarm:      swarm,
		EscrowMgr:  escrowMgr,
		Auditor:    auditorCoord,
		RNSManager: rnsManager,
		RNSDest:    rnsDest,
		IsPublic:   *isPublic,
		Standalone: *standalone,
	})

	server := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", *host, *port),
		Handler: gw.Handler(),
	}

	// Startup Banner
	fmt.Println("======================================================================")
	fmt.Printf(" %s\n", version.GetVersionSummary())
	fmt.Println("======================================================================")
	fmt.Printf(" Node ID:             %s\n", *nodeID)
	fmt.Printf(" Running Mode:        %s\n", getModeName(*standalone))
	fmt.Printf(" HTTP & Dashboard:    http://%s:%d/dashboard\n", *host, *port)
	fmt.Printf(" Public REST API:     http://%s:%d/api\n", *host, *port)
	fmt.Printf(" Reticulum UDP & TCP: %s:%d\n", *host, *rnsPort)
	fmt.Printf(" Reticulum RNS Dest:  %s\n", rnsDest)
	fmt.Printf(" Storage Directory:   %s\n", absDataDir)
	fmt.Printf(" Settings Updates:    %s\n", version.CurrentVersion)
	fmt.Println("======================================================================")

	// Graceful shutdown handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error on %d: %v", *port, err)
		}
	}()

	// Secondary listener on dashboard-port if specified and distinct from port
	var dashServer *http.Server
	if *dashPort > 0 && *dashPort != *port {
		dashServer = &http.Server{
			Addr:    fmt.Sprintf("%s:%d", *host, *dashPort),
			Handler: gw.Handler(),
		}
		go func() {
			if err := dashServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Printf("Notice: Dashboard server on %d: %v", *dashPort, err)
			}
		}()
	}

	// Connect to bootstrap peers
	if *peers != "" && !*standalone {
		go func() {
			time.Sleep(300 * time.Millisecond)
			peerList := strings.Split(*peers, ",")
			for _, pURL := range peerList {
				pURL = strings.TrimSpace(pURL)
				if pURL != "" && pURL != listenAddr {
					if err := swarm.ConnectPeer(pURL, rnsDest, *isPublic, *price); err != nil {
						log.Printf("[%s] Notice: Connecting to peer %s: %v", *nodeID, pURL, err)
					} else {
						log.Printf("[%s] Successfully peered with %s", *nodeID, pURL)
					}
				}
			}
		}()
	}

	<-sigChan
	fmt.Println("\nShutting down TestCloud node gracefully...")
	rnsManager.Stop()
	_ = server.Close()
	if dashServer != nil {
		_ = dashServer.Close()
	}
	fmt.Println("Shutdown complete.")
}

func getModeName(standalone bool) string {
	if standalone {
		return "Standalone Local Database (Offline/Zero Network)"
	}
	return "Decentralized P2P Mesh (3-Auditor + 3-Storage Quorum & Reticulum)"
}
