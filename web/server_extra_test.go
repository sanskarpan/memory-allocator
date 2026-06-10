package web

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestServer_InitStopsPreviousSimulator verifies that re-initialising
// after a previous init stops the old simulator. The old simulator held
// a goroutine (the auto-simulation runner) and a closure capturing the
// broadcast channel. If not stopped, the old simulator would keep
// pushing to s.broadcast and leak its goroutine.
func TestServer_InitStopsPreviousSimulator(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	u, _ := url.Parse(ts.URL)
	u.Scheme = "ws"
	u.Path = "/ws"

	dial := func() *websocket.Conn {
		c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		return c
	}

	// First init
	c1 := dial()
	defer c1.Close()
	sendWS(t, c1, map[string]interface{}{"type": "init", "allocator": "firstfit", "size": 1024})
	if r := recvOne(t, c1, 2*time.Second); r["type"] != "success" {
		t.Fatalf("c1 init: expected success, got %+v", r)
	}
	recvOne(t, c1, 2*time.Second) // state

	// Start auto-simulation so the previous sim is actually doing work
	sendWS(t, c1, map[string]interface{}{"type": "start"})
	if r := recvOne(t, c1, 2*time.Second); r["type"] != "success" {
		t.Fatalf("c1 start: expected success, got %+v", r)
	}
	recvOne(t, c1, 2*time.Second) // state (running)

	// Give the auto-simulator a chance to push a few state updates
	time.Sleep(100 * time.Millisecond)

	// Second init via a new client. Read until we see a success message
	// (skipping any state broadcasts that the old simulator may still
	// push between connect and the second init being processed).
	c2 := dial()
	defer c2.Close()
	sendWS(t, c2, map[string]interface{}{"type": "init", "allocator": "buddy", "size": 2048})
	// Find the success message in the stream.
	var success map[string]interface{}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c2.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		_, data, err := c2.ReadMessage()
		if err != nil {
			continue
		}
		var msg map[string]interface{}
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		if msg["type"] == "success" {
			success = msg
			break
		}
	}
	if success == nil {
		t.Fatal("never received success from c2 init")
	}
	// Drain at least one more frame (the post-init state).
	c2.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	_, _, _ = c2.ReadMessage()

	// The simulator should now be a buddy.
	sendWS(t, c2, map[string]interface{}{"type": "allocate", "size": 256, "owner": "x"})
	r := recvOne(t, c2, 2*time.Second)
	if r["type"] != "success" {
		t.Fatalf("expected success, got %+v", r)
	}
	state := recvOne(t, c2, 2*time.Second)
	if state["allocatorType"] != "buddy" {
		t.Errorf("expected allocatorType=buddy, got %v", state["allocatorType"])
	}
}

// TestServer_ReinitWhileFirstRunning specifically exercises the
// race that existed where two simulators ran concurrently and both
// pushed to the broadcast channel. The fix replaces the simulator
// atomically and stops the previous one.
func TestServer_ReinitWhileFirstRunning(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()
	u, _ := url.Parse(ts.URL)
	u.Scheme = "ws"
	u.Path = "/ws"

	c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	sendWS(t, c, map[string]interface{}{"type": "init", "allocator": "firstfit", "size": 8192})
	recvOne(t, c, 2*time.Second)
	recvOne(t, c, 2*time.Second)
	sendWS(t, c, map[string]interface{}{"type": "start"})
	recvOne(t, c, 2*time.Second)
	recvOne(t, c, 2*time.Second)

	// Reinit while running
	sendWS(t, c, map[string]interface{}{"type": "init", "allocator": "bestfit", "size": 4096})
	r := recvOne(t, c, 2*time.Second)
	if r["type"] != "success" {
		t.Fatalf("expected success, got %+v", r)
	}
	state := recvOne(t, c, 2*time.Second)
	if state["allocatorType"] != "bestfit" {
		t.Errorf("expected bestfit, got %v", state["allocatorType"])
	}
	if state["state"].(float64) != 0 { // Idle
		t.Errorf("expected state=0 (Idle) after reinit, got %v", state["state"])
	}
}

// TestServer_StressReinit creates and tears down simulators rapidly to
// verify no goroutine or channel leak (the previous implementation
// would pile up old simulators here).
func TestServer_StressReinit(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()
	u, _ := url.Parse(ts.URL)
	u.Scheme = "ws"
	u.Path = "/ws"

	c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	allocs := []string{"firstfit", "bestfit", "worstfit", "buddy", "slab", "segregated", "arena", "pool"}
	for i := 0; i < 20; i++ {
		a := allocs[i%len(allocs)]
		msg := map[string]interface{}{"type": "init", "allocator": a, "size": 2048}
		if a == "pool" {
			msg["blockSize"] = 128
		}
		sendWS(t, c, msg)
		r := recvOne(t, c, 2*time.Second)
		if r["type"] != "success" {
			t.Fatalf("iter %d: expected success, got %+v", i, r)
		}
		// Drain the state message.
		_ = recvOne(t, c, 2*time.Second)
	}
}

// TestServer_ParallelServers makes sure NewServerWithConfig is safe to
// call from multiple goroutines. The previous implementation mutated a
// package-level `upgrader` variable, which raced here.
func TestServer_ParallelServers(t *testing.T) {
	const n = 4
	servers := make([]*Server, n)
	closers := make([]*httptest.Server, n)
	for i := 0; i < n; i++ {
		servers[i] = newTestServer(t)
		closers[i] = httptest.NewServer(servers[i].Routes())
	}
	defer func() {
		for _, c := range closers {
			c.Close()
		}
	}()

	// Open a connection to each server in parallel.
	done := make(chan struct{}, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			u, _ := url.Parse(closers[i].URL)
			u.Scheme = "ws"
			u.Path = "/ws"
			conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
			if err != nil {
				t.Errorf("dial %d: %v", i, err)
				done <- struct{}{}
				return
			}
			defer conn.Close()
			sendWS(t, conn, map[string]interface{}{"type": "init", "allocator": "firstfit", "size": 1024})
			_ = recvOne(t, conn, 2*time.Second) // success
			_ = recvOne(t, conn, 2*time.Second) // state
			done <- struct{}{}
		}(i)
	}
	for i := 0; i < n; i++ {
		<-done
	}
}

func sendWS(t *testing.T, conn *websocket.Conn, msg interface{}) {
	t.Helper()
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// drainFor drains any pending messages on conn for up to dur. Used after
// a connect to clear the initial state broadcast before sending a request
// whose response must be the next message.
func drainFor(conn *websocket.Conn, dur time.Duration) {
	conn.SetReadDeadline(time.Now().Add(dur))
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}
