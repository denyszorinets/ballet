// Core is the Ballet system of record: tenancy, tracker, scheduler, orchestrator, planner and APIs.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/app/plannertools"
	"github.com/denyszorinets/ballet/core/internal/domain/rbac"
	"github.com/denyszorinets/ballet/core/internal/infra/anthropic"
	"github.com/denyszorinets/ballet/core/internal/infra/knowledge"
	"github.com/denyszorinets/ballet/core/internal/infra/secrets"
	"github.com/denyszorinets/ballet/core/internal/infra/servicetokens"
	"github.com/denyszorinets/ballet/core/internal/infra/store"
	"github.com/denyszorinets/ballet/core/internal/transport/httpapi"
	"github.com/denyszorinets/ballet/core/internal/transport/internalapi"
	"github.com/denyszorinets/ballet/core/internal/transport/realtime"
	"github.com/denyszorinets/ballet/core/internal/transport/webui"
	"github.com/denyszorinets/ballet/kit/auth/oidc"
	"github.com/denyszorinets/ballet/kit/auth/runtoken"
	"github.com/denyszorinets/ballet/kit/config"
	"github.com/denyszorinets/ballet/kit/embed"
	"github.com/denyszorinets/ballet/kit/health"
	"github.com/denyszorinets/ballet/kit/rpc"
	"github.com/denyszorinets/ballet/kit/service"
)

const (
	serviceName = "core"
	envPrefix   = "BALLET_CORE"
)

// serviceConfig is the complete core configuration.
type serviceConfig struct {
	service.Config
	OIDC      oidc.Config     `toml:"oidc"`
	Tokens    tokensConfig    `toml:"tokens"`
	Storage   storageConfig   `toml:"storage"`
	RBAC      rbacConfig      `toml:"rbac"`
	Web       webConfig       `toml:"web"`
	Services  servicesConfig  `toml:"services"`
	Secrets   secretsConfig   `toml:"secrets"`
	Knowledge knowledgeConfig `toml:"knowledge"`
	Gateway   gatewayConfig   `toml:"gateway"`
	Planner   plannerConfig   `toml:"planner"`
}

// gatewayConfig locates the LLM gateway (ADR-0011), used by the planner.
type gatewayConfig struct {
	URL string `toml:"url"`
}

// plannerConfig configures the planner agent (ADR-0020).
type plannerConfig struct {
	Model     string `toml:"model"`
	MaxTokens int    `toml:"max_tokens"` // per model response
	MaxRounds int    `toml:"max_rounds"` // tool rounds per turn
	Skill     string `toml:"skill"`      // project skill appended to the instructions
}

// knowledgeConfig locates the Knowledge service (ADR-0022).
type knowledgeConfig struct {
	URL string `toml:"url"`
}

// secretsConfig locates the key that encrypts secrets at rest.
type secretsConfig struct {
	KeyFile string `toml:"key_file"`
}

// servicesConfig configures the identities of Ballet's own services.
type servicesConfig struct {
	TokensDir string        `toml:"tokens_dir"` // where <service>.token files are written
	TokenTTL  time.Duration `toml:"token_ttl"`
}

// webConfig configures the web UI.
type webConfig struct {
	ClientID string `toml:"client_id"` // public OIDC client of the SPA
	Dir      string `toml:"dir"`       // built SPA to serve; empty: not served
}

// rbacConfig configures authorization.
type rbacConfig struct {
	// BootstrapOrgAdmins are "claim:value" matchers granted org-admin
	// regardless of stored bindings, e.g. "groups:ballet-admins".
	BootstrapOrgAdmins []string `toml:"bootstrap_org_admins"`
}

func (c rbacConfig) bindings() ([]rbac.Binding, error) {
	var out []rbac.Binding
	for _, s := range c.BootstrapOrgAdmins {
		b, err := rbac.ParseBootstrap(s)
		if err != nil {
			return nil, fmt.Errorf("rbac.bootstrap_org_admins: %w", err)
		}
		out = append(out, b)
	}
	return out, nil
}

// storageConfig locates Core's database.
type storageConfig struct {
	Path string `toml:"path"`
}

// tokensConfig configures run token signing (ADR-0006).
type tokensConfig struct {
	KeyFile string `toml:"key_file"`
}

func defaultConfig() serviceConfig {
	return serviceConfig{
		Config:    service.DefaultConfig(":8080"),
		OIDC:      oidc.Config{Audience: "ballet"},
		Tokens:    tokensConfig{KeyFile: "data/token-keys.json"},
		Storage:   storageConfig{Path: "data/core.db"},
		Web:       webConfig{ClientID: "ballet-web"},
		Services:  servicesConfig{TokensDir: "data/service-tokens", TokenTTL: 30 * 24 * time.Hour},
		Secrets:   secretsConfig{KeyFile: "data/secrets.key"},
		Knowledge: knowledgeConfig{URL: "http://localhost:8081"},
		Gateway:   gatewayConfig{URL: "http://localhost:8082"},
		Planner:   plannerConfig{Model: "claude-sonnet-5-5", MaxTokens: 8192, MaxRounds: 20, Skill: "planner"},
	}
}

func (c serviceConfig) Validate() error {
	var errs []error
	if c.Tokens.KeyFile == "" {
		errs = append(errs, errors.New("tokens.key_file must not be empty"))
	}
	if c.Services.TokensDir == "" || c.Services.TokenTTL < time.Hour {
		errs = append(errs, errors.New("services.tokens_dir must be set and services.token_ttl at least 1h"))
	}
	if c.Storage.Path == "" {
		errs = append(errs, errors.New("storage.path must not be empty"))
	}
	if c.Planner.Model == "" || c.Planner.MaxTokens < 1 || c.Planner.MaxRounds < 1 {
		errs = append(errs, errors.New("planner.model must be set; planner.max_tokens and planner.max_rounds at least 1"))
	}
	if u, err := url.Parse(c.Gateway.URL); err != nil || u.Host == "" {
		errs = append(errs, fmt.Errorf("gateway.url: invalid URL %q", c.Gateway.URL))
	}
	if _, err := c.RBAC.bindings(); err != nil {
		errs = append(errs, err)
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

	if err := os.MkdirAll(filepath.Dir(cfg.Storage.Path), 0o700); err != nil {
		return fmt.Errorf("create storage directory: %w", err)
	}
	st, err := store.Open(ctx, cfg.Storage.Path)
	if err != nil {
		return err
	}
	defer st.Close()
	svc.AddReadinessCheck(health.Check{Name: "database", Func: st.DB().Ping})

	verifier, err := oidc.NewVerifier(ctx, cfg.OIDC)
	if err != nil {
		return err
	}
	tokenKeys, err := runtoken.LoadKeyRing(cfg.Tokens.KeyFile, time.Now)
	if err != nil {
		return err
	}
	tokenIssuer := runtoken.NewIssuer(tokenKeys, time.Now)
	svcTokens := &servicetokens.Issuer{
		Tokens: tokenIssuer, Dir: cfg.Services.TokensDir, TTL: cfg.Services.TokenTTL, Logger: svc.Logger,
	}
	if err := svcTokens.IssueAll(); err != nil {
		return err
	}
	go svcTokens.Run(ctx)
	internalAPI := internalapi.Register(svc.Mux, runtoken.NewRingVerifier(tokenKeys, time.Now))
	box, err := secrets.LoadKey(cfg.Secrets.KeyFile)
	if err != nil {
		return err
	}

	bootstrap, _ := cfg.RBAC.bindings() // validated with the configuration
	if len(bootstrap) == 0 {
		svc.Logger.WarnContext(ctx, "no rbac.bootstrap_org_admins configured; only stored role bindings grant access")
	}
	authz := &app.RBAC{Store: st, Bootstrap: bootstrap}
	knowledgeURL, err := url.Parse(cfg.Knowledge.URL)
	if err != nil || knowledgeURL.Host == "" {
		return fmt.Errorf("knowledge.url: invalid URL %q", cfg.Knowledge.URL)
	}
	credentials := &app.Credentials{Store: st, Tenancy: st, Authz: authz, Box: box, Now: time.Now, NewID: store.NewID}
	internalapi.RegisterCredentials(internalAPI, credentials)
	usage := &app.Usage{Store: st, Tenancy: st, Authz: authz}
	internalapi.RegisterUsage(internalAPI, usage)
	tracker := &app.Tracker{
		Items: st, Deps: st, Tenancy: st, Events: st, Authz: authz, Now: time.Now, NewID: store.NewID,
	}
	skills := &app.Skills{Store: st, Tenancy: st, Authz: authz, Now: time.Now, NewID: store.NewID}
	search := &app.Search{Store: st, Tenancy: st, Authz: authz, Embedder: embed.Hash{}}
	knowledgeAccess := &app.KnowledgeAccess{Tenancy: st, Authz: authz}
	changesets := &app.Changesets{Store: st, Tracker: tracker}
	plannerSvc := &app.Planner{
		Store: st, Tenancy: st, Authz: authz,
		LLM: &anthropic.Client{GatewayURL: cfg.Gateway.URL, Tokens: tokenIssuer},
		Tools: plannertools.All(plannertools.Deps{
			Tracker: tracker, Changesets: changesets, Skills: skills, Search: search,
			Knowledge: &knowledge.Client{URL: knowledgeURL, Access: knowledgeAccess, Tokens: tokenIssuer},
		}),
		Instructions: func(ctx context.Context, projectKey string) (string, error) {
			return skills.ProjectSkillBody(ctx, projectKey, cfg.Planner.Skill)
		},
		Model: cfg.Planner.Model, MaxTokens: cfg.Planner.MaxTokens, MaxRounds: cfg.Planner.MaxRounds,
		Now: time.Now, NewID: store.NewID, Logger: svc.Logger, Context: ctx,
	}
	httpapi.Register(svc.Mux, httpapi.Deps{
		Authenticate: oidc.Middleware(verifier),
		TokenKeys:    tokenKeys,
		Tenancy:      &app.Tenancy{Store: st, Authz: authz, Now: time.Now, NewID: store.NewID},
		RBAC:         authz,
		RoleBindings: &app.RoleBindings{RBAC: authz, Tenancy: st, Now: time.Now, NewID: store.NewID},
		Credentials:  credentials,
		Usage:        usage,
		Skills:       skills,
		Search:       search,
		Knowledge: &httpapi.KnowledgeProxy{
			URL: knowledgeURL, Access: knowledgeAccess, Tokens: tokenIssuer,
		},
		Tracker:    tracker,
		Changesets: changesets,
		Planner:    plannerSvc,
	})

	searchIndexer := &app.SearchIndexer{
		Store: st, Log: st, Items: st, Skills: st, Tenancy: st, Embedder: embed.Hash{}, Logger: svc.Logger,
	}
	go searchIndexer.Run(ctx)

	feed := &app.Feed{Log: st, Logger: svc.Logger}
	go func() {
		if err := feed.Run(ctx); err != nil {
			svc.Logger.ErrorContext(ctx, "event feed stopped", "error", err)
		}
	}()
	realtime.Register(svc.Mux, realtime.Deps{
		Verifier: verifier,
		Streams:  &app.Streams{Feed: feed, Log: st, Items: st, Tenancy: st, Authz: authz},
		Planner:  plannerSvc,
		Now:      time.Now,
		Options:  rpc.Options{Logger: svc.Logger},
	})

	webui.Register(svc.Mux, webui.Config{OIDCIssuer: cfg.OIDC.IssuerURL, ClientID: cfg.Web.ClientID, Dir: cfg.Web.Dir})

	return svc.ListenAndServe(ctx)
}
