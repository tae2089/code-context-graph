package cli

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/tae2089/trace"
)

// ServeConfig holds parsed flags for the serve subcommand.
// @intent MCP 서버 실행에 필요한 전송 방식과 세션 관련 옵션을 전달한다.
type ServeConfig struct {
	CacheTTL                time.Duration
	NoCache                 bool
	Transport               string // "stdio" (default) | "streamable-http"
	HTTPAddr                string // listen address for HTTP transport (default "127.0.0.1:8080")
	HTTPBearerToken         string
	OTELEndpoint            string
	InsecureHTTP            bool
	Stateless               bool   // stateless session management for multi-instance deployments
	NamespaceRoot           string // root directory for file namespaces (default "workspaces")
	WorkspaceRoot           string // root directory for file workspaces (default "workspaces")
	WebhookWorkers          int
	AllowRepo               []string
	WebhookSecret           string
	InsecureWebhook         bool
	RepoCloneBaseURL        string
	RepoCloneBaseURLs       []string
	RepoRoot                string
	WebhookMaxTrackedRepos  int
	WebhookAttemptTimeout   time.Duration
	WebhookRetryAttempts    int
	WebhookRetryBaseDelay   time.Duration
	WebhookRetryMaxDelay    time.Duration
	MaxFileBytes            int64
	MaxTotalParsedBytes     int64
	WebhookFailOnUnreadable bool
}

// validateServeConfig checks that the serve configuration is self-consistent.
// @intent reject invalid flag combinations before the server starts
// @requires cfg.Transport is set; webhook fields are only validated when Transport == "streamable-http"
func validateServeConfig(cfg ServeConfig) error {
	if cfg.Transport != "streamable-http" {
		return nil
	}
	if cfg.WebhookWorkers <= 0 {
		return fmt.Errorf("--webhook-workers must be > 0")
	}
	if cfg.WebhookMaxTrackedRepos <= 0 {
		return fmt.Errorf("--webhook-max-tracked-repos must be > 0")
	}
	if cfg.WebhookAttemptTimeout <= 0 {
		return fmt.Errorf("--webhook-attempt-timeout must be > 0")
	}
	if cfg.WebhookRetryAttempts <= 0 {
		return fmt.Errorf("--webhook-retry-attempts must be > 0")
	}
	if cfg.WebhookRetryBaseDelay <= 0 {
		return fmt.Errorf("--webhook-retry-base-delay must be > 0")
	}
	if cfg.WebhookRetryMaxDelay <= 0 {
		return fmt.Errorf("--webhook-retry-max-delay must be > 0")
	}
	if len(cfg.AllowRepo) == 0 {
		return nil
	}
	if cfg.WebhookSecret != "" && cfg.InsecureWebhook {
		return fmt.Errorf("--webhook-secret and --insecure-webhook are mutually exclusive")
	}
	if cfg.WebhookSecret == "" && !cfg.InsecureWebhook {
		return fmt.Errorf("webhook sync requires --webhook-secret or --insecure-webhook when --allow-repo is set")
	}
	if strings.TrimSpace(cfg.RepoRoot) == "" {
		return fmt.Errorf("webhook sync requires --repo-root when --allow-repo is set")
	}
	cloneBaseURLs := configuredCloneBaseURLs(cfg)
	if !cfg.InsecureWebhook && len(cloneBaseURLs) == 0 {
		return fmt.Errorf("webhook sync requires --repo-clone-base-url unless --insecure-webhook is set")
	}
	for _, cloneBaseURL := range cloneBaseURLs {
		if _, err := url.ParseRequestURI(cloneBaseURL); err != nil {
			return fmt.Errorf("--repo-clone-base-url must include scheme and host")
		}
		parsed, err := url.Parse(cloneBaseURL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Opaque != "" {
			return fmt.Errorf("--repo-clone-base-url must include scheme and host")
		}
	}
	return nil
}

// configuredCloneBaseURLs merges the singular and plural clone base URL flags into a deduplicated list.
// @intent normalize clone base URL inputs from both legacy and current flags into a single ordered slice
func configuredCloneBaseURLs(cfg ServeConfig) []string {
	baseURLs := append([]string(nil), cfg.RepoCloneBaseURLs...)
	if cfg.RepoCloneBaseURL != "" {
		baseURLs = append([]string{cfg.RepoCloneBaseURL}, baseURLs...)
	}
	filtered := baseURLs[:0]
	for _, baseURL := range baseURLs {
		if strings.TrimSpace(baseURL) != "" {
			filtered = append(filtered, baseURL)
		}
	}
	return filtered
}

// newServeCmd creates the MCP server command.
// @intent CLI에서 stdio 또는 HTTP 기반 MCP 서버를 시작할 수 있게 한다.
// @requires deps.ServeFunc가 설정되어 있어야 한다.
// @sideEffect 실행 시 장시간 서버 프로세스를 시작한다.
func newServeCmd(deps *Deps) *cobra.Command {
	var cfg ServeConfig

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the MCP server over stdio or HTTP",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateServeConfig(cfg); err != nil {
				return err
			}
			if deps.ServeFunc != nil {
				return deps.ServeFunc(cfg)
			}
			return trace.New("ServeFunc not configured")
		},
	}

	cmd.Flags().DurationVar(&cfg.CacheTTL, "cache-ttl", 5*time.Minute, "TTL for MCP serve session cache (0 or --no-cache to disable)")
	cmd.Flags().BoolVar(&cfg.NoCache, "no-cache", false, "Disable in-memory cache for MCP serve session")
	cmd.Flags().StringVar(&cfg.Transport, "transport", "stdio", "Transport mode: stdio or streamable-http")
	cmd.Flags().StringVar(&cfg.HTTPAddr, "http-addr", "127.0.0.1:8080", "Listen address for HTTP transport")
	cmd.Flags().StringVar(&cfg.HTTPBearerToken, "http-bearer-token", os.Getenv("CCG_HTTP_BEARER_TOKEN"), "Bearer token required for MCP HTTP requests when set")
	cmd.Flags().StringVar(&cfg.OTELEndpoint, "otel-endpoint", os.Getenv("CCG_OTEL_ENDPOINT"), "OTLP HTTP trace endpoint (optional; enables span export when set)")
	cmd.Flags().BoolVar(&cfg.InsecureHTTP, "insecure-http", false, "Allow externally bound HTTP transport without bearer token (unsafe; testing only)")
	cmd.Flags().BoolVar(&cfg.Stateless, "stateless", false, "Stateless session management (for multi-instance deployments)")
	cmd.Flags().StringVar(&cfg.NamespaceRoot, "namespace-root", "workspaces", "Root directory for file namespaces")
	cmd.Flags().StringVar(&cfg.WorkspaceRoot, "workspace-root", "", "Deprecated alias for --namespace-root")
	cmd.Flags().IntVar(&cfg.WebhookWorkers, "webhook-workers", envInt("CCG_WEBHOOK_WORKERS", 4), "Number of webhook sync workers")
	cmd.Flags().IntVar(&cfg.WebhookMaxTrackedRepos, "webhook-max-tracked-repos", envInt("CCG_WEBHOOK_MAX_TRACKED_REPOS", 1024), "Maximum repositories tracked by the webhook sync queue")
	cmd.Flags().DurationVar(&cfg.WebhookAttemptTimeout, "webhook-attempt-timeout", envDuration("CCG_WEBHOOK_ATTEMPT_TIMEOUT", 15*time.Minute), "Timeout for one webhook sync attempt")
	cmd.Flags().IntVar(&cfg.WebhookRetryAttempts, "webhook-retry-attempts", envInt("CCG_WEBHOOK_RETRY_ATTEMPTS", 3), "Maximum webhook sync attempts per queued item")
	cmd.Flags().DurationVar(&cfg.WebhookRetryBaseDelay, "webhook-retry-base-delay", envDuration("CCG_WEBHOOK_RETRY_BASE_DELAY", time.Second), "Initial webhook sync retry delay")
	cmd.Flags().DurationVar(&cfg.WebhookRetryMaxDelay, "webhook-retry-max-delay", envDuration("CCG_WEBHOOK_RETRY_MAX_DELAY", 30*time.Second), "Maximum webhook sync retry delay")
	cmd.Flags().StringSliceVar(&cfg.AllowRepo, "allow-repo", nil, "Allowed repo patterns for webhook sync (repeatable, e.g. org/*, !org/private)")
	cmd.Flags().StringVar(&cfg.WebhookSecret, "webhook-secret", "", "HMAC secret for GitHub webhook signature verification")
	cmd.Flags().BoolVar(&cfg.InsecureWebhook, "insecure-webhook", false, "Allow unsigned webhook requests (unsafe; testing only)")
	cmd.Flags().StringArrayVar(&cfg.RepoCloneBaseURLs, "repo-clone-base-url", nil, "Canonical base URL used to reconstruct clone targets for allowed repos (repeatable)")
	cmd.Flags().StringVar(&cfg.RepoRoot, "repo-root", os.Getenv("CCG_REPO_ROOT"), "Root directory for cloned repositories")
	cmd.Flags().Int64Var(&cfg.MaxFileBytes, "max-file-bytes", 0, "Maximum bytes allowed per parsed source file (0 disables limit; config: parse.max_file_bytes)")
	cmd.Flags().Int64Var(&cfg.MaxTotalParsedBytes, "max-total-parsed-bytes", 0, "Maximum total bytes allowed across parsed source files (0 disables limit; config: parse.max_total_parsed_bytes)")
	cmd.Flags().BoolVar(&cfg.WebhookFailOnUnreadable, "webhook-fail-on-unreadable", false, "Fail webhook sync attempts when source files cannot be read instead of skipping them")

	cmd.PreRun = func(cmd *cobra.Command, args []string) {
		if cmd.Flags().Changed("workspace-root") && !cmd.Flags().Changed("namespace-root") {
			cfg.NamespaceRoot = cfg.WorkspaceRoot
		}
		if cfg.Transport == "streamable-http" &&
			len(cfg.AllowRepo) > 0 &&
			!cmd.Flags().Changed("webhook-workers") &&
			!envIsSet("CCG_WEBHOOK_WORKERS") &&
			viper.GetString("db.driver") == "sqlite" {
			cfg.WebhookWorkers = 1
		}
		cfg.WorkspaceRoot = cfg.NamespaceRoot
		cfg.MaxFileBytes = resolveMaxFileBytes(cfg.MaxFileBytes)
		cfg.MaxTotalParsedBytes = resolveMaxTotalParsedBytes(cfg.MaxTotalParsedBytes)
	}

	return cmd
}

// envInt reads an integer from an environment variable, returning fallback if unset or unparseable.
// @intent provide env-based defaults for webhook worker flags without panicking on bad input
func envInt(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return v
}

// envIsSet reports whether an environment variable is present in the process environment.
// @intent distinguish between an unset variable and one explicitly set to empty string
func envIsSet(name string) bool {
	_, ok := os.LookupEnv(name)
	return ok
}

// envDuration reads a time.Duration from an environment variable, returning fallback if unset or unparseable.
// @intent provide env-based defaults for webhook timeout and retry delay flags without panicking on bad input
func envDuration(name string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	v, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	return v
}
