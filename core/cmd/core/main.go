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
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/app/plannertools"
	"github.com/denyszorinets/ballet/core/internal/domain/agent"
	"github.com/denyszorinets/ballet/core/internal/domain/agent/claudecode"
	"github.com/denyszorinets/ballet/core/internal/domain/credential"
	"github.com/denyszorinets/ballet/core/internal/domain/execution"
	"github.com/denyszorinets/ballet/core/internal/domain/forge"
	"github.com/denyszorinets/ballet/core/internal/domain/rbac"
	domainrun "github.com/denyszorinets/ballet/core/internal/domain/run"
	"github.com/denyszorinets/ballet/core/internal/infra/anthropic"
	"github.com/denyszorinets/ballet/core/internal/infra/forge/gitforge"
	"github.com/denyszorinets/ballet/core/internal/infra/forge/github"
	"github.com/denyszorinets/ballet/core/internal/infra/knowledge"
	"github.com/denyszorinets/ballet/core/internal/infra/secrets"
	"github.com/denyszorinets/ballet/core/internal/infra/servicetokens"
	"github.com/denyszorinets/ballet/core/internal/infra/store"
	"github.com/denyszorinets/ballet/core/internal/transport/httpapi"
	"github.com/denyszorinets/ballet/core/internal/transport/internalapi"
	"github.com/denyszorinets/ballet/core/internal/transport/realtime"
	"github.com/denyszorinets/ballet/core/internal/transport/runnerapi"
	"github.com/denyszorinets/ballet/core/internal/transport/trackermcp"
	"github.com/denyszorinets/ballet/core/internal/transport/webui"
	"github.com/denyszorinets/ballet/kit/auth/oidc"
	"github.com/denyszorinets/ballet/kit/auth/runtoken"
	"github.com/denyszorinets/ballet/kit/config"
	"github.com/denyszorinets/ballet/kit/embed"
	"github.com/denyszorinets/ballet/kit/health"
	"github.com/denyszorinets/ballet/kit/rpc"
	"github.com/denyszorinets/ballet/kit/service"
)

// runTokenEnv carries a run's token to its session (secret env).
const runTokenEnv = "BALLET_RUN_TOKEN"

const (
	serviceName = "core"
	envPrefix   = "BALLET_CORE"
)

// serviceConfig is the complete core configuration.
type serviceConfig struct {
	service.Config
	OIDC       oidc.Config      `toml:"oidc"`
	Tokens     tokensConfig     `toml:"tokens"`
	Storage    storageConfig    `toml:"storage"`
	RBAC       rbacConfig       `toml:"rbac"`
	Web        webConfig        `toml:"web"`
	Services   servicesConfig   `toml:"services"`
	Secrets    secretsConfig    `toml:"secrets"`
	Knowledge  knowledgeConfig  `toml:"knowledge"`
	Gateway    gatewayConfig    `toml:"gateway"`
	Planner    plannerConfig    `toml:"planner"`
	Agents     agentsConfig     `toml:"agents"`
	Forge      forgeConfig      `toml:"forge"`
	Scheduler  schedulerConfig  `toml:"scheduler"`
	Reconciler reconcilerConfig `toml:"reconciler"`
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
	// CompactAtTokens: estimated context size above which older messages
	// are summarized for the model; 0 disables compaction.
	CompactAtTokens int `toml:"compact_at_tokens"`
}

// forgeConfig configures following pull requests (ADR-0007).
type forgeConfig struct {
	PollInterval time.Duration `toml:"poll_interval"` // refresh of open pull requests
}

// schedulerConfig configures starting runnable tickets automatically.
type schedulerConfig struct {
	Enabled             bool          `toml:"enabled"`
	MaxActive           int           `toml:"max_active"`             // flows occupying a slot, globally
	MaxActivePerProject int           `toml:"max_active_per_project"` // and per project
	Interval            time.Duration `toml:"interval"`               // how often to look for runnable tickets
}

// reconcilerConfig configures repairing flows after crashes and flagging
// stuck stages.
type reconcilerConfig struct {
	Interval time.Duration `toml:"interval"` // between passes
	Slack    time.Duration `toml:"slack"`    // beyond a run's timeout before it is stuck
}

// agentsConfig configures coding-agent runs (ADR-0003).
type agentsConfig struct {
	// GatewayURL and KnowledgeMCPURL as reached from inside run sessions
	// (containers may see other host names); empty: gateway.url and
	// knowledge.url + "/mcp".
	GatewayURL      string        `toml:"gateway_url"`
	KnowledgeMCPURL string        `toml:"knowledge_mcp_url"`
	TrackerMCPURL   string        `toml:"tracker_mcp_url"`
	ClaudeCommand   string        `toml:"claude_command"`
	Model           string        `toml:"model"`
	RunTokenTTL     time.Duration `toml:"run_token_ttl"`
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
	Dir      string `toml:"dir"`       // built SPA to serve; empty: the embedded one, if any
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
		Config:     service.DefaultConfig(":8080"),
		OIDC:       oidc.Config{Audience: "ballet"},
		Tokens:     tokensConfig{KeyFile: "data/token-keys.json"},
		Storage:    storageConfig{Path: "data/core.db"},
		Web:        webConfig{ClientID: "ballet-web"},
		Services:   servicesConfig{TokensDir: "data/service-tokens", TokenTTL: 30 * 24 * time.Hour},
		Secrets:    secretsConfig{KeyFile: "data/secrets.key"},
		Knowledge:  knowledgeConfig{URL: "http://localhost:8081"},
		Gateway:    gatewayConfig{URL: "http://localhost:8082"},
		Forge:      forgeConfig{PollInterval: time.Minute},
		Scheduler:  schedulerConfig{Enabled: true, MaxActive: 4, MaxActivePerProject: 2, Interval: 10 * time.Second},
		Reconciler: reconcilerConfig{Interval: time.Minute, Slack: 10 * time.Minute},
		Agents: agentsConfig{ClaudeCommand: "claude", RunTokenTTL: 3 * time.Hour,
			TrackerMCPURL: "http://localhost:8080" + trackermcp.Path},
		Planner: plannerConfig{
			Model: "claude-sonnet-5-5", MaxTokens: 8192, MaxRounds: 20, Skill: "planner", CompactAtTokens: 100_000,
		},
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
	if c.Forge.PollInterval < 10*time.Second {
		errs = append(errs, errors.New("forge.poll_interval must be at least 10s"))
	}
	if c.Scheduler.MaxActive < 1 || c.Scheduler.MaxActivePerProject < 1 || c.Scheduler.Interval < time.Second {
		errs = append(errs, errors.New("scheduler.max_active and scheduler.max_active_per_project must be at least 1, "+
			"scheduler.interval at least 1s"))
	}
	if c.Reconciler.Interval < time.Second || c.Reconciler.Slack < time.Minute {
		errs = append(errs, errors.New("reconciler.interval must be at least 1s and reconciler.slack at least 1m"))
	}
	if c.Agents.RunTokenTTL < 10*time.Minute {
		errs = append(errs, errors.New("agents.run_token_ttl must be at least 10m"))
	}
	if c.Planner.Model == "" || c.Planner.MaxTokens < 1 || c.Planner.MaxRounds < 1 || c.Planner.CompactAtTokens < 0 {
		errs = append(errs, errors.New("planner.model must be set; planner.max_tokens and planner.max_rounds at least 1; "+
			"planner.compact_at_tokens not negative"))
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
	jobResults := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ballet_jobs_total",
		Help: "Durable jobs executed, by kind and result (done, retry, dead).",
	}, []string{"kind", "result"})
	svc.Metrics.MustRegister(jobResults)
	orchestrator := &app.Orchestrator{Store: st, Now: time.Now, Logger: svc.Logger,
		OnFinished: func(kind, result string) { jobResults.WithLabelValues(kind, result).Inc() }}
	compactions := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "ballet_planner_compactions_total",
		Help: "Planner conversations compacted (older messages summarized for the model).",
	})
	svc.Metrics.MustRegister(compactions)
	search := &app.Search{Store: st, Tenancy: st, Authz: authz, Embedder: embed.Hash{}}
	knowledgeAccess := &app.KnowledgeAccess{Tenancy: st, Authz: authz}
	changesets := &app.Changesets{Store: st, Tracker: tracker}
	llm := &anthropic.Client{GatewayURL: cfg.Gateway.URL, Tokens: tokenIssuer}
	knowledgeReader := &knowledge.Reader{URL: knowledgeURL, Tokens: tokenIssuer}
	plannerSvc := &app.Planner{
		Store: st, Tenancy: st, Authz: authz,
		LLM: llm,
		Tools: plannertools.All(plannertools.Deps{
			Tracker: tracker, Changesets: changesets, Skills: skills, Search: search,
			Knowledge: &knowledge.Client{URL: knowledgeURL, Access: knowledgeAccess, Tokens: tokenIssuer},
		}),
		Instructions: func(ctx context.Context, projectKey string) (string, error) {
			return skills.ProjectSkillBody(ctx, projectKey, cfg.Planner.Skill)
		},
		Model: cfg.Planner.Model, MaxTokens: cfg.Planner.MaxTokens, MaxRounds: cfg.Planner.MaxRounds,
		CompactAt: cfg.Planner.CompactAtTokens, OnCompact: compactions.Inc,
		Now: time.Now, NewID: store.NewID, Logger: svc.Logger, Context: ctx,
	}
	agentGateway := cfg.Agents.GatewayURL
	if agentGateway == "" {
		agentGateway = cfg.Gateway.URL
	}
	knowledgeMCP := cfg.Agents.KnowledgeMCPURL
	if knowledgeMCP == "" {
		knowledgeMCP = strings.TrimSuffix(cfg.Knowledge.URL, "/") + "/mcp"
	}
	agents := map[string]agent.Adapter{
		claudecode.Name: claudecode.Adapter{Command: cfg.Agents.ClaudeCommand, GatewayURL: agentGateway, APIKeyEnv: runTokenEnv},
	}
	dispatcher := &app.Dispatcher{Store: st, Tenancy: st, Now: time.Now, Logger: svc.Logger, Adapters: agents,
		// Secrets reach the Runner with the run's start only: the run's own
		// token and the project's git token.
		SecretEnv: func(ctx context.Context, r domainrun.Run) (map[string]string, error) {
			p, err := st.ProjectByID(ctx, r.ProjectID)
			if err != nil {
				return nil, err
			}
			c, err := st.CustomerByID(ctx, p.CustomerID)
			if err != nil {
				return nil, err
			}
			it, err := st.ItemByID(ctx, r.TicketID)
			if err != nil {
				return nil, err
			}
			tok, err := tokenIssuer.Issue(runtoken.Claims{
				Kind: runtoken.KindRun, Subject: "run:" + r.ID, Audience: []string{"gateway", "knowledge", "core"},
				Customer: c.Key, Project: p.Key, Ticket: it.Key,
				Capabilities: []string{runtoken.CapLLMInvoke, runtoken.CapKnowledgeRead, runtoken.CapKnowledgeWrite,
					runtoken.CapTrackerRead, runtoken.CapTrackerReport},
			}, cfg.Agents.RunTokenTTL)
			if err != nil {
				return nil, err
			}
			secrets := map[string]string{runTokenEnv: tok}
			cred, err := credentials.Resolve(ctx, c.Key, p.Key, credential.ProviderGit)
			switch {
			case err == nil:
				secrets[execution.TokenEnv] = cred.APIKey
			case !errors.Is(err, app.ErrNotFound):
				return nil, err
			}
			return secrets, nil
		},
	}
	runnerapi.Register(svc.Mux, runnerapi.Deps{
		Verifier: runtoken.NewRingVerifier(tokenKeys, time.Now), Dispatcher: dispatcher,
		Options: rpc.Options{Logger: svc.Logger},
	})
	runs := &app.Runs{Store: st, Execution: st, Items: st, Tenancy: st, Authz: authz, Dispatcher: dispatcher,
		Agents: agents, SessionSkills: skills.SessionSkills, Model: cfg.Agents.Model, Deps: st,
		Knowledge: knowledgeReader.ForTicket,
		MCP: []agent.MCPServer{
			{Name: "tracker", URL: cfg.Agents.TrackerMCPURL, TokenEnv: runTokenEnv},
			{Name: "knowledge", URL: knowledgeMCP, TokenEnv: runTokenEnv},
		},
		Now: time.Now, NewID: store.NewID}
	questions := &app.Questions{Store: st, Items: st, Tenancy: st, Authz: authz, Orchestrator: orchestrator,
		Knowledge: knowledgeReader, LLM: llm, Model: cfg.Planner.Model, MaxTokens: cfg.Planner.MaxTokens,
		Inbox: st, Reports: st, Planner: plannerSvc,
		Now: time.Now, NewID: store.NewID, Logger: svc.Logger}
	questions.Register(orchestrator)
	agentTracker := &app.AgentTracker{Reports: st, RunStore: st, Runs: runs, Items: st, Tenancy: st, Authz: authz,
		Changesets: changesets, Now: time.Now, NewID: store.NewID, OnQuestion: questions.Route}
	trackermcp.Register(svc.Mux, runtoken.NewRingVerifier(tokenKeys, time.Now), agentTracker, "v1")
	pullRequests := &app.PullRequests{Store: st, Execution: st, Items: st, Tenancy: st, Authz: authz,
		Token: app.GitToken(credentials), Now: time.Now, Logger: svc.Logger,
		Forges: func(s execution.Settings) (forge.Forge, error) {
			switch s.ForgeName() {
			case github.Name:
				return github.Adapter{APIURL: s.ForgeAPIURL}, nil
			case gitforge.Name:
				return gitforge.Adapter{LinkTemplate: s.LinkTemplate}, nil
			}
			return nil, fmt.Errorf("unknown forge %q", s.Forge)
		},
	}
	go pullRequests.Poll(ctx, cfg.Forge.PollInterval)
	adapterNames := make([]string, 0, len(agents))
	for name := range agents {
		adapterNames = append(adapterNames, name)
	}
	pipelines := &app.Pipelines{Store: st, Tenancy: st, Authz: authz, Adapters: adapterNames, Now: time.Now}
	flows := &app.Flows{Store: st, Items: st, Tenancy: st, Authz: authz, Pipelines: pipelines, Runs: runs, RunStore: st,
		Reports: st, PullRequests: pullRequests, Orchestrator: orchestrator, Now: time.Now, NewID: store.NewID,
		Logger: svc.Logger}
	flows.Register(orchestrator)
	dispatcher.OnFinished = flows.RunFinished
	scheduler := &app.Scheduler{Store: st, Start: flows.StartTicket, Logger: svc.Logger, MaxActive: cfg.Scheduler.MaxActive,
		MaxActivePerProject: cfg.Scheduler.MaxActivePerProject, Interval: cfg.Scheduler.Interval}
	flows.Changed = scheduler.Kick
	// Start once every job kind has its handler: a job claimed without one
	// would be marked dead.
	go orchestrator.Run(ctx)
	go dispatcher.Run(ctx)
	if cfg.Scheduler.Enabled {
		go scheduler.Run(ctx)
	}
	reconciler := &app.Reconciler{Flows: flows, Jobs: st, Interval: cfg.Reconciler.Interval, Slack: cfg.Reconciler.Slack}
	go reconciler.Run(ctx)
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
		Tracker:      tracker,
		Changesets:   changesets,
		Planner:      plannerSvc,
		Runs:         runs,
		Execution:    &app.Execution{Store: st, Tenancy: st, Authz: authz, Now: time.Now},
		AgentTracker: agentTracker,
		PullRequests: pullRequests,
		Pipelines:    pipelines,
		Flows:        flows,
		Questions:    questions,
		Assumptions: &app.Assumptions{Store: st, Questions: st, Reports: st, Items: st, Tenancy: st, Authz: authz,
			Changesets: changesets, Now: time.Now, NewID: store.NewID},
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

	// web.dir overrides the SPA embedded in a bindata build.
	assets := webui.Bundled()
	if cfg.Web.Dir != "" {
		assets = os.DirFS(cfg.Web.Dir)
	}
	webui.Register(svc.Mux, webui.Config{OIDCIssuer: cfg.OIDC.IssuerURL, ClientID: cfg.Web.ClientID, Assets: assets})

	return svc.ListenAndServe(ctx)
}
