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

	"github.com/denyszorinets/ballet/core/internal/transport/httpapi"
	"github.com/denyszorinets/ballet/kit/auth/oidc"
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
	OIDC oidc.Config `toml:"oidc"`
}

func defaultConfig() serviceConfig {
	return serviceConfig{
		Config: service.DefaultConfig(":8080"),
		OIDC:   oidc.Config{Audience: "ballet"},
	}
}

func (c serviceConfig) Validate() error {
	return errors.Join(c.Config.Validate(), c.OIDC.Validate())
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
	httpapi.Register(svc.Mux, httpapi.Deps{Verifier: verifier})

	return svc.ListenAndServe(ctx)
}
