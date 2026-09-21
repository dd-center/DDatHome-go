package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

func defaultRoomConfigURL(id int64) string {
	return fmt.Sprintf("https://api.live.bilibili.com/xlive/web-room/v1/index/getDanmuInfo?id=%d&type=0", id)
}
func validLiveHost(host string) bool {
	return host == "chat.bilibili.com" || strings.HasSuffix(host, ".chat.bilibili.com")
}

func (s *session) relay() {
	if s.w.config.RoomLimit == 0 {
		return
	}
	rooms := make(map[int64]bool)
	gate := make(chan struct{}, 1)
	gate <- struct{}{}
	var wg sync.WaitGroup
	defer wg.Wait()
	for s.ctx.Err() == nil {
		if len(rooms) < s.w.config.RoomLimit {
			b, err := s.ask(map[string]string{"type": "pickRoom"})
			var id int64
			if err == nil && json.Unmarshal(b, &id) == nil && id > 0 && id <= 9007199254740991 && !rooms[id] {
				rooms[id] = true
				s.w.update(func(st *Stats) { st.Rooms++ })
				wg.Add(1)
				go func() { defer wg.Done(); s.roomLoop(id, gate) }()
			}
		}
		if !pause(s.ctx, s.w.time.roomPick) {
			return
		}
	}
}

func (s *session) roomLoop(id int64, gate chan struct{}) {
	failures := 0
	for s.ctx.Err() == nil {
		started := time.Now()
		err := s.roomAttempt(id, gate)
		if s.ctx.Err() != nil {
			return
		}
		if time.Since(started) >= s.w.time.stable {
			failures = 0
		}
		delay := retryDelay(s.w.time.roomRetry, time.Minute, failures)
		failures = min(failures+1, 20)
		s.w.update(func(st *Stats) { st.RelayFailures++; st.LastRelayFailure = err.Error() })
		s.w.log.Warn("live room retry", "room", id, "error", err, "delay", delay)
		if !pause(s.ctx, delay) {
			return
		}
	}
}

type liveConfig struct {
	Code *int `json:"code"`
	Data struct {
		Token string `json:"token"`
		Hosts []struct {
			Host string `json:"host"`
			Port int    `json:"wss_port"`
		} `json:"host_list"`
	} `json:"data"`
}

func (s *session) getLiveConfig(id int64, gate chan struct{}) (address, key string, err error) {
	select {
	case <-s.ctx.Done():
		return "", "", context.Cause(s.ctx)
	case <-gate:
	}
	delay := s.w.time.roomRetry
	defer func() {
		if err != nil {
			delay = s.w.time.cooldown
		}
		pause(s.ctx, delay)
		gate <- struct{}{}
	}()
	b, status, err := s.w.fetch.get(s.ctx, s.w.roomConfigURL(id), 256<<10)
	if err != nil {
		return "", "", err
	}
	var cfg liveConfig
	if json.Unmarshal(b, &cfg) != nil || status != http.StatusOK || cfg.Code == nil || *cfg.Code != 0 || cfg.Data.Token == "" {
		return "", "", errors.New("live config rejected or malformed")
	}
	for _, host := range cfg.Data.Hosts {
		if !s.w.allowLiveHost(host.Host) {
			continue
		}
		port := host.Port
		if port == 0 {
			port = 443
		}
		if port < 1 || port > 65535 {
			continue
		}
		return "wss://" + net.JoinHostPort(host.Host, strconv.Itoa(port)) + "/sub", cfg.Data.Token, nil
	}
	return "", "", errors.New("no allowed live host")
}

func (s *session) roomAttempt(id int64, gate chan struct{}) error {
	address, key, err := s.getLiveConfig(id, gate)
	if err != nil {
		return err
	}
	dialer := websocket.Dialer{Proxy: http.ProxyFromEnvironment, HandshakeTimeout: s.w.time.handshake}
	// Reuse the injected trust configuration only in local TLS integration tests.
	if tr, ok := s.w.fetch.client.Transport.(*http.Transport); ok {
		dialer.TLSClientConfig = tr.TLSClientConfig
	}
	conn, resp, err := dialer.DialContext(s.ctx, address, nil)
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(s.ctx)
	stopClose := context.AfterFunc(ctx, func() { conn.Close() })
	var heartbeat sync.WaitGroup
	defer func() { cancel(); conn.Close(); stopClose(); heartbeat.Wait() }()
	conn.SetReadLimit(maxBodyBytes)
	_ = conn.SetReadDeadline(time.Now().Add(s.w.time.handshake))
	write := func(op uint32, body []byte) error {
		if err := conn.SetWriteDeadline(time.Now().Add(s.w.time.write)); err != nil {
			return err
		}
		return conn.WriteMessage(websocket.BinaryMessage, encodePacket(op, body))
	}
	auth, _ := json.Marshal(map[string]any{"uid": 0, "roomid": id, "protover": 3, "platform": "web", "type": 2, "key": key})
	if err = write(7, auth); err != nil {
		return err
	}
	live := false
	defer func() {
		if live {
			s.w.update(func(st *Stats) { st.Live-- })
		}
	}()
	for {
		kind, b, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		if kind != websocket.BinaryMessage {
			return errors.New("expected binary live packet")
		}
		packets, err := decodePackets(b)
		if err != nil {
			return err
		}
		for _, p := range packets {
			switch p.op {
			case 8:
				var auth struct {
					Code *int `json:"code"`
				}
				if json.Unmarshal(p.body, &auth) != nil || auth.Code == nil || *auth.Code != 0 {
					return errors.New("live authentication denied")
				}
				if live {
					continue
				}
				live = true
				s.w.update(func(st *Stats) { st.Live++ })
				_ = conn.SetReadDeadline(time.Now().Add(s.w.time.roomTimeout))
				heartbeat.Add(1)
				go func() {
					defer heartbeat.Done()
					for {
						if write(2, nil) != nil {
							cancel()
							return
						}
						if !pause(ctx, s.w.time.roomHeartbeat) {
							return
						}
					}
				}()
			case 3:
				if !live {
					continue
				}
				_ = conn.SetReadDeadline(time.Now().Add(s.w.time.roomTimeout))
				if err = s.forward(&relayEvent{RoomID: id, Event: "heartbeat", Data: binary.BigEndian.Uint32(p.body)}); err != nil {
					return err
				}
			case 5:
				if live {
					if event := mapEvent(id, p.body); event != nil {
						if err = s.forward(event); err != nil {
							return err
						}
					}
				}
			}
		}
	}
}

func (s *session) forward(event *relayEvent) error {
	if err := s.send(map[string]any{"relay": event}); err != nil {
		return err
	}
	s.w.update(func(st *Stats) { st.Forwarded++ })
	return nil
}
