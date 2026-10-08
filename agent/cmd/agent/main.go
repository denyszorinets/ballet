// Agent runs Ballet's coding-agent sessions (ADR-0025): a long-lived
// process in a container or VM that connects to Core, receives runs and
// executes them as OS processes.
package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/denyszorinets/ballet/agent/internal/driver"
	"github.com/denyszorinets/ballet/agent/internal/driver/claudecode"
	"github.com/denyszorinets/ballet/agent/internal/link"
	"github.com/denyszorinets/ballet/agent/internal/process"
	"github.com/denyszorinets/ballet/kit/config"
	"github.com/denyszorinets/ballet/kit/runnerproto"
	"github.com/denyszorinets/ballet/kit/service"
)

const (
	serviceName = "agent"
	envPrefix   = "BALLET_AGENT"
)

// serviceConfig is the complete agent configuration.
type serviceConfig struct {
	service.Config
	Core    coreConfig    `toml:"core"`
	Agent   agentConfig   `toml:"agent"`
	Session sessionConfig `toml:"session"`
	Drivers driversConfig `toml:"drivers"`
}

// driversConfig configures the coding-agent runtimes.
type driversConfig struct {
	ClaudeCommand string `toml:"claude_command"` // the claude executable
}

// coreConfig locates Core and the agent token.
type coreConfig struct {
	URL       string `toml:"url"`
	TokenFile string `toml:"token_file"`
}

// agentConfig describes this agent.
type agentConfig struct {
	Name     string   `toml:"name"` // "": the host name plus a random suffix
	Capacity int      `toml:"capacity"`
	Labels   []string `toml:"labels"` // key=value
}

// sessionConfig configures how sessions run (ADR-0025).
type sessionConfig struct {
	User           string        `toml:"user"`            // "": the agent's own user
	WorkDir        string        `toml:"work_dir"`        // "": the OS temp dir
	KeepWorkspaces bool          `toml:"keep_workspaces"` // for debugging
	DefaultTimeout time.Duration `toml:"default_timeout"`
}

func defaultConfig() serviceConfig {
	return serviceConfig{
		Config:  service.DefaultConfig(":8083"),
		Core:    coreConfig{URL: "http://localhost:8080", TokenFile: "data/service-tokens/agent.token"},
		Agent:   agentConfig{Capacity: 1},
		Session: sessionConfig{DefaultTimeout: 2 * time.Hour},
		Drivers: driversConfig{ClaudeCommand: "claude"},
	}
}

// defaultName is the host name plus a random suffix: replicas sharing a
// host name (host networking) still get distinct names.
func defaultName() string {
	host, _ := os.Hostname()
	if host == "" {
		host = "agent"
	}
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s-%x", host, b)
}

func (c serviceConfig) Validate() error {
	var errs []error
	if u, err := url.Parse(c.Core.URL); err != nil || u.Host == "" {
		errs = append(errs, fmt.Errorf("core.url: invalid URL %q", c.Core.URL))
	}
	if c.Core.TokenFile == "" {
		errs = append(errs, errors.New("core.token_file must be set"))
	}
	if c.Agent.Capacity < 1 {
		errs = append(errs, errors.New("agent.capacity must be at least 1"))
	}
	if _, err := c.Agent.labels(); err != nil {
		errs = append(errs, err)
	}
	if c.Session.DefaultTimeout < time.Minute {
		errs = append(errs, errors.New("session.default_timeout must be at least 1m"))
	}
	return errors.Join(append(errs, c.Config.Validate())...)
}

func (a agentConfig) labels() (map[string]string, error) {
	out := map[string]string{}
	for _, l := range a.Labels {
		k, v, ok := strings.Cut(l, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("agent.labels: %q is not key=value", l)
		}
		out[k] = v
	}
	return out, nil
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
	if cfg.Agent.Name == "" {
		cfg.Agent.Name = defaultName()
	}
	labels, _ := cfg.Agent.labels()

	svc, err := service.New(serviceName, cfg.Config, os.Stdout)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if cfg.Session.User == "" && os.Getuid() == 0 {
		svc.Logger.Warn("sessions run as root with the agent's privileges; set session.user")
	}
	backend := &process.Backend{WorkRoot: cfg.Session.WorkDir, Keep: cfg.Session.KeepWorkspaces, User: cfg.Session.User,
		Drivers: map[string]driver.Driver{
			claudecode.Name: claudecode.Driver{Command: cfg.Drivers.ClaudeCommand},
		}}

	r := &link.Agent{
		URL: "ws" + strings.TrimPrefix(strings.TrimSuffix(cfg.Core.URL, "/"), "http") + runnerproto.Path,
		Token: func(context.Context) (string, error) {
			// Read on every connection: Core rotates the token file.
			data, err := os.ReadFile(cfg.Core.TokenFile)
			return strings.TrimSpace(string(data)), err
		},
		Name: cfg.Agent.Name, Labels: labels, Capacity: cfg.Agent.Capacity, Backend: backend,
		DefaultTimeout: cfg.Session.DefaultTimeout, Logger: svc.Logger,
	}
	go func() {
		if err := r.Run(ctx); err != nil {
			svc.Logger.ErrorContext(ctx, "agent stopped", "error", err)
			stop()
		}
	}()
	return svc.ListenAndServe(ctx)
}
