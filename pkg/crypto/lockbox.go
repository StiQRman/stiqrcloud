package crypto

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// EncryptedSharePacket contains the post-quantum ML-KEM ciphertext and the AES-GCM encrypted share payload.
// Total payload size is ~1.1 KB, perfectly suited for low-overhead distribution.
type EncryptedSharePacket struct {
	MLKEMCiphertext string `json:"mlkem_ciphertext"` // Encapsulated key under recipient's ML-KEM-768 public key
	Nonce           string `json:"nonce"`            // 12-byte AES-GCM nonce (hex)
	Ciphertext      string `json:"ciphertext"`       // Encrypted serialized Share payload (hex)
}

// UserShareLockbox represents a single node's encrypted share slot for a specific friend identity.
type UserShareLockbox struct {
	FriendID          string               `json:"friend_id"`            // User address or public key identifier
	FriendMLKEMPubKey string               `json:"friend_mlkem_pub_key"` // Recipient's ML-KEM-768 public key (hex)
	FriendSignPubKey  string               `json:"friend_sign_pub_key"`  // Recipient's signing public key (hex)
	ShareIndex        byte                 `json:"share_index"`          // 1, 2, or 3
	Packet            EncryptedSharePacket `json:"packet"`               // ~1.1 KB encrypted share packet
	CreatedAt         time.Time            `json:"created_at"`
}

// EncryptShareForUser encapsulates and encrypts a secret share using a friend's ML-KEM-768 public key.
func EncryptShareForUser(share Share, friendMLKEMPubKey string) (*EncryptedSharePacket, error) {
	if len(share.Value) == 0 {
		return nil, errors.New("cannot encrypt empty share")
	}

	// 1. Post-Quantum Key Encapsulation (ML-KEM-768)
	kemCTHex, sharedSecret, err := EncapsulateMLKEM768(friendMLKEMPubKey)
	if err != nil {
		return nil, fmt.Errorf("failed to encapsulate share with ML-KEM-768: %w", err)
	}

	// 2. Serialize share
	shareBytes, err := json.Marshal(share)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal share: %w", err)
	}

	// 3. Encrypt share using AES-256-GCM with the 32-byte shared secret
	encBytes, err := EncryptAESGCM(shareBytes, sharedSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt share with AES-GCM: %w", err)
	}

	if len(encBytes) < 12 {
		return nil, errors.New("ciphertext payload too short")
	}

	nonceHex := hex.EncodeToString(encBytes[:12])
	actualCTHex := hex.EncodeToString(encBytes[12:])

	return &EncryptedSharePacket{
		MLKEMCiphertext: kemCTHex,
		Nonce:           nonceHex,
		Ciphertext:      actualCTHex,
	}, nil
}

// DecryptShareForUser decapsulates and decrypts a share packet using the friend's ML-KEM-768 private key.
func DecryptShareForUser(packet *EncryptedSharePacket, friendMLKEMPrivKey string) (*Share, error) {
	if packet == nil {
		return nil, errors.New("nil encrypted share packet")
	}

	// 1. Post-Quantum Decapsulation (ML-KEM-768)
	sharedSecret, err := DecapsulateMLKEM768(friendMLKEMPrivKey, packet.MLKEMCiphertext)
	if err != nil {
		return nil, fmt.Errorf("failed to decapsulate with ML-KEM-768: %w", err)
	}

	// 2. Reassemble full AES-GCM ciphertext (nonce + actual ciphertext)
	nonceBytes, err := hex.DecodeString(packet.Nonce)
	if err != nil {
		return nil, fmt.Errorf("invalid nonce hex: %w", err)
	}
	actualCTBytes, err := hex.DecodeString(packet.Ciphertext)
	if err != nil {
		return nil, fmt.Errorf("invalid ciphertext hex: %w", err)
	}

	fullPayload := append(nonceBytes, actualCTBytes...)

	// 3. Decrypt AES-GCM
	plainShareBytes, err := DecryptAESGCM(fullPayload, sharedSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt share with AES-GCM: %w", err)
	}

	var share Share
	if err := json.Unmarshal(plainShareBytes, &share); err != nil {
		return nil, fmt.Errorf("failed to unmarshal decrypted share: %w", err)
	}

	return &share, nil
}

// CreateThresholdEnvelope splits a 256-bit DEK into 3 Shamir shares (2-of-3 threshold)
// and creates an encrypted UserShareLockbox for each of the 3 storage nodes.
func CreateThresholdEnvelope(
	dek []byte,
	friendID string,
	friendMLKEMPubKey string,
	friendSignPubKey string,
) ([]Share, map[int]*UserShareLockbox, error) {
	if len(dek) != 32 {
		return nil, nil, errors.New("DEK must be exactly 32 bytes (256 bits)")
	}

	// Split DEK into 3 shares (k=2, n=3 threshold)
	shares, err := SplitSecret(dek, 2, 3)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to split DEK: %w", err)
	}

	nodeLockboxes := make(map[int]*UserShareLockbox)
	for i, share := range shares {
		nodeIndex := int(share.Index) // 1, 2, 3
		packet, err := EncryptShareForUser(share, friendMLKEMPubKey)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to encrypt share %d: %w", nodeIndex, err)
		}

		nodeLockboxes[nodeIndex] = &UserShareLockbox{
			FriendID:          friendID,
			FriendMLKEMPubKey: friendMLKEMPubKey,
			FriendSignPubKey:  friendSignPubKey,
			ShareIndex:        share.Index,
			Packet:            *packet,
			CreatedAt:         time.Now().UTC(),
		}
		_ = i
	}

	return shares, nodeLockboxes, nil
}

// AddFriendToEnvelope creates 3 encrypted lockbox packets for a new friend using existing shares,
// without requiring any re-encryption of the original file data.
func AddFriendToEnvelope(
	shares []Share,
	friendID string,
	friendMLKEMPubKey string,
	friendSignPubKey string,
) (map[int]*UserShareLockbox, error) {
	if len(shares) < 3 {
		return nil, fmt.Errorf("expected 3 shares for 2-of-3 quorum, got %d", len(shares))
	}

	lockboxes := make(map[int]*UserShareLockbox)
	for _, share := range shares {
		nodeIndex := int(share.Index)
		packet, err := EncryptShareForUser(share, friendMLKEMPubKey)
		if err != nil {
			return nil, fmt.Errorf("failed to encrypt share %d for friend %s: %w", nodeIndex, friendID, err)
		}

		lockboxes[nodeIndex] = &UserShareLockbox{
			FriendID:          friendID,
			FriendMLKEMPubKey: friendMLKEMPubKey,
			FriendSignPubKey:  friendSignPubKey,
			ShareIndex:        share.Index,
			Packet:            *packet,
			CreatedAt:         time.Now().UTC(),
		}
	}

	return lockboxes, nil
}

// ReconstructDEKFromLockboxes recovers the 32-byte DEK by decrypting at least 2 lockbox packets
// using the friend's ML-KEM-768 private key and performing Lagrange interpolation.
func ReconstructDEKFromLockboxes(packets []*EncryptedSharePacket, friendMLKEMPrivKey string) ([]byte, error) {
	if len(packets) < 2 {
		return nil, fmt.Errorf("insufficient share packets: got %d, need at least 2", len(packets))
	}

	var decryptedShares []Share
	for i, pkt := range packets {
		share, err := DecryptShareForUser(pkt, friendMLKEMPrivKey)
		if err != nil {
			return nil, fmt.Errorf("packet %d decryption failed: %w", i, err)
		}
		decryptedShares = append(decryptedShares, *share)
	}

	// Reconstruct secret using 2-of-3 Shamir threshold
	dek, err := ReconstructSecret(decryptedShares, 2)
	if err != nil {
		return nil, fmt.Errorf("failed to reconstruct DEK from shares: %w", err)
	}

	return dek, nil
}
