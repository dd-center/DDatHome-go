package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Opt-in fault injection, not a production soak or a Bilibili load test.
func TestFaultSoak(t *testing.T) {
	raw := os.Getenv("DDATHOME_SOAK")
	if raw == "" {
		t.Skip("set DDATHOME_SOAK=2m to run local fault injection")
	}
	duration, err := time.ParseDuration(raw)
	if err != nil || duration < time.Second {
		t.Fatal("invalid DDATHOME_SOAK duration")
	}
	baseline := runtime.NumGoroutine()
	var requests, connections atomic.Int64
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)
		switch n % 13 {
		case 0:
			<-r.Context().Done()
		case 1:
			w.WriteHeader(502)
			io.WriteString(w, "upstream error")
		default:
			io.WriteString(w, `{"code":0,"data":{"mock":true}}`)
		}
	}))
	defer api.Close()
	w := testWorker(t)
	allowHTTP(w.fetch, api.URL)
	w.fetch.timeout = 40 * time.Millisecond
	w.config.URL = cluster(t, func(c *websocket.Conn) {
		n := connections.Add(1)
		pulls := 0
		for {
			_, b, err := c.ReadMessage()
			if err != nil {
				return
			}
			if queryReply(c, b) {
				continue
			}
			if string(b) == "DDDhttp" {
				pulls++
				if pulls%3 == 0 {
					c.WriteMessage(websocket.TextMessage, []byte("{"))
				}
				sendTask(c, fmt.Sprintf("%d-%d", n, pulls), api.URL)
				if pulls == 8 {
					return
				}
			}
		}
	})
	stop := startWorker(t, w)
	defer stop()
	deadline := time.Now().Add(duration)
	peak := 0
	for time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
		peak = max(peak, runtime.NumGoroutine())
		if w.snapshot().InFlight > w.config.Concurrency {
			t.Fatal("unbounded concurrency")
		}
	}
	stop()
	eventually(t, func() bool { return runtime.NumGoroutine() <= baseline+8 })
	s := w.snapshot()
	if s.Connections < 3 || s.Valid < 5 || s.Failed == 0 || s.Malformed == 0 || s.InFlight != 0 {
		t.Fatal(s)
	}
	t.Logf("duration=%s connections=%d valid=%d failed=%d cancelled=%d malformed=%d peakGoroutines=%d finalGoroutines=%d baseline=%d", duration, s.Connections, s.Valid, s.Failed, s.Cancelled, s.Malformed, peak, runtime.NumGoroutine(), baseline)
}
