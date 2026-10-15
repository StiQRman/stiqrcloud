package crypto

import (
	"bytes"
	"testing"
)

func TestShamir2of3SecretSharing(t *testing.T) {
	dek := []byte("0123456789abcdef0123456789abcdef") // 32-byte DEK
	k := 2
	n := 3

	shares, err := SplitSecret(dek, k, n)
	if err != nil {
		t.Fatalf("SplitSecret failed: %v", err)
	}

	if len(shares) != 3 {
		t.Fatalf("Expected 3 shares, got %d", len(shares))
	}

	for i, s := range shares {
		if s.Index != byte(i+1) {
			t.Errorf("Share %d has unexpected index %d", i, s.Index)
		}
		if len(s.Value) != len(dek) {
			t.Errorf("Share %d has invalid length %d", i, len(s.Value))
		}
	}

	// Any 2 shares should reconstruct the exact DEK
	pairs := [][]Share{
		{shares[0], shares[1]}, // 1 & 2
		{shares[0], shares[2]}, // 1 & 3
		{shares[1], shares[2]}, // 2 & 3
	}

	for idx, pair := range pairs {
		recovered, err := ReconstructSecret(pair, k)
		if err != nil {
			t.Fatalf("Pair %d failed to reconstruct secret: %v", idx, err)
		}
		if !bytes.Equal(recovered, dek) {
			t.Fatalf("Pair %d reconstructed wrong secret: got %x, want %x", idx, recovered, dek)
		}
	}

	// 1 share alone should fail
	_, err = ReconstructSecret([]Share{shares[0]}, k)
	if err == nil {
		t.Fatalf("Expected error when attempting reconstruction with only 1 share")
	}

	// Duplicate shares should fail
	_, err = ReconstructSecret([]Share{shares[0], shares[0]}, k)
	if err == nil {
		t.Fatalf("Expected error when attempting reconstruction with duplicate shares")
	}
}

func BenchmarkShamirSplitAndReconstruct(b *testing.B) {
	dek := []byte("0123456789abcdef0123456789abcdef")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		shares, err := SplitSecret(dek, 2, 3)
		if err != nil {
			b.Fatal(err)
		}
		_, err = ReconstructSecret(shares[:2], 2)
		if err != nil {
			b.Fatal(err)
		}
	}
}
