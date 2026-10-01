// Core is the Ballet system of record: tenancy, tracker, scheduler, orchestrator, planner and APIs.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/rbac"
	"github.com/denyszorinets/ballet/core/internal/infra/servicetokens"
	"github.com/denyszorinets/ballet/core/internal/infra/store"
	"github.com/denyszorinets/ballet/core/internal/transport/httpapi"
	"github.com/denyszorinets/ballet/core/internal/transport/internalapi"
	"github.com/denyszorinets/ballet/core/internal/transport/realtime"
	"github.com/denyszorinets/ballet/core/internal/transport/webui"
	"github.com/denyszorinets/ballet/kit/auth/oidc"
	"github.com/denyszorinets/ballet/kit/auth/runtoken"
	"github.com/denyszorinets/ballet/kit/config"
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
	OIDC     oidc.Config    `toml:"oidc"`
	Tokens   tokensConfig   `toml:"tokens"`
	Storage  storageConfig  `toml:"storage"`
	RBAC     rbacConfig     `toml:"rbac"`
	Web      webConfig      `toml:"web"`
	Services servicesConfig `toml:"services"`
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
		Config:   service.DefaultConfig(":8080"),
		OIDC:     oidc.Config{Audience: "ballet"},
		Tokens:   tokensConfig{KeyFile: "data/token-keys.json"},
		Storage:  storageConfig{Path: "data/core.db"},
		Web:      webConfig{ClientID: "ballet-web"},
		Services: servicesConfig{TokensDir: "data/service-tokens", TokenTTL: 30 * 24 * time.Hour},
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
	internalapi.Register(svc.Mux, runtoken.NewRingVerifier(tokenKeys, time.Now))

	bootstrap, _ := cfg.RBAC.bindings() // validated with the configuration
	if len(bootstrap) == 0 {
		svc.Logger.WarnContext(ctx, "no rbac.bootstrap_org_admins configured; only stored role bindings grant access")
	}
	authz := &app.RBAC{Store: st, Bootstrap: bootstrap}
	httpapi.Register(svc.Mux, httpapi.Deps{
		Authenticate: oidc.Middleware(verifier),
		TokenKeys:    tokenKeys,
		Tenancy:      &app.Tenancy{Store: st, Authz: authz, Now: time.Now, NewID: store.NewID},
		RBAC:         authz,
		RoleBindings: &app.RoleBindings{RBAC: authz, Tenancy: st, Now: time.Now, NewID: store.NewID},
		Tracker: &app.Tracker{
			Items: st, Deps: st, Tenancy: st, Events: st, Authz: authz, Now: time.Now, NewID: store.NewID,
		},
	})

	feed := &app.Feed{Log: st, Logger: svc.Logger}
	go func() {
		if err := feed.Run(ctx); err != nil {
			svc.Logger.ErrorContext(ctx, "event feed stopped", "error", err)
		}
	}()
	realtime.Register(svc.Mux, realtime.Deps{
		Verifier: verifier,
		Streams:  &app.Streams{Feed: feed, Log: st, Items: st, Tenancy: st, Authz: authz},
		Now:      time.Now,
		Options:  rpc.Options{Logger: svc.Logger},
	})

	webui.Register(svc.Mux, webui.Config{OIDCIssuer: cfg.OIDC.IssuerURL, ClientID: cfg.Web.ClientID, Dir: cfg.Web.Dir})

	return svc.ListenAndServe(ctx)
}
