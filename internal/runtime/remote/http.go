// @index Remote HTTP runtime composition for MCP, Wiki, webhook, and repository sync.
package remote

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/tae2089/trace"

	httpin "github.com/tae2089/code-context-graph/internal/adapters/inbound/http"
	"github.com/tae2089/code-context-graph/internal/adapters/inbound/webhook"
	wikihttp "github.com/tae2089/code-context-graph/internal/adapters/inbound/wikihttp"
	"github.com/tae2089/code-context-graph/internal/adapters/outbound/configfiles"
	"github.com/tae2089/code-context-graph/internal/adapters/outbound/gitrepo"
	"github.com/tae2089/code-context-graph/internal/adapters/outbound/reposyncgraph"
	"github.com/tae2089/code-context-graph/internal/adapters/outbound/reposyncobs"
	"github.com/tae2089/code-context-graph/internal/app/ingest/workflow"
	"github.com/tae2089/code-context-graph/internal/app/reposync"
	"github.com/tae2089/code-context-graph/internal/app/search/retrieval"
	ccgruntime "github.com/tae2089/code-context-graph/internal/runtime"
	mcpruntime "github.com/tae2089/code-context-graph/internal/runtime/mcp"
)

// RunHTTP assembles remote-only MCP, Wiki, webhook, and repository-sync resources and hosts them over HTTP.
// @intent keep all remote runtime construction outside inbound adapters and the local ccg binary.
// @sideEffect initializes telemetry/cache, optional sync workers, and a long-running HTTP listener.
func RunHTTP(rt *ccgruntime.Runtime, cfg httpin.Config, serviceVersion, ragIndexDir, ragProjectDesc string) error {
	if cfg.Transport != "streamable-http" {
		return fmt.Errorf("ccg-server only supports streamable-http transport")
	}
	rules := make([]reposync.RepoRule, 0, len(cfg.AllowRepo))
	for _, raw := range cfg.AllowRepo {
		rules = append(rules, reposync.ParseRepoRule(raw))
	}
	if err := reposync.ValidateRepoNameNamespaceRules(rules); err != nil {
		return err
	}
	inst, err := mcpruntime.New(rt.MCPComponents(), mcpruntime.Options{
		CacheTTL: cfg.CacheTTL, NoCache: cfg.NoCache, OTELEndpoint: cfg.OTELEndpoint,
		NamespaceRoot: cfg.NamespaceRoot, RepoRoot: cfg.RepoRoot,
		MaxFileBytes: cfg.MaxFileBytes, MaxTotalParsedBytes: cfg.MaxTotalParsedBytes,
		ServiceVersion: serviceVersion, RagIndexDir: ragIndexDir, RagProjectDesc: ragProjectDesc,
	})
	if err != nil {
		return err
	}
	defer inst.Close()
	cfg.RagIndexDir = ragIndexDir
	if err := httpin.ValidateHTTPExposure(cfg); err != nil {
		return err
	}

	deps := httpin.HostDeps{Logger: rt.Logger, MCPServer: inst.Server}
	deps.DBReady = func(r *http.Request) error {
		if rt.DB == nil {
			return fmt.Errorf("database not configured")
		}
		sqlDB, err := rt.DB.DB()
		if err != nil {
			return trace.Wrap(err, "get sql db")
		}
		return sqlDB.PingContext(r.Context())
	}

	if cfg.WikiDir != "" {
		wiki, err := wikihttp.New(wikihttp.Config{
			StaticDir: cfg.WikiDir, RagIndexDir: cfg.RagIndexDir,
			NamespaceRoot: cfg.NamespaceRoot, Repository: rt.Store,
			Retrieval: retrieval.New(rt.SearchReader, rt.SearchReader), Logger: rt.Logger,
		})
		if err != nil {
			return err
		}
		deps.WikiAPI = wiki.APIHandler()
		deps.WikiStatic = wiki.StaticHandler()
	}

	cleanupQueue := func() {}
	if len(cfg.AllowRepo) > 0 {
		queue, handler, cleanup := buildRepoSyncHTTP(rt, cfg, rules, inst)
		deps.SyncQueue = queue
		deps.Webhook = handler
		cleanupQueue = cleanup
	}
	deps.CleanupQueue = cleanupQueue
	defer cleanupQueue()
	return httpin.RunStreamableHTTP(deps, cfg)
}

// buildRepoSyncHTTP assembles the optional webhook handler and its daemonless worker queue.
// @intent centralize remote repository-sync adapter construction and return one idempotent cleanup hook.
func buildRepoSyncHTTP(rt *ccgruntime.Runtime, cfg httpin.Config, rules []reposync.RepoRule, inst *mcpruntime.Instance) (*reposync.SyncQueue, http.Handler, func()) {
	filter := reposync.NewRepoFilterFromRules(rules)
	repoLocker := gitrepo.NewRepoLocker(30 * time.Second)
	walkers := make(map[string]workflow.Parser, len(rt.Walkers))
	for ext, walker := range rt.Walkers {
		walkers[ext] = walker
	}
	graphSvc := &workflow.Service{Store: rt.Store, UnitOfWork: rt.UnitOfWork, Search: rt.Search, ParseCache: rt.Store, Walkers: walkers, Logger: rt.Logger}
	syncService := &reposync.Service{
		Checkout: gitrepo.NewCheckout(cfg.RepoRoot, repoLocker, nil), BuildScope: configfiles.BuildScope{},
		Graph:         reposyncgraph.Updater{Service: graphSvc, Syncer: rt.Syncer},
		Cache:         reposync.CacheInvalidatorFunc(func() { mcpruntime.FlushQueryCache(inst.Cache) }),
		Observability: reposyncobs.Hooks{}, AttemptTimeout: cfg.WebhookAttemptTimeout,
		MaxFileBytes: cfg.MaxFileBytes, MaxTotalParsedBytes: cfg.MaxTotalParsedBytes,
		FailOnUnreadable: cfg.WebhookFailOnUnreadable, Logger: rt.Logger,
	}
	syncCtx, cancel := context.WithCancel(context.Background())
	queue := reposync.NewSyncQueueWithConfig(syncCtx, cfg.WebhookWorkers, syncService.Sync, reposync.QueueConfig{
		RetryConfig:     reposync.RetryConfig{MaxAttempts: cfg.WebhookRetryAttempts, BaseDelay: cfg.WebhookRetryBaseDelay, MaxDelay: cfg.WebhookRetryMaxDelay},
		ShutdownTimeout: cfg.WebhookShutdownTimeout, MaxTrackedRepos: cfg.WebhookMaxTrackedRepos, Observability: reposyncobs.Hooks{},
	})
	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			cancel()
			rt.Logger.Info("cancelling sync context and draining workers")
			queue.Shutdown()
		})
	}
	handler := webhook.NewWebhookHandlerWithConfig(webhook.WebhookHandlerConfig{
		Secret: []byte(cfg.WebhookSecret), Filter: filter, OnSync: queue.Add,
		Insecure: cfg.InsecureWebhook, CloneBaseURLs: httpin.ConfiguredCloneBaseURLs(cfg),
	})
	return queue, handler, cleanup
}
