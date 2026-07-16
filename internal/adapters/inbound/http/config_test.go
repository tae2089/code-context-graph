package server

import (
	"strings"
	"testing"
	"time"
)

func TestValidateConfig_WebhookRequiresSecretOrInsecure(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Transport = "streamable-http"
	cfg.AllowRepo = []string{"org/*"}
	cfg.RepoRoot = "/var/repos"

	err := ValidateConfig(cfg)
	if err == nil || !strings.Contains(err.Error(), "webhook-secret") {
		t.Fatalf("expected webhook secret validation error, got %v", err)
	}
}

func TestValidateConfig_WebhookRequiresRepoRoot(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Transport = "streamable-http"
	cfg.AllowRepo = []string{"org/*"}
	cfg.WebhookSecret = "secret"
	cfg.RepoCloneBaseURLs = []string{"https://github.com"}

	err := ValidateConfig(cfg)
	if err == nil || !strings.Contains(err.Error(), "--repo-root") {
		t.Fatalf("expected repo root validation error, got %v", err)
	}
}

func TestValidateConfig_WebhookAllowsSecureModeWithCloneBaseURL(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Transport = "streamable-http"
	cfg.AllowRepo = []string{"org/*"}
	cfg.WebhookSecret = "secret"
	cfg.RepoRoot = "/var/repos"
	cfg.RepoCloneBaseURLs = []string{"https://github.com"}

	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestValidateConfig_RejectsMultiOwnerRepoNameNamespaces(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Transport = "streamable-http"
	cfg.AllowRepo = []string{"org-a/*", "org-b/api"}
	cfg.WebhookSecret = "test-placeholder"
	cfg.RepoRoot = "/var/repos"
	cfg.RepoCloneBaseURLs = []string{"https://github.com"}

	err := ValidateConfig(cfg)
	if err == nil || !strings.Contains(err.Error(), "repo-name namespaces") {
		t.Fatalf("expected repo-name namespace validation error, got %v", err)
	}
}

func TestConfiguredCloneBaseURLs_PreservesLegacySingularFirst(t *testing.T) {
	cfg := Config{
		RepoCloneBaseURL:  "https://github.com",
		RepoCloneBaseURLs: []string{"https://gitea.example.com", ""},
	}

	got := ConfiguredCloneBaseURLs(cfg)
	want := []string{"https://github.com", "https://gitea.example.com"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("ConfiguredCloneBaseURLs = %#v, want %#v", got, want)
	}
}

func TestValidateConfig_RejectsNonPositiveWebhookSettings(t *testing.T) {
	tests := []struct {
		name string
		mut  func(*Config)
		want string
	}{
		{"workers", func(c *Config) { c.WebhookWorkers = 0 }, "--webhook-workers"},
		{"tracked repos", func(c *Config) { c.WebhookMaxTrackedRepos = 0 }, "--webhook-max-tracked-repos"},
		{"attempt timeout", func(c *Config) { c.WebhookAttemptTimeout = 0 }, "--webhook-attempt-timeout"},
		{"shutdown timeout", func(c *Config) { c.WebhookShutdownTimeout = 0 }, "--webhook-shutdown-timeout"},
		{"retry attempts", func(c *Config) { c.WebhookRetryAttempts = 0 }, "--webhook-retry-attempts"},
		{"retry base delay", func(c *Config) { c.WebhookRetryBaseDelay = 0 }, "--webhook-retry-base-delay"},
		{"retry max delay", func(c *Config) { c.WebhookRetryMaxDelay = 0 }, "--webhook-retry-max-delay"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Transport = "streamable-http"
			tc.mut(&cfg)
			err := ValidateConfig(cfg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %s validation error, got %v", tc.want, err)
			}
		})
	}
}

func TestDefaultConfig_UsesWebhookTuningFromEnv(t *testing.T) {
	t.Setenv("CCG_WEBHOOK_MAX_TRACKED_REPOS", "9")
	t.Setenv("CCG_WEBHOOK_ATTEMPT_TIMEOUT", "3m")
	t.Setenv("CCG_WEBHOOK_SHUTDOWN_TIMEOUT", "40s")
	t.Setenv("CCG_WEBHOOK_RETRY_ATTEMPTS", "6")
	t.Setenv("CCG_WEBHOOK_RETRY_BASE_DELAY", "3s")
	t.Setenv("CCG_WEBHOOK_RETRY_MAX_DELAY", "30s")

	cfg := DefaultConfig()
	if cfg.WebhookMaxTrackedRepos != 9 ||
		cfg.WebhookAttemptTimeout != 3*time.Minute ||
		cfg.WebhookShutdownTimeout != 40*time.Second ||
		cfg.WebhookRetryAttempts != 6 ||
		cfg.WebhookRetryBaseDelay != 3*time.Second ||
		cfg.WebhookRetryMaxDelay != 30*time.Second {
		t.Fatalf("unexpected webhook env config: %+v", cfg)
	}
}

func TestDefaultConfig_UsesWebhookSecretFromEnv(t *testing.T) {
	t.Setenv("CCG_WEBHOOK_SECRET", "configured-for-test")

	cfg := DefaultConfig()
	if cfg.WebhookSecret != "configured-for-test" {
		t.Fatal("DefaultConfig did not load CCG_WEBHOOK_SECRET")
	}
}
