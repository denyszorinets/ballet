// Knowledge is the Ballet knowledge service: customer-scoped knowledge, search and MCP.
package main

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"time"

	"flag"
	"fmt"
	"github.com/google/uuid"
	"os"
	"os/signal"
	"syscall"

	"github.com/denyszorinets/ballet/kit/auth/runtoken"
	"github.com/denyszorinets/ballet/kit/config"
	"github.com/denyszorinets/ballet/kit/health"
	"github.com/denyszorinets/ballet/kit/service"
	"github.com/denyszorinets/ballet/knowledge/internal/app"
	"github.com/denyszorinets/ballet/knowledge/internal/store"
	"github.com/denyszorinets/ballet/knowledge/internal/transport/httpapi"
)

const (
	serviceName = "knowledge"
	envPrefix   = "BALLET_KNOWLEDGE"
)

// serviceConfig is the complete knowledge configuration.
type serviceConfig struct {
	service.Config
	Core    coreConfig    `toml:"core"`
	Storage storageConfig `toml:"storage"`
}

// coreConfig locates Core (JWKS of the tokens Knowledge accepts).
type coreConfig struct {
	URL string `toml:"url"`
}

// storageConfig locates Knowledge's database.
type storageConfig struct {
	Path string `toml:"path"`
}

func defaultConfig() serviceConfig {
	return serviceConfig{
		Config:  service.DefaultConfig(":8081"),
		Core:    coreConfig{URL: "http://localhost:8080"},
		Storage: storageConfig{Path: "data/knowledge.db"},
	}
}

func (c serviceConfig) Validate() error {
	var errs []error
	if c.Core.URL == "" || c.Storage.Path == "" {
		errs = append(errs, errors.New("core.url and storage.path must be set"))
	}
	return errors.Join(append(errs, c.Config.Validate())...)
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

	svc, err := service.New(serviceName, cfg.Config, os.Stdout)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := os.MkdirAll(filepath.Dir(cfg.Storage.Path), 0o700); err != nil {
		return fmt.Errorf("create storage directory: %w", err)
	}
	st, err := store.Open(ctx, cfg.Storage.Path)
	if err != nil {
		return err
	}
	defer st.Close()
	svc.AddReadinessCheck(health.Check{Name: "database", Func: st.DB().Ping})

	verifier := runtoken.NewRemoteVerifier(cfg.Core.URL+"/.well-known/jwks.json", http.DefaultClient, time.Now)
	knowledge := &app.Service{Store: st, Now: time.Now, NewID: func() string { return uuid.Must(uuid.NewV7()).String() }}
	httpapi.Register(svc.Mux, verifier, knowledge)
	return svc.ListenAndServe(ctx)
}
