package reticulum

import (
	"bytes"
	"net"
	"testing"
	"time"
)

func TestReticulumIdentityAndDestination(t *testing.T) {
	id, err := NewIdentity("testcloud.node")
	if err != nil {
		t.Fatalf("NewIdentity failed: %v", err)
	}

	dest := id.Destination()
	if len(dest) != 16 {
		t.Fatalf("Destination must be exactly 16 bytes, got %d", len(dest))
	}

	hexStr := dest.Hex()
	if len(hexStr) != 32 {
		t.Fatalf("Destination hex string must be 32 characters, got %d", len(hexStr))
	}

	parsedDest, err := DestinationFromHex(hexStr)
	if err != nil {
		t.Fatalf("DestinationFromHex failed: %v", err)
	}

	if parsedDest != dest {
		t.Fatalf("Parsed destination does not match: got %s, want %s", parsedDest, dest)
	}
}

func TestReticulumAnnounceAndVerify(t *testing.T) {
	id, err := NewIdentity("testcloud.node")
	if err != nil {
		t.Fatalf("NewIdentity failed: %v", err)
	}

	appData := []byte("price=5;storage=100gb")
	ann, err := id.CreateAnnounce(appData)
	if err != nil {
		t.Fatalf("CreateAnnounce failed: %v", err)
	}

	if !VerifyAnnounce(ann) {
		t.Fatalf("VerifyAnnounce failed for valid announce")
	}

	// Tamper with announce
	ann.AppData = []byte("tampered")
	if VerifyAnnounce(ann) {
		t.Fatalf("VerifyAnnounce succeeded for tampered announce")
	}
}

func TestReticulumPacketFraming(t *testing.T) {
	id, _ := NewIdentity("testcloud.node")
	dest := id.Destination()

	originalPacket := &Packet{
		HeaderFlags: 0x01,
		Hops:        0x02,
		Destination: dest,
		Context:     0x00,
		Payload:     []byte("testcloud query record-xyz"),
	}

	encoded := EncodePacket(originalPacket)
	decoded, err := DecodePacket(encoded)
	if err != nil {
		t.Fatalf("DecodePacket failed: %v", err)
	}

	if decoded.Destination != originalPacket.Destination {
		t.Fatalf("Decoded destination mismatch")
	}
	if string(decoded.Payload) != string(originalPacket.Payload) {
		t.Fatalf("Decoded payload mismatch: got %s, want %s", decoded.Payload, originalPacket.Payload)
	}
}

func TestHDLCFramingAndTCPServer(t *testing.T) {
	// 1. Test HDLC escape and unescape
	raw := []byte{0x01, 0x7E, 0x02, 0x7D, 0x03, 0x7E, 0x04}
	escaped := EscapeHDLC(raw)
	unescaped := UnescapeHDLC(escaped)
	if !bytes.Equal(raw, unescaped) {
		t.Fatalf("HDLC escape/unescape mismatch: got %v, want %v", unescaped, raw)
	}

	// 2. Test TCP Server Interface with HDLC framed Announce
	serverID, _ := NewIdentity("testcloud.server")
	mgr := NewInterfaceManager(serverID)
	port := 24242
	if err := mgr.StartTCPAddr("127.0.0.1", port); err != nil {
		t.Fatalf("StartTCPAddr failed: %v", err)
	}
	defer mgr.Stop()

	// Client connects via TCP
	conn, err := net.Dial("tcp", "127.0.0.1:24242")
	if err != nil {
		t.Fatalf("Client TCP dial failed: %v", err)
	}
	defer conn.Close()

	// Read initial announce frame sent by server on connection
	_ = conn.SetReadDeadline(time.Now().Add(1 * time.Second))
	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	if err != nil || n < 3 {
		t.Fatalf("Failed to read HDLC frame from server: %v (bytes: %d)", err, n)
	}

	if buf[0] != HDLCFlag || buf[n-1] != HDLCFlag {
		t.Fatalf("Expected HDLC framing flags 0x7E at boundaries, got: %x ... %x", buf[0], buf[n-1])
	}
}
