package p2p

import (
	"os"
	"testing"
	"time"

	"github.com/stiqrhub/stiqrcloud/pkg/storage"
)

func TestPersonalSwarmOperations(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "stiqrcloud_personal_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	store, err := storage.NewStore(tempDir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	swarm := NewSwarm("local-test-node", "http://10.0.0.23:8100", store)

	// Test 1: NormalizeRNSDest
	rawDest := "<A5E2992b3adcad7808a44c944f871660> "
	normalized := NormalizeRNSDest(rawDest)
	if normalized != "a5e2992b3adcad7808a44c944f871660" {
		t.Fatalf("expected normalized destination 'a5e2992b3adcad7808a44c944f871660', got %q", normalized)
	}

	// Test 2: DetectLANAddress
	if !DetectLANAddress("http://10.0.0.2:8100") {
		t.Fatalf("expected 10.0.0.2 to be recognized as LAN")
	}
	if !DetectLANAddress("http://192.168.1.50:8100") {
		t.Fatalf("expected 192.168.1.50 to be recognized as LAN")
	}
	if !DetectLANAddress("http://localhost:8100") {
		t.Fatalf("expected localhost to be recognized as LAN")
	}
	if DetectLANAddress("http://93.184.216.34:8100") {
		t.Fatalf("expected public IP to NOT be recognized as LAN")
	}

	// Test 3: AddPersonalNode & Persistence
	pNode, err := swarm.AddPersonalNode(rawDest, "Raspberry Pi Node", "", "http://10.0.0.2:8100")
	if err != nil {
		t.Fatalf("failed to add personal node: %v", err)
	}
	if pNode.Name != "Raspberry Pi Node" {
		t.Fatalf("unexpected node name: %s", pNode.Name)
	}
	// LocalAddr should be auto-detected from LAN public addr
	if pNode.LocalAddr != "http://10.0.0.2:8100" {
		t.Fatalf("expected LocalAddr auto-detection, got %s", pNode.LocalAddr)
	}
	if !swarm.IsPersonalPeer(rawDest) {
		t.Fatalf("expected rawDest to be recognized as personal peer")
	}

	// Test 4: Reload Swarm and verify persistence
	swarmReloaded := NewSwarm("local-test-node", "http://10.0.0.23:8100", store)
	list := swarmReloaded.ListPersonalNodes()
	if len(list) != 1 {
		t.Fatalf("expected 1 personal node after reload, got %d", len(list))
	}
	if list[0].Name != "Raspberry Pi Node" {
		t.Fatalf("expected name 'Raspberry Pi Node', got %s", list[0].Name)
	}

	// Test 5: AirgapPrep
	res, err := swarmReloaded.AirgapPrep()
	if err != nil {
		t.Fatalf("airgap prep failed: %v", err)
	}
	if safe, ok := res["safe_to_disconnect"].(bool); !ok || !safe {
		t.Fatalf("expected safe_to_disconnect to be true, got %v", res)
	}

	// Test 6: AirgapStatus
	status := swarmReloaded.AirgapStatus()
	if safe, ok := status["safe_to_disconnect"].(bool); !ok || !safe {
		t.Fatalf("expected airgap status safe_to_disconnect to be true, got %v", status)
	}

	// Test 7: RemovePersonalNode
	if err := swarmReloaded.RemovePersonalNode(rawDest); err != nil {
		t.Fatalf("failed to remove personal node: %v", err)
	}
	if swarmReloaded.IsPersonalPeer(rawDest) {
		t.Fatalf("expected peer to be removed")
	}
	if len(swarmReloaded.ListPersonalNodes()) != 0 {
		t.Fatalf("expected 0 personal nodes after removal")
	}
}

func TestTouchPeerPersonalAutoDetect(t *testing.T) {
	tempDir, _ := os.MkdirTemp("", "stiqrcloud_touch_test_*")
	defer os.RemoveAll(tempDir)
	store, _ := storage.NewStore(tempDir)
	swarm := NewSwarm("local-node", "http://10.0.0.23:8100", store)

	dest := "<a5e2992b3adcad7808a44c944f871660>"
	_, _ = swarm.AddPersonalNode(dest, "Pi", "", "")

	// Register peer
	swarm.RegisterPeer(&Peer{
		NodeID:        "pi-node-id",
		PublicAddr:    "http://10.0.0.2:8100",
		ReticulumDest: dest,
	})

	swarm.TouchPeer("pi-node-id")

	nodes := swarm.ListPersonalNodes()
	if len(nodes) != 1 {
		t.Fatalf("expected 1 personal node, got %d", len(nodes))
	}
	if nodes[0].LocalAddr != "http://10.0.0.2:8100" {
		t.Fatalf("expected TouchPeer/RegisterPeer to detect LAN addr, got %s", nodes[0].LocalAddr)
	}
	if time.Since(nodes[0].LastSeen) > 5*time.Second {
		t.Fatalf("expected LastSeen to be updated")
	}
}
