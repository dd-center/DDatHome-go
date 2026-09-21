package main

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestConfigMigrationAndIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	legacy := `{"NickName":"旧昵称","Interval":1280,"UUID":null,"UpstreamURL":"wss://cluster.vtbs.moe/?custom=keep","HidePlatformInfo":false}`
	if err := os.WriteFile(path, []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if a != b || !uuidPattern.MatchString(a.UUID) || a.NickName != "旧昵称" || a.Concurrency != 2 {
		t.Fatalf("migration: %+v %+v", a, b)
	}
	u, _ := url.Parse(a.upstreamURL())
	if u.Query().Get("custom") != "keep" || u.Query().Get("name") != "旧昵称" {
		t.Fatal(u)
	}
	a.Hide = true
	a.URL += "&runtime=leak&version=leak&platform=leak"
	u, _ = url.Parse(a.upstreamURL())
	for _, key := range []string{"runtime", "platform", "version"} {
		if u.Query().Has(key) {
			t.Fatal("hidden metadata leaked")
		}
	}
}

func TestConfigRejectsInvalidWithoutOverwriting(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `{`, `{} {}`, `{"Intervall":1}`, `{"Interval":0}`, `{"HTTPTimeoutMS":15000}`, `{"HTTPConcurrency":0}`, `{"UUID":"bad"}`, `{"RoomLimit":-1}`, `{"StatusAddress":"0.0.0.0:9465"}`, `{"UpstreamURL":"http://example.com"}`} {
		t.Run(raw, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			os.WriteFile(path, []byte(raw), 0600)
			if _, err := loadConfig(path); err == nil {
				t.Fatal("accepted invalid config")
			}
			b, _ := os.ReadFile(path)
			if string(b) != raw {
				t.Fatal("overwrote invalid file")
			}
		})
	}
}

func TestConfigFreshAndExplicitZero(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")
	c, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.UUID == "" || c.NickName == "" {
		t.Fatal(c)
	}
	c.RoomLimit = 0
	c.StatusAddress = ""
	b, _ := json.Marshal(c)
	os.WriteFile(path, b, 0600)
	got, err := loadConfig(path)
	if err != nil || got.RoomLimit != 0 || got.StatusAddress != "" {
		t.Fatal(got, err)
	}
}
