package server

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/nezhahq/nezha/internal/xtpro/config"
	"github.com/nezhahq/nezha/internal/xtpro/tunnel"
)

const (
	clientIdleTimeout = 60 * time.Second
	maxConnections    = 10000
)

const (
	udpMsgHandshake byte = 1
	udpMsgData      byte = 2
	udpMsgClose     byte = 3
	udpMsgPing      byte = 4
	udpMsgPong      byte = 5
)

type server struct {
	listenPort int
	publicHost string
	publicPortStart int
	publicPortEnd   int

	clients   map[string]*clientSession
	clientsMu sync.RWMutex

	availablePorts []int
	usedPorts      map[int]bool
	portMu         sync.Mutex

	// proxy connection rendezvous
	proxyWaiting map[string]chan net.Conn
	proxyMu      sync.Mutex

	connSemaphore chan struct{}

	runtimeStart      time.Time
	totalConnections  uint64
	activeConnections int64
	totalBytesUp      uint64
	totalBytesDown    uint64

	tunnelListener net.Listener

	udpServer   *net.UDPConn
	udpMu       sync.Mutex
	udpSessions map[string]*udpServerSession

	udpClientAddrs map[string]*net.UDPAddr // clientKey -> last known UDP control addr
}

type clientSession struct {
	server *server
	conn   net.Conn
	enc    *jsonWriter
	dec    *jsonReader

	clientID string
	key      string
	target   string
	protocol string

	publicPort int
	lastSeen   time.Time
	closeOnce  sync.Once
	done       chan struct{}
	mu         sync.Mutex

	bytesUp   uint64
	bytesDown uint64

	remoteIP string

	publicListener net.Listener
	publicUDP      *net.UDPConn

	activeProxyConnections int64

	udpSecret []byte
}

type udpServerSession struct {
	id        string
	clientKey string
	udpSecret []byte

	conn       *net.UDPConn
	remoteAddr *net.UDPAddr
	clientAddr *net.UDPAddr // client's UDP control addr (where to send control packets)

	publicConn *net.UDPConn
	publicPeer *net.UDPAddr

	closeOnce sync.Once
	closed    chan struct{}

	mu         sync.Mutex
	lastActive time.Time
}

type jsonWriter struct {
	enc *json.Encoder
	mu  sync.Mutex
}

type jsonReader struct {
	dec *json.Decoder
	mu  sync.Mutex
}

func (w *jsonWriter) Encode(msg tunnel.Message) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.enc.Encode(msg)
}

func (r *jsonReader) Decode(msg *tunnel.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.dec.Decode(msg)
}

func newServer(cfg *config.Config) *server {
	s := &server{
		listenPort:     cfg.Server.Port,
		publicHost:     cfg.Server.PublicHost,
		publicPortStart: cfg.Server.PublicPortStart,
		publicPortEnd:   cfg.Server.PublicPortEnd,
		clients:        make(map[string]*clientSession),
		usedPorts:      make(map[int]bool),
		proxyWaiting:   make(map[string]chan net.Conn),
		connSemaphore:  make(chan struct{}, maxConnections),
		runtimeStart:   time.Now(),
		availablePorts: make([]int, 0, cfg.Server.PublicPortEnd-cfg.Server.PublicPortStart+1),
		udpSessions:    make(map[string]*udpServerSession),
		udpClientAddrs: make(map[string]*net.UDPAddr),
	}
	for port := cfg.Server.PublicPortStart; port <= cfg.Server.PublicPortEnd; port++ {
		s.availablePorts = append(s.availablePorts, port)
	}
	return s
}

func (s *server) run(ctx context.Context) error {
	tunnelPort := s.listenPort + 1

	if err := s.startUDPServer(tunnelPort); err != nil {
		log.Printf("[xtpro/udp] failed to start UDP control server on port %d: %v", tunnelPort, err)
	}

	certFile := "server.crt"
	keyFile := "server.key"
	if err := generateSelfSignedCert(certFile, keyFile); err != nil {
		return err
	}

	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return fmt.Errorf("failed to load key pair: %w", err)
	}
	tlsConfig := &tls.Config{Certificates: []tls.Certificate{cert}}

	listener, err := tls.Listen("tcp", fmt.Sprintf(":%d", tunnelPort), tlsConfig)
	if err != nil {
		return fmt.Errorf("failed to listen on tunnel port %d: %w", tunnelPort, err)
	}
	s.tunnelListener = listener
	defer listener.Close()

	log.Printf("[xtpro/tunnel] listening on port %d (TLS)", tunnelPort)

	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || strings.Contains(err.Error(), "use of closed network connection") {
				return nil
			}
			log.Printf("[xtpro/tunnel] accept error: %v", err)
			continue
		}

		select {
		case s.connSemaphore <- struct{}{}:
			go func() {
				defer func() { <-s.connSemaphore }()
				s.handleConnection(conn)
			}()
		default:
			_ = conn.Close()
			log.Printf("[xtpro] connection limit reached")
		}
	}
}

func (s *server) stop(ctx context.Context) error {
	if s.tunnelListener != nil {
		_ = s.tunnelListener.Close()
	}
	if s.udpServer != nil {
		_ = s.udpServer.Close()
	}
	return nil
}

func (s *server) dashboardSnapshot() DashboardSnapshot {
	now := time.Now()
	s.clientsMu.RLock()
	tunnels := make([]TunnelView, 0, len(s.clients))
	for _, session := range s.clients {
		tunnels = append(tunnels, TunnelView{
			ClientID:          session.clientID,
			Key:               session.key,
			Target:            session.target,
			Protocol:          session.protocol,
			PublicHost:        s.publicHost,
			PublicPort:        session.publicPort,
			RemoteIP:          session.remoteIP,
			LastSeen:          session.lastSeen,
			BytesUp:           atomic.LoadUint64(&session.bytesUp),
			BytesDown:         atomic.LoadUint64(&session.bytesDown),
			ActiveConnections: atomic.LoadInt64(&session.activeProxyConnections),
		})
	}
	s.clientsMu.RUnlock()

	sort.Slice(tunnels, func(i, j int) bool {
		if tunnels[i].LastSeen.Equal(tunnels[j].LastSeen) {
			return tunnels[i].ClientID < tunnels[j].ClientID
		}
		return tunnels[i].LastSeen.After(tunnels[j].LastSeen)
	})

	return DashboardSnapshot{
		ActiveTunnels:    len(tunnels),
		ActiveUsers:      len(tunnels),
		TotalConnections: atomic.LoadUint64(&s.totalConnections),
		TotalBytesUp:     atomic.LoadUint64(&s.totalBytesUp),
		TotalBytesDown:   atomic.LoadUint64(&s.totalBytesDown),
		UptimeSeconds:    now.Sub(s.runtimeStart).Seconds(),
		Uptime:           now.Sub(s.runtimeStart).String(),
		Tunnels:          tunnels,
	}
}

func (s *server) handleConnection(conn net.Conn) {
	br := bufio.NewReader(conn)
	if _, err := br.Peek(1); err != nil {
		_ = conn.Close()
		return
	}

	dec := tunnel.NewDecoder(br)
	var msg tunnel.Message
	if err := dec.Decode(&msg); err != nil {
		_ = conn.Close()
		return
	}

	switch msg.Type {
	case "register":
		session := &clientSession{
			server:   s,
			conn:     conn,
			enc:      &jsonWriter{enc: tunnel.NewEncoder(conn)},
			dec:      &jsonReader{dec: dec},
			lastSeen: time.Now(),
			done:     make(chan struct{}),
			protocol: "tcp",
		}
		host, _, _ := net.SplitHostPort(conn.RemoteAddr().String())
		session.remoteIP = host

		if err := s.handleClient(session, msg); err != nil {
			_ = session.conn.Close()
		}
		s.removeClient(session)
		return

	case "proxy":
		s.dispatchProxyConnection(conn, msg.ID)
		return

	default:
		_ = conn.Close()
	}
}

func (s *server) handleClient(session *clientSession, msg tunnel.Message) error {
	key := strings.TrimSpace(msg.Key)
	if key == "" {
		var err error
		key, err = tunnel.GenerateID()
		if err != nil {
			return err
		}
	}

	session.clientID = strings.TrimSpace(msg.ClientID)
	if session.clientID == "" {
		session.clientID = fmt.Sprintf("client-%s", key[:8])
	}
	session.key = key
	session.target = msg.Target
	session.protocol = strings.ToLower(strings.TrimSpace(msg.Protocol))
	if session.protocol == "" {
		session.protocol = "tcp"
	}
	session.publicPort = s.getNextPublicPort(msg.RequestedPort)
	log.Printf("[xtpro] register client=%s protocol=%s requested_port=%d assigned_port=%d target=%s",
		session.clientID, session.protocol, msg.RequestedPort, session.publicPort, session.target)

	udpSecret, err := tunnel.GenerateKey()
	if err == nil {
		session.udpSecret = udpSecret
	}

	s.addClient(session)

	resp := tunnel.Message{
		Type:       "registered",
		Key:        key,
		ClientID:   session.clientID,
		RemotePort: session.publicPort,
		Protocol:   session.protocol,
		Version:    tunnel.Version,
	}
	if session.udpSecret != nil {
		resp.UDPSecret = base64.StdEncoding.EncodeToString(session.udpSecret)
	}
	if err := session.enc.Encode(resp); err != nil {
		return err
	}

	go s.heartbeatChecker(session)
	if session.protocol == "tcp" {
		go s.startPublicListener(session)
	} else if session.protocol == "udp" {
		go s.startPublicUDPListener(session)
	}

	atomic.AddInt64(&s.activeConnections, 1)
	defer atomic.AddInt64(&s.activeConnections, -1)

	for {
		m := tunnel.Message{}
		if err := session.dec.Decode(&m); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		session.mu.Lock()
		session.lastSeen = time.Now()
		session.mu.Unlock()
		switch m.Type {
		case "ping":
			_ = session.enc.Encode(tunnel.Message{Type: "pong"})
		case "udp_open":
			go s.handleUDPOpen(session, m)
		case "udp_close", "udp_idle":
			s.handleUDPClose(m.ID)
		}
	}
}

func (s *server) heartbeatChecker(session *clientSession) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			session.mu.Lock()
			idle := time.Since(session.lastSeen)
			session.mu.Unlock()
			if idle > clientIdleTimeout {
				session.Close()
				return
			}
		case <-session.done:
			return
		}
	}
}

func (s *server) addClient(session *clientSession) {
	s.clientsMu.Lock()
	s.clients[session.clientID] = session
	s.clientsMu.Unlock()
}

func (s *server) removeClient(session *clientSession) {
	s.clientsMu.Lock()
	if existing, ok := s.clients[session.clientID]; ok && existing == session {
		delete(s.clients, session.clientID)
		s.releasePort(session.publicPort)
	}
	s.clientsMu.Unlock()
	session.Close()
}

func (s *server) getNextPublicPort(requestedPort int) int {
	s.portMu.Lock()
	defer s.portMu.Unlock()

	// Honor requested port if it's within the public port pool and not currently used.
	// We remove it from availablePorts if present, but we don't require it to be present there
	// (availablePorts can get out of sync if other operations reserve/release ports).
	if requestedPort >= s.publicPortStart && requestedPort <= s.publicPortEnd && !s.usedPorts[requestedPort] {
		for i, p := range s.availablePorts {
			if p == requestedPort {
				s.availablePorts = append(s.availablePorts[:i], s.availablePorts[i+1:]...)
				break
			}
		}
		s.usedPorts[requestedPort] = true
		return requestedPort
	}

	if len(s.availablePorts) == 0 {
		return 10000
	}
	port := s.availablePorts[0]
	s.availablePorts = s.availablePorts[1:]
	s.usedPorts[port] = true
	return port
}

func (s *server) releasePort(port int) {
	s.portMu.Lock()
	defer s.portMu.Unlock()
	if !s.usedPorts[port] {
		return
	}
	delete(s.usedPorts, port)
	s.availablePorts = append(s.availablePorts, port)
	sort.Ints(s.availablePorts)
}

func (s *server) startPublicListener(session *clientSession) {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", session.publicPort))
	if err != nil {
		return
	}

	session.mu.Lock()
	select {
	case <-session.done:
		session.mu.Unlock()
		_ = listener.Close()
		return
	default:
		session.publicListener = listener
	}
	session.mu.Unlock()

	defer listener.Close()

	for {
		conn, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || strings.Contains(err.Error(), "use of closed network connection") {
				return
			}
			continue
		}
		go s.handlePublicConnection(session, conn)
	}
}

func (s *server) startPublicUDPListener(session *clientSession) {
	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf(":%d", session.publicPort))
	if err != nil {
		return
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return
	}

	session.mu.Lock()
	select {
	case <-session.done:
		session.mu.Unlock()
		_ = conn.Close()
		return
	default:
		session.publicUDP = conn
	}
	session.mu.Unlock()

	defer conn.Close()

	buf := make([]byte, 65535)
	for {
		n, peer, err := conn.ReadFromUDP(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) || strings.Contains(err.Error(), "use of closed network connection") {
				return
			}
			continue
		}
		if n == 0 {
			continue
		}

		payload := make([]byte, n)
		copy(payload, buf[:n])
		s.handlePublicUDPDatagram(session, conn, peer, payload)
	}
}

func (s *server) handlePublicConnection(session *clientSession, publicConn net.Conn) {
	defer publicConn.Close()

	proxyID, err := tunnel.GenerateID()
	if err != nil {
		return
	}

	waitCh := make(chan net.Conn, 1)
	s.proxyMu.Lock()
	s.proxyWaiting[proxyID] = waitCh
	s.proxyMu.Unlock()
	defer func() {
		s.proxyMu.Lock()
		delete(s.proxyWaiting, proxyID)
		s.proxyMu.Unlock()
	}()

	proxyMsg := tunnel.Message{Type: "proxy", Key: session.key, ClientID: session.clientID, ID: proxyID}
	if err := session.enc.Encode(proxyMsg); err != nil {
		return
	}

	select {
	case clientConn := <-waitCh:
		if clientConn == nil {
			return
		}
		s.handleProxyStream(session, publicConn, clientConn)
	case <-time.After(10 * time.Second):
		return
	case <-session.done:
		return
	}
}

func (s *server) dispatchProxyConnection(conn net.Conn, proxyID string) {
	s.proxyMu.Lock()
	ch, ok := s.proxyWaiting[proxyID]
	if ok {
		delete(s.proxyWaiting, proxyID)
	}
	s.proxyMu.Unlock()
	if !ok {
		_ = conn.Close()
		return
	}

	select {
	case ch <- conn:
	case <-time.After(10 * time.Second):
		_ = conn.Close()
	}
}

func (s *server) handleProxyStream(session *clientSession, publicConn, clientConn net.Conn) {
	atomic.AddInt64(&session.activeProxyConnections, 1)
	atomic.AddUint64(&s.totalConnections, 1)
	defer atomic.AddInt64(&session.activeProxyConnections, -1)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		n, _ := io.Copy(publicConn, clientConn)
		atomic.AddUint64(&session.bytesUp, uint64(n))
		atomic.AddUint64(&s.totalBytesUp, uint64(n))
	}()
	n, _ := io.Copy(clientConn, publicConn)
	atomic.AddUint64(&session.bytesDown, uint64(n))
	atomic.AddUint64(&s.totalBytesDown, uint64(n))
	wg.Wait()
}

func (s *server) handlePublicUDPDatagram(cs *clientSession, publicConn *net.UDPConn, peer *net.UDPAddr, payload []byte) {
	// We need a known UDP control address for this client key to deliver packets.
	s.udpMu.Lock()
	clientAddr := s.udpClientAddrs[cs.key]
	s.udpMu.Unlock()
	if clientAddr == nil {
		// Client hasn't established UDP control path yet (handshake/ping/data).
		return
	}

	peerKey := peer.String()
	sessionID := cs.key + "|" + peerKey

	s.udpMu.Lock()
	udpSess := s.udpSessions[sessionID]
	if udpSess == nil {
		udpSess = &udpServerSession{
			id:         sessionID,
			clientKey:  cs.key,
			udpSecret:  cs.udpSecret,
			publicConn: publicConn,
			publicPeer: peer,
			clientAddr: clientAddr,
			closed:     make(chan struct{}),
			lastActive: time.Now(),
		}
		s.udpSessions[sessionID] = udpSess
	}
	s.udpMu.Unlock()

	udpSess.mu.Lock()
	udpSess.publicPeer = peer
	udpSess.lastActive = time.Now()
	udpSess.mu.Unlock()

	// Send datagram to client over UDP control channel.
	_ = s.sendUDPData(cs.key, sessionID, payload)
}

func (s *server) sendDashboardUpdate(conn *websocket.Conn) error {
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()

	type tunnelRow struct {
		Name       string `json:"name"`
		Status     string `json:"status"`
		Protocol   string `json:"protocol"`
		PublicPort int    `json:"public_port"`
		PublicHost string `json:"public_host"`
		BytesUp    uint64 `json:"bytes_up"`
		BytesDown  uint64 `json:"bytes_down"`
	}

	rows := make([]tunnelRow, 0, len(s.clients))
	var totalUp, totalDown uint64
	var activeProxyConnections int64

	keys := make([]string, 0, len(s.clients))
	for k := range s.clients {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		session := s.clients[k]
		up := atomic.LoadUint64(&session.bytesUp)
		down := atomic.LoadUint64(&session.bytesDown)
		totalUp += up
		totalDown += down
		activeProxyConnections += atomic.LoadInt64(&session.activeProxyConnections)

		publicHost := fmt.Sprintf(":%d", session.publicPort)
		if s.publicHost != "" {
			publicHost = fmt.Sprintf("%s:%d", s.publicHost, session.publicPort)
		}
		rows = append(rows, tunnelRow{
			Name:       session.clientID,
			Status:     "active",
			Protocol:   session.protocol,
			PublicPort: session.publicPort,
			PublicHost: publicHost,
			BytesUp:    up,
			BytesDown:  down,
		})
	}

	if err := conn.WriteJSON(map[string]interface{}{
		"type": "tunnel_update",
		"data": rows,
	}); err != nil {
		return err
	}

	return conn.WriteJSON(map[string]interface{}{
		"type": "metrics",
		"data": map[string]interface{}{
			"activeTunnels":          len(s.clients),
			"activeProxyConnections": activeProxyConnections,
			"activeBytesUp":          totalUp,
			"activeBytesDown":        totalDown,
			"totalConnections":       atomic.LoadUint64(&s.totalConnections),
			"totalBytesUp":           atomic.LoadUint64(&s.totalBytesUp),
			"totalBytesDown":         atomic.LoadUint64(&s.totalBytesDown),
			"uptimeSeconds":          time.Since(s.runtimeStart).Seconds(),
		},
	})
}

func (session *clientSession) Close() {
	session.closeOnce.Do(func() {
		close(session.done)
		if session.conn != nil {
			_ = session.conn.Close()
		}
		session.mu.Lock()
		if session.publicListener != nil {
			_ = session.publicListener.Close()
		}
		if session.publicUDP != nil {
			_ = session.publicUDP.Close()
		}
		session.mu.Unlock()
	})
}

func (s *udpServerSession) Close() {
	s.closeOnce.Do(func() {
		close(s.closed)
		if s.conn != nil {
			_ = s.conn.Close()
		}
	})
}

func (s *server) startUDPServer(port int) error {
	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf(":%d", port))
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return err
	}

	s.udpServer = conn
	_ = conn.SetReadBuffer(4 * 1024 * 1024)
	_ = conn.SetWriteBuffer(4 * 1024 * 1024)

	go s.readUDPControl()
	log.Printf("[xtpro/udp] UDP control server listening on port %d", port)
	return nil
}

func (s *server) readUDPControl() {
	if s.udpServer == nil {
		return
	}
	buf := make([]byte, 65535)
	for {
		n, addr, err := s.udpServer.ReadFromUDP(buf)
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				log.Printf("[xtpro/udp] UDP control read error: %v", err)
			}
			return
		}
		if n == 0 {
			continue
		}
		packet := make([]byte, n)
		copy(packet, buf[:n])
		go s.handleUDPControlPacket(packet, addr)
	}
}

func (s *server) handleUDPControlPacket(packet []byte, addr *net.UDPAddr) {
	if len(packet) < 3 {
		return
	}
	msgType := packet[0]
	key, idx, ok := decodeUDPField(packet, 1)
	if !ok || key == "" {
		return
	}
	switch msgType {
	case udpMsgHandshake:
		s.udpMu.Lock()
		s.udpClientAddrs[key] = addr
		s.udpMu.Unlock()
		_ = s.sendUDPResponse(addr, udpMsgHandshake, key, "", nil)
	case udpMsgData:
		s.udpMu.Lock()
		s.udpClientAddrs[key] = addr
		s.udpMu.Unlock()
		id, next, ok := decodeUDPField(packet, idx)
		if !ok || id == "" {
			return
		}
		payload := make([]byte, len(packet)-next)
		copy(payload, packet[next:])
		s.handleUDPDataFromClient(key, id, payload, addr)
	case udpMsgClose:
		id, _, ok := decodeUDPField(packet, idx)
		if !ok || id == "" {
			return
		}
		s.handleUDPClose(id)
	case udpMsgPing:
		s.udpMu.Lock()
		s.udpClientAddrs[key] = addr
		s.udpMu.Unlock()
		payload := make([]byte, len(packet)-idx)
		copy(payload, packet[idx:])
		_ = s.sendUDPResponse(addr, udpMsgPong, key, "", payload)
	}
}

func (s *server) handleUDPOpen(session *clientSession, msg tunnel.Message) {
	if session.protocol != "udp" {
		return
	}
	remoteAddr := strings.TrimSpace(msg.RemoteAddr)
	if remoteAddr == "" || strings.TrimSpace(msg.ID) == "" {
		return
	}
	addr, err := net.ResolveUDPAddr("udp", remoteAddr)
	if err != nil {
		return
	}
	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return
	}

	udpSession := &udpServerSession{
		id:         msg.ID,
		clientKey:  session.key,
		udpSecret:  session.udpSecret,
		conn:       conn,
		remoteAddr: addr,
		closed:     make(chan struct{}),
	}

	s.udpMu.Lock()
	s.udpSessions[msg.ID] = udpSession
	s.udpMu.Unlock()

	go s.readFromUDPRemote(udpSession)
}

func (s *server) handleUDPClose(sessionID string) {
	if strings.TrimSpace(sessionID) == "" {
		return
	}
	s.udpMu.Lock()
	udpSession := s.udpSessions[sessionID]
	if udpSession != nil {
		delete(s.udpSessions, sessionID)
	}
	s.udpMu.Unlock()
	if udpSession != nil {
		udpSession.Close()
	}
}

func (s *server) readFromUDPRemote(session *udpServerSession) {
	defer s.handleUDPClose(session.id)
	buf := make([]byte, 65535)
	for {
		n, err := session.conn.Read(buf)
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				log.Printf("[xtpro/udp] UDP remote read error (%s): %v", session.id, err)
			}
			return
		}
		if n == 0 {
			continue
		}
		payload := make([]byte, n)
		copy(payload, buf[:n])
		if err := s.sendUDPData(session.clientKey, session.id, payload); err != nil {
			return
		}
	}
}

func (s *server) handleUDPDataFromClient(clientKey, sessionID string, payload []byte, clientAddr *net.UDPAddr) {
	s.udpMu.Lock()
	session := s.udpSessions[sessionID]
	s.udpMu.Unlock()
	if session == nil || session.clientKey != clientKey {
		return
	}

	if session.clientAddr == nil || session.clientAddr.String() != clientAddr.String() {
		session.clientAddr = clientAddr
	}
	// Keep latest UDP-control address for this client key (NAT can change source port).
	s.udpMu.Lock()
	s.udpClientAddrs[clientKey] = clientAddr
	s.udpMu.Unlock()

	if session.udpSecret != nil {
		decrypted, err := tunnel.DecryptUDP(session.udpSecret, payload)
		if err != nil {
			return
		}
		payload = decrypted
	}

	session.mu.Lock()
	session.lastActive = time.Now()
	publicConn := session.publicConn
	publicPeer := session.publicPeer
	remoteConn := session.conn
	session.mu.Unlock()

	switch {
	case remoteConn != nil:
		if _, err := remoteConn.Write(payload); err != nil {
			s.handleUDPClose(sessionID)
		}
	case publicConn != nil && publicPeer != nil:
		_, _ = publicConn.WriteToUDP(payload, publicPeer)
	default:
		// nowhere to forward
	}
}

func (s *server) sendUDPData(clientKey, sessionID string, payload []byte) error {
	s.udpMu.Lock()
	session := s.udpSessions[sessionID]
	s.udpMu.Unlock()
	if session == nil {
		return errors.New("udp session not found")
	}

	if session.udpSecret != nil {
		encrypted, err := tunnel.EncryptUDP(session.udpSecret, payload)
		if err != nil {
			return err
		}
		payload = encrypted
	}

	// Prefer session-bound addr; if missing, fall back to last known addr for this client key.
	addr := session.clientAddr
	if addr == nil {
		s.udpMu.Lock()
		addr = s.udpClientAddrs[clientKey]
		s.udpMu.Unlock()
		// Cache it back for next sends.
		if addr != nil {
			session.clientAddr = addr
		}
	}
	if addr == nil {
		return nil
	}
	return s.writeUDP(udpMsgData, clientKey, sessionID, payload, addr)
}

func (s *server) sendUDPResponse(addr *net.UDPAddr, msgType byte, key, id string, payload []byte) error {
	return s.writeUDP(msgType, key, id, payload, addr)
}

func (s *server) writeUDP(msgType byte, key, id string, payload []byte, addr *net.UDPAddr) error {
	if s.udpServer == nil {
		return errors.New("udp server not available")
	}
	buf := buildUDPMessage(msgType, key, id, payload)
	_, err := s.udpServer.WriteToUDP(buf, addr)
	return err
}

func decodeUDPField(packet []byte, offset int) (string, int, bool) {
	if offset+2 > len(packet) {
		return "", offset, false
	}
	l := int(binary.BigEndian.Uint16(packet[offset : offset+2]))
	offset += 2
	if l < 0 || offset+l > len(packet) {
		return "", offset, false
	}
	return string(packet[offset : offset+l]), offset + l, true
}

func buildUDPMessage(msgType byte, key, id string, payload []byte) []byte {
	keyLen := len(key)
	idLen := len(id)
	total := 1 + 2 + keyLen
	if msgType != udpMsgHandshake {
		total += 2 + idLen
	}
	total += len(payload)

	buf := make([]byte, total)
	buf[0] = msgType
	binary.BigEndian.PutUint16(buf[1:], uint16(keyLen))
	copy(buf[3:], key)
	offset := 3 + keyLen

	if msgType != udpMsgHandshake {
		binary.BigEndian.PutUint16(buf[offset:], uint16(idLen))
		offset += 2
		copy(buf[offset:], id)
		offset += idLen
	}
	copy(buf[offset:], payload)
	return buf
}

func generateSelfSignedCert(certFile, keyFile string) error {
	if _, err := os.Stat(certFile); err == nil {
		if _, err := os.Stat(keyFile); err == nil {
			return nil
		}
	}

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"XTPro Tunnel"},
		},
		NotBefore: time.Now(),
		NotAfter:  time.Now().Add(365 * 24 * time.Hour),

		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return err
	}

	certOut, err := os.Create(certFile)
	if err != nil {
		return err
	}
	defer certOut.Close()
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}); err != nil {
		return err
	}

	keyOut, err := os.Create(keyFile)
	if err != nil {
		return err
	}
	defer keyOut.Close()
	privBytes := x509.MarshalPKCS1PrivateKey(priv)
	if err := pem.Encode(keyOut, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: privBytes}); err != nil {
		return err
	}

	log.Printf("[xtpro] generated self-signed certificate: %s, %s", certFile, keyFile)
	return nil
}
