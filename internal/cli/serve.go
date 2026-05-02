package cli

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/tae2089/trace"
)

// ServeConfig holds parsed flags for the serve subcommand.
// @intent MCP 서버 실행에 필요한 전송 방식과 세션 관련 옵션을 전달한다.
type ServeConfig struct {
	CacheTTL      time.Duration
	NoCache       bool
	Transport     string // "stdio" (default) | "streamable-http"
	HTTPAddr      string // listen address for HTTP transport (default "127.0.0.1:8080")
	HTTPBearerToken string
	InsecureHTTP  bool
	Stateless     bool   // stateless session management for multi-instance deployments
	WorkspaceRoot string // root directory for file workspaces (default "workspaces")
	WebhookWorkers int
	AllowRepo     []string
	WebhookSecret string
	InsecureWebhook bool
	RepoCloneBaseURL string
	RepoRoot      string
}

func validateServeConfig(cfg ServeConfig) error {
	if cfg.Transport != "streamable-http" {
		return nil
	}
	if cfg.WebhookWorkers <= 0 {
		return fmt.Errorf("--webhook-workers must be > 0")
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
	if !cfg.InsecureWebhook && strings.TrimSpace(cfg.RepoCloneBaseURL) == "" {
		return fmt.Errorf("webhook sync requires --repo-clone-base-url unless --insecure-webhook is set")
	}
	if strings.TrimSpace(cfg.RepoCloneBaseURL) != "" {
		parsed, err := url.Parse(cfg.RepoCloneBaseURL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return fmt.Errorf("--repo-clone-base-url must include scheme and host")
		}
	}
	return nil
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
	cmd.Flags().BoolVar(&cfg.InsecureHTTP, "insecure-http", false, "Allow externally bound HTTP transport without bearer token (unsafe; testing only)")
	cmd.Flags().BoolVar(&cfg.Stateless, "stateless", false, "Stateless session management (for multi-instance deployments)")
	cmd.Flags().StringVar(&cfg.WorkspaceRoot, "workspace-root", "workspaces", "Root directory for file workspaces")
	cmd.Flags().IntVar(&cfg.WebhookWorkers, "webhook-workers", 4, "Number of webhook sync workers")
	cmd.Flags().StringSliceVar(&cfg.AllowRepo, "allow-repo", nil, "Allowed repo patterns for webhook sync (repeatable, e.g. org/*, !org/private)")
	cmd.Flags().StringVar(&cfg.WebhookSecret, "webhook-secret", "", "HMAC secret for GitHub webhook signature verification")
	cmd.Flags().BoolVar(&cfg.InsecureWebhook, "insecure-webhook", false, "Allow unsigned webhook requests (unsafe; testing only)")
	cmd.Flags().StringVar(&cfg.RepoCloneBaseURL, "repo-clone-base-url", "", "Canonical base URL used to reconstruct clone targets for allowed repos")
	cmd.Flags().StringVar(&cfg.RepoRoot, "repo-root", os.Getenv("CCG_REPO_ROOT"), "Root directory for cloned repositories")

	return cmd
}
