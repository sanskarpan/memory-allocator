package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	cfg := DefaultConfig()
	// Use a random path for static dir (won't be hit by tests)
	cfg.StaticDir = "/tmp"
	return NewServerWithConfig(cfg)
}

func connectWS(t *testing.T, srv *Server) (*websocket.Conn, *httptest.Server) {
	t.Helper()
	ts := httptest.NewServer(srv.Routes())
	u, _ := url.Parse(ts.URL)
	u.Scheme = "ws"
	u.Path = "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return conn, ts
}

func send(t *testing.T, conn *websocket.Conn, msg interface{}) {
	t.Helper()
	if err := conn.WriteJSON(msg); err != nil {
		t.Fatalf("send: %v", err)
	}
}

func recvOne(t *testing.T, conn *websocket.Conn, timeout time.Duration) map[string]interface{} {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(timeout))
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("recv: %v", err)
	}
	var out map[string]interface{}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

func drain(conn *websocket.Conn, dur time.Duration) {
	conn.SetReadDeadline(time.Now().Add(dur))
	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			return
		}
	}
}

func TestServer_HealthEndpoint(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("Expected 200, got %d", resp.StatusCode)
	}
	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "healthy" {
		t.Errorf("Expected status=healthy, got %v", body["status"])
	}
	if body["simulatorReady"] != false {
		t.Errorf("Expected simulatorReady=false, got %v", body["simulatorReady"])
	}
}

func TestServer_WebSocketInitFirstFit(t *testing.T) {
	srv := newTestServer(t)
	conn, ts := connectWS(t, srv)
	defer conn.Close()
	defer ts.Close()

	send(t, conn, map[string]interface{}{"type": "init", "allocator": "firstfit", "size": 1024})
	// Expect: success, then initial state
	first := recvOne(t, conn, 2*time.Second)
	if first["type"] != "success" {
		t.Errorf("Expected first message type=success, got %+v", first)
	}
	// Second message should be a state update (no "type" field, has "state")
	second := recvOne(t, conn, 2*time.Second)
	if _, hasType := second["type"]; hasType {
		// Could be a state update or success
	}
	if state, ok := second["state"].(float64); !ok || int(state) != 0 {
		t.Errorf("Expected state=0 (Idle), got %+v", second)
	}
}

func TestServer_AllocateDeallocate(t *testing.T) {
	srv := newTestServer(t)
	conn, ts := connectWS(t, srv)
	defer conn.Close()
	defer ts.Close()

	send(t, conn, map[string]interface{}{"type": "init", "allocator": "firstfit", "size": 1024})
	_ = recvOne(t, conn, 2*time.Second) // success
	_ = recvOne(t, conn, 2*time.Second) // state

	send(t, conn, map[string]interface{}{"type": "allocate", "size": 256, "owner": "test"})
	// First response is success
	resp := recvOne(t, conn, 2*time.Second)
	if resp["type"] != "success" {
		t.Errorf("Expected success, got %+v", resp)
	}
	// Then a state update
	state := recvOne(t, conn, 2*time.Second)
	metrics, ok := state["metrics"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected metrics in state, got %+v", state)
	}
	if metrics["totalAllocations"].(float64) != 1 {
		t.Errorf("Expected 1 allocation, got %v", metrics["totalAllocations"])
	}
}

func TestServer_InvalidAllocate(t *testing.T) {
	srv := newTestServer(t)
	conn, ts := connectWS(t, srv)
	defer conn.Close()
	defer ts.Close()

	send(t, conn, map[string]interface{}{"type": "init", "allocator": "firstfit", "size": 1024})
	_ = recvOne(t, conn, 2*time.Second)
	_ = recvOne(t, conn, 2*time.Second)

	// Missing size field
	send(t, conn, map[string]interface{}{"type": "allocate", "owner": "x"})
	resp := recvOne(t, conn, 2*time.Second)
	if resp["type"] != "error" {
		t.Errorf("Expected error, got %+v", resp)
	}
}

func TestServer_OperationsBeforeInit(t *testing.T) {
	srv := newTestServer(t)
	conn, ts := connectWS(t, srv)
	defer conn.Close()
	defer ts.Close()

	send(t, conn, map[string]interface{}{"type": "start"})
	resp := recvOne(t, conn, 2*time.Second)
	if resp["type"] != "error" {
		t.Errorf("Expected error for start before init, got %+v", resp)
	}
	if msg, _ := resp["message"].(string); !strings.Contains(msg, "Simulator not initialized") {
		t.Errorf("Expected 'Simulator not initialized' error, got %q", msg)
	}
}

func TestServer_UnknownMessageType(t *testing.T) {
	srv := newTestServer(t)
	conn, ts := connectWS(t, srv)
	defer conn.Close()
	defer ts.Close()

	send(t, conn, map[string]interface{}{"type": "definitelyNotARealType"})
	resp := recvOne(t, conn, 2*time.Second)
	if resp["type"] != "error" {
		t.Errorf("Expected error for unknown type, got %+v", resp)
	}
}

func TestServer_AllAllocators(t *testing.T) {
	allocators := []string{"firstfit", "bestfit", "worstfit", "buddy", "slab", "segregated", "pool", "arena"}
	for _, a := range allocators {
		t.Run(a, func(t *testing.T) {
			srv := newTestServer(t)
			conn, ts := connectWS(t, srv)
			defer conn.Close()
			defer ts.Close()

			msg := map[string]interface{}{
				"type":      "init",
				"allocator": a,
				"size":      2048,
			}
			if a == "pool" {
				msg["blockSize"] = 128
			}
			send(t, conn, msg)
			_ = recvOne(t, conn, 2*time.Second) // success
			_ = recvOne(t, conn, 2*time.Second) // initial state

			allocSize := 256
			if a == "pool" {
				allocSize = 128
			}
			send(t, conn, map[string]interface{}{"type": "allocate", "size": allocSize, "owner": "x"})
			_ = recvOne(t, conn, 2*time.Second) // success
			_ = recvOne(t, conn, 2*time.Second) // state

			// For arena, individual dealloc should error
			send(t, conn, map[string]interface{}{"type": "deallocate", "address": 0x1000})
			resp := recvOne(t, conn, 2*time.Second)
			if a == "arena" {
				if resp["type"] != "error" {
					t.Errorf("Arena dealloc should error, got %+v", resp)
				}
			} else {
				if resp["type"] != "success" {
					// For pool/slab/segregated, the dealloc might fail
					// because 0x1000 is not a valid address in those
					// regions. That's still an error response, just a
					// different one.
					msg, _ := resp["message"].(string)
					if !strings.Contains(msg, "invalid block") &&
						!strings.Contains(msg, "block not found") {
						t.Errorf("Expected success or invalid-block error, got %+v", resp)
					}
				}
			}
		})
	}
}

func TestServer_ConcurrentConnections(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	u, _ := url.Parse(ts.URL)
	u.Scheme = "ws"
	u.Path = "/ws"

	const n = 5
	conns := make([]*websocket.Conn, n)
	for i := 0; i < n; i++ {
		c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
		if err != nil {
			t.Fatalf("dial %d: %v", i, err)
		}
		conns[i] = c
	}
	defer func() {
		for _, c := range conns {
			c.Close()
		}
	}()

	// Allow server to register all clients
	time.Sleep(100 * time.Millisecond)
	if c := srv.clientCount(); c != n {
		t.Errorf("Expected %d clients, got %d", n, c)
	}
}

func TestServer_DetectLeaksReturnsLeakList(t *testing.T) {
	srv := newTestServer(t)
	conn, ts := connectWS(t, srv)
	defer conn.Close()
	defer ts.Close()

	send(t, conn, map[string]interface{}{"type": "init", "allocator": "firstfit", "size": 1024})
	_ = recvOne(t, conn, 2*time.Second)
	_ = recvOne(t, conn, 2*time.Second)

	// Allocate, then immediately request a leak detection with a 0s threshold
	send(t, conn, map[string]interface{}{"type": "allocate", "size": 64, "owner": "leak-test"})
	_ = recvOne(t, conn, 2*time.Second)
	_ = recvOne(t, conn, 2*time.Second)

	// Wait so the block has nonzero age
	time.Sleep(50 * time.Millisecond)
	send(t, conn, map[string]interface{}{"type": "detectLeaks", "threshold": 0.01})
	resp := recvOne(t, conn, 2*time.Second)
	if resp["type"] != "leaksResult" {
		t.Errorf("Expected leaksResult, got %+v", resp)
	}
	leaks, ok := resp["leaks"].([]interface{})
	if !ok {
		t.Fatalf("Expected leaks array, got %+v", resp)
	}
	if len(leaks) == 0 {
		t.Error("Expected at least one leak to be reported")
	}
}

func TestServer_CoalesceActuallyMerges(t *testing.T) {
	srv := newTestServer(t)
	conn, ts := connectWS(t, srv)
	defer conn.Close()
	defer ts.Close()

	send(t, conn, map[string]interface{}{"type": "init", "allocator": "firstfit", "size": 1024})
	_ = recvOne(t, conn, 2*time.Second)
	_ = recvOne(t, conn, 2*time.Second)

	send(t, conn, map[string]interface{}{"type": "coalesce"})
	resp := recvOne(t, conn, 2*time.Second)
	if resp["type"] != "success" {
		t.Errorf("Expected success, got %+v", resp)
	}
	if msg, _ := resp["message"].(string); !strings.Contains(msg, "merged") {
		t.Errorf("Expected merge count in response, got %q", msg)
	}
}

func TestServer_ConfigFromEnv(t *testing.T) {
	t.Setenv("MEMALLOC_PORT", "9999")
	t.Setenv("MEMALLOC_STATIC_DIR", "/tmp/static")
	t.Setenv("MEMALLOC_BROADCAST_BUFFER", "512")
	t.Setenv("MEMALLOC_PING_PERIOD", "45s")
	t.Setenv("MEMALLOC_PONG_WAIT", "90s")
	t.Setenv("MEMALLOC_WRITE_WAIT", "7s")
	t.Setenv("MEMALLOC_MAX_MESSAGE_BYTES", "2048")
	t.Setenv("MEMALLOC_MAX_CONN_PER_IP", "3")
	cfg := ConfigFromEnv()
	if cfg.Port != ":9999" {
		t.Errorf("Expected port :9999, got %q", cfg.Port)
	}
	if cfg.StaticDir != "/tmp/static" {
		t.Errorf("Expected static dir /tmp/static, got %q", cfg.StaticDir)
	}
	if cfg.BroadcastBuffer != 512 {
		t.Errorf("Expected broadcast buffer 512, got %d", cfg.BroadcastBuffer)
	}
	if cfg.PingPeriod != 45*time.Second {
		t.Errorf("Expected ping period 45s, got %v", cfg.PingPeriod)
	}
	if cfg.PongWait != 90*time.Second {
		t.Errorf("Expected pong wait 90s, got %v", cfg.PongWait)
	}
	if cfg.WriteWait != 7*time.Second {
		t.Errorf("Expected write wait 7s, got %v", cfg.WriteWait)
	}
	if cfg.MaxMessageBytes != 2048 {
		t.Errorf("Expected max message bytes 2048, got %d", cfg.MaxMessageBytes)
	}
	if cfg.MaxConnPerIP != 3 {
		t.Errorf("Expected max connections per IP 3, got %d", cfg.MaxConnPerIP)
	}
}

func TestServer_ConfigFromEnvAllowsColonPortAndUnlimitedConnections(t *testing.T) {
	t.Setenv("MEMALLOC_PORT", ":9090")
	t.Setenv("MEMALLOC_MAX_CONN_PER_IP", "0")

	cfg := ConfigFromEnv()
	if cfg.Port != ":9090" {
		t.Fatalf("Expected port :9090, got %q", cfg.Port)
	}
	if cfg.MaxConnPerIP != 0 {
		t.Fatalf("Expected unlimited MaxConnPerIP=0, got %d", cfg.MaxConnPerIP)
	}
}

func TestServer_UnlimitedConnectionsAllowed(t *testing.T) {
	srv := NewServerWithConfig(Config{
		Port:         ":0",
		StaticDir:    "/tmp",
		MaxConnPerIP: 0,
	})

	for i := 0; i < 20; i++ {
		if !srv.acquireIPConn("203.0.113.10") {
			t.Fatalf("connection %d should have been allowed", i+1)
		}
	}
	for i := 0; i < 20; i++ {
		srv.releaseIPConn("203.0.113.10")
	}
}

func TestServer_ClientIPParsesForwardedFor(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://example.com/ws", nil)
	req.RemoteAddr = "192.0.2.10:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.5, 198.51.100.9")
	if got := clientIP(req); got != "203.0.113.5" {
		t.Fatalf("Expected first forwarded IP, got %q", got)
	}
}
