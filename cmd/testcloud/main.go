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
	"syscall"

	"github.com/stiqrhub/testcloud/pkg/audit"
	"github.com/stiqrhub/testcloud/pkg/escrow"
	"github.com/stiqrhub/testcloud/pkg/gateway"
	"github.com/stiqrhub/testcloud/pkg/p2p"
	"github.com/stiqrhub/testcloud/pkg/reticulum"
	"github.com/stiqrhub/testcloud/pkg/storage"
	"github.com/stiqrhub/testcloud/pkg/version"
)

func main() {
	port := flag.Int("port", 8101, "Port for HTTP REST API and local Dashboard")
	dataDir := flag.String("data-dir", "./data", "Local directory for database records and chunks")
	standalone := flag.Bool("standalone", false, "Run in pure standalone database mode (offline/local only)")
	nodeID := flag.String("node-id", "", "Custom node identifier (auto-generated if empty)")
	isPublic := flag.Bool("public", false, "Expose node as a public gateway")
	rnsPort := flag.Int("rns-port", 4242, "Reticulum UDP interface port")
	price := flag.Int64("price", 100, "Price per GB per month in sats")
	showVersion := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version.GetVersionSummary())
		return
	}

	if *nodeID == "" {
		b := make([]byte, 6)
		_, _ = rand.Read(b)
		*nodeID = "node_" + hex.EncodeToString(b)
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
		_ = rnsManager.StartUDP(*rnsPort)
	}

	// 3. Initialize Swarm, Escrow, and Auditor
	listenAddr := fmt.Sprintf("http://127.0.0.1:%d", *port)
	swarm := p2p.NewSwarm(*nodeID, listenAddr, store)
	escrowMgr := escrow.NewManager(*nodeID, *price)
	auditorCoord := audit.NewCoordinator(*nodeID, store, escrowMgr, swarm)

	// 4. Initialize Gateway & Dashboard Server
	gw := gateway.NewServer(gateway.Config{
		NodeID:     *nodeID,
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
		Addr:    fmt.Sprintf(":%d", *port),
		Handler: gw.Handler(),
	}

	// Startup Banner
	fmt.Println("======================================================================")
	fmt.Printf(" %s\n", version.GetVersionSummary())
	fmt.Println("======================================================================")
	fmt.Printf(" Node ID:             %s\n", *nodeID)
	fmt.Printf(" Running Mode:        %s\n", getModeName(*standalone))
	fmt.Printf(" HTTP & Dashboard:    http://127.0.0.1:%d/dashboard\n", *port)
	fmt.Printf(" Public REST API:     http://127.0.0.1:%d/api\n", *port)
	fmt.Printf(" Reticulum RNS Dest:  %s\n", rnsDest)
	fmt.Printf(" Storage Directory:   %s\n", absDataDir)
	fmt.Printf(" Settings Updates:    %s\n", version.CurrentVersion)
	fmt.Println("======================================================================")

	// Graceful shutdown handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	<-sigChan
	fmt.Println("\nShutting down TestCloud node gracefully...")
	rnsManager.Stop()
	_ = server.Close()
	fmt.Println("Shutdown complete.")
}

func getModeName(standalone bool) string {
	if standalone {
		return "Standalone Local Database (Offline/Zero Network)"
	}
	return "Decentralized P2P Mesh (3-Auditor + 3-Storage Quorum & Reticulum)"
}
