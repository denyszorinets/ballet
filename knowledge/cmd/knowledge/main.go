// Knowledge is the Ballet knowledge service: customer-scoped knowledge, search and MCP.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/denyszorinets/ballet/kit/config"
	"github.com/denyszorinets/ballet/kit/service"
)

const (
	serviceName = "knowledge"
	envPrefix   = "BALLET_KNOWLEDGE"
)

// serviceConfig is the complete knowledge configuration.
type serviceConfig struct {
	service.Config
}

func defaultConfig() serviceConfig {
	return serviceConfig{Config: service.DefaultConfig(":8081")}
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
	return svc.ListenAndServe(ctx)
}
