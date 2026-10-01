// Core is the Ballet system of record: tenancy, tracker, scheduler, orchestrator, planner and APIs.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/denyszorinets/ballet/core/internal/transport/httpapi"
	"github.com/denyszorinets/ballet/kit/auth/oidc"
	"github.com/denyszorinets/ballet/kit/auth/runtoken"
	"github.com/denyszorinets/ballet/kit/config"
	"github.com/denyszorinets/ballet/kit/service"
)

const (
	serviceName = "core"
	envPrefix   = "BALLET_CORE"
)

// serviceConfig is the complete core configuration.
type serviceConfig struct {
	service.Config
	OIDC   oidc.Config  `toml:"oidc"`
	Tokens tokensConfig `toml:"tokens"`
}

// tokensConfig configures run token signing (ADR-0006).
type tokensConfig struct {
	KeyFile string `toml:"key_file"`
}

func defaultConfig() serviceConfig {
	return serviceConfig{
		Config: service.DefaultConfig(":8080"),
		OIDC:   oidc.Config{Audience: "ballet"},
		Tokens: tokensConfig{KeyFile: "data/token-keys.json"},
	}
}

func (c serviceConfig) Validate() error {
	var errs []error
	if c.Tokens.KeyFile == "" {
		errs = append(errs, errors.New("tokens.key_file must not be empty"))
	}
	return errors.Join(append(errs, c.Config.Validate(), c.OIDC.Validate())...)
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

	verifier, err := oidc.NewVerifier(ctx, cfg.OIDC)
	if err != nil {
		return err
	}
	tokenKeys, err := runtoken.LoadKeyRing(cfg.Tokens.KeyFile, time.Now)
	if err != nil {
		return err
	}
	httpapi.Register(svc.Mux, httpapi.Deps{Verifier: verifier, TokenKeys: tokenKeys})

	return svc.ListenAndServe(ctx)
}
