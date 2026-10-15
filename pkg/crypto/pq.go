package crypto

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/cloudflare/circl/kem/mlkem/mlkem768"
	"github.com/cloudflare/circl/sign/mldsa/mldsa65"
)

// Post-quantum algorithm constants.
const (
	AlgMLKEM768 = "ML-KEM-768"
	AlgMLDSA65  = "ML-DSA-65"
	AlgEd25519  = "Ed25519"
)

// MLKEMKeypair contains hex-encoded ML-KEM-768 public and private keys (FIPS 203).
type MLKEMKeypair struct {
	Algorithm  string `json:"algorithm"`
	PublicKey  string `json:"public_key"`
	PrivateKey string `json:"private_key"`
}

// MLDSAKeypair contains hex-encoded ML-DSA-65 public and private keys (FIPS 204).
type MLDSAKeypair struct {
	Algorithm  string `json:"algorithm"`
	PublicKey  string `json:"public_key"`
	PrivateKey string `json:"private_key"`
}

// HybridIdentity contains both classical (Ed25519) and post-quantum (ML-DSA-65 & ML-KEM-768) keys.
type HybridIdentity struct {
	Ed25519Keypair *Keypair      `json:"ed25519"`
	MLDSAKeypair   *MLDSAKeypair `json:"mldsa65"`
	MLKEMKeypair   *MLKEMKeypair `json:"mlkem768"`
}

// GenerateMLKEM768Keypair generates a new post-quantum ML-KEM-768 key pair (FIPS 203).
func GenerateMLKEM768Keypair() (*MLKEMKeypair, error) {
	scheme := mlkem768.Scheme()
	pk, sk, err := scheme.GenerateKeyPair()
	if err != nil {
		return nil, fmt.Errorf("failed to generate ML-KEM-768 keypair: %w", err)
	}

	pkBytes, err := pk.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("failed to serialize ML-KEM-768 public key: %w", err)
	}

	skBytes, err := sk.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("failed to serialize ML-KEM-768 private key: %w", err)
	}

	return &MLKEMKeypair{
		Algorithm:  AlgMLKEM768,
		PublicKey:  hex.EncodeToString(pkBytes),
		PrivateKey: hex.EncodeToString(skBytes),
	}, nil
}

// EncapsulateMLKEM768 encapsulates a 32-byte shared secret using recipient's ML-KEM-768 public key.
// Returns hex-encoded ciphertext and the raw 32-byte shared secret.
func EncapsulateMLKEM768(pubKeyHex string) (string, []byte, error) {
	pkBytes, err := hex.DecodeString(pubKeyHex)
	if err != nil {
		return "", nil, fmt.Errorf("invalid public key hex: %w", err)
	}

	scheme := mlkem768.Scheme()
	pk, err := scheme.UnmarshalBinaryPublicKey(pkBytes)
	if err != nil {
		return "", nil, fmt.Errorf("failed to parse ML-KEM-768 public key: %w", err)
	}

	ct, ss, err := scheme.Encapsulate(pk)
	if err != nil {
		return "", nil, fmt.Errorf("failed to encapsulate with ML-KEM-768: %w", err)
	}

	return hex.EncodeToString(ct), ss, nil
}

// DecapsulateMLKEM768 decapsulates the shared secret using recipient's ML-KEM-768 private key.
func DecapsulateMLKEM768(privKeyHex string, ciphertextHex string) ([]byte, error) {
	skBytes, err := hex.DecodeString(privKeyHex)
	if err != nil {
		return nil, fmt.Errorf("invalid private key hex: %w", err)
	}

	ctBytes, err := hex.DecodeString(ciphertextHex)
	if err != nil {
		return nil, fmt.Errorf("invalid ciphertext hex: %w", err)
	}

	scheme := mlkem768.Scheme()
	sk, err := scheme.UnmarshalBinaryPrivateKey(skBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse ML-KEM-768 private key: %w", err)
	}

	ss, err := scheme.Decapsulate(sk, ctBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to decapsulate with ML-KEM-768: %w", err)
	}

	return ss, nil
}

// GenerateMLDSA65Keypair generates a post-quantum ML-DSA-65 digital signature key pair (FIPS 204).
func GenerateMLDSA65Keypair() (*MLDSAKeypair, error) {
	scheme := mldsa65.Scheme()
	pk, sk, err := scheme.GenerateKey()
	if err != nil {
		return nil, fmt.Errorf("failed to generate ML-DSA-65 keypair: %w", err)
	}

	pkBytes, err := pk.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("failed to serialize ML-DSA-65 public key: %w", err)
	}

	skBytes, err := sk.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("failed to serialize ML-DSA-65 private key: %w", err)
	}

	return &MLDSAKeypair{
		Algorithm:  AlgMLDSA65,
		PublicKey:  hex.EncodeToString(pkBytes),
		PrivateKey: hex.EncodeToString(skBytes),
	}, nil
}

// SignMLDSA65 signs payload bytes using a hex-encoded ML-DSA-65 private key.
func SignMLDSA65(payload []byte, privKeyHex string) (string, error) {
	skBytes, err := hex.DecodeString(privKeyHex)
	if err != nil {
		return "", fmt.Errorf("invalid private key hex: %w", err)
	}

	scheme := mldsa65.Scheme()
	sk, err := scheme.UnmarshalBinaryPrivateKey(skBytes)
	if err != nil {
		return "", fmt.Errorf("failed to parse ML-DSA-65 private key: %w", err)
	}

	sig := scheme.Sign(sk, payload, nil)
	return hex.EncodeToString(sig), nil
}

// VerifyMLDSA65 verifies an ML-DSA-65 signature against a public key hex.
func VerifyMLDSA65(payload []byte, pubKeyHex, sigHex string) bool {
	pkBytes, err := hex.DecodeString(pubKeyHex)
	if err != nil {
		return false
	}

	sigBytes, err := hex.DecodeString(sigHex)
	if err != nil {
		return false
	}

	scheme := mldsa65.Scheme()
	pk, err := scheme.UnmarshalBinaryPublicKey(pkBytes)
	if err != nil {
		return false
	}

	return scheme.Verify(pk, payload, sigBytes, nil)
}

// GenerateHybridIdentity creates a full identity with both classical and post-quantum keys.
func GenerateHybridIdentity() (*HybridIdentity, error) {
	ed, err := GenerateKeypair()
	if err != nil {
		return nil, err
	}
	dsa, err := GenerateMLDSA65Keypair()
	if err != nil {
		return nil, err
	}
	kem, err := GenerateMLKEM768Keypair()
	if err != nil {
		return nil, err
	}

	return &HybridIdentity{
		Ed25519Keypair: ed,
		MLDSAKeypair:   dsa,
		MLKEMKeypair:   kem,
	}, nil
}

// VerifyHybridSignature verifies a signature using either Ed25519 (classical) or ML-DSA-65 (post-quantum).
func VerifyHybridSignature(payload []byte, pubKeyHex, sigHex string) bool {
	if pubKeyHex == "" || sigHex == "" {
		return false
	}

	pkBytes, err := hex.DecodeString(pubKeyHex)
	if err != nil {
		return false
	}

	sigBytes, err := hex.DecodeString(sigHex)
	if err != nil {
		return false
	}

	// 1. Classical Ed25519: 32-byte public key, 64-byte signature
	if len(pkBytes) == ed25519.PublicKeySize && len(sigBytes) == ed25519.SignatureSize {
		return ed25519.Verify(pkBytes, payload, sigBytes)
	}

	// 2. Post-Quantum ML-DSA-65 (FIPS 204): 1952-byte public key, 3309-byte signature
	scheme := mldsa65.Scheme()
	if len(pkBytes) == scheme.PublicKeySize() && len(sigBytes) == scheme.SignatureSize() {
		pk, err := scheme.UnmarshalBinaryPublicKey(pkBytes)
		if err != nil {
			return false
		}
		return scheme.Verify(pk, payload, sigBytes, nil)
	}

	return false
}

// SignHybrid signs payload using whichever key type is passed (Ed25519 or ML-DSA-65).
func SignHybrid(payload []byte, privKeyHex string) (string, error) {
	privBytes, err := hex.DecodeString(privKeyHex)
	if err != nil {
		return "", fmt.Errorf("invalid private key hex: %w", err)
	}

	if len(privBytes) == ed25519.PrivateKeySize {
		return SignPayload(payload, privKeyHex)
	}

	scheme := mldsa65.Scheme()
	if len(privBytes) == scheme.PrivateKeySize() {
		return SignMLDSA65(payload, privKeyHex)
	}

	return "", errors.New("unsupported private key format for hybrid signing")
}
