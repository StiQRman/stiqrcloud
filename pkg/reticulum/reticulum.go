package reticulum

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

// Standard Reticulum Constants
const (
	DestinationLength = 16  // 16 bytes (128 bits)
	HeaderMinLength   = 19  // Flags (1) + Hops (1) + Destination (16) + Context (1)
	MaxMTU            = 500 // Reticulum Maximum Transmission Unit
	DefaultRNSAspect  = "stiqrcloud.node"
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
// In Reticulum specification:
// - PublicKey is 64 bytes: 32 bytes X25519 (ECDH) + 32 bytes Ed25519 (Signatures).
// - PrivateKey is 64 bytes: 32 bytes X25519 private key + 32 bytes Ed25519 private key seed.
type Identity struct {
	x25519Priv *ecdh.PrivateKey
	sigPubKey  ed25519.PublicKey
	sigPrivKey ed25519.PrivateKey
	PublicKey  []byte // 64 bytes combined
	Aspect     string
}

// NewIdentity generates a new cryptographic Reticulum Identity conforming to standard RNS.
func NewIdentity(aspect string) (*Identity, error) {
	if aspect == "" {
		aspect = DefaultRNSAspect
	}
	xPriv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate X25519 key: %w", err)
	}
	edPub, edPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate Ed25519 key: %w", err)
	}

	pubBytes := make([]byte, 64)
	copy(pubBytes[:32], xPriv.PublicKey().Bytes())
	copy(pubBytes[32:], edPub)

	return &Identity{
		x25519Priv: xPriv,
		sigPubKey:  edPub,
		sigPrivKey: edPriv,
		PublicKey:  pubBytes,
		Aspect:     aspect,
	}, nil
}

// Destination computes the 16-byte Reticulum destination hash:
// In Reticulum standard:
// name_hash = sha256(aspect)[:10]
// id_hash = sha256(pubkey_64_bytes)[:16]
// destination_hash = sha256(name_hash + id_hash)[:16]
func (id *Identity) Destination() Destination {
	nameH := sha256.Sum256([]byte(id.Aspect))
	idH := sha256.Sum256(id.PublicKey)
	combined := append(nameH[:10], idH[:16]...)
	destH := sha256.Sum256(combined)

	var dest Destination
	copy(dest[:], destH[:DestinationLength])
	return dest
}

// BuildAnnouncePacket creates a standard Reticulum Announce packet conforming to the Reticulum Network Stack.
func (id *Identity) BuildAnnouncePacket(appData []byte) *Packet {
	dest := id.Destination()
	nameH := sha256.Sum256([]byte(id.Aspect))
	nameHash := nameH[:10]

	randomBytes := make([]byte, 10)
	_, _ = rand.Read(randomBytes)

	// signed_data = dest_hash (16) + public_key (64) + name_hash (10) + random_hash (10) + app_data
	signedData := make([]byte, 0, 16+64+10+10+len(appData))
	signedData = append(signedData, dest[:]...)
	signedData = append(signedData, id.PublicKey...)
	signedData = append(signedData, nameHash...)
	signedData = append(signedData, randomBytes...)
	signedData = append(signedData, appData...)

	sig := ed25519.Sign(id.sigPrivKey, signedData)

	// announce_data = public_key (64) + name_hash (10) + random_hash (10) + sig (64) + app_data
	annData := make([]byte, 0, 64+10+10+64+len(appData))
	annData = append(annData, id.PublicKey...)
	annData = append(annData, nameHash...)
	annData = append(annData, randomBytes...)
	annData = append(annData, sig...)
	annData = append(annData, appData...)

	return &Packet{
		HeaderFlags: 0x01, // ANNOUNCE = 1, HEADER_1 = 0
		Hops:        0x00,
		Destination: dest,
		Context:     0x00,
		Payload:     annData,
	}
}

// AnnouncePacket represents a Reticulum Announce payload.
type AnnouncePacket struct {
	Destination Destination `json:"destination"`
	PublicKey   string      `json:"public_key"`
	NameHash    []byte      `json:"name_hash"`
	RandomHash  []byte      `json:"random_hash"`
	AppData     []byte      `json:"app_data"`
	Timestamp   time.Time   `json:"timestamp"`
	Signature   string      `json:"signature"`
}

// CreateAnnounce creates and signs an Announce packet.
func (id *Identity) CreateAnnounce(appData []byte) (*AnnouncePacket, error) {
	annPacket := id.BuildAnnouncePacket(appData)
	return &AnnouncePacket{
		Destination: annPacket.Destination,
		PublicKey:   hex.EncodeToString(id.PublicKey),
		NameHash:    annPacket.Payload[64 : 64+10],
		RandomHash:  annPacket.Payload[64+10 : 64+20],
		AppData:     appData,
		Timestamp:   time.Now().UTC(),
		Signature:   hex.EncodeToString(annPacket.Payload[64+20 : 64+20+64]),
	}, nil
}

// VerifyAnnounce verifies an Announce packet.
func VerifyAnnounce(ann *AnnouncePacket) bool {
	pubBytes, err := hex.DecodeString(ann.PublicKey)
	if err != nil || len(pubBytes) != 64 {
		return false
	}
	sigBytes, err := hex.DecodeString(ann.Signature)
	if err != nil || len(sigBytes) != ed25519.SignatureSize {
		return false
	}

	signedData := make([]byte, 0, 16+64+10+10+len(ann.AppData))
	signedData = append(signedData, ann.Destination[:]...)
	signedData = append(signedData, pubBytes...)
	signedData = append(signedData, ann.NameHash...)
	signedData = append(signedData, ann.RandomHash...)
	signedData = append(signedData, ann.AppData...)

	return ed25519.Verify(pubBytes[32:], signedData, sigBytes)
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
	mu            sync.RWMutex
	identity      *Identity
	udpConn       *net.UDPConn
	tcpListener   net.Listener
	tcpClients    map[string]net.Conn
	activeGateway string
	listening     bool
	peers         map[string]time.Time // destination hex -> last seen
	onPacket      func(p *Packet, addr net.Addr)
	stopCh        chan struct{}
}

// NewInterfaceManager creates a Reticulum interface manager.
func NewInterfaceManager(identity *Identity) *InterfaceManager {
	return &InterfaceManager{
		identity:   identity,
		peers:      make(map[string]time.Time),
		tcpClients: make(map[string]net.Conn),
		stopCh:     make(chan struct{}),
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
			annPacket := im.identity.BuildAnnouncePacket([]byte(DefaultRNSAspect))
			frame := FrameHDLC(EncodePacket(annPacket))
			_, _ = conn.Write(frame)
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

	const maxHDLCFrameSize = 65536
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
						if len(im.peers) >= 1000 {
							for k := range im.peers {
								delete(im.peers, k)
								break
							}
						}
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
				if len(frameBuf) < maxHDLCFrameSize {
					frameBuf = append(frameBuf, b)
				} else {
					// Drop oversized frame to prevent memory exhaustion DoS
					frameBuf = frameBuf[:0]
					inFrame = false
				}
			}
		}
	}
}

// ConnectTCP establishes an outbound TCP connection to a Reticulum hub or gateway (HDLC framed).
func (im *InterfaceManager) ConnectTCP(targetAddr string) error {
	conn, err := net.DialTimeout("tcp", targetAddr, 6*time.Second)
	if err != nil {
		return fmt.Errorf("failed to connect to Reticulum gateway %s: %w", targetAddr, err)
	}

	im.mu.Lock()
	im.tcpClients[targetAddr] = conn
	im.mu.Unlock()

	// Send initial Announce frame over TCP to newly connected gateway
	if im.identity != nil {
		annPacket := im.identity.BuildAnnouncePacket([]byte(DefaultRNSAspect))
		frame := FrameHDLC(EncodePacket(annPacket))
		_, _ = conn.Write(frame)
	}

	go im.readTCP(conn, targetAddr)
	return nil
}

// BroadcastTCP sends an HDLC framed Reticulum packet across all connected TCP interfaces and gateways.
func (im *InterfaceManager) BroadcastTCP(p *Packet) error {
	data := EncodePacket(p)
	frame := FrameHDLC(data)

	im.mu.RLock()
	clients := make([]net.Conn, 0, len(im.tcpClients))
	for _, c := range im.tcpClients {
		clients = append(clients, c)
	}
	im.mu.RUnlock()

	if len(clients) == 0 {
		return nil
	}

	var writeErr error
	sent := 0
	for _, c := range clients {
		if _, err := c.Write(frame); err != nil {
			writeErr = err
		} else {
			sent++
		}
	}

	if sent == 0 && writeErr != nil {
		return writeErr
	}
	return nil
}

// SendPacketToDest sends a packet to a specific Reticulum Destination over TCP mesh and UDP.
func (im *InterfaceManager) SendPacketToDest(dest Destination, context byte, payload []byte) error {
	if HeaderMinLength+len(payload) > MaxMTU {
		return fmt.Errorf("packet size %d exceeds Reticulum MTU (%d bytes)", HeaderMinLength+len(payload), MaxMTU)
	}
	p := &Packet{
		HeaderFlags: 0x00,
		Hops:        0x00,
		Destination: dest,
		Context:     context,
		Payload:     payload,
	}

	return im.BroadcastTCP(p)
}

// Announce broadcasts a signed Announce packet across all active interfaces.
func (im *InterfaceManager) Announce(appData []byte) error {
	if im.identity == nil {
		return errors.New("cannot announce without identity")
	}

	annPacket := im.identity.BuildAnnouncePacket(appData)
	return im.BroadcastTCP(annPacket)
}

// ConnectTCPWithFailover connects to the primary Reticulum gateway with automated failover to backups.
func (im *InterfaceManager) ConnectTCPWithFailover(gateways []string) {
	if len(gateways) == 0 {
		return
	}

	go func() {
		for {
			select {
			case <-im.stopCh:
				return
			default:
			}

			connected := false
			for _, gw := range gateways {
				gw = strings.TrimSpace(gw)
				if gw == "" {
					continue
				}

				im.mu.RLock()
				_, already := im.tcpClients[gw]
				im.mu.RUnlock()
				if already {
					connected = true
					break
				}

				if err := im.ConnectTCP(gw); err == nil {
					im.mu.Lock()
					im.activeGateway = gw
					im.mu.Unlock()
					connected = true
					break
				}
			}

			// Periodic announce to keep destination active on gateway
			if connected {
				_ = im.Announce([]byte(DefaultRNSAspect))
			}

			select {
			case <-im.stopCh:
				return
			case <-time.After(30 * time.Second):
			}
		}
	}()
}

// GetActiveGateway returns the current connected Reticulum gateway address.
func (im *InterfaceManager) GetActiveGateway() string {
	im.mu.RLock()
	defer im.mu.RUnlock()
	return im.activeGateway
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
		if len(im.peers) >= 1000 {
			for k := range im.peers {
				delete(im.peers, k)
				break
			}
		}
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
	select {
	case <-im.stopCh:
	default:
		close(im.stopCh)
	}
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
