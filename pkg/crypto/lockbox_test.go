package crypto

import (
	"bytes"
	"testing"
)

func TestThresholdEnvelopeAndLockboxes(t *testing.T) {
	// 1. Generate 256-bit random DEK
	dek, err := GenerateRandomKey()
	if err != nil {
		t.Fatalf("GenerateRandomKey failed: %v", err)
	}

	// 2. Alice's identity (ML-KEM-768 for encapsulation, ML-DSA-65 for signing)
	aliceKEM, err := GenerateMLKEM768Keypair()
	if err != nil {
		t.Fatalf("GenerateMLKEM768Keypair failed: %v", err)
	}
	aliceSign, err := GenerateMLDSA65Keypair()
	if err != nil {
		t.Fatalf("GenerateMLDSA65Keypair failed: %v", err)
	}

	// 3. Create threshold envelope (2-of-3 quorum)
	shares, nodeBoxes, err := CreateThresholdEnvelope(dek, "alice_id", aliceKEM.PublicKey, aliceSign.PublicKey)
	if err != nil {
		t.Fatalf("CreateThresholdEnvelope failed: %v", err)
	}

	if len(nodeBoxes) != 3 {
		t.Fatalf("Expected 3 node lockboxes, got %d", len(nodeBoxes))
	}

	// Verify each node has a lockbox (~1.1 KB packet)
	for nodeIdx := 1; nodeIdx <= 3; nodeIdx++ {
		box, exists := nodeBoxes[nodeIdx]
		if !exists {
			t.Fatalf("Missing lockbox for node %d", nodeIdx)
		}
		if box.ShareIndex != byte(nodeIdx) {
			t.Errorf("Node %d has mismatching share index %d", nodeIdx, box.ShareIndex)
		}
		if box.Packet.MLKEMCiphertext == "" || box.Packet.Ciphertext == "" {
			t.Errorf("Node %d has empty ciphertext payload", nodeIdx)
		}
	}

	// 4. Test 2-of-3 retrieval combinations for Alice
	combos := [][]int{
		{1, 2},
		{1, 3},
		{2, 3},
	}
	for i, combo := range combos {
		packets := []*EncryptedSharePacket{
			&nodeBoxes[combo[0]].Packet,
			&nodeBoxes[combo[1]].Packet,
		}
		recoveredDEK, err := ReconstructDEKFromLockboxes(packets, aliceKEM.PrivateKey)
		if err != nil {
			t.Fatalf("Combo %d (nodes %v) failed to reconstruct DEK: %v", i, combo, err)
		}
		if !bytes.Equal(recoveredDEK, dek) {
			t.Fatalf("Combo %d reconstructed wrong DEK", i)
		}
	}

	// 5. Test 1 share alone cannot reconstruct
	singlePacket := []*EncryptedSharePacket{&nodeBoxes[1].Packet}
	_, err = ReconstructDEKFromLockboxes(singlePacket, aliceKEM.PrivateKey)
	if err == nil {
		t.Fatalf("Expected reconstruction to fail with only 1 share")
	}

	// 6. Add Friend #2 (Bob) without re-encrypting data
	bobKEM, err := GenerateMLKEM768Keypair()
	if err != nil {
		t.Fatalf("Bob GenerateMLKEM768Keypair failed: %v", err)
	}
	bobSign, err := GenerateMLDSA65Keypair()
	if err != nil {
		t.Fatalf("Bob GenerateMLDSA65Keypair failed: %v", err)
	}

	bobBoxes, err := AddFriendToEnvelope(shares, "bob_id", bobKEM.PublicKey, bobSign.PublicKey)
	if err != nil {
		t.Fatalf("AddFriendToEnvelope failed: %v", err)
	}

	// Bob retrieves from Node 2 and Node 3
	bobPackets := []*EncryptedSharePacket{
		&bobBoxes[2].Packet,
		&bobBoxes[3].Packet,
	}
	bobRecoveredDEK, err := ReconstructDEKFromLockboxes(bobPackets, bobKEM.PrivateKey)
	if err != nil {
		t.Fatalf("Bob failed to reconstruct DEK: %v", err)
	}
	if !bytes.Equal(bobRecoveredDEK, dek) {
		t.Fatalf("Bob recovered wrong DEK")
	}

	// 7. Revoke Bob: Node 1 and Node 2 purge Bob's lockbox slot
	// Now Bob can only access Node 3's packet
	revokedPackets := []*EncryptedSharePacket{
		&bobBoxes[3].Packet,
	}
	_, err = ReconstructDEKFromLockboxes(revokedPackets, bobKEM.PrivateKey)
	if err == nil {
		t.Fatalf("Expected Bob to be unable to recover DEK after revocation")
	}
}

func BenchmarkThresholdEnvelopeOperations(b *testing.B) {
	dek, _ := GenerateRandomKey()
	aliceKEM, _ := GenerateMLKEM768Keypair()
	aliceSign, _ := GenerateMLDSA65Keypair()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, nodeBoxes, err := CreateThresholdEnvelope(dek, "alice", aliceKEM.PublicKey, aliceSign.PublicKey)
		if err != nil {
			b.Fatal(err)
		}

		packets := []*EncryptedSharePacket{
			&nodeBoxes[1].Packet,
			&nodeBoxes[2].Packet,
		}
		_, err = ReconstructDEKFromLockboxes(packets, aliceKEM.PrivateKey)
		if err != nil {
			b.Fatal(err)
		}
	}
}
