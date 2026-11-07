package relay

import (
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"snet/internal/protocol"
)

const (
	defaultSessionTTL   = 30 * time.Second
	defaultMaxSessions  = 4096
	defaultControlRate  = 200
	defaultControlBurst = 400
	directAddrTTL       = 2 * time.Minute
	cleanupInterval     = 5 * time.Second
	controlBucketIdle   = time.Minute
	controlBucketMax    = 16384
	relayReadBuffer     = 1 << 20
)

// Options tune the relay server. Zero values use production defaults.
type Options struct {
	// SessionTTL bounds how long a session may stay idle without a valid
	// HELLO/PING/DATA. The signed ticket expiry is also enforced.
	SessionTTL time.Duration
	// MaxSessions caps concurrent sessions so a ticket flood cannot grow
	// memory without bound.
	MaxSessions int
	// ControlRate and ControlBurst limit control frames (HELLO/PING/BYE and
	// direct-path registration/lookup) per source IP. Data frames are not
	// throttled here; they are bounded by session TTL and the IP's NAT.
	ControlRate  float64
	ControlBurst int
}

// Stats is a point-in-time relay counter snapshot.
type Stats struct {
	Sessions     int    `json:"sessions"`
	PacketsIn    uint64 `json:"packetsIn"`
	PacketsOut   uint64 `json:"packetsOut"`
	BytesIn      uint64 `json:"bytesIn"`
	BytesOut     uint64 `json:"bytesOut"`
	Drops        uint64 `json:"drops"`
	Errors       uint64 `json:"errors"`
	ControlDrops uint64 `json:"controlDrops"`
}

type controlBucket struct {
	tokens float64
	last   time.Time
}

type relaySession struct {
	id         [16]byte
	node       uint64
	network    string
	addr       *net.UDPAddr
	directAddr *net.UDPAddr
	directSeen time.Time
	lastSeen   time.Time
	expires    time.Time
}

// Server is a standalone Relay v2 UDP service. It owns no coordination state:
// tickets are signed by the control plane and verified here with a shared
// HMAC secret.
type Server struct {
	conn   *net.UDPConn
	signer *TicketSigner
	ttl    time.Duration
	max    int

	mu       sync.RWMutex
	sessions map[[16]byte]*relaySession
	byNode   map[uint64]*relaySession

	rateMu    sync.Mutex
	control   map[string]*controlBucket
	ctrlRate  float64
	ctrlBurst float64

	closed    chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup

	packetsIn    atomic.Uint64
	packetsOut   atomic.Uint64
	bytesIn      atomic.Uint64
	bytesOut     atomic.Uint64
	drops        atomic.Uint64
	errors       atomic.Uint64
	controlDrops atomic.Uint64
}

// Listen binds a relay server to addr (e.g. ":8092" or "127.0.0.1:0").
func Listen(addr string, signer *TicketSigner, opts Options) (*Server, error) {
	if signer == nil {
		return nil, errors.New("relay: ticket signer required")
	}
	ua, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", addr, err)
	}
	conn, err := net.ListenUDP("udp", ua)
	if err != nil {
		return nil, fmt.Errorf("listen udp %s: %w", addr, err)
	}
	_ = conn.SetReadBuffer(relayReadBuffer)
	ttl := opts.SessionTTL
	if ttl <= 0 {
		ttl = defaultSessionTTL
	}
	max := opts.MaxSessions
	if max <= 0 {
		max = defaultMaxSessions
	}
	rate := opts.ControlRate
	if rate <= 0 {
		rate = defaultControlRate
	}
	burst := float64(opts.ControlBurst)
	if burst <= 0 {
		burst = defaultControlBurst
	}
	return &Server{
		conn:      conn,
		signer:    signer,
		ttl:       ttl,
		max:       max,
		sessions:  make(map[[16]byte]*relaySession),
		byNode:    make(map[uint64]*relaySession),
		control:   make(map[string]*controlBucket),
		ctrlRate:  rate,
		ctrlBurst: burst,
		closed:    make(chan struct{}),
	}, nil
}

// Addr returns the bound UDP address.
func (s *Server) Addr() string { return s.conn.LocalAddr().String() }

// Serve reads and forwards packets until Close. It also starts the session
// cleanup loop.
func (s *Server) Serve() error {
	s.wg.Add(1)
	go s.cleanupLoop()
	defer s.wg.Done()
	buf := make([]byte, 65535)
	for {
		n, addr, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-s.closed:
				return nil
			default:
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			log.Printf("relay: read: %v", err)
			continue
		}
		if n > 0 {
			s.packetsIn.Add(1)
			s.bytesIn.Add(uint64(n))
			s.handle(buf[:n], addr, time.Now())
		}
	}
}

// Close stops the server and releases the UDP socket.
func (s *Server) Close() error {
	var err error
	s.closeOnce.Do(func() {
		close(s.closed)
		err = s.conn.Close()
		s.wg.Wait()
	})
	return err
}

// Stats returns a snapshot of sessions and counters.
func (s *Server) Stats() Stats {
	s.mu.RLock()
	n := len(s.sessions)
	s.mu.RUnlock()
	return Stats{
		Sessions:     n,
		PacketsIn:    s.packetsIn.Load(),
		PacketsOut:   s.packetsOut.Load(),
		BytesIn:      s.bytesIn.Load(),
		BytesOut:     s.bytesOut.Load(),
		Drops:        s.drops.Load(),
		Errors:       s.errors.Load(),
		ControlDrops: s.controlDrops.Load(),
	}
}

func isControlFrame(typ uint8) bool {
	switch typ {
	case protocol.RelayTypeHello, protocol.RelayTypePing, protocol.RelayTypeBye,
		protocol.RelayTypeDirectRegister, protocol.RelayTypeDirectLookup:
		return true
	default:
		return false
	}
}

// allowControl applies a per-source-IP token bucket to control frames. DATA is
// intentionally exempt so legitimate WireGuard traffic is never throttled by
// the relay's control-plane protections.
func (s *Server) allowControl(addr *net.UDPAddr, now time.Time) bool {
	ip := "unknown"
	if addr != nil && addr.IP != nil {
		if v4 := addr.IP.To4(); v4 != nil {
			ip = v4.String()
		} else {
			ip = addr.IP.String()
		}
	}
	s.rateMu.Lock()
	defer s.rateMu.Unlock()
	b := s.control[ip]
	if b == nil {
		if len(s.control) >= controlBucketMax {
			return false
		}
		b = &controlBucket{tokens: s.ctrlBurst, last: now}
		s.control[ip] = b
	} else {
		elapsed := now.Sub(b.last).Seconds()
		if elapsed > 0 {
			b.tokens += elapsed * s.ctrlRate
			if b.tokens > s.ctrlBurst {
				b.tokens = s.ctrlBurst
			}
			b.last = now
		}
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (s *Server) handle(raw []byte, addr *net.UDPAddr, now time.Time) {
	f, err := protocol.ParseRelayFrame(raw)
	if err != nil {
		s.drops.Add(1)
		return
	}
	if isControlFrame(f.Type) && !s.allowControl(addr, now) {
		s.controlDrops.Add(1)
		s.drops.Add(1)
		return
	}
	switch f.Type {
	case protocol.RelayTypeHello:
		s.handleHello(f, addr, now)
	case protocol.RelayTypePing:
		s.handlePing(f, addr, now)
	case protocol.RelayTypeData:
		s.handleData(f, addr, now)
	case protocol.RelayTypeBye:
		s.handleBye(f, addr)
	case protocol.RelayTypeDirectRegister:
		s.handleDirectRegister(f, addr, now)
	case protocol.RelayTypeDirectLookup:
		s.handleDirectLookup(f, addr, now)
	default:
		// Server-to-client frames and peer-to-peer direct frames must never
		// be accepted by the relay itself.
		s.drops.Add(1)
	}
}

func (s *Server) handleHello(f protocol.RelayFrame, addr *net.UDPAddr, now time.Time) {
	claims, err := s.signer.Verify(string(f.Payload), now)
	if err != nil {
		// Do not answer invalid HELLOs: that would turn the relay into an
		// unauthenticated reflection source.
		s.errors.Add(1)
		return
	}
	var sid [16]byte
	rawSID, _ := hex.DecodeString(claims.SessionID)
	copy(sid[:], rawSID)
	if sid != f.SessionID || f.SenderID != protocol.RelayNodeKey(claims.NodeID) {
		s.errors.Add(1)
		return
	}
	expires := time.Unix(claims.ExpiresAt, 0)
	if !expires.After(now) {
		s.errors.Add(1)
		return
	}
	sess := &relaySession{
		id:       sid,
		node:     f.SenderID,
		network:  claims.NetworkID,
		addr:     cloneAddr(addr),
		lastSeen: now,
		expires:  expires,
	}
	s.mu.Lock()
	if old := s.byNode[sess.node]; old != nil && old.id != sess.id {
		delete(s.sessions, old.id)
	}
	if existing := s.sessions[sess.id]; existing == nil && len(s.sessions) >= s.max {
		s.mu.Unlock()
		s.errors.Add(1)
		return
	}
	s.sessions[sess.id] = sess
	s.byNode[sess.node] = sess
	s.mu.Unlock()

	ack := protocol.RelayFrame{
		Type:        protocol.RelayTypeHelloAck,
		SessionID:   sess.id,
		RecipientID: sess.node,
	}
	_ = s.writeFrame(ack, addr)
}

func (s *Server) handlePing(f protocol.RelayFrame, addr *net.UDPAddr, now time.Time) {
	sess := s.touch(f, addr, now)
	if sess == nil {
		s.drops.Add(1)
		return
	}
	_ = s.writeFrame(protocol.RelayFrame{
		Type:        protocol.RelayTypePong,
		SessionID:   sess.id,
		RecipientID: sess.node,
	}, addr)
}

func (s *Server) handleData(f protocol.RelayFrame, addr *net.UDPAddr, now time.Time) {
	sender := s.touch(f, addr, now)
	if sender == nil {
		s.drops.Add(1)
		return
	}
	s.mu.RLock()
	recipient := s.byNode[f.RecipientID]
	if recipient != nil && (recipient.network != sender.network ||
		!recipient.expires.After(now) || now.Sub(recipient.lastSeen) > s.ttl) {
		recipient = nil
	}
	s.mu.RUnlock()
	if recipient == nil {
		s.drops.Add(1)
		_ = s.writeFrame(protocol.RelayFrame{
			Type:        protocol.RelayTypeError,
			SessionID:   sender.id,
			RecipientID: sender.node,
			Payload:     []byte("recipient offline"),
		}, addr)
		return
	}
	if err := s.writeFrame(f, recipient.addr); err != nil {
		s.errors.Add(1)
	}
}

func (s *Server) handleBye(f protocol.RelayFrame, addr *net.UDPAddr) {
	sess := s.touch(f, addr, time.Now())
	if sess == nil {
		return
	}
	s.mu.Lock()
	if cur := s.sessions[sess.id]; cur == sess {
		delete(s.sessions, sess.id)
	}
	if cur := s.byNode[sess.node]; cur == sess {
		delete(s.byNode, sess.node)
	}
	s.mu.Unlock()
}

// handleDirectRegister records the public source address of the client's
// direct UDP socket. Registration travels from that same socket, so the relay
// observes the NAT mapping peers should try later. A valid ticket is required
// so arbitrary hosts cannot poison a session's direct address.
func (s *Server) handleDirectRegister(f protocol.RelayFrame, addr *net.UDPAddr, now time.Time) {
	claims, sid, ok := s.verifyTicketFrame(f, now)
	if !ok {
		s.errors.Add(1)
		return
	}
	s.mu.Lock()
	sess := s.sessions[sid]
	if sess == nil || sess.node != f.SenderID || sess.network != claims.NetworkID || !sess.expires.After(now) {
		s.mu.Unlock()
		s.drops.Add(1)
		return
	}
	sess.directAddr = cloneAddr(addr)
	sess.directSeen = now
	sess.lastSeen = now
	sessID := sess.id
	node := sess.node
	s.mu.Unlock()

	_ = s.writeFrame(protocol.RelayFrame{
		Type:        protocol.RelayTypeDirectRegisterAck,
		SessionID:   sessID,
		RecipientID: node,
		// Echo the relay-observed direct-socket mapping so the client can
		// classify its NAT behaviour without a second probe path.
		Payload: []byte(addr.String()),
	}, addr)
}

// handleDirectLookup returns the peer's most recent registered direct address.
// The empty payload means "not registered yet"; clients retry rather than
// treating it as an error.
func (s *Server) handleDirectLookup(f protocol.RelayFrame, addr *net.UDPAddr, now time.Time) {
	sender := s.touch(f, addr, now)
	if sender == nil {
		s.drops.Add(1)
		return
	}
	s.mu.RLock()
	recipient := s.byNode[f.RecipientID]
	var direct string
	if recipient != nil && recipient.network == sender.network &&
		recipient.expires.After(now) && now.Sub(recipient.lastSeen) <= s.ttl &&
		recipient.directAddr != nil && now.Sub(recipient.directSeen) <= directAddrTTL {
		direct = recipient.directAddr.String()
	}
	senderID := sender.node
	sessID := sender.id
	s.mu.RUnlock()

	_ = s.writeFrame(protocol.RelayFrame{
		Type:        protocol.RelayTypeDirectPeer,
		SessionID:   sessID,
		SenderID:    senderID,
		RecipientID: f.RecipientID,
		Payload:     []byte(direct),
	}, addr)
}

// verifyTicketFrame validates a ticket-bearing frame and returns the signed
// claims plus the decoded session ID.
func (s *Server) verifyTicketFrame(f protocol.RelayFrame, now time.Time) (TicketClaims, [16]byte, bool) {
	var sid [16]byte
	claims, err := s.signer.Verify(string(f.Payload), now)
	if err != nil {
		return claims, sid, false
	}
	raw, err := hex.DecodeString(claims.SessionID)
	if err != nil || len(raw) != len(sid) {
		return claims, sid, false
	}
	copy(sid[:], raw)
	if sid != f.SessionID || f.SenderID != protocol.RelayNodeKey(claims.NodeID) {
		return claims, sid, false
	}
	return claims, sid, true
}

// touch validates a frame against its session, refreshes the source address
// (NAT rebinding) and returns the session. It returns nil for unknown, idle or
// expired sessions, so DATA/PING/BYE cannot be spoofed with a guessed ID and
// SessionTTL cannot be extended indefinitely by trickling packets.
func (s *Server) touch(f protocol.RelayFrame, addr *net.UDPAddr, now time.Time) *relaySession {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.sessions[f.SessionID]
	if sess == nil || sess.node != f.SenderID {
		return nil
	}
	if !sess.expires.After(now) || now.Sub(sess.lastSeen) > s.ttl {
		delete(s.sessions, sess.id)
		if cur := s.byNode[sess.node]; cur == sess {
			delete(s.byNode, sess.node)
		}
		return nil
	}
	sess.addr = cloneAddr(addr)
	sess.lastSeen = now
	return sess
}

func (s *Server) writeFrame(f protocol.RelayFrame, addr *net.UDPAddr) error {
	b, err := f.MarshalBinary()
	if err != nil {
		s.errors.Add(1)
		return err
	}
	n, err := s.conn.WriteToUDP(b, addr)
	if err != nil {
		s.errors.Add(1)
		return err
	}
	s.packetsOut.Add(1)
	s.bytesOut.Add(uint64(n))
	return nil
}

func (s *Server) cleanupLoop() {
	t := time.NewTicker(cleanupInterval)
	defer t.Stop()
	for {
		select {
		case <-s.closed:
			return
		case now := <-t.C:
			s.mu.Lock()
			for id, sess := range s.sessions {
				if now.Sub(sess.lastSeen) > s.ttl || !sess.expires.After(now) {
					delete(s.sessions, id)
					if cur := s.byNode[sess.node]; cur == sess {
						delete(s.byNode, sess.node)
					}
					continue
				}
				if sess.directAddr != nil && now.Sub(sess.directSeen) > directAddrTTL {
					sess.directAddr = nil
				}
			}
			s.mu.Unlock()
			s.rateMu.Lock()
			for ip, b := range s.control {
				if now.Sub(b.last) > controlBucketIdle {
					delete(s.control, ip)
				}
			}
			s.rateMu.Unlock()
		}
	}
}

func cloneAddr(a *net.UDPAddr) *net.UDPAddr {
	if a == nil {
		return nil
	}
	ip := append(net.IP(nil), a.IP...)
	return &net.UDPAddr{IP: ip, Port: a.Port, Zone: a.Zone}
}
