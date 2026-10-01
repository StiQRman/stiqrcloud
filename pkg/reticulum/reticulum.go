package reticulum

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

// Standard Reticulum Constants
const (
	DestinationLength = 16 // 16 bytes (128 bits)
	HeaderMinLength   = 19 // Flags (1) + Hops (1) + Destination (16) + Context (1)
	DefaultRNSAspect  = "testcloud.node"
	DefaultRNSPort    = 4242 // Default Reticulum TCP/UDP port
)

// Destination represents a 16-byte Reticulum destination hash.
type Destination [DestinationLength]byte

func (d Destination) Hex() string {
	return hex.EncodeToString(d[:])
}

func (d Destination) String() string {
	return fmt.Sprintf("<%s>", d.Hex())
}

// DestinationFromHex parses a 32-character hex string into a Reticulum Destination.
func DestinationFromHex(s string) (Destination, error) {
	var dest Destination
	if len(s) == 34 && s[0] == '<' && s[33] == '>' {
		s = s[1:33]
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return dest, fmt.Errorf("invalid hex destination: %w", err)
	}
	if len(b) != DestinationLength {
		return dest, fmt.Errorf("invalid destination length: got %d, want %d", len(b), DestinationLength)
	}
	copy(dest[:], b)
	return dest, nil
}

// Identity holds a Reticulum node identity.
type Identity struct {
	PublicKey  ed25519.PublicKey
	PrivateKey ed25519.PrivateKey
	Aspect     string
}

// NewIdentity generates a new cryptographic Reticulum Identity.
func NewIdentity(aspect string) (*Identity, error) {
	if aspect == "" {
		aspect = DefaultRNSAspect
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &Identity{
		PublicKey:  pub,
		PrivateKey: priv,
		Aspect:     aspect,
	}, nil
}

// Destination computes the 16-byte Reticulum destination hash: SHA256(aspect + pubkey)[:16].
func (id *Identity) Destination() Destination {
	h := sha256.New()
	h.Write([]byte(id.Aspect))
	h.Write(id.PublicKey)
	sum := h.Sum(nil)

	var dest Destination
	copy(dest[:], sum[:DestinationLength])
	return dest
}

// AnnouncePacket represents a Reticulum Announce payload.
type AnnouncePacket struct {
	Destination Destination `json:"destination"`
	PublicKey   string      `json:"public_key"`
	AppData     []byte      `json:"app_data"`
	Timestamp   time.Time   `json:"timestamp"`
	Signature   string      `json:"signature"`
}

// CreateAnnounce creates and signs an Announce packet.
func (id *Identity) CreateAnnounce(appData []byte) (*AnnouncePacket, error) {
	dest := id.Destination()
	now := time.Now().UTC()

	// Data to sign: destination + appData + timestamp
	toSign := append(dest[:], appData...)
	toSign = append(toSign, []byte(now.Format(time.RFC3339))...)

	sig := ed25519.Sign(id.PrivateKey, toSign)

	return &AnnouncePacket{
		Destination: dest,
		PublicKey:   hex.EncodeToString(id.PublicKey),
		AppData:     appData,
		Timestamp:   now,
		Signature:   hex.EncodeToString(sig),
	}, nil
}

// VerifyAnnounce verifies an Announce packet.
func VerifyAnnounce(ann *AnnouncePacket) bool {
	pubBytes, err := hex.DecodeString(ann.PublicKey)
	if err != nil || len(pubBytes) != ed25519.PublicKeySize {
		return false
	}
	sigBytes, err := hex.DecodeString(ann.Signature)
	if err != nil || len(sigBytes) != ed25519.SignatureSize {
		return false
	}

	toSign := append(ann.Destination[:], ann.AppData...)
	toSign = append(toSign, []byte(ann.Timestamp.Format(time.RFC3339))...)

	return ed25519.Verify(pubBytes, toSign, sigBytes)
}

// Packet encapsulates a Reticulum wire packet.
type Packet struct {
	HeaderFlags byte
	Hops        byte
	Destination Destination
	Context     byte
	Payload     []byte
}

// EncodePacket serializes a packet into bytes.
func EncodePacket(p *Packet) []byte {
	buf := make([]byte, 0, HeaderMinLength+len(p.Payload))
	buf = append(buf, p.HeaderFlags)
	buf = append(buf, p.Hops)
	buf = append(buf, p.Destination[:]...)
	buf = append(buf, p.Context)
	buf = append(buf, p.Payload...)
	return buf
}

// DecodePacket decodes bytes into a Reticulum packet.
func DecodePacket(data []byte) (*Packet, error) {
	if len(data) < HeaderMinLength {
		return nil, errors.New("packet shorter than minimum header length")
	}

	p := &Packet{
		HeaderFlags: data[0],
		Hops:        data[1],
		Context:     data[18],
		Payload:     data[HeaderMinLength:],
	}
	copy(p.Destination[:], data[2:18])
	return p, nil
}

// InterfaceManager manages local Reticulum networking interfaces (UDP/TCP listener).
type InterfaceManager struct {
	mu        sync.RWMutex
	identity  *Identity
	udpConn   *net.UDPConn
	listening bool
	peers     map[string]time.Time // destination hex -> last seen
	onPacket  func(p *Packet, addr *net.UDPAddr)
}

// NewInterfaceManager creates a Reticulum interface manager.
func NewInterfaceManager(identity *Identity) *InterfaceManager {
	return &InterfaceManager{
		identity: identity,
		peers:    make(map[string]time.Time),
	}
}

// SetPacketHandler registers a callback for received Reticulum packets.
func (im *InterfaceManager) SetPacketHandler(fn func(p *Packet, addr *net.UDPAddr)) {
	im.mu.Lock()
	defer im.mu.Unlock()
	im.onPacket = fn
}

// StartUDP starts listening on the specified UDP port for Reticulum packets.
func (im *InterfaceManager) StartUDP(port int) error {
	addr := &net.UDPAddr{Port: port}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return fmt.Errorf("failed to bind Reticulum UDP interface on port %d: %w", port, err)
	}

	im.mu.Lock()
	im.udpConn = conn
	im.listening = true
	im.mu.Unlock()

	go im.readLoop()
	return nil
}

func (im *InterfaceManager) readLoop() {
	buf := make([]byte, 65535)
	for {
		im.mu.RLock()
		conn := im.udpConn
		listening := im.listening
		im.mu.RUnlock()

		if !listening || conn == nil {
			return
		}

		n, remoteAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			if !im.listening {
				return
			}
			continue
		}

		packetData := make([]byte, n)
		copy(packetData, buf[:n])

		p, err := DecodePacket(packetData)
		if err != nil {
			continue
		}

		im.mu.Lock()
		im.peers[p.Destination.Hex()] = time.Now()
		handler := im.onPacket
		im.mu.Unlock()

		if handler != nil {
			go handler(p, remoteAddr)
		}
	}
}

// Stop gracefully shuts down the Reticulum interface.
func (im *InterfaceManager) Stop() {
	im.mu.Lock()
	defer im.mu.Unlock()
	im.listening = false
	if im.udpConn != nil {
		_ = im.udpConn.Close()
		im.udpConn = nil
	}
}

// GetPeersCount returns the number of active Reticulum peers seen.
func (im *InterfaceManager) GetPeersCount() int {
	im.mu.RLock()
	defer im.mu.RUnlock()
	return len(im.peers)
}
