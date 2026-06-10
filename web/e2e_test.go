package web

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestE2E_FullFlowFirstFit exercises a complete user journey over the
// WebSocket API: init, allocate, deallocate, coalesce, reset.
func TestE2E_FullFlowFirstFit(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	u, _ := url.Parse(ts.URL)
	u.Scheme = "ws"
	u.Path = "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	type msg struct {
		Type string `json:"type"`
	}

	read := func() (map[string]interface{}, error) {
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, data, err := conn.ReadMessage()
		if err != nil {
			return nil, err
		}
		var out map[string]interface{}
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, err
		}
		return out, nil
	}

	send := func(m interface{}) error {
		return conn.WriteJSON(m)
	}

	// 1. Init
	if err := send(map[string]interface{}{"type": "init", "allocator": "firstfit", "size": 8192}); err != nil {
		t.Fatalf("send init: %v", err)
	}
	// Expect success then state
	r, err := read()
	if err != nil {
		t.Fatalf("read init: %v", err)
	}
	if r["type"] != "success" {
		t.Fatalf("expected init success, got %+v", r)
	}
	// The state follows - read it
	if _, err := read(); err != nil {
		t.Fatalf("read init state: %v", err)
	}

	// 2. Allocate
	if err := send(map[string]interface{}{"type": "allocate", "size": 256, "owner": "test"}); err != nil {
		t.Fatalf("send alloc: %v", err)
	}
	r, err = read()
	if err != nil {
		t.Fatalf("read alloc: %v", err)
	}
	if r["type"] != "success" {
		t.Fatalf("expected alloc success, got %+v", r)
	}
	r, err = read()
	if err != nil {
		t.Fatalf("read alloc state: %v", err)
	}
	metrics, ok := r["metrics"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected metrics, got %+v", r)
	}
	if metrics["totalAllocations"].(float64) != 1 {
		t.Errorf("expected 1 alloc, got %v", metrics["totalAllocations"])
	}

	// 3. Deallocate (extract address from state, then dealloc)
	blocks, _ := r["blocks"].([]interface{})
	if len(blocks) == 0 {
		t.Fatal("expected blocks in state")
	}
	first := blocks[0].(map[string]interface{})
	addr, _ := first["address"].(float64)
	if err := send(map[string]interface{}{"type": "deallocate", "address": addr}); err != nil {
		t.Fatalf("send dealloc: %v", err)
	}
	r, err = read()
	if err != nil {
		t.Fatalf("read dealloc: %v", err)
	}
	if r["type"] != "success" {
		t.Fatalf("expected dealloc success, got %+v", r)
	}
	r, err = read()
	if err != nil {
		t.Fatalf("read dealloc state: %v", err)
	}
	metrics = r["metrics"].(map[string]interface{})
	if metrics["totalDeallocations"].(float64) != 1 {
		t.Errorf("expected 1 dealloc, got %v", metrics["totalDeallocations"])
	}

	// 4. Coalesce
	if err := send(map[string]interface{}{"type": "coalesce"}); err != nil {
		t.Fatal(err)
	}
	if r, err := read(); err != nil {
		t.Fatal(err)
	} else if r["type"] != "success" {
		t.Fatalf("expected coalesce success, got %+v", r)
	}

	// 5. Reset
	if err := send(map[string]interface{}{"type": "reset"}); err != nil {
		t.Fatal(err)
	}
	if r, err := read(); err != nil {
		t.Fatal(err)
	} else if r["type"] != "success" {
		t.Fatalf("expected reset success, got %+v", r)
	}
	if r, err := read(); err != nil {
		t.Fatal(err)
	} else {
		metrics := r["metrics"].(map[string]interface{})
		if metrics["totalAllocations"].(float64) != 0 {
			t.Errorf("expected 0 allocs after reset, got %v", metrics["totalAllocations"])
		}
	}

	// 6. Health check
	ts2 := httptest.NewServer(srv.Routes())
	defer ts2.Close()
	resp, err := ts2.Client().Get(ts2.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var h map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		t.Fatal(err)
	}
	if h["simulatorReady"] != true {
		t.Errorf("expected simulatorReady=true, got %v", h["simulatorReady"])
	}
}
