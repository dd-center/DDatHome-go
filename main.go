package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/kardianos/service"
)

type program struct {
	worker *Worker
	cancel context.CancelFunc
	done   chan struct{}
}

func (p *program) Start(service.Service) error {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.done = make(chan struct{})
	var server *http.Server
	var listener net.Listener
	if p.worker.config.StatusAddress != "" {
		var err error
		listener, err = net.Listen("tcp", p.worker.config.StatusAddress)
		if err != nil {
			cancel()
			close(p.done)
			return fmt.Errorf("status listener: %w", err)
		}
		server = &http.Server{Handler: p.worker.statusHandler(), ReadHeaderTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second, IdleTimeout: 30 * time.Second}
	}
	go func() {
		defer close(p.done)
		serverDone := make(chan struct{})
		if server != nil {
			go func() {
				defer close(serverDone)
				if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
					p.worker.log.Error("status server failed", "error", err)
					cancel()
				}
			}()
		} else {
			close(serverDone)
		}
		p.worker.Run(ctx)
		if server != nil {
			server.Close()
		}
		<-serverDone
	}()
	return nil
}
func (p *program) Stop(service.Service) error {
	if p.cancel != nil {
		p.cancel()
		<-p.done
	}
	return nil
}

func (w *Worker) statusHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", func(rw http.ResponseWriter, r *http.Request) { rw.Write([]byte("ok\n")) })
	mux.HandleFunc("GET /healthz", func(rw http.ResponseWriter, r *http.Request) {
		if !w.snapshot().Ready {
			http.Error(rw, "scheduler not ready", http.StatusServiceUnavailable)
			return
		}
		rw.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /status", func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		rw.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(rw).Encode(struct {
			Stats
			Name    string `json:"name"`
			UUID    string `json:"uuid"`
			Version string `json:"version"`
		}{w.snapshot(), w.config.NickName, w.config.UUID, version})
	})
	return mux
}

func main() {
	if err := runCLI(os.Args[1:]); err != nil {
		slog.Error("DDatHome-go", "error", err)
		os.Exit(1)
	}
}
func runCLI(args []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet(programName, flag.ContinueOnError)
	configPath := flags.String("config", filepath.Join(filepath.Dir(exe), "config.json"), "configuration file (default: next to executable)")
	showVersion := flags.Bool("version", false, "print version and exit")
	check := flags.Bool("check-config", false, "validate configuration and persist missing identity, then exit")
	command := ""
	if len(args) > 0 {
		switch args[0] {
		case "install", "uninstall", "start", "stop", "restart":
			command = args[0]
			args = args[1:]
		}
	}
	if err = flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}
	if *showVersion {
		fmt.Printf("%s %s (%s %s/%s)\n", programName, version, runtime.Version(), runtime.GOOS, runtime.GOARCH)
		return nil
	}
	absolute, err := filepath.Abs(*configPath)
	if err != nil {
		return err
	}
	// Service control must still work when an existing config has been damaged.
	c := defaultConfig()
	if command == "" || command == "install" || *check {
		c, err = loadConfig(absolute)
		if err != nil {
			return err
		}
	}
	if *check {
		fmt.Printf("Configuration OK: %s\nName: %s\nUUID: %s\n", absolute, c.NickName, c.UUID)
		return nil
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	p := &program{worker: newWorker(c, log)}
	svc, err := service.New(p, serviceConfig(exe, absolute))
	if err != nil {
		return err
	}
	if command != "" {
		if err = service.Control(svc, command); err != nil {
			return err
		}
		fmt.Println("Service", command, "successful")
		return nil
	}
	log.Info("starting", "version", version, "go", runtime.Version(), "name", c.NickName, "uuid", c.UUID, "config", absolute, "roomLimit", c.RoomLimit)
	return svc.Run()
}

func serviceConfig(exe, configPath string) *service.Config {
	return &service.Config{Name: programName, DisplayName: "DD@Home", Description: "DD@Home Go worker", Executable: exe, Arguments: []string{"--config", configPath}, WorkingDirectory: filepath.Dir(configPath)}
}
