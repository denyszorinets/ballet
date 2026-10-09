// Knowledge is the Ballet knowledge service: organization-scoped knowledge, search and MCP.
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
	"github.com/denyszorinets/ballet/kit/embed"
	"github.com/denyszorinets/ballet/kit/health"
	"github.com/denyszorinets/ballet/kit/service"
	"github.com/denyszorinets/ballet/knowledge/internal/app"
	"github.com/denyszorinets/ballet/knowledge/internal/store"
	"github.com/denyszorinets/ballet/knowledge/internal/transport/httpapi"
	"github.com/denyszorinets/ballet/knowledge/internal/transport/mcpapi"
)

const (
	serviceName = "knowledge"
	envPrefix   = "BALLET_KNOWLEDGE"
)

// serviceConfig is the complete knowledge configuration.
type serviceConfig struct {
	service.Config
	Core       coreConfig       `toml:"core"`
	Storage    storageConfig    `toml:"storage"`
	Embeddings embeddingsConfig `toml:"embeddings"`
	Search     searchConfig     `toml:"search"`
}

// embeddingsConfig selects how entries are embedded for semantic search.
type embeddingsConfig struct {
	// Mode is "local" (hash embedder in-process) or "gateway" (LLM gateway
	// with the knowledge service token, attributed to each organization).
	Mode       string `toml:"mode"`
	Model      string `toml:"model"`
	GatewayURL string `toml:"gateway_url"`
	TokenFile  string `toml:"token_file"`
}

// searchConfig tunes hybrid search.
type searchConfig struct {
	// MaxDistance drops semantic matches farther than this cosine distance.
	MaxDistance float64 `toml:"max_distance"`
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
		Embeddings: embeddingsConfig{
			Mode: "local", Model: embed.HashModel, GatewayURL: "http://localhost:8082",
			TokenFile: "data/service-tokens/knowledge.token",
		},
		Search: searchConfig{MaxDistance: app.DefaultMaxDistance},
	}
}

func (c serviceConfig) Validate() error {
	var errs []error
	if c.Core.URL == "" || c.Storage.Path == "" {
		errs = append(errs, errors.New("core.url and storage.path must be set"))
	}
	if c.Embeddings.Mode != "local" && c.Embeddings.Mode != "gateway" {
		errs = append(errs, errors.New(`embeddings.mode must be "local" or "gateway"`))
	}
	if c.Embeddings.Mode == "local" && c.Embeddings.Model != embed.HashModel {
		errs = append(errs, errors.New("embeddings.mode local supports only model "+embed.HashModel))
	}
	if c.Search.MaxDistance <= 0 || c.Search.MaxDistance > 2 {
		errs = append(errs, errors.New("search.max_distance must be in (0, 2]"))
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
	var embedders app.Embedders = app.LocalEmbedders{Embedder: embed.Hash{}}
	if cfg.Embeddings.Mode == "gateway" {
		embedders = app.GatewayEmbedders{
			URL: cfg.Embeddings.GatewayURL, Token: runtoken.FileSource(cfg.Embeddings.TokenFile), Model: cfg.Embeddings.Model,
		}
	}
	indexer := &app.Indexer{Store: st, Embedders: embedders, Model: cfg.Embeddings.Model, Logger: svc.Logger}
	go indexer.Run(ctx)
	knowledge := &app.Service{
		Store: st, Searcher: st, Embedders: embedders, Indexer: indexer, MaxDistance: cfg.Search.MaxDistance,
		Now: time.Now, NewID: func() string { return uuid.Must(uuid.NewV7()).String() },
	}
	httpapi.Register(svc.Mux, verifier, knowledge)
	mcpapi.Register(svc.Mux, verifier, knowledge, "v1")
	return svc.ListenAndServe(ctx)
}
