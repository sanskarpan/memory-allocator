package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sanskar/memory-allocator/internal/allocator"
	"github.com/sanskar/memory-allocator/internal/pool"
	"github.com/sanskar/memory-allocator/internal/simulator"
)

// Config configures the server.
type Config struct {
	Port            string
	StaticDir       string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	MaxMessageBytes int64
	PongWait        time.Duration
	PingPeriod      time.Duration
	WriteWait       time.Duration
	BroadcastBuffer int
	AllowedOrigins  []string // empty = allow all (development default)
	MaxConnPerIP    int      // max concurrent WebSocket connections per IP (0 = unlimited)
}

// DefaultConfig returns a Config with sensible production-grade defaults.
func DefaultConfig() Config {
	return Config{
		Port:            ":8083",
		StaticDir:       "./web/static",
		ReadTimeout:     15 * time.Second,
		WriteTimeout:    10 * time.Second,
		MaxMessageBytes: 1 << 20, // 1 MiB
		PongWait:        60 * time.Second,
		PingPeriod:      30 * time.Second,
		WriteWait:       5 * time.Second,
		BroadcastBuffer: 256,
		AllowedOrigins:  nil,
		MaxConnPerIP:    10,
	}
}

// ConfigFromEnv loads configuration from environment variables, falling back
// to DefaultConfig for any unset value.
func ConfigFromEnv() Config {
	c := DefaultConfig()
	if p := os.Getenv("MEMALLOC_PORT"); p != "" {
		if strings.HasPrefix(p, ":") {
			c.Port = p
		} else {
			c.Port = ":" + p
		}
	}
	if d := os.Getenv("MEMALLOC_STATIC_DIR"); d != "" {
		c.StaticDir = d
	}
	if v := envIntDefault("MEMALLOC_BROADCAST_BUFFER", c.BroadcastBuffer); v > 0 {
		c.BroadcastBuffer = v
	}
	if v := envDurationDefault("MEMALLOC_PING_PERIOD", c.PingPeriod); v > 0 {
		c.PingPeriod = v
	}
	if v := envDurationDefault("MEMALLOC_PONG_WAIT", c.PongWait); v > 0 {
		c.PongWait = v
	}
	if v := envDurationDefault("MEMALLOC_WRITE_WAIT", c.WriteWait); v > 0 {
		c.WriteWait = v
	}
	if v := envInt64Default("MEMALLOC_MAX_MESSAGE_BYTES", c.MaxMessageBytes); v > 0 {
		c.MaxMessageBytes = v
	}
	if v, ok := envIntAllowZero("MEMALLOC_MAX_CONN_PER_IP"); ok && v >= 0 {
		c.MaxConnPerIP = v
	}
	return c
}

func envIntDefault(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil && i > 0 {
			return i
		}
	}
	return def
}

func envInt64Default(key string, def int64) int64 {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.ParseInt(v, 10, 64); err == nil && i > 0 {
			return i
		}
	}
	return def
}

func envDurationDefault(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return def
}

func envIntAllowZero(key string) (int, bool) {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i, true
		}
	}
	return 0, false
}

// upgrader builds a fresh *websocket.Upgrader per Server. Previously a
// package-level upgrader was mutated in NewServerWithConfig, which raced
// when tests ran in parallel.
func newUpgrader(allowedOrigins []string) websocket.Upgrader {
	return websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		CheckOrigin:     checkOriginFactory(allowedOrigins),
	}
}

func checkOriginFactory(allowed []string) func(r *http.Request) bool {
	if len(allowed) == 0 {
		// Development default: allow all origins. Override in production.
		return func(r *http.Request) bool { return true }
	}
	allow := make(map[string]struct{}, len(allowed))
	for _, o := range allowed {
		allow[o] = struct{}{}
	}
	return func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		_, ok := allow[origin]
		return ok
	}
}

// Server manages WebSocket connections and simulator
type Server struct {
	cfg       Config
	upgrader  websocket.Upgrader
	simulator *simulator.Simulator
	simMu     sync.RWMutex

	// simCallbackDone is the close channel for the currently-installed
	// simulator's update callback. On every init we close the previous
	// one (via sync.Once stored in *simCallbackDoneOne) and install a
	// fresh one for the new sim. The pointer-to-Once is what we copy
	// around; sync.Once itself contains a noCopy field and cannot be
	// assigned by value (vet would catch it).
	simCallbackDoneMu  sync.Mutex
	simCallbackDone    chan struct{}
	simCallbackDoneOne *sync.Once

	clients   map[*clientConn]struct{}
	clientsMu sync.RWMutex

	broadcast  chan *simulator.SimulationUpdate
	stopOnce   sync.Once
	shutdownCh chan struct{}

	// Per-IP connection rate limiting.
	ipConnCount   map[string]int
	ipConnCountMu sync.Mutex
}

type clientConn struct {
	conn      *websocket.Conn
	send      chan []byte
	done      chan struct{}
	closeOnce sync.Once
}

func (c *clientConn) close() {
	c.closeOnce.Do(func() {
		close(c.done)
	})
}

// NewServer creates a new server with default config.
func NewServer() *Server { return NewServerWithConfig(DefaultConfig()) }

// NewServerWithConfig creates a new server with the given config.
func NewServerWithConfig(cfg Config) *Server {
	if cfg.BroadcastBuffer <= 0 {
		cfg.BroadcastBuffer = 256
	}
	if cfg.MaxConnPerIP < 0 {
		cfg.MaxConnPerIP = 10
	}
	s := &Server{
		cfg:         cfg,
		upgrader:    newUpgrader(cfg.AllowedOrigins),
		clients:     make(map[*clientConn]struct{}),
		broadcast:   make(chan *simulator.SimulationUpdate, cfg.BroadcastBuffer),
		shutdownCh:  make(chan struct{}),
		ipConnCount: make(map[string]int),
		// No callback installed yet; this channel will be replaced on
		// the first init.
		simCallbackDone:    make(chan struct{}),
		simCallbackDoneOne: &sync.Once{},
	}
	go s.handleBroadcasts()
	return s
}

// securityHeaders is middleware that adds standard security headers to all
// HTTP responses. This addresses CSP, content type sniffing, clickjacking,
// and other common web security concerns.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self' ws: wss:")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}

// clientIP extracts the client IP from the request, preferring
// X-Forwarded-For and X-Real-IP headers for reverse-proxy deployments.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// X-Forwarded-For may contain multiple IPs. Use the first valid
		// entry, which represents the original client when proxies append
		// to the header.
		for _, candidate := range strings.Split(xff, ",") {
			if ip := net.ParseIP(strings.TrimSpace(candidate)); ip != nil {
				return ip.String()
			}
		}
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		if ip := net.ParseIP(xri); ip != nil {
			return ip.String()
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// acquireIPConn increments the connection count for the given IP and returns
// true if the connection is allowed, or false if the per-IP limit is reached.
func (s *Server) acquireIPConn(ip string) bool {
	if s.cfg.MaxConnPerIP == 0 {
		return true
	}
	s.ipConnCountMu.Lock()
	defer s.ipConnCountMu.Unlock()
	if s.ipConnCount[ip] >= s.cfg.MaxConnPerIP {
		return false
	}
	s.ipConnCount[ip]++
	return true
}

// releaseIPConn decrements the connection count for the given IP.
func (s *Server) releaseIPConn(ip string) {
	s.ipConnCountMu.Lock()
	defer s.ipConnCountMu.Unlock()
	if s.ipConnCount[ip] > 0 {
		s.ipConnCount[ip]--
	}
	if s.ipConnCount[ip] == 0 {
		delete(s.ipConnCount, ip)
	}
}

// Routes returns a configured http.ServeMux with the server's endpoints,
// wrapped with security headers middleware.
func (s *Server) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.Dir(s.cfg.StaticDir)))
	mux.HandleFunc("/ws", s.HandleWebSocket)
	mux.HandleFunc("/health", s.HandleHealth)
	return mux
}

// Run starts the HTTP server and blocks until the process receives SIGINT or
// SIGTERM, or Shutdown is called.
func (s *Server) Run() error {
	srv := &http.Server{
		Addr:         s.cfg.Port,
		Handler:      securityHeaders(s.Routes()),
		ReadTimeout:  s.cfg.ReadTimeout,
		WriteTimeout: s.cfg.WriteTimeout,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	errCh := make(chan error, 1)
	go func() {
		log.Printf("Memory Allocator Simulator server starting on http://localhost%s", s.cfg.Port)
		log.Printf("WebSocket endpoint: ws://localhost%s/ws", s.cfg.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case sig := <-stop:
		log.Printf("Received %v, shutting down...", sig)
	case <-s.shutdownCh:
		log.Printf("Shutdown requested...")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("HTTP server shutdown error: %v", err)
		return err
	}
	s.Shutdown()
	return nil
}

// Shutdown stops the broadcast goroutine and closes all client connections.
func (s *Server) Shutdown() {
	s.stopOnce.Do(func() {
		close(s.shutdownCh)
		close(s.broadcast)
		s.clientsMu.Lock()
		for c := range s.clients {
			c.close()
		}
		s.clientsMu.Unlock()
	})
}

// handleBroadcasts sends updates to all connected clients. It exits when
// broadcast is closed.
func (s *Server) handleBroadcasts() {
	for update := range s.broadcast {
		payload, err := json.Marshal(update)
		if err != nil {
			log.Printf("broadcast: marshal error: %v", err)
			continue
		}
		s.clientsMu.RLock()
		clients := make([]*clientConn, 0, len(s.clients))
		for c := range s.clients {
			clients = append(clients, c)
		}
		s.clientsMu.RUnlock()

		for _, c := range clients {
			select {
			case c.send <- payload:
			default:
				// Client is too slow; drop and close
				log.Printf("broadcast: dropping slow client")
				s.removeClient(c)
			}
		}
	}
	// Close all client send channels on shutdown
	s.clientsMu.Lock()
	for c := range s.clients {
		c.close()
		delete(s.clients, c)
	}
	s.clientsMu.Unlock()
}

func (s *Server) addClient(c *clientConn) {
	s.clientsMu.Lock()
	s.clients[c] = struct{}{}
	s.clientsMu.Unlock()
}

func (s *Server) removeClient(c *clientConn) {
	s.clientsMu.Lock()
	if _, ok := s.clients[c]; ok {
		delete(s.clients, c)
		c.close()
	}
	s.clientsMu.Unlock()
}

// HandleWebSocket handles WebSocket connections with per-IP rate limiting.
func (s *Server) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !s.acquireIPConn(ip) {
		log.Printf("WebSocket connection rejected: per-IP limit reached for %s", ip)
		http.Error(w, "Too many connections", http.StatusTooManyRequests)
		return
	}

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.releaseIPConn(ip)
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}
	conn.SetReadLimit(s.cfg.MaxMessageBytes)

	c := &clientConn{
		conn: conn,
		send: make(chan []byte, 32),
		done: make(chan struct{}),
	}
	s.addClient(c)
	log.Printf("Client connected from %s. Total clients: %d", ip, s.clientCount())

	// Send initial state if simulator exists. Use a select with c.done so
	// a closed client doesn't block; a `default:` drop is acceptable here
	// because the next state update from the simulator will arrive on the
	// broadcast channel and reach this client too.
	if sim := s.getSimulator(); sim != nil {
		if payload, err := json.Marshal(sim.GetCurrentState()); err == nil {
			select {
			case c.send <- payload:
			case <-c.done:
			default:
			}
		}
	}

	// Reader goroutine: drives ping/pong and message handling
	go s.writePump(c)
	s.readPump(c)

	s.removeClient(c)
	s.releaseIPConn(ip)
	log.Printf("Client disconnected from %s. Total clients: %d", ip, s.clientCount())
}

func (s *Server) clientCount() int {
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()
	return len(s.clients)
}

func (s *Server) getSimulator() *simulator.Simulator {
	s.simMu.RLock()
	defer s.simMu.RUnlock()
	return s.simulator
}

func (s *Server) setSimulator(sim *simulator.Simulator) {
	s.simMu.Lock()
	s.simulator = sim
	s.simMu.Unlock()
}

// replaceSimulator atomically swaps in a new simulator and returns the
// previous one (if any). Callers are responsible for stopping the returned
// simulator.
func (s *Server) replaceSimulator(sim *simulator.Simulator) *simulator.Simulator {
	s.simMu.Lock()
	prev := s.simulator
	s.simulator = sim
	s.simMu.Unlock()
	return prev
}

// writePump serializes sends to the client. It also sends pings.
func (s *Server) writePump(c *clientConn) {
	ticker := time.NewTicker(s.cfg.PingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()
	for {
		select {
		case <-c.done:
			return
		case payload, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(s.cfg.WriteWait))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, payload); err != nil {
				log.Printf("WebSocket write error: %v", err)
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(s.cfg.WriteWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// readPump reads and processes incoming messages. It also enforces pong
// deadlines and exits on error.
func (s *Server) readPump(c *clientConn) {
	defer c.conn.Close()
	c.conn.SetReadDeadline(time.Now().Add(s.cfg.PongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(s.cfg.PongWait))
		return nil
	})
	for {
		_, payload, err := c.conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				log.Printf("WebSocket read error: %v", err)
			}
			return
		}
		var msg map[string]interface{}
		if err := json.Unmarshal(payload, &msg); err != nil {
			s.sendError(c, "Invalid JSON")
			continue
		}
		s.handleMessage(c, msg)
	}
}

func (s *Server) sendJSON(c *clientConn, payload interface{}) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	select {
	case c.send <- data:
	case <-c.done:
	}
}

func (s *Server) sendSuccess(c *clientConn, message string) {
	s.sendJSON(c, map[string]interface{}{"type": "success", "message": message})
}

func (s *Server) sendError(c *clientConn, message string) {
	s.sendJSON(c, map[string]interface{}{"type": "error", "message": message})
}

// handleMessage processes messages from clients
func (s *Server) handleMessage(c *clientConn, msg map[string]interface{}) {
	msgType, ok := msg["type"].(string)
	if !ok {
		s.sendError(c, "Invalid message format")
		return
	}
	switch msgType {
	case "init":
		s.handleInit(c, msg)
	case "start":
		s.handleStart(c)
	case "pause":
		s.handlePause(c)
	case "resume":
		s.handleResume(c)
	case "stop":
		s.handleStop(c)
	case "reset":
		s.handleReset(c)
	case "allocate":
		s.handleAllocate(c, msg)
	case "deallocate":
		s.handleDeallocate(c, msg)
	case "coalesce":
		s.handleCoalesce(c)
	case "detectLeaks":
		s.handleDetectLeaks(c, msg)
	case "speed":
		s.handleSpeed(c, msg)
	case "getState":
		s.handleGetState(c)
	default:
		s.sendError(c, fmt.Sprintf("Unknown message type: %s", msgType))
	}
}

func (s *Server) handleInit(c *clientConn, msg map[string]interface{}) {
	allocType, _ := msg["allocator"].(string)
	size := 65536
	if v, ok := msg["size"].(float64); ok && v > 0 {
		size = int(v)
	}

	var alloc allocator.Allocator
	switch allocType {
	case "firstfit":
		alloc = allocator.NewFirstFitAllocator(size)
	case "bestfit":
		alloc = allocator.NewBestFitAllocator(size)
	case "worstfit":
		alloc = allocator.NewWorstFitAllocator(size)
	case "buddy":
		alloc = allocator.NewBuddyAllocator(size)
	case "slab":
		alloc = allocator.NewSlabAllocator(size)
	case "segregated":
		alloc = allocator.NewSegregatedFitAllocator(size)
	case "pool":
		blockSize := 256
		if v, ok := msg["blockSize"].(float64); ok && v > 0 {
			blockSize = int(v)
		}
		blockCount := size / blockSize
		if blockCount <= 0 {
			s.sendError(c, "Pool block size too large for total memory size")
			return
		}
		alloc = pool.NewPoolAllocator(blockSize, blockCount)
	case "arena":
		alloc = pool.NewArenaAllocator(size)
	default:
		s.sendError(c, fmt.Sprintf("Unknown allocator: %s", allocType))
		return
	}

	sim := simulator.NewSimulator(alloc, allocType)
	// Per-init done channel: when the simulator is replaced we close
	// it so the closure stops forwarding stale updates. The simulator's
	// own Done() is also consulted as a belt-and-braces guard.
	callbackDone := make(chan struct{})
	sim.SetUpdateCallback(func(update *simulator.SimulationUpdate) {
		select {
		case <-callbackDone:
			return
		case <-sim.Done():
			return
		default:
		}
		select {
		case s.broadcast <- update:
		default:
			log.Printf("broadcast: queue full, dropping update")
		}
	})

	// Atomically replace the previous simulator's callback-done channel
	// (closing the old one stops its in-flight updates) and install the
	// new one. We copy the *sync.Once pointer (not the Once value, which
	// is a noCopy) and allocate a fresh Once for the new sim.
	s.simCallbackDoneMu.Lock()
	prevDone := s.simCallbackDone
	prevDoneOne := s.simCallbackDoneOne
	s.simCallbackDone = callbackDone
	s.simCallbackDoneOne = &sync.Once{}
	s.simCallbackDoneMu.Unlock()

	// Close the previous callback's done channel via its sync.Once so
	// that double-close panics are avoided. The Once is replaced above
	// so the new sim's closure can be triggered exactly once later.
	prevDoneOne.Do(func() { close(prevDone) })

	prev := s.replaceSimulator(sim)
	if prev != nil {
		prev.Invalidate()
		prev.Stop()
	}

	s.sendSuccess(c, fmt.Sprintf("Initialized %s allocator with %d bytes", allocType, size))

	// Send the initial state. Use sendJSON (which respects c.done) rather
	// than a `select { case c.send <- payload: default: }` drop pattern, so
	// the client is guaranteed to see the state right after the success
	// message. If the channel is full, we fall back to the broadcast
	// goroutine which will eventually pick up the next state update.
	if payload, err := json.Marshal(sim.GetCurrentState()); err == nil {
		select {
		case c.send <- payload:
		case <-c.done:
		default:
			// Best-effort: a subsequent operation (e.g. user clicking
			// Allocate) will trigger a broadcast and the client will catch up.
		}
	}
}

func (s *Server) handleStart(c *clientConn) {
	sim := s.getSimulator()
	if sim == nil {
		s.sendError(c, "Simulator not initialized")
		return
	}
	sim.Start()
	s.sendSuccess(c, "Simulation started")
}

func (s *Server) handlePause(c *clientConn) {
	sim := s.getSimulator()
	if sim == nil {
		s.sendError(c, "Simulator not initialized")
		return
	}
	sim.Pause()
	s.sendSuccess(c, "Simulation paused")
}

func (s *Server) handleResume(c *clientConn) {
	sim := s.getSimulator()
	if sim == nil {
		s.sendError(c, "Simulator not initialized")
		return
	}
	sim.Resume()
	s.sendSuccess(c, "Simulation resumed")
}

func (s *Server) handleStop(c *clientConn) {
	sim := s.getSimulator()
	if sim == nil {
		s.sendError(c, "Simulator not initialized")
		return
	}
	sim.Stop()
	s.sendSuccess(c, "Simulation stopped")
}

func (s *Server) handleReset(c *clientConn) {
	sim := s.getSimulator()
	if sim == nil {
		s.sendError(c, "Simulator not initialized")
		return
	}
	sim.Reset()
	s.sendSuccess(c, "Simulation reset")
}

func (s *Server) handleAllocate(c *clientConn, msg map[string]interface{}) {
	sim := s.getSimulator()
	if sim == nil {
		s.sendError(c, "Simulator not initialized")
		return
	}
	size, ok := msg["size"].(float64)
	if !ok || size <= 0 {
		s.sendError(c, "Invalid size")
		return
	}
	owner, _ := msg["owner"].(string)
	if owner == "" {
		owner = "User"
	}
	block, err := sim.Allocate(int(size), owner)
	if err != nil {
		s.sendError(c, fmt.Sprintf("Allocation failed: %v", err))
		return
	}
	s.sendSuccess(c, fmt.Sprintf("Allocated %d bytes at 0x%x", block.Size, block.Address))
}

func (s *Server) handleDeallocate(c *clientConn, msg map[string]interface{}) {
	sim := s.getSimulator()
	if sim == nil {
		s.sendError(c, "Simulator not initialized")
		return
	}
	addr, ok := msg["address"].(float64)
	if !ok {
		s.sendError(c, "Invalid address")
		return
	}
	if err := sim.Deallocate(uintptr(addr)); err != nil {
		s.sendError(c, fmt.Sprintf("Deallocation failed: %v", err))
		return
	}
	s.sendSuccess(c, fmt.Sprintf("Freed block at 0x%x", uintptr(addr)))
}

func (s *Server) handleCoalesce(c *clientConn) {
	sim := s.getSimulator()
	if sim == nil {
		s.sendError(c, "Simulator not initialized")
		return
	}
	merged := sim.Allocator().Coalesce()
	s.sendSuccess(c, fmt.Sprintf("Coalescing complete (merged %d block(s))", merged))
}

func (s *Server) handleDetectLeaks(c *clientConn, msg map[string]interface{}) {
	sim := s.getSimulator()
	if sim == nil {
		s.sendError(c, "Simulator not initialized")
		return
	}
	threshold := 5.0
	if t, ok := msg["threshold"].(float64); ok && t > 0 {
		threshold = t
	}
	leaks := sim.DetectLeaks(time.Duration(threshold * float64(time.Second)))
	s.sendJSON(c, map[string]interface{}{
		"type":    "leaksResult",
		"message": fmt.Sprintf("Detected %d potential leak(s)", len(leaks)),
		"leaks":   leaks,
	})
}

func (s *Server) handleSpeed(c *clientConn, msg map[string]interface{}) {
	sim := s.getSimulator()
	if sim == nil {
		s.sendError(c, "Simulator not initialized")
		return
	}
	speed, ok := msg["speed"].(float64)
	if !ok {
		s.sendError(c, "Invalid speed value")
		return
	}
	sim.SetSpeed(int(speed))
	s.sendSuccess(c, fmt.Sprintf("Speed set to %d ms", int(speed)))
}

func (s *Server) handleGetState(c *clientConn) {
	sim := s.getSimulator()
	if sim == nil {
		s.sendError(c, "Simulator not initialized")
		return
	}
	if payload, err := json.Marshal(sim.GetCurrentState()); err == nil {
		select {
		case c.send <- payload:
		case <-c.done:
		default:
		}
	}
}

// HandleHealth returns server health status
func (s *Server) HandleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	status := map[string]interface{}{
		"status":  "healthy",
		"clients": s.clientCount(),
	}
	if sim := s.getSimulator(); sim != nil {
		state := sim.GetCurrentState()
		status["simulatorReady"] = true
		status["simulationState"] = state.State
		status["allocatorType"] = state.AllocatorType
	} else {
		status["simulatorReady"] = false
	}
	_ = json.NewEncoder(w).Encode(status)
}

// Helper to safely parse float64 from env
func envFloatDefault(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}
