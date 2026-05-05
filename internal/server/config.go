package server

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds self-hosted HTTP server runtime options.
// @intent keep long-running HTTP, webhook, cache, and parse-limit settings out of the local CLI layer.
type Config struct {
	CacheTTL                time.Duration
	NoCache                 bool
	Transport               string // "stdio" | "streamable-http"
	HTTPAddr                string
	HTTPBearerToken         string
	OTELEndpoint            string
	InsecureHTTP            bool
	Stateless               bool
	NamespaceRoot           string
	WorkspaceRoot           string
	WebhookWorkers          int
	AllowRepo               []string
	WebhookSecret           string
	InsecureWebhook         bool
	RepoCloneBaseURL        string
	RepoCloneBaseURLs       []string
	RepoRoot                string
	WebhookMaxTrackedRepos  int
	WebhookAttemptTimeout   time.Duration
	WebhookShutdownTimeout  time.Duration
	WebhookRetryAttempts    int
	WebhookRetryBaseDelay   time.Duration
	WebhookRetryMaxDelay    time.Duration
	MaxFileBytes            int64
	MaxTotalParsedBytes     int64
	WebhookFailOnUnreadable bool
}

// DefaultConfig returns the self-hosted server defaults.
// @intent centralize default server flag values for ccg-server.
func DefaultConfig() Config {
	return Config{
		CacheTTL:               5 * time.Minute,
		Transport:              "stdio",
		HTTPAddr:               "127.0.0.1:8080",
		HTTPBearerToken:        os.Getenv("CCG_HTTP_BEARER_TOKEN"),
		OTELEndpoint:           os.Getenv("CCG_OTEL_ENDPOINT"),
		NamespaceRoot:          "workspaces",
		WebhookWorkers:         EnvInt("CCG_WEBHOOK_WORKERS", 4),
		WebhookMaxTrackedRepos: EnvInt("CCG_WEBHOOK_MAX_TRACKED_REPOS", 1024),
		WebhookAttemptTimeout:  EnvDuration("CCG_WEBHOOK_ATTEMPT_TIMEOUT", 15*time.Minute),
		WebhookShutdownTimeout: EnvDuration("CCG_WEBHOOK_SHUTDOWN_TIMEOUT", 30*time.Second),
		WebhookRetryAttempts:   EnvInt("CCG_WEBHOOK_RETRY_ATTEMPTS", 3),
		WebhookRetryBaseDelay:  EnvDuration("CCG_WEBHOOK_RETRY_BASE_DELAY", time.Second),
		WebhookRetryMaxDelay:   EnvDuration("CCG_WEBHOOK_RETRY_MAX_DELAY", 30*time.Second),
		RepoRoot:               os.Getenv("CCG_REPO_ROOT"),
	}
}

// ValidateConfig checks that the server configuration is self-consistent.
// @intent reject invalid webhook and HTTP exposure settings before opening listeners.
func ValidateConfig(cfg Config) error {
	switch cfg.Transport {
	case "stdio", "streamable-http":
	default:
		return fmt.Errorf("--transport must be stdio or streamable-http")
	}
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
	if cfg.WebhookShutdownTimeout <= 0 {
		return fmt.Errorf("--webhook-shutdown-timeout must be > 0")
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
	cloneBaseURLs := ConfiguredCloneBaseURLs(cfg)
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

// ConfiguredCloneBaseURLs merges singular and repeatable clone base URL settings.
// @intent preserve legacy singular URL behavior while exposing one ordered clone URL list.
func ConfiguredCloneBaseURLs(cfg Config) []string {
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

// EnvInt reads an integer from an environment variable, returning fallback if unset or unparseable.
// @intent provide env-based defaults for server flags without panicking on bad input.
func EnvInt(name string, fallback int) int {
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

// EnvIsSet reports whether an environment variable is present in the process environment.
// @intent distinguish between an unset variable and one explicitly set to empty string.
func EnvIsSet(name string) bool {
	_, ok := os.LookupEnv(name)
	return ok
}

// EnvDuration reads a time.Duration from an environment variable, returning fallback if unset or unparseable.
// @intent provide env-based defaults for server timeout and retry flags without panicking on bad input.
func EnvDuration(name string, fallback time.Duration) time.Duration {
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
