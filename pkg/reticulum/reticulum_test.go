package reticulum

import (
	"testing"
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
