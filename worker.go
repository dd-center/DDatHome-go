package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type timings struct{ handshake, write, ping, pong, probe, query, pull, retryMin, retryMax, stable, roomPick, roomRetry, roomHeartbeat, roomTimeout, cooldown time.Duration }

func defaultTimings() timings {
	return timings{
		handshake: 10 * time.Second, write: 2 * time.Second,
		ping: 30 * time.Second, pong: 70 * time.Second,
		probe: 30 * time.Second, query: 5 * time.Second, pull: 5 * time.Second,
		retryMin: time.Second, retryMax: time.Minute, stable: time.Minute,
		roomPick: 5 * time.Second, roomRetry: 2 * time.Second,
		roomHeartbeat: 30 * time.Second, roomTimeout: 70 * time.Second,
		cooldown: time.Minute,
	}
}

type Stats struct {
	Connected        bool      `json:"connected"`
	Ready            bool      `json:"ready"`
	Connections      uint64    `json:"connections"`
	Received         uint64    `json:"received"`
	Submitted        uint64    `json:"submitted"`
	Valid            uint64    `json:"valid"`
	Failed           uint64    `json:"failed"`
	Cancelled        uint64    `json:"cancelled"`
	Malformed        uint64    `json:"malformed"`
	Overloaded       uint64    `json:"overloaded"`
	InFlight         int       `json:"inFlight"`
	Rooms            int       `json:"rooms"`
	Live             int       `json:"live"`
	Forwarded        uint64    `json:"forwarded"`
	RelayFailures    uint64    `json:"relayFailures"`
	LastFailure      string    `json:"lastFailure,omitempty"`
	LastRelayFailure string    `json:"lastRelayFailure,omitempty"`
	CooldownUntil    time.Time `json:"cooldownUntil"`
}

type Worker struct {
	config       Config
	fetch        *fetcher
	log          *slog.Logger
	time         timings
	mu           sync.Mutex
	stats        Stats
	rateFailures int
	// Injectable endpoints keep all fault tests off production services.
	roomConfigURL func(int64) string
	allowLiveHost func(string) bool
}

func newWorker(c Config, log *slog.Logger) *Worker {
	return &Worker{config: c, fetch: newFetcher(c), log: log, time: defaultTimings(), roomConfigURL: defaultRoomConfigURL, allowLiveHost: validLiveHost}
}
func (w *Worker) update(f func(*Stats)) { w.mu.Lock(); defer w.mu.Unlock(); f(&w.stats) }
func (w *Worker) snapshot() Stats       { w.mu.Lock(); defer w.mu.Unlock(); return w.stats }

func pause(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
func retryDelay(minimum, maximum time.Duration, failures int) time.Duration {
	d := minimum
	for i := 0; i < failures && d < maximum; i++ {
		d = min(d*2, maximum)
	}
	return time.Duration(float64(d) * (0.5 + rand.Float64()*0.5))
}

func (w *Worker) Run(ctx context.Context) {
	defer w.fetch.client.CloseIdleConnections()
	failures := 0
	for ctx.Err() == nil {
		started := time.Now()
		dialer := websocket.Dialer{Proxy: http.ProxyFromEnvironment, HandshakeTimeout: w.time.handshake}
		conn, resp, err := dialer.DialContext(ctx, w.config.upstreamURL(), nil)
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		if err == nil {
			w.update(func(s *Stats) { s.Connected = true; s.Connections++ })
			w.log.Info("connected to scheduler")
			err = w.runSession(ctx, conn)
		}
		w.update(func(s *Stats) { s.Connected = false; s.Ready = false; s.InFlight = 0; s.Rooms = 0; s.Live = 0 })
		if ctx.Err() != nil {
			return
		}
		if time.Since(started) >= w.time.stable {
			failures = 0
		}
		delay := retryDelay(w.time.retryMin, w.time.retryMax, failures)
		failures = min(failures+1, 20)
		w.update(func(s *Stats) { s.LastFailure = err.Error() })
		w.log.Warn("scheduler disconnected; will retry", "error", err, "delay", delay)
		if !pause(ctx, delay) {
			return
		}
	}
}

type envelope struct {
	Key   string `json:"key"`
	Empty bool   `json:"empty"`
	Data  *struct {
		Type   string          `json:"type"`
		URL    string          `json:"url"`
		Result json.RawMessage `json:"result"`
	} `json:"data"`
}
type session struct {
	w         *Worker
	ctx       context.Context
	cancel    context.CancelCauseFunc
	conn      *websocket.Conn
	writeGate chan struct{}
	queryMu   sync.Mutex
	queries   map[string]chan json.RawMessage
	incoming  chan envelope
}

func (s *session) send(v any) error {
	var b []byte
	var err error
	if text, ok := v.(string); ok {
		b = []byte(text)
	} else {
		b, err = json.Marshal(v)
	}
	if err != nil {
		return err
	}
	if len(b) > maxBodyBytes*6+1024 {
		return errors.New("outgoing message too large")
	}
	// The deadline includes waiting for other writers, so busy relay rooms
	// cannot starve HTTP results beyond the scheduler's 15-second deadline.
	deadline := time.Now().Add(s.w.time.write)
	timer := time.NewTimer(s.w.time.write)
	defer timer.Stop()
	select {
	case s.writeGate <- struct{}{}:
		defer func() { <-s.writeGate }()
	case <-s.ctx.Done():
		return context.Cause(s.ctx)
	case <-timer.C:
		err := errors.New("scheduler write queue timed out")
		s.cancel(err)
		return err
	}
	if s.ctx.Err() != nil {
		return context.Cause(s.ctx)
	}
	if err = s.conn.SetWriteDeadline(deadline); err == nil {
		err = s.conn.WriteMessage(websocket.TextMessage, b)
	}
	if err != nil {
		s.cancel(err)
	}
	return err
}

func (s *session) ask(query any) (json.RawMessage, error) {
	key := newUUID()
	ch := make(chan json.RawMessage, 1)
	s.queryMu.Lock()
	if len(s.queries) >= 8 {
		s.queryMu.Unlock()
		return nil, errors.New("query capacity exceeded")
	}
	s.queries[key] = ch
	s.queryMu.Unlock()
	defer func() { s.queryMu.Lock(); delete(s.queries, key); s.queryMu.Unlock() }()
	if err := s.send(map[string]any{"key": key, "query": query}); err != nil {
		return nil, err
	}
	t := time.NewTimer(s.w.time.query)
	defer t.Stop()
	select {
	case <-s.ctx.Done():
		return nil, context.Cause(s.ctx)
	case <-t.C:
		return nil, errors.New("scheduler query timed out")
	case data := <-ch:
		return data, nil
	}
}

func (s *session) read() {
	s.conn.SetReadLimit(1 << 20)
	_ = s.conn.SetReadDeadline(time.Now().Add(s.w.time.pong))
	s.conn.SetPongHandler(func(string) error { return s.conn.SetReadDeadline(time.Now().Add(s.w.time.pong)) })
	for {
		kind, b, err := s.conn.ReadMessage()
		if err != nil {
			s.cancel(err)
			return
		}
		var msg envelope
		if kind == websocket.TextMessage && string(b) == "wait" {
			msg.Empty = true
		} else if kind != websocket.TextMessage || json.Unmarshal(b, &msg) != nil || !bytes.HasPrefix(bytes.TrimSpace(b), []byte("{")) {
			s.w.update(func(st *Stats) { st.Malformed++ })
			continue
		}
		if msg.Data != nil && msg.Data.Type == "query" {
			s.queryMu.Lock()
			if ch := s.queries[msg.Key]; ch != nil {
				select {
				case ch <- msg.Data.Result:
				default:
				}
			}
			s.queryMu.Unlock()
			continue
		}
		select {
		case s.incoming <- msg:
		case <-s.ctx.Done():
			return
		}
	}
}

func (s *session) probe() {
	for {
		b, err := s.ask("online")
		var n *float64
		if err == nil && (json.Unmarshal(b, &n) != nil || n == nil || *n < 0) {
			err = errors.New("invalid online query response")
		}
		if err != nil {
			s.cancel(err)
			return
		}
		s.w.update(func(st *Stats) { st.Ready = true })
		if !pause(s.ctx, s.w.time.probe) {
			return
		}
	}
}

type taskDone struct {
	key    string
	result fetchResult
}

func (w *Worker) runSession(parent context.Context, conn *websocket.Conn) error {
	ctx, cancel := context.WithCancelCause(parent)
	s := &session{w: w, ctx: ctx, cancel: cancel, conn: conn, writeGate: make(chan struct{}, 1), queries: make(map[string]chan json.RawMessage), incoming: make(chan envelope, 32)}
	closed := make(chan struct{})
	go func() { <-ctx.Done(); conn.Close(); close(closed) }()
	var background, jobs sync.WaitGroup
	for _, f := range []func(){s.read, s.probe, s.relay} {
		background.Add(1)
		go func() { defer background.Done(); f() }()
	}
	defer func() { cancel(context.Canceled); <-closed; background.Wait(); jobs.Wait() }()
	tick := time.NewTicker(min(time.Duration(w.config.Interval)*time.Millisecond, 100*time.Millisecond))
	defer tick.Stop()
	ping := time.NewTicker(w.time.ping)
	defer ping.Stop()
	summary := time.NewTicker(time.Minute)
	defer summary.Stop()
	done := make(chan taskDone, w.config.Concurrency)
	active := make(map[string]bool)
	var nextPull, pullUntil time.Time
	for {
		select {
		case <-ctx.Done():
			w.update(func(st *Stats) { st.Cancelled += uint64(len(active)) })
			return context.Cause(ctx)
		case <-summary.C:
			st := w.snapshot()
			w.log.Info("status", "ready", st.Ready, "valid", st.Valid, "failed", st.Failed, "rooms", st.Rooms, "live", st.Live)
		case <-ping.C:
			if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(w.time.write)); err != nil {
				cancel(err)
			}
		case now := <-tick.C:
			if len(active) < w.config.Concurrency && !now.Before(nextPull) && !now.Before(pullUntil) && !now.Before(w.snapshot().CooldownUntil) {
				if s.send("DDDhttp") == nil {
					nextPull = now.Add(time.Duration(w.config.Interval) * time.Millisecond)
					pullUntil = now.Add(w.time.pull)
				}
			}
		case msg := <-s.incoming:
			if msg.Empty || (msg.Data != nil && msg.Data.Type == "wait") {
				pullUntil = time.Time{}
				continue
			}
			if msg.Data == nil || msg.Data.Type != "http" {
				continue
			}
			pullUntil = time.Time{}
			if msg.Key == "" || len(msg.Key) > 256 || msg.Data.URL == "" {
				w.update(func(st *Stats) { st.Malformed++ })
				continue
			}
			if active[msg.Key] {
				continue
			}
			if len(active) >= w.config.Concurrency {
				w.update(func(st *Stats) { st.Overloaded++ })
				_ = s.send(map[string]any{"key": msg.Key, "data": failedData(errors.New("worker capacity exceeded"))})
				continue
			}
			active[msg.Key] = true
			w.update(func(st *Stats) { st.Received++; st.InFlight = len(active) })
			jobs.Add(1)
			go func(key, raw string) {
				defer jobs.Done()
				r := w.fetch.task(ctx, raw)
				select {
				case done <- taskDone{key, r}:
				case <-ctx.Done():
				}
			}(msg.Key, msg.Data.URL)
		case result := <-done:
			delete(active, result.key)
			w.update(func(st *Stats) { st.InFlight = len(active) })
			r := result.result
			if r.Limited {
				delay := min(w.time.cooldown*time.Duration(1<<min(w.rateFailures, 4)), 15*time.Minute)
				w.rateFailures = min(w.rateFailures+1, 20)
				w.update(func(st *Stats) { st.CooldownUntil = time.Now().Add(delay) })
				w.log.Warn("API rate limited; pausing task requests", "duration", delay)
			} else if r.Valid {
				w.rateFailures = 0
			}
			if err := s.send(map[string]any{"key": result.key, "data": r.Text}); err != nil {
				w.update(func(st *Stats) { st.Cancelled++ })
				continue
			}
			w.update(func(st *Stats) {
				st.Submitted++
				if r.Valid {
					st.Valid++
				} else {
					st.Failed++
					st.LastFailure = fmt.Sprint(r.Err)
				}
			})
			if !r.Valid || w.config.Verbose {
				level := slog.LevelInfo
				if !r.Valid {
					level = slog.LevelWarn
				}
				w.log.Log(ctx, level, "task result", "key", result.key, "valid", r.Valid, "error", r.Err)
			}
		}
	}
}
