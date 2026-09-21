package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func testWorker(t *testing.T) *Worker {
	t.Helper()
	c := defaultConfig()
	c.RoomLimit = 0
	c.StatusAddress = ""
	c.Interval = 20
	c.UUID = newUUID()
	w := newWorker(c, slog.New(slog.NewTextHandler(io.Discard, nil)))
	w.time = timings{200 * time.Millisecond, 80 * time.Millisecond, 30 * time.Millisecond, 150 * time.Millisecond, 40 * time.Millisecond, 100 * time.Millisecond, 50 * time.Millisecond, 10 * time.Millisecond, 40 * time.Millisecond, time.Second, 20 * time.Millisecond, 10 * time.Millisecond, 30 * time.Millisecond, 150 * time.Millisecond, time.Second}
	return w
}
func startWorker(t *testing.T, w *Worker) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); w.Run(ctx) }()
	stop := func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("worker failed to stop")
		}
	}
	t.Cleanup(stop)
	return stop
}
func eventually(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not reached")
}
func cluster(t *testing.T, handler func(*websocket.Conn)) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		handler(c)
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}
func queryReply(c *websocket.Conn, b []byte) bool {
	var m struct {
		Key   string          `json:"key"`
		Query json.RawMessage `json:"query"`
	}
	if json.Unmarshal(b, &m) != nil || m.Query == nil {
		return false
	}
	result := any(1)
	if string(m.Query) != `"online"` {
		result = nil
	}
	_ = c.WriteJSON(map[string]any{"key": m.Key, "data": map[string]any{"type": "query", "result": result}})
	return true
}
func sendTask(c *websocket.Conn, key, url string) {
	_ = c.WriteJSON(map[string]any{"key": key, "data": map[string]any{"type": "http", "url": url}})
}

func TestSessionProtocolAndMalformedMessages(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"code":0,"data":{"ok":true}}`) }))
	defer api.Close()
	var received atomic.Bool
	w := testWorker(t)
	allowHTTP(w.fetch, api.URL)
	w.config.URL = cluster(t, func(c *websocket.Conn) {
		sent := false
		for {
			_, b, err := c.ReadMessage()
			if err != nil {
				return
			}
			if queryReply(c, b) {
				continue
			}
			if string(b) == "DDDhttp" {
				if !sent {
					sent = true
					for _, raw := range []string{"{", "null", "[]", "wait", `{"payload":{"type":"danmaku"}}`} {
						_ = c.WriteMessage(websocket.TextMessage, []byte(raw))
					}
					sendTask(c, "job", api.URL)
				} else {
					_ = c.WriteMessage(websocket.TextMessage, []byte(`{"empty":true}`))
				}
				continue
			}
			var m struct{ Key, Data string }
			if json.Unmarshal(b, &m) == nil && m.Key == "job" && m.Data == `{"code":0,"data":{"ok":true}}` {
				received.Store(true)
			}
		}
	})
	stop := startWorker(t, w)
	defer stop()
	eventually(t, func() bool { return received.Load() && w.snapshot().Ready })
	stop()
	s := w.snapshot()
	if s.Valid != 1 || s.Malformed != 3 || s.InFlight != 0 || s.Connected {
		t.Fatal(s)
	}
}

func TestReconnectCancelsOldSessionTasks(t *testing.T) {
	started, cancelled := make(chan struct{}), make(chan struct{})
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			close(started)
			<-r.Context().Done()
			close(cancelled)
			return
		}
		io.WriteString(w, `{"code":0}`)
	}))
	defer api.Close()
	w := testWorker(t)
	allowHTTP(w.fetch, api.URL)
	var connections atomic.Int32
	var stale atomic.Bool
	w.config.URL = cluster(t, func(c *websocket.Conn) {
		n := connections.Add(1)
		sent := false
		for {
			_, b, err := c.ReadMessage()
			if err != nil {
				return
			}
			if queryReply(c, b) {
				continue
			}
			if string(b) == "DDDhttp" && !sent {
				sent = true
				if n == 1 {
					sendTask(c, "old", api.URL+"/slow")
					select {
					case <-started:
					case <-time.After(time.Second):
						return
					}
					return
				}
				sendTask(c, "new", api.URL)
				continue
			}
			var m struct{ Key string }
			json.Unmarshal(b, &m)
			if n > 1 && m.Key == "old" {
				stale.Store(true)
			}
		}
	})
	stop := startWorker(t, w)
	defer stop()
	eventually(t, func() bool { return w.snapshot().Valid >= 1 })
	stop()
	select {
	case <-cancelled:
	default:
		t.Fatal("old HTTP request survived reconnect")
	}
	if stale.Load() || w.snapshot().Cancelled != 1 || connections.Load() < 2 {
		t.Fatal(w.snapshot(), connections.Load())
	}
}

func TestDeadSchedulerRecovers(t *testing.T) {
	for _, mode := range []string{"application", "pong", "no tasks"} {
		t.Run(mode, func(t *testing.T) {
			w := testWorker(t)
			var connections atomic.Int32
			w.config.URL = cluster(t, func(c *websocket.Conn) {
				connections.Add(1)
				if mode == "pong" {
					c.SetPingHandler(func(string) error { return nil })
				}
				for {
					_, b, err := c.ReadMessage()
					if err != nil {
						return
					}
					if mode != "application" {
						queryReply(c, b)
					}
				}
			})
			stop := startWorker(t, w)
			defer stop()
			if mode == "no tasks" {
				eventually(t, func() bool { return w.snapshot().Ready })
				time.Sleep(250 * time.Millisecond)
				if connections.Load() != 1 {
					t.Fatal("idle connection reconnected")
				}
			} else {
				eventually(t, func() bool { return connections.Load() >= 2 })
			}
			stop()
		})
	}
}

func TestHandshakeTimeoutAndInitialFailureRetry(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); <-r.Context().Done() }))
	defer srv.Close()
	w := testWorker(t)
	w.config.URL = "ws" + strings.TrimPrefix(srv.URL, "http")
	w.time.handshake = 50 * time.Millisecond
	stop := startWorker(t, w)
	defer stop()
	eventually(t, func() bool { return calls.Load() >= 2 })
	stop()
	if w.snapshot().Connections != 0 {
		t.Fatal("handshake unexpectedly succeeded")
	}
}

func TestTaskCapacityAndDuplicate(t *testing.T) {
	var running, peak, requests atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		n := running.Add(1)
		defer running.Add(-1)
		for {
			old := peak.Load()
			if n <= old || peak.CompareAndSwap(old, n) {
				break
			}
		}
		<-r.Context().Done()
	}))
	defer api.Close()
	w := testWorker(t)
	allowHTTP(w.fetch, api.URL)
	w.config.URL = cluster(t, func(c *websocket.Conn) {
		sent := false
		for {
			_, b, err := c.ReadMessage()
			if err != nil {
				return
			}
			if queryReply(c, b) {
				continue
			}
			if string(b) == "DDDhttp" && !sent {
				sent = true
				sendTask(c, "a", api.URL)
				sendTask(c, "a", api.URL)
				sendTask(c, "b", api.URL)
				sendTask(c, "c", api.URL)
			}
		}
	})
	stop := startWorker(t, w)
	defer stop()
	eventually(t, func() bool { return peak.Load() == 2 && w.snapshot().Overloaded == 1 })
	stop()
	if requests.Load() != 2 || peak.Load() > 2 || w.snapshot().Cancelled != 2 {
		t.Fatal(w.snapshot(), requests.Load(), peak.Load())
	}
}

func TestRateLimitCooldownSurvivesReconnect(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"code":-352}`) }))
	defer api.Close()
	w := testWorker(t)
	allowHTTP(w.fetch, api.URL)
	var connections, pulls atomic.Int32
	w.config.URL = cluster(t, func(c *websocket.Conn) {
		n := connections.Add(1)
		for {
			_, b, err := c.ReadMessage()
			if err != nil {
				return
			}
			if queryReply(c, b) {
				continue
			}
			if string(b) == "DDDhttp" {
				pulls.Add(1)
				sendTask(c, "limited", api.URL)
			} else if n == 1 {
				return
			}
		}
	})
	stop := startWorker(t, w)
	defer stop()
	eventually(t, func() bool { return connections.Load() >= 2 && w.snapshot().Ready })
	time.Sleep(120 * time.Millisecond)
	stop()
	if pulls.Load() != 1 || w.snapshot().Valid != 0 || w.snapshot().Failed != 1 {
		t.Fatal(w.snapshot(), pulls.Load())
	}
}

func TestHealthReflectsReadiness(t *testing.T) {
	w := testWorker(t)
	h := w.statusHandler()
	for _, path := range []string{"/livez", "/healthz", "/status"} {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest("GET", path, nil))
		want := 200
		if path == "/healthz" {
			want = 503
		}
		if r.Code != want {
			t.Fatal(path, r.Code)
		}
	}
	w.update(func(s *Stats) { s.Ready = true })
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("GET", "/healthz", nil))
	if r.Code != 200 {
		t.Fatal(r.Code)
	}
}
