package main

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

const programName = "DDatHome-go"

var version = "2.0.0-dev"

// Keep the v1 JSON names so existing config.json files continue to work.
type Config struct {
	NickName      string `json:"NickName"`
	Interval      int    `json:"Interval"`
	UUID          string `json:"UUID"`
	URL           string `json:"UpstreamURL"`
	Hide          bool   `json:"HidePlatformInfo"`
	Concurrency   int    `json:"HTTPConcurrency"`
	HTTPTimeoutMS int    `json:"HTTPTimeoutMS"`
	RoomLimit     int    `json:"RoomLimit"`
	StatusAddress string `json:"StatusAddress"`
	Verbose       bool   `json:"Verbose"`
}

func defaultConfig() Config {
	return Config{Interval: 1280, URL: "wss://cluster.vtbs.moe/", Concurrency: 2, HTTPTimeoutMS: 10000, RoomLimit: 5, StatusAddress: "127.0.0.1:9465"}
}

var uuidPattern = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)

func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b) // crypto/rand.Read always fills b or terminates the process.
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func (c Config) validate() error {
	u, err := url.Parse(c.URL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "ws" && u.Scheme != "wss") || u.User != nil || u.Fragment != "" {
		return errors.New("UpstreamURL must be a ws:// or wss:// URL without credentials or fragment")
	}
	if c.Interval < 100 || c.Interval > 60000 {
		return errors.New("Interval must be 100..60000 ms")
	}
	if c.Concurrency < 1 || c.Concurrency > 16 {
		return errors.New("HTTPConcurrency must be 1..16")
	}
	if c.HTTPTimeoutMS < 100 || c.HTTPTimeoutMS > 12000 {
		return errors.New("HTTPTimeoutMS must be 100..12000 (server deadline is 15 seconds)")
	}
	if c.RoomLimit < 0 || c.RoomLimit > 100 {
		return errors.New("RoomLimit must be 0..100")
	}
	if c.UUID != "" && !uuidPattern.MatchString(c.UUID) {
		return errors.New("UUID is invalid")
	}
	if len(c.NickName) > 256 {
		return errors.New("NickName must be at most 256 bytes")
	}
	if c.StatusAddress != "" {
		host, port, err := net.SplitHostPort(c.StatusAddress)
		if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
			return errors.New("StatusAddress must use a loopback IP, e.g. 127.0.0.1:9465, or be empty to disable")
		}
		if _, err := net.LookupPort("tcp", port); err != nil {
			return fmt.Errorf("StatusAddress: %w", err)
		}
	}
	return nil
}

func loadConfig(path string) (Config, error) {
	c := defaultConfig()
	data, err := os.ReadFile(path)
	missing := errors.Is(err, os.ErrNotExist)
	if err != nil && !missing {
		return c, err
	}
	if !missing {
		if len(data) > 65536 || !bytes.HasPrefix(bytes.TrimSpace(data), []byte("{")) {
			return c, errors.New("config must be a JSON object of at most 64 KiB")
		}
		d := json.NewDecoder(bytes.NewReader(data))
		d.DisallowUnknownFields()
		if err = d.Decode(&c); err != nil {
			return c, fmt.Errorf("config: %w", err)
		}
		var extra any
		if d.Decode(&extra) != io.EOF {
			return c, errors.New("config contains trailing JSON")
		}
	}
	if err = c.validate(); err != nil {
		return c, err
	}
	changed := missing
	if c.UUID == "" {
		c.UUID = newUUID()
		changed = true
	}
	if c.NickName == "" {
		c.NickName = "DD-Go-" + runtime.GOOS + "-" + runtime.GOARCH + "-" + c.UUID[:8]
		changed = true
	}
	if changed {
		data, err = json.MarshalIndent(c, "", "  ")
		if err == nil {
			err = writeAtomic(path, append(data, '\n'))
		}
		if err != nil {
			return c, fmt.Errorf("persist config/identity: %w", err)
		}
	}
	return c, nil
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".ddathome-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}

func (c Config) upstreamURL() string {
	u, _ := url.Parse(c.URL)
	q := u.Query()
	q.Set("name", c.NickName)
	q.Set("uuid", c.UUID)
	for _, key := range []string{"runtime", "version", "platform"} {
		q.Del(key)
	}
	if !c.Hide {
		q.Set("runtime", "go "+strings.TrimPrefix(runtime.Version(), "go"))
		q.Set("version", version)
		q.Set("platform", runtime.GOOS+"-"+runtime.GOARCH)
	}
	u.RawQuery = q.Encode()
	return u.String()
}
