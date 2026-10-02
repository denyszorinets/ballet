// Gateway is the Ballet LLM gateway: credential injection and usage metering.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/denyszorinets/ballet/gateway/internal/core"
	"github.com/denyszorinets/ballet/gateway/internal/embeddings"
	"github.com/denyszorinets/ballet/gateway/internal/proxy"
	"github.com/denyszorinets/ballet/gateway/internal/usage"
	"github.com/denyszorinets/ballet/kit/auth/runtoken"
	"github.com/denyszorinets/ballet/kit/config"
	"github.com/denyszorinets/ballet/kit/embed"
	"github.com/denyszorinets/ballet/kit/service"
)

const (
	serviceName = "gateway"
	envPrefix   = "BALLET_GATEWAY"
)

// serviceConfig is the complete gateway configuration.
type serviceConfig struct {
	service.Config
	Core       coreConfig       `toml:"core"`
	Anthropic  anthropicConfig  `toml:"anthropic"`
	OpenAI     openAIConfig     `toml:"openai"`
	Embeddings embeddingsConfig `toml:"embeddings"`
}

// openAIConfig configures the OpenAI-compatible upstream (embeddings).
type openAIConfig struct {
	URL string `toml:"url"` // used when a credential has no base URL
}

// embeddingsConfig configures the embeddings endpoint.
type embeddingsConfig struct {
	DefaultModel string `toml:"default_model"`
}

// coreConfig locates Core and the gateway's service token.
type coreConfig struct {
	URL       string `toml:"url"`
	TokenFile string `toml:"token_file"`
}

// anthropicConfig configures the Anthropic upstream.
type anthropicConfig struct {
	URL string `toml:"url"` // used when a credential has no base URL
}

func defaultConfig() serviceConfig {
	return serviceConfig{
		Config:     service.DefaultConfig(":8082"),
		Core:       coreConfig{URL: "http://localhost:8080", TokenFile: "data/service-tokens/gateway.token"},
		Anthropic:  anthropicConfig{URL: "https://api.anthropic.com"},
		OpenAI:     openAIConfig{URL: "https://api.openai.com"},
		Embeddings: embeddingsConfig{DefaultModel: embed.HashModel},
	}
}

func (c serviceConfig) Validate() error {
	var errs []error
	if c.Core.URL == "" || c.Core.TokenFile == "" {
		errs = append(errs, errors.New("core.url and core.token_file must be set"))
	}
	if c.Anthropic.URL == "" {
		errs = append(errs, errors.New("anthropic.url must be set"))
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

	coreClient := &core.Client{BaseURL: cfg.Core.URL, Token: runtoken.FileSource(cfg.Core.TokenFile)}
	reporter := usage.NewReporter(coreClient, svc.Metrics, 2*time.Second, svc.Logger)
	go reporter.Run(ctx)
	verifier := runtoken.NewRemoteVerifier(cfg.Core.URL+"/.well-known/jwks.json", http.DefaultClient, time.Now)
	svc.Mux.Handle("/v1/embeddings", &embeddings.Handler{
		Verifier: verifier, Core: coreClient, DefaultModel: cfg.Embeddings.DefaultModel, OpenAIURL: cfg.OpenAI.URL,
		Sink: reporter.Sink, Logger: svc.Logger, Now: time.Now,
	})
	svc.Mux.Handle("/v1/", &proxy.Anthropic{
		Verifier:   verifier,
		Core:       coreClient,
		Budget:     coreClient,
		DefaultURL: cfg.Anthropic.URL,
		Logger:     svc.Logger,
		Observe:    usage.Observe(reporter.Sink, time.Now),
	})
	return svc.ListenAndServe(ctx)
}
