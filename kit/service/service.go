// Package service wires the operational foundation every Ballet service
// shares: configuration, structured logging, Prometheus metrics, health and
// readiness endpoints, and HTTP serving with graceful shutdown.
package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/denyszorinets/ballet/kit/health"
	"github.com/denyszorinets/ballet/kit/logging"
	"github.com/denyszorinets/ballet/kit/server"
)

// Config holds the settings common to all services. Service configurations
// embed it so its keys appear at the top level of the TOML file.
type Config struct {
	Server ServerConfig `toml:"server"`
	Log    LogConfig    `toml:"log"`
}

// ServerConfig configures the HTTP listener.
type ServerConfig struct {
	Addr            string        `toml:"addr"`
	ShutdownTimeout time.Duration `toml:"shutdown_timeout"`
}

// LogConfig configures structured logging.
type LogConfig struct {
	Level string `toml:"level"`
}

// DefaultConfig returns the common defaults for a service listening on addr.
func DefaultConfig(addr string) Config {
	return Config{
		Server: ServerConfig{Addr: addr, ShutdownTimeout: 10 * time.Second},
		Log:    LogConfig{Level: "info"},
	}
}

// Validate checks the common settings.
func (c Config) Validate() error {
	var errs []error
	if c.Server.Addr == "" {
		errs = append(errs, errors.New("server.addr must not be empty"))
	}
	if c.Server.ShutdownTimeout <= 0 {
		errs = append(errs, errors.New("server.shutdown_timeout must be positive"))
	}
	if _, err := logging.ParseLevel(c.Log.Level); err != nil {
		errs = append(errs, fmt.Errorf("log.level: %w", err))
	}
	return errors.Join(errs...)
}

// Service is a running Ballet service. Register routes on Mux and metrics
// on Metrics before calling Serve.
type Service struct {
	Name    string
	Logger  *slog.Logger
	Metrics *prometheus.Registry
	Mux     *http.ServeMux

	cfg    Config
	checks []health.Check
}

// New creates a service that logs to logOut.
func New(name string, cfg Config, logOut io.Writer) (*Service, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}
	logger, err := logging.New(logOut, name, cfg.Log.Level)
	if err != nil {
		return nil, err
	}

	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		buildInfo(name),
	)

	s := &Service{Name: name, Logger: logger, Metrics: reg, Mux: http.NewServeMux(), cfg: cfg}
	s.Mux.Handle("GET /healthz", health.Handler(name))
	s.Mux.Handle("GET /readyz", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		health.Readiness(s.checks...).ServeHTTP(w, r)
	}))
	s.Mux.Handle("GET /metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg}))
	return s, nil
}

// AddReadinessCheck adds a check to the /readyz endpoint. Call before Serve.
func (s *Service) AddReadinessCheck(c health.Check) {
	s.checks = append(s.checks, c)
}

// ListenAndServe listens on the configured address and serves until ctx is
// cancelled.
func (s *Service) ListenAndServe(ctx context.Context) error {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", s.cfg.Server.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.cfg.Server.Addr, err)
	}
	return s.Serve(ctx, ln)
}

// Serve serves on ln until ctx is cancelled, then shuts down gracefully.
func (s *Service) Serve(ctx context.Context, ln net.Listener) error {
	s.Logger.InfoContext(ctx, "service started", "addr", ln.Addr().String())
	err := server.Serve(ctx, ln, s.Mux, s.cfg.Server.ShutdownTimeout)
	if err != nil {
		s.Logger.ErrorContext(ctx, "service failed", "error", err)
		return err
	}
	s.Logger.InfoContext(context.WithoutCancel(ctx), "service stopped")
	return nil
}

func buildInfo(service string) prometheus.Collector {
	version := "unknown"
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
		version = bi.Main.Version
	}
	g := prometheus.NewGauge(prometheus.GaugeOpts{
		Name:        "ballet_build_info",
		Help:        "Build information of the Ballet service; always 1.",
		ConstLabels: prometheus.Labels{"service": service, "version": version},
	})
	g.Set(1)
	return g
}
