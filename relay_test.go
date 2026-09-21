package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestLiveRelayReconnectRefreshAndCleanup(t *testing.T) {
	var liveConnections, configCalls atomic.Int32
	var mu sync.Mutex
	var keys []string
	live := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		n := liveConnections.Add(1)
		_, b, err := c.ReadMessage()
		if err != nil {
			return
		}
		packets, err := decodePackets(b)
		// Operation 7 is intentionally ignored by the public decoder; inspect auth header/body.
		_ = packets
		if err != nil || len(b) < 16 {
			t.Error("invalid auth packet")
			return
		}
		var auth struct {
			Key      string
			RoomID   int64
			ProtoVer int
		}
		if json.Unmarshal(b[16:], &auth) != nil || auth.RoomID != 7 || auth.ProtoVer != 3 {
			t.Error("invalid auth body")
			return
		}
		mu.Lock()
		keys = append(keys, auth.Key)
		mu.Unlock()
		c.WriteMessage(websocket.BinaryMessage, encodePacket(8, []byte(`{"code":0}`)))
		c.WriteMessage(websocket.BinaryMessage, compressedPacket(3, encodePacket(5, []byte(`{"cmd":"ROOM_CHANGE","data":{"title":"测试"}}`))))
		for {
			_, _, err = c.ReadMessage()
			if err != nil {
				return
			}
			c.WriteMessage(websocket.BinaryMessage, encodePacket(3, []byte{0, 0, 0, 0}))
			if n == 1 {
				return
			}
		}
	}))
	defer live.Close()
	u, _ := url.Parse(live.URL)
	host, portString, _ := net.SplitHostPort(u.Host)
	port, _ := strconv.Atoi(portString)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := configCalls.Add(1)
		fmt.Fprintf(w, `{"code":0,"data":{"token":"token-%d","host_list":[{"host":%q,"wss_port":%d}]}}`, n, host, port)
	}))
	defer api.Close()
	w := testWorker(t)
	w.config.RoomLimit = 1
	w.roomConfigURL = func(int64) string { return api.URL }
	w.allowLiveHost = func(h string) bool { return h == host }
	allowHTTP(w.fetch, api.URL)
	w.fetch.client.Transport.(*http.Transport).TLSClientConfig = live.Client().Transport.(*http.Transport).TLSClientConfig
	var forwarded atomic.Int32
	var zeroHeartbeat atomic.Bool
	w.config.URL = cluster(t, func(c *websocket.Conn) {
		for {
			_, b, err := c.ReadMessage()
			if err != nil {
				return
			}
			var m struct {
				Key   string
				Query json.RawMessage
				Relay *relayEvent
			}
			if json.Unmarshal(b, &m) != nil {
				continue
			}
			if len(m.Query) > 0 {
				result := 1
				if strings.Contains(string(m.Query), "pickRoom") {
					result = 7
				}
				c.WriteJSON(map[string]any{"key": m.Key, "data": map[string]any{"type": "query", "result": result}})
			}
			if m.Relay != nil {
				forwarded.Add(1)
				if m.Relay.Event == "heartbeat" && m.Relay.Data == float64(0) {
					zeroHeartbeat.Store(true)
				}
			}
		}
	})
	stop := startWorker(t, w)
	defer stop()
	eventually(t, func() bool {
		return liveConnections.Load() >= 2 && forwarded.Load() >= 3 && w.snapshot().Live == 1 && zeroHeartbeat.Load()
	})
	stop()
	mu.Lock()
	defer mu.Unlock()
	if len(keys) < 2 || keys[0] == keys[1] || configCalls.Load() < 2 {
		t.Fatal("stale token reused", keys)
	}
	if w.snapshot().Rooms != 0 || w.snapshot().Live != 0 {
		t.Fatal("rooms survived shutdown", w.snapshot())
	}
}

func TestLiveAuthenticationTimeoutAndDenial(t *testing.T) {
	for _, mode := range []string{"timeout", "denied", "heartbeat timeout"} {
		t.Run(mode, func(t *testing.T) {
			var connections atomic.Int32
			live := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer c.Close()
				connections.Add(1)
				if _, _, err = c.ReadMessage(); err != nil {
					return
				}
				if mode == "denied" {
					c.WriteMessage(websocket.BinaryMessage, encodePacket(8, []byte(`{"code":-101}`)))
				}
				if mode == "heartbeat timeout" {
					c.WriteMessage(websocket.BinaryMessage, encodePacket(8, []byte(`{"code":0}`)))
				}
				for {
					if _, _, err = c.ReadMessage(); err != nil {
						return
					}
				}
			}))
			defer live.Close()
			u, _ := url.Parse(live.URL)
			host, portText, _ := net.SplitHostPort(u.Host)
			port, _ := strconv.Atoi(portText)
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, `{"code":0,"data":{"token":"x","host_list":[{"host":%q,"wss_port":%d}]}}`, host, port)
			}))
			defer api.Close()
			w := testWorker(t)
			w.config.RoomLimit = 1
			w.roomConfigURL = func(int64) string { return api.URL }
			w.allowLiveHost = func(h string) bool { return h == host }
			allowHTTP(w.fetch, api.URL)
			w.fetch.client.Transport.(*http.Transport).TLSClientConfig = live.Client().Transport.(*http.Transport).TLSClientConfig
			w.config.URL = cluster(t, func(c *websocket.Conn) {
				for {
					_, b, err := c.ReadMessage()
					if err != nil {
						return
					}
					var m struct {
						Key   string
						Query json.RawMessage
					}
					json.Unmarshal(b, &m)
					if len(m.Query) > 0 {
						c.WriteJSON(map[string]any{"key": m.Key, "data": map[string]any{"type": "query", "result": 1}})
					}
				}
			})
			stop := startWorker(t, w)
			defer stop()
			eventually(t, func() bool { return connections.Load() >= 2 && w.snapshot().RelayFailures >= 1 })
			stop()
			if w.snapshot().Live != 0 || w.snapshot().Forwarded != 0 {
				t.Fatal(w.snapshot())
			}
		})
	}
}

func TestFailedRoomDoesNotBlockOtherAssignments(t *testing.T) {
	var requested atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"code":-352}`) }))
	defer api.Close()
	w := testWorker(t)
	w.config.RoomLimit = 3
	w.time.cooldown = 30 * time.Millisecond
	w.roomConfigURL = func(int64) string { return api.URL }
	allowHTTP(w.fetch, api.URL)
	w.config.URL = cluster(t, func(c *websocket.Conn) {
		for {
			_, b, err := c.ReadMessage()
			if err != nil {
				return
			}
			var m struct {
				Key   string
				Query json.RawMessage
			}
			json.Unmarshal(b, &m)
			if len(m.Query) > 0 {
				result := int32(1)
				if strings.Contains(string(m.Query), "pickRoom") {
					result = requested.Add(1)
				}
				c.WriteJSON(map[string]any{"key": m.Key, "data": map[string]any{"type": "query", "result": result}})
			}
		}
	})
	stop := startWorker(t, w)
	defer stop()
	eventually(t, func() bool { return w.snapshot().Rooms == 3 && w.snapshot().RelayFailures > 0 })
	stop()
	if requested.Load() != 3 {
		t.Fatal("room limit exceeded", requested.Load())
	}
}
