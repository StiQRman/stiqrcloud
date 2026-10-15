package crypto

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
)

// GF(2^8) field arithmetic with generator 3 and modulus 0x11b (AES field).
var (
	gfExp [512]byte
	gfLog [256]byte
)

func init() {
	x := 1
	for i := 0; i < 255; i++ {
		gfExp[i] = byte(x)
		gfExp[i+255] = byte(x)
		gfLog[x] = byte(i)
		// multiply by 3 in GF(2^8) with irreducible poly 0x11b
		// 3*x = (2*x) ^ x
		x2 := x << 1
		if x2&0x100 != 0 {
			x2 ^= 0x11b
		}
		x = x2 ^ x
	}
	gfExp[510] = gfExp[0]
	gfExp[511] = gfExp[1]
}

func gfMul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return gfExp[int(gfLog[a])+int(gfLog[b])]
}

func gfDiv(a, b byte) (byte, error) {
	if b == 0 {
		return 0, errors.New("division by zero in GF(2^8)")
	}
	if a == 0 {
		return 0, nil
	}
	diff := int(gfLog[a]) - int(gfLog[b]) + 255
	return gfExp[diff%255], nil
}

// Share represents a single share in Shamir's Secret Sharing scheme.
type Share struct {
	Index byte   `json:"index"` // Evaluation point x (1..255)
	Value []byte `json:"value"` // P(x) values for each byte of the secret
}

// SplitSecret splits a secret into n shares such that any k shares can reconstruct it.
// Here k is threshold (e.g. 2) and n is total shares (e.g. 3).
func SplitSecret(secret []byte, k, n int) ([]Share, error) {
	if k < 2 {
		return nil, errors.New("threshold k must be at least 2")
	}
	if n < k {
		return nil, errors.New("total shares n must be >= threshold k")
	}
	if n > 255 {
		return nil, errors.New("total shares n cannot exceed 255")
	}
	if len(secret) == 0 {
		return nil, errors.New("secret cannot be empty")
	}

	shares := make([]Share, n)
	for i := 0; i < n; i++ {
		shares[i] = Share{
			Index: byte(i + 1), // x in 1..n
			Value: make([]byte, len(secret)),
		}
	}

	// For each byte of the secret, build a random polynomial of degree k-1:
	// P(x) = secret_byte + a_1*x + a_2*x^2 + ... + a_{k-1}*x^{k-1}
	coeffBuf := make([]byte, k-1)
	for byteIdx, secretByte := range secret {
		if _, err := io.ReadFull(rand.Reader, coeffBuf); err != nil {
			return nil, fmt.Errorf("failed to generate random polynomial coefficients: %w", err)
		}

		for i := 0; i < n; i++ {
			x := shares[i].Index
			val := secretByte
			xPower := byte(1)

			for _, coeff := range coeffBuf {
				xPower = gfMul(xPower, x)
				val ^= gfMul(coeff, xPower)
			}
			shares[i].Value[byteIdx] = val
		}
	}

	return shares, nil
}

// ReconstructSecret reconstructs the original secret from at least k shares via Lagrange interpolation at x=0.
func ReconstructSecret(shares []Share, k int) ([]byte, error) {
	if len(shares) < k {
		return nil, fmt.Errorf("insufficient shares: got %d, require threshold %d", len(shares), k)
	}

	// Use the first k shares
	subset := shares[:k]

	// Verify indices are distinct and non-zero
	seen := make(map[byte]bool)
	secretLen := len(subset[0].Value)
	if secretLen == 0 {
		return nil, errors.New("shares contain empty values")
	}

	for _, s := range subset {
		if s.Index == 0 {
			return nil, errors.New("share index cannot be 0")
		}
		if seen[s.Index] {
			return nil, fmt.Errorf("duplicate share index %d", s.Index)
		}
		seen[s.Index] = true
		if len(s.Value) != secretLen {
			return nil, errors.New("share values have mismatched lengths")
		}
	}

	secret := make([]byte, secretLen)

	// Precompute Lagrange basis polynomials at x=0:
	// l_i(0) = \prod_{j != i} (0 - x_j) / (x_i - x_j)
	// In GF(2^8): (0 ^ x_j) / (x_i ^ x_j) = x_j / (x_i ^ x_j)
	basis := make([]byte, k)
	for i := 0; i < k; i++ {
		xi := subset[i].Index
		num := byte(1)
		den := byte(1)

		for j := 0; j < k; j++ {
			if i == j {
				continue
			}
			xj := subset[j].Index
			num = gfMul(num, xj)
			den = gfMul(den, xi^xj)
		}

		l0, err := gfDiv(num, den)
		if err != nil {
			return nil, fmt.Errorf("failed to compute Lagrange basis: %w", err)
		}
		basis[i] = l0
	}

	// Evaluate P(0) = \sum_{i=0}^{k-1} y_i * l_i(0)
	for byteIdx := 0; byteIdx < secretLen; byteIdx++ {
		var val byte
		for i := 0; i < k; i++ {
			val ^= gfMul(subset[i].Value[byteIdx], basis[i])
		}
		secret[byteIdx] = val
	}

	return secret, nil
}
