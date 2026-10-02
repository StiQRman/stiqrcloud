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

// HDLC Framing Constants for Reticulum TCP / Serial Links
const (
	HDLCFlag    byte = 0x7E
	HDLCEsc     byte = 0x7D
	HDLCEscMask byte = 0x20
)

// EscapeHDLC escapes bytes for HDLC framing.
func EscapeHDLC(data []byte) []byte {
	buf := make([]byte, 0, len(data)+8)
	for _, b := range data {
		if b == HDLCEsc {
			buf = append(buf, HDLCEsc, HDLCEsc^HDLCEscMask)
		} else if b == HDLCFlag {
			buf = append(buf, HDLCEsc, HDLCFlag^HDLCEscMask)
		} else {
			buf = append(buf, b)
		}
	}
	return buf
}

// UnescapeHDLC decodes escaped HDLC bytes.
func UnescapeHDLC(data []byte) []byte {
	out := make([]byte, 0, len(data))
	for i := 0; i < len(data); i++ {
		if data[i] == HDLCEsc && i+1 < len(data) {
			i++
			out = append(out, data[i]^HDLCEscMask)
		} else {
			out = append(out, data[i])
		}
	}
	return out
}

// FrameHDLC wraps a packet with HDLC start/end flags and escaped payload.
func FrameHDLC(data []byte) []byte {
	escaped := EscapeHDLC(data)
	frame := make([]byte, 0, len(escaped)+2)
	frame = append(frame, HDLCFlag)
	frame = append(frame, escaped...)
	frame = append(frame, HDLCFlag)
	return frame
}

// InterfaceManager manages local Reticulum networking interfaces (UDP/TCP listeners).
type InterfaceManager struct {
	mu          sync.RWMutex
	identity    *Identity
	udpConn     *net.UDPConn
	tcpListener net.Listener
	tcpClients  map[string]net.Conn
	listening   bool
	peers       map[string]time.Time // destination hex -> last seen
	onPacket    func(p *Packet, addr net.Addr)
}

// NewInterfaceManager creates a Reticulum interface manager.
func NewInterfaceManager(identity *Identity) *InterfaceManager {
	return &InterfaceManager{
		identity:   identity,
		peers:      make(map[string]time.Time),
		tcpClients: make(map[string]net.Conn),
	}
}

// SetPacketHandler registers a callback for received Reticulum packets.
func (im *InterfaceManager) SetPacketHandler(fn func(p *Packet, addr net.Addr)) {
	im.mu.Lock()
	defer im.mu.Unlock()
	im.onPacket = fn
}

// StartUDP starts listening on the specified UDP port for Reticulum packets across all interfaces.
func (im *InterfaceManager) StartUDP(port int) error {
	return im.StartUDPAddr("", port)
}

// StartUDPAddr starts listening on the specified host and port for Reticulum packets.
func (im *InterfaceManager) StartUDPAddr(host string, port int) error {
	var ip net.IP
	if host != "" {
		ip = net.ParseIP(host)
	}
	addr := &net.UDPAddr{IP: ip, Port: port}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return fmt.Errorf("failed to bind Reticulum UDP interface on %s:%d: %w", host, port, err)
	}

	im.mu.Lock()
	im.udpConn = conn
	im.listening = true
	im.mu.Unlock()

	go im.readLoop()
	return nil
}

// StartTCPAddr starts a Reticulum TCP server listener with HDLC framing (for Columba / TCPClientInterface).
func (im *InterfaceManager) StartTCPAddr(host string, port int) error {
	bindAddr := fmt.Sprintf("%s:%d", host, port)
	l, err := net.Listen("tcp", bindAddr)
	if err != nil {
		return fmt.Errorf("failed to bind Reticulum TCP interface on %s: %w", bindAddr, err)
	}

	im.mu.Lock()
	im.tcpListener = l
	im.listening = true
	im.mu.Unlock()

	go im.acceptTCP(l)
	return nil
}

func (im *InterfaceManager) acceptTCP(l net.Listener) {
	for {
		conn, err := l.Accept()
		if err != nil {
			return
		}

		remote := conn.RemoteAddr().String()
		im.mu.Lock()
		im.tcpClients[remote] = conn
		im.mu.Unlock()

		// Send initial Announce frame over TCP to newly connected peer (e.g. Columba)
		if im.identity != nil {
			ann, err := im.identity.CreateAnnounce([]byte("testcloud.node"))
			if err == nil {
				annPacket := &Packet{
					HeaderFlags: 0x00,
					Hops:        0x00,
					Destination: ann.Destination,
					Context:     0x00,
					Payload:     append(ann.Destination[:], ann.AppData...),
				}
				frame := FrameHDLC(EncodePacket(annPacket))
				_, _ = conn.Write(frame)
			}
		}

		go im.readTCP(conn, remote)
	}
}

func (im *InterfaceManager) readTCP(conn net.Conn, remote string) {
	defer func() {
		_ = conn.Close()
		im.mu.Lock()
		delete(im.tcpClients, remote)
		im.mu.Unlock()
	}()

	buf := make([]byte, 4096)
	var frameBuf []byte
	inFrame := false

	for {
		n, err := conn.Read(buf)
		if err != nil {
			return
		}

		for i := 0; i < n; i++ {
			b := buf[i]
			if b == HDLCFlag {
				if inFrame && len(frameBuf) > 0 {
					unescaped := UnescapeHDLC(frameBuf)
					if p, err := DecodePacket(unescaped); err == nil {
						im.mu.Lock()
						im.peers[p.Destination.Hex()] = time.Now()
						handler := im.onPacket
						im.mu.Unlock()

						if handler != nil {
							go handler(p, conn.RemoteAddr())
						}
					}
					frameBuf = frameBuf[:0]
				}
				inFrame = true
			} else if inFrame {
				frameBuf = append(frameBuf, b)
			}
		}
	}
}

// SendPacket sends a Reticulum packet to target UDP address (host:port).
func (im *InterfaceManager) SendPacket(targetAddr string, p *Packet) error {
	im.mu.RLock()
	conn := im.udpConn
	im.mu.RUnlock()
	if conn == nil {
		return errors.New("UDP interface is not active")
	}

	rAddr, err := net.ResolveUDPAddr("udp", targetAddr)
	if err != nil {
		return err
	}

	data := EncodePacket(p)
	_, err = conn.WriteToUDP(data, rAddr)
	return err
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

// Stop gracefully shuts down the Reticulum interfaces (UDP & TCP).
func (im *InterfaceManager) Stop() {
	im.mu.Lock()
	defer im.mu.Unlock()
	im.listening = false
	if im.udpConn != nil {
		_ = im.udpConn.Close()
		im.udpConn = nil
	}
	if im.tcpListener != nil {
		_ = im.tcpListener.Close()
		im.tcpListener = nil
	}
	for _, c := range im.tcpClients {
		_ = c.Close()
	}
	im.tcpClients = make(map[string]net.Conn)
}

// GetPeersCount returns the number of active Reticulum peers seen.
func (im *InterfaceManager) GetPeersCount() int {
	im.mu.RLock()
	defer im.mu.RUnlock()
	return len(im.peers)
}
