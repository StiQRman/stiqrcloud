package crypto

import (
	"bytes"
	"testing"
)

func TestMLKEM768Encapsulation(t *testing.T) {
	kp, err := GenerateMLKEM768Keypair()
	if err != nil {
		t.Fatalf("GenerateMLKEM768Keypair failed: %v", err)
	}

	ctHex, ss1, err := EncapsulateMLKEM768(kp.PublicKey)
	if err != nil {
		t.Fatalf("EncapsulateMLKEM768 failed: %v", err)
	}

	ss2, err := DecapsulateMLKEM768(kp.PrivateKey, ctHex)
	if err != nil {
		t.Fatalf("DecapsulateMLKEM768 failed: %v", err)
	}

	if !bytes.Equal(ss1, ss2) {
		t.Fatalf("Decapsulated shared secret does not match: %x != %x", ss1, ss2)
	}
}

func TestMLDSA65SigningAndVerification(t *testing.T) {
	kp, err := GenerateMLDSA65Keypair()
	if err != nil {
		t.Fatalf("GenerateMLDSA65Keypair failed: %v", err)
	}

	msg := []byte("critical threshold quorum write update payload")
	sig, err := SignMLDSA65(msg, kp.PrivateKey)
	if err != nil {
		t.Fatalf("SignMLDSA65 failed: %v", err)
	}

	if !VerifyMLDSA65(msg, kp.PublicKey, sig) {
		t.Fatalf("VerifyMLDSA65 failed for valid signature")
	}

	if VerifyMLDSA65([]byte("tampered msg"), kp.PublicKey, sig) {
		t.Fatalf("VerifyMLDSA65 passed on tampered message")
	}
}

func TestHybridSignatureVerification(t *testing.T) {
	msg := []byte("authorization payload for record edit")

	// 1. Ed25519 classical key
	edKP, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair failed: %v", err)
	}
	edSig, err := SignHybrid(msg, edKP.PrivateKey)
	if err != nil {
		t.Fatalf("SignHybrid ed25519 failed: %v", err)
	}
	if !VerifyHybridSignature(msg, edKP.PublicKey, edSig) {
		t.Fatalf("VerifyHybridSignature failed for Ed25519")
	}

	// 2. ML-DSA-65 post-quantum key
	pqKP, err := GenerateMLDSA65Keypair()
	if err != nil {
		t.Fatalf("GenerateMLDSA65Keypair failed: %v", err)
	}
	pqSig, err := SignHybrid(msg, pqKP.PrivateKey)
	if err != nil {
		t.Fatalf("SignHybrid mldsa65 failed: %v", err)
	}
	if !VerifyHybridSignature(msg, pqKP.PublicKey, pqSig) {
		t.Fatalf("VerifyHybridSignature failed for ML-DSA-65")
	}

	// Cross tampering
	if VerifyHybridSignature(msg, edKP.PublicKey, pqSig) {
		t.Fatalf("VerifyHybridSignature passed with mismatched key/sig types")
	}
}

func BenchmarkMLKEM768(b *testing.B) {
	kp, err := GenerateMLKEM768Keypair()
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctHex, _, err := EncapsulateMLKEM768(kp.PublicKey)
		if err != nil {
			b.Fatal(err)
		}
		_, err = DecapsulateMLKEM768(kp.PrivateKey, ctHex)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMLDSA65(b *testing.B) {
	kp, err := GenerateMLDSA65Keypair()
	if err != nil {
		b.Fatal(err)
	}
	msg := []byte("benchmark test message payload")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sig, err := SignMLDSA65(msg, kp.PrivateKey)
		if err != nil {
			b.Fatal(err)
		}
		if !VerifyMLDSA65(msg, kp.PublicKey, sig) {
			b.Fatal("verify failed")
		}
	}
}
