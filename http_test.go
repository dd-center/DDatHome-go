package main

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func allowHTTP(f *fetcher, raw string) { u, _ := url.Parse(raw); f.allowed[u.Host] = true }
func TestHTTPResponseValidation(t *testing.T) {
	for _, test := range []struct {
		name, body     string
		status         int
		valid, limited bool
	}{
		{"ok", `{"code":0,"data":{"x":1}}`, 200, true, false},
		{"html", "<html>failure</html>", 200, false, false},
		{"null", "null", 200, false, false},
		{"wrong code", `{"code":"0"}`, 200, false, false},
		{"HTTP error", `{"code":0}`, 500, false, false},
		{"rate limit", `{"code":0}`, 429, false, true},
		{"API limit", `{"code":-352}`, 200, false, true},
		{"API error", `{"code":-400}`, 200, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("User-Agent") != userAgent || r.Header.Get("Cookie") != "_uuid=;rpdid=" {
					t.Error("missing compatibility headers")
				}
				w.WriteHeader(test.status)
				io.WriteString(w, test.body)
			}))
			defer srv.Close()
			f := newFetcher(defaultConfig())
			defer f.client.CloseIdleConnections()
			allowHTTP(f, srv.URL)
			r := f.task(context.Background(), srv.URL)
			if r.Valid != test.valid || r.Limited != test.limited || r.Text == "" {
				t.Fatalf("%+v", r)
			}
			if !test.valid && strings.Contains(r.Text, `"code":0`) {
				t.Fatalf("false success: %+v", r)
			}
		})
	}
}

func TestHTTPTimeoutIncludesHeadersAndBody(t *testing.T) {
	for _, flush := range []bool{false, true} {
		t.Run(map[bool]string{false: "headers", true: "body"}[flush], func(t *testing.T) {
			cancelled := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if flush {
					io.WriteString(w, `{"code":`)
					w.(http.Flusher).Flush()
				}
				<-r.Context().Done()
				close(cancelled)
			}))
			defer srv.Close()
			f := newFetcher(defaultConfig())
			defer f.client.CloseIdleConnections()
			allowHTTP(f, srv.URL)
			f.timeout = 80 * time.Millisecond
			start := time.Now()
			r := f.task(context.Background(), srv.URL)
			if r.Err == nil || time.Since(start) > time.Second {
				t.Fatal("timeout did not bound request", r)
			}
			select {
			case <-cancelled:
			case <-time.After(time.Second):
				t.Fatal("request not cancelled")
			}
		})
	}
}

func TestHTTPGzipAndLimits(t *testing.T) {
	for _, mode := range []string{"valid", "corrupt", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Encoding", "gzip")
				if mode == "corrupt" {
					io.WriteString(w, "broken")
					return
				}
				g := gzip.NewWriter(w)
				defer g.Close()
				if mode == "oversized" {
					io.WriteString(g, strings.Repeat("x", maxBodyBytes+1))
				} else {
					io.WriteString(g, `{"code":0}`)
				}
			}))
			defer srv.Close()
			f := newFetcher(defaultConfig())
			defer f.client.CloseIdleConnections()
			allowHTTP(f, srv.URL)
			r := f.task(context.Background(), srv.URL)
			if r.Valid != (mode == "valid") {
				t.Fatal(r)
			}
		})
	}
}

func TestHTTPRejectsRedirectAndForeignURL(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("followed redirect") }))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer srv.Close()
	f := newFetcher(defaultConfig())
	defer f.client.CloseIdleConnections()
	allowHTTP(f, srv.URL)
	if r := f.task(context.Background(), srv.URL); r.Valid || r.Err == nil {
		t.Fatal(r)
	}
	for _, u := range []string{"file:///etc/passwd", "http://localhost/", "https://api.bilibili.com.evil.test/", "https://name:pass@api.bilibili.com/"} {
		if r := f.task(context.Background(), u); r.Err == nil {
			t.Fatal(u)
		}
	}
}
