package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestServiceUsesExplicitConfigPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	exe := filepath.Join(dir, "DDatHome-go")
	s := serviceConfig(exe, path)
	if s.Executable != exe || s.WorkingDirectory != dir || len(s.Arguments) != 2 || s.Arguments[0] != "--config" || s.Arguments[1] != path {
		t.Fatalf("service would lose config: %+v", s)
	}
}
func TestProgramStopCancelsConnectionRetry(t *testing.T) {
	w := testWorker(t)
	w.config.URL = "ws://127.0.0.1:1/"
	w.config.StatusAddress = "127.0.0.1:0"
	p := &program{worker: w}
	if err := p.Start(nil); err != nil {
		t.Fatal(err)
	}
	if err := p.Stop(nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.done:
	default:
		t.Fatal("service did not stop")
	}
}
func TestWriteQueueTimeoutCancelsSession(t *testing.T) {
	w := testWorker(t)
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(context.Canceled)
	s := &session{w: w, ctx: ctx, cancel: cancel, writeGate: make(chan struct{}, 1)}
	s.writeGate <- struct{}{}
	start := time.Now()
	if err := s.send("test"); err == nil {
		t.Fatal("unbounded write queue")
	}
	if !errors.Is(ctx.Err(), context.Canceled) || time.Since(start) > time.Second {
		t.Fatal("write deadline did not cancel")
	}
}
