package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const maxBodyBytes = 2 << 20
const userAgent = "Mozilla/5.0 (iPad; CPU OS 15_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/105.0.5195.100 Mobile/15E148 Safari/604.1"

type fetcher struct {
	client  *http.Client
	timeout time.Duration
	allowed map[string]bool
}
type fetchResult struct {
	Text    string
	Valid   bool
	Limited bool
	Err     error
}

func newFetcher(c Config) *fetcher {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.MaxIdleConns = 32
	t.MaxIdleConnsPerHost = c.Concurrency + 2
	t.MaxConnsPerHost = c.Concurrency + 2
	t.ResponseHeaderTimeout = time.Duration(c.HTTPTimeoutMS) * time.Millisecond
	return &fetcher{client: &http.Client{Transport: t, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, timeout: time.Duration(c.HTTPTimeoutMS) * time.Millisecond, allowed: map[string]bool{"api.bilibili.com": true, "api.live.bilibili.com": true}}
}

func (f *fetcher) get(ctx context.Context, raw string, limit int64) ([]byte, int, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || !f.allowed[u.Host] {
		return nil, 0, errors.New("task URL is not allowed")
	}
	ctx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Cookie", "_uuid=;rpdid=")
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.ContentLength > limit {
		return nil, resp.StatusCode, errors.New("HTTP body exceeds limit")
	}
	// net/http decompresses gzip; the limit applies to decompressed bytes too.
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err == nil && int64(len(b)) > limit {
		err = errors.New("HTTP body exceeds limit")
	}
	return b, resp.StatusCode, err
}

func failedData(err error) string {
	b, _ := json.Marshal(map[string]any{"code": 233, "message": err.Error()})
	return string(b)
}

func (f *fetcher) task(ctx context.Context, raw string) fetchResult {
	b, status, err := f.get(ctx, raw, maxBodyBytes)
	r := fetchResult{Limited: status == 403 || status == 412 || status == 429}
	if err != nil {
		r.Err = err
		r.Text = failedData(err)
		return r
	}
	var api struct {
		Code    *int   `json:"code"`
		Message string `json:"message"`
	}
	err = json.Unmarshal(b, &api)
	if err == nil && api.Code != nil {
		code := *api.Code
		r.Limited = r.Limited || code == -352 || code == -412 || code == -509
		if status >= 200 && status < 300 && code == 0 {
			r.Text = string(b)
			r.Valid = true
			return r
		}
		r.Err = fmt.Errorf("HTTP %d, API code %d: %.120s", status, code, api.Message)
		if code != 0 {
			r.Text = string(b)
			return r
		}
	} else {
		r.Err = fmt.Errorf("HTTP %d: missing numeric API code", status)
	}
	r.Text = failedData(r.Err)
	return r
}
