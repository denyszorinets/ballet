// Runner executes Ballet agent sessions (ADR-0009): it connects to Core,
// receives runs and executes them through a backend.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/denyszorinets/ballet/kit/config"
	"github.com/denyszorinets/ballet/kit/runnerproto"
	"github.com/denyszorinets/ballet/kit/service"
	"github.com/denyszorinets/ballet/runner/internal/backend/docker"
	"github.com/denyszorinets/ballet/runner/internal/backend/process"
	"github.com/denyszorinets/ballet/runner/internal/link"
)

const (
	serviceName = "runner"
	envPrefix   = "BALLET_RUNNER"
)

// serviceConfig is the complete runner configuration.
type serviceConfig struct {
	service.Config
	Core    coreConfig    `toml:"core"`
	Runner  runnerConfig  `toml:"runner"`
	Docker  dockerConfig  `toml:"docker"`
	Process processConfig `toml:"process"`
}

// dockerConfig configures the docker backend.
type dockerConfig struct {
	Host     string  `toml:"host"`      // unix:///…, tcp://… or http(s)://…
	CPUs     float64 `toml:"cpus"`      // per container; 0: unlimited
	MemoryMB int64   `toml:"memory_mb"` // per container; 0: unlimited
	Network  string  `toml:"network"`   // "": the engine default
}

// processConfig configures the development process backend (ADR-0023).
type processConfig struct {
	WorkDir        string `toml:"work_dir"`        // "": the OS temp dir
	KeepWorkspaces bool   `toml:"keep_workspaces"` // for debugging
}

// coreConfig locates Core and the runner token.
type coreConfig struct {
	URL       string `toml:"url"`
	TokenFile string `toml:"token_file"`
}

// runnerConfig describes this Runner.
type runnerConfig struct {
	Name           string        `toml:"name"`
	Capacity       int           `toml:"capacity"`
	Labels         []string      `toml:"labels"` // key=value
	Backend        string        `toml:"backend"`
	DefaultTimeout time.Duration `toml:"default_timeout"`
}

func defaultConfig() serviceConfig {
	host, _ := os.Hostname()
	return serviceConfig{
		Config: service.DefaultConfig(":8083"),
		Core:   coreConfig{URL: "http://localhost:8080", TokenFile: "data/service-tokens/runner.token"},
		Runner: runnerConfig{Name: host, Capacity: 2, Backend: "docker", DefaultTimeout: 2 * time.Hour},
		Docker: dockerConfig{Host: dockerHost()},
	}
}

// dockerHost is DOCKER_HOST, or the Docker socket.
func dockerHost() string {
	if h := os.Getenv("DOCKER_HOST"); h != "" {
		return h
	}
	return "unix:///var/run/docker.sock"
}

func (c serviceConfig) Validate() error {
	var errs []error
	if u, err := url.Parse(c.Core.URL); err != nil || u.Host == "" {
		errs = append(errs, fmt.Errorf("core.url: invalid URL %q", c.Core.URL))
	}
	if c.Core.TokenFile == "" {
		errs = append(errs, errors.New("core.token_file must be set"))
	}
	if c.Runner.Name == "" || c.Runner.Capacity < 1 {
		errs = append(errs, errors.New("runner.name must be set and runner.capacity at least 1"))
	}
	if _, err := c.Runner.labels(); err != nil {
		errs = append(errs, err)
	}
	if c.Docker.CPUs < 0 || c.Docker.MemoryMB < 0 {
		errs = append(errs, errors.New("docker.cpus and docker.memory_mb must not be negative"))
	}
	if c.Runner.DefaultTimeout < time.Minute {
		errs = append(errs, errors.New("runner.default_timeout must be at least 1m"))
	}
	return errors.Join(append(errs, c.Config.Validate())...)
}

func (r runnerConfig) labels() (map[string]string, error) {
	out := map[string]string{"backend": r.Backend}
	for _, l := range r.Labels {
		k, v, ok := strings.Cut(l, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("runner.labels: %q is not key=value", l)
		}
		out[k] = v
	}
	return out, nil
}

// backends maps runner.backend to an implementation.
var backends = map[string]func(ctx context.Context, cfg serviceConfig, logger *slog.Logger) (link.Backend, error){
	"docker": func(ctx context.Context, cfg serviceConfig, _ *slog.Logger) (link.Backend, error) {
		b, err := docker.New(cfg.Docker.Host)
		if err != nil {
			return nil, err
		}
		b.NanoCPUs = int64(cfg.Docker.CPUs * 1e9)
		b.MemoryBytes = cfg.Docker.MemoryMB << 20
		b.Network = cfg.Docker.Network
		pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := b.Ping(pingCtx); err != nil {
			return nil, fmt.Errorf("docker backend: engine at %s unreachable: %w", cfg.Docker.Host, err)
		}
		return b, nil
	},
	"process": func(_ context.Context, cfg serviceConfig, logger *slog.Logger) (link.Backend, error) {
		logger.Warn("process backend: runs execute on this host WITHOUT isolation; for development only (ADR-0023)")
		return &process.Backend{WorkRoot: cfg.Process.WorkDir, Keep: cfg.Process.KeepWorkspaces}, nil
	},
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", serviceName, err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", os.Getenv(envPrefix+"_CONFIG"), "path to the TOML configuration file")
	flag.Parse()

	cfg := defaultConfig()
	if err := config.Load(*configPath, envPrefix, &cfg); err != nil {
		return err
	}
	newBackend, ok := backends[cfg.Runner.Backend]
	if !ok {
		return fmt.Errorf("runner.backend: unknown backend %q (docker or process)", cfg.Runner.Backend)
	}
	labels, _ := cfg.Runner.labels()

	svc, err := service.New(serviceName, cfg.Config, os.Stdout)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	backend, err := newBackend(ctx, cfg, svc.Logger)
	if err != nil {
		return err
	}

	r := &link.Runner{
		URL: "ws" + strings.TrimPrefix(strings.TrimSuffix(cfg.Core.URL, "/"), "http") + runnerproto.Path,
		Token: func(context.Context) (string, error) {
			// Read on every connection: Core rotates the token file.
			data, err := os.ReadFile(cfg.Core.TokenFile)
			return strings.TrimSpace(string(data)), err
		},
		Name: cfg.Runner.Name, Labels: labels, Capacity: cfg.Runner.Capacity, Backend: backend,
		DefaultTimeout: cfg.Runner.DefaultTimeout, Logger: svc.Logger,
	}
	go func() {
		if err := r.Run(ctx); err != nil {
			svc.Logger.ErrorContext(ctx, "runner stopped", "error", err)
			stop()
		}
	}()
	return svc.ListenAndServe(ctx)
}
