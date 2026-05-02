package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

type integrationComposeConfig struct {
	Services struct {
		CCG struct {
			Command []string `yaml:"command"`
		} `yaml:"ccg"`
	} `yaml:"services"`
}

func TestServeCommand_ExecutesServeFunc(t *testing.T) {
	deps, stdout, stderr := newTestDeps()

	called := false
	deps.ServeFunc = func(cfg ServeConfig) error {
		called = true
		return nil
	}

	err := executeCmd(deps, stdout, stderr, "serve")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !called {
		t.Fatal("expected ServeFunc to be called")
	}
}

func TestServeCmdFlags_AllowRepo(t *testing.T) {
	deps, stdout, stderr := newTestDeps()

	var got ServeConfig
	deps.ServeFunc = func(cfg ServeConfig) error {
		got = cfg
		return nil
	}

	err := executeCmd(deps, stdout, stderr, "serve", "--allow-repo", "org/*", "--allow-repo", "external/shared", "--insecure-webhook", "--repo-root", "/var/repos")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(got.AllowRepo) != 2 {
		t.Fatalf("AllowRepo len = %d, want 2", len(got.AllowRepo))
	}
	if got.AllowRepo[0] != "org/*" {
		t.Errorf("AllowRepo[0] = %q, want %q", got.AllowRepo[0], "org/*")
	}
	if got.AllowRepo[1] != "external/shared" {
		t.Errorf("AllowRepo[1] = %q, want %q", got.AllowRepo[1], "external/shared")
	}
}

func TestServeCmdFlags_WebhookSecret(t *testing.T) {
	deps, stdout, stderr := newTestDeps()

	var got ServeConfig
	deps.ServeFunc = func(cfg ServeConfig) error {
		got = cfg
		return nil
	}

	err := executeCmd(deps, stdout, stderr, "serve", "--webhook-secret", "my-secret-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got.WebhookSecret != "my-secret-123" {
		t.Errorf("WebhookSecret = %q, want %q", got.WebhookSecret, "my-secret-123")
	}
}

func TestServeCmdFlags_InsecureWebhook(t *testing.T) {
	deps, stdout, stderr := newTestDeps()

	var got ServeConfig
	deps.ServeFunc = func(cfg ServeConfig) error {
		got = cfg
		return nil
	}

	err := executeCmd(deps, stdout, stderr, "serve", "--insecure-webhook")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !got.InsecureWebhook {
		t.Fatal("expected InsecureWebhook to be true")
	}
}

func TestServeCmdFlags_RepoRoot(t *testing.T) {
	deps, stdout, stderr := newTestDeps()

	var got ServeConfig
	deps.ServeFunc = func(cfg ServeConfig) error {
		got = cfg
		return nil
	}

	err := executeCmd(deps, stdout, stderr, "serve", "--repo-root", "/var/repos")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got.RepoRoot != "/var/repos" {
		t.Errorf("RepoRoot = %q, want %q", got.RepoRoot, "/var/repos")
	}
}

func TestServeCmdFlags_RepoCloneBaseURL(t *testing.T) {
	deps, stdout, stderr := newTestDeps()

	var got ServeConfig
	deps.ServeFunc = func(cfg ServeConfig) error {
		got = cfg
		return nil
	}

	err := executeCmd(deps, stdout, stderr, "serve", "--repo-clone-base-url", "https://github.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got.RepoCloneBaseURL != "https://github.com" {
		t.Errorf("RepoCloneBaseURL = %q, want %q", got.RepoCloneBaseURL, "https://github.com")
	}
}

func TestServeCmdFlags_HTTPAuth(t *testing.T) {
	deps, stdout, stderr := newTestDeps()

	var got ServeConfig
	deps.ServeFunc = func(cfg ServeConfig) error {
		got = cfg
		return nil
	}

	err := executeCmd(deps, stdout, stderr, "serve", "--http-bearer-token", "secret-token", "--insecure-http")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.HTTPBearerToken != "secret-token" {
		t.Fatalf("HTTPBearerToken = %q, want %q", got.HTTPBearerToken, "secret-token")
	}
	if !got.InsecureHTTP {
		t.Fatal("expected InsecureHTTP to be true")
	}
}

func TestServeCmd_UsesHTTPBearerTokenFromEnv(t *testing.T) {
	t.Setenv("CCG_HTTP_BEARER_TOKEN", "env-secret")

	deps, stdout, stderr := newTestDeps()
	var got ServeConfig
	deps.ServeFunc = func(cfg ServeConfig) error {
		got = cfg
		return nil
	}

	err := executeCmd(deps, stdout, stderr, "serve")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.HTTPBearerToken != "env-secret" {
		t.Fatalf("HTTPBearerToken = %q, want %q", got.HTTPBearerToken, "env-secret")
	}
}

func TestServeCmd_UsesRepoRootFromEnv(t *testing.T) {
	t.Setenv("CCG_REPO_ROOT", "/env/repos")

	deps, stdout, stderr := newTestDeps()
	var got ServeConfig
	deps.ServeFunc = func(cfg ServeConfig) error {
		got = cfg
		return nil
	}

	err := executeCmd(deps, stdout, stderr, "serve")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.RepoRoot != "/env/repos" {
		t.Fatalf("RepoRoot = %q, want %q", got.RepoRoot, "/env/repos")
	}
}

func TestServeCmd_DefaultHTTPAddrIsLoopback(t *testing.T) {
	deps, stdout, stderr := newTestDeps()

	var got ServeConfig
	deps.ServeFunc = func(cfg ServeConfig) error {
		got = cfg
		return nil
	}

	err := executeCmd(deps, stdout, stderr, "serve")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.HTTPAddr != "127.0.0.1:8080" {
		t.Fatalf("HTTPAddr = %q, want %q", got.HTTPAddr, "127.0.0.1:8080")
	}
	if got.WebhookWorkers != 4 {
		t.Fatalf("WebhookWorkers = %d, want 4", got.WebhookWorkers)
	}
}

func TestServeCmdFlags_WebhookWorkers(t *testing.T) {
	deps, stdout, stderr := newTestDeps()

	var got ServeConfig
	deps.ServeFunc = func(cfg ServeConfig) error {
		got = cfg
		return nil
	}

	err := executeCmd(deps, stdout, stderr, "serve", "--webhook-workers", "2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.WebhookWorkers != 2 {
		t.Fatalf("WebhookWorkers = %d, want 2", got.WebhookWorkers)
	}
}

func TestServeCmd_RejectsNonPositiveWebhookWorkers(t *testing.T) {
	deps, stdout, stderr := newTestDeps()
	deps.ServeFunc = func(cfg ServeConfig) error { return nil }

	for _, workers := range []string{"0", "-1"} {
		err := executeCmd(deps, stdout, stderr, "serve", "--transport", "streamable-http", "--webhook-workers", workers)
		if err == nil || !strings.Contains(err.Error(), "--webhook-workers must be > 0") {
			t.Fatalf("expected webhook worker validation error for %s, got %v", workers, err)
		}
	}
}

func TestServeCmd_WebhookRequiresSecretOrInsecure(t *testing.T) {
	deps, stdout, stderr := newTestDeps()
	deps.ServeFunc = func(cfg ServeConfig) error { return nil }

	err := executeCmd(deps, stdout, stderr, "serve", "--transport", "streamable-http", "--allow-repo", "org/*", "--repo-root", "/var/repos")
	if err == nil {
		t.Fatal("expected error when webhook secret and insecure flag are both absent")
	}
}

func TestServeCmd_WebhookRequiresRepoRoot(t *testing.T) {
	deps, stdout, stderr := newTestDeps()
	deps.ServeFunc = func(cfg ServeConfig) error { return nil }

	err := executeCmd(deps, stdout, stderr, "serve", "--transport", "streamable-http", "--allow-repo", "org/*", "--webhook-secret", "secret", "--repo-clone-base-url", "https://github.com")
	if err == nil {
		t.Fatal("expected error when repo root is absent")
	}
}

func TestServeCmd_WebhookRequiresCloneBaseURLInSecureMode(t *testing.T) {
	deps, stdout, stderr := newTestDeps()
	deps.ServeFunc = func(cfg ServeConfig) error { return nil }

	err := executeCmd(deps, stdout, stderr, "serve", "--transport", "streamable-http", "--allow-repo", "org/*", "--webhook-secret", "secret", "--repo-root", "/var/repos")
	if err == nil {
		t.Fatal("expected error when clone base URL is absent in secure mode")
	}
}

func TestServeCmd_WebhookRejectsInvalidCloneBaseURL(t *testing.T) {
	deps, stdout, stderr := newTestDeps()
	deps.ServeFunc = func(cfg ServeConfig) error { return nil }

	err := executeCmd(deps, stdout, stderr, "serve", "--transport", "streamable-http", "--allow-repo", "org/*", "--webhook-secret", "secret", "--repo-root", "/var/repos", "--repo-clone-base-url", "://bad")
	if err == nil {
		t.Fatal("expected error when clone base URL is invalid")
	}
}

func TestServeCmd_WebhookRejectsSecretAndInsecureTogether(t *testing.T) {
	deps, stdout, stderr := newTestDeps()
	deps.ServeFunc = func(cfg ServeConfig) error { return nil }

	err := executeCmd(deps, stdout, stderr, "serve", "--transport", "streamable-http", "--allow-repo", "org/*", "--webhook-secret", "secret", "--insecure-webhook", "--repo-root", "/var/repos")
	if err == nil {
		t.Fatal("expected error when webhook secret and insecure-webhook are both set")
	}
}

func TestServeCmd_WebhookAllowsExplicitInsecureMode(t *testing.T) {
	deps, stdout, stderr := newTestDeps()

	called := false
	deps.ServeFunc = func(cfg ServeConfig) error {
		called = true
		if !cfg.InsecureWebhook {
			t.Fatal("expected insecure webhook flag to reach ServeFunc")
		}
		return nil
	}

	err := executeCmd(deps, stdout, stderr, "serve", "--transport", "streamable-http", "--allow-repo", "org/*", "--insecure-webhook", "--repo-root", "/var/repos")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("expected ServeFunc to be called")
	}
}

func TestServeCmd_WebhookAllowsSecureModeWithCloneBaseURL(t *testing.T) {
	deps, stdout, stderr := newTestDeps()

	called := false
	deps.ServeFunc = func(cfg ServeConfig) error {
		called = true
		if cfg.RepoCloneBaseURL != "https://github.com" {
			t.Fatalf("RepoCloneBaseURL = %q, want %q", cfg.RepoCloneBaseURL, "https://github.com")
		}
		return nil
	}

	err := executeCmd(deps, stdout, stderr, "serve", "--transport", "streamable-http", "--allow-repo", "org/*", "--webhook-secret", "secret", "--repo-root", "/var/repos", "--repo-clone-base-url", "https://github.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("expected ServeFunc to be called")
	}
}

func TestIntegrationCompose_UsesSecureWebhookCloneBaseURL(t *testing.T) {
	composePath := filepath.Join("..", "..", "docker-compose.integration.yml")
	data, err := os.ReadFile(composePath)
	if err != nil {
		t.Fatalf("read compose file: %v", err)
	}

	var cfg integrationComposeConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal compose file: %v", err)
	}

	command := cfg.Services.CCG.Command
	if len(command) == 0 {
		t.Fatal("ccg command is empty in docker-compose.integration.yml")
	}

	if !containsArg(command, "--repo-clone-base-url=http://gitea:3000") {
		t.Fatalf("ccg command must include --repo-clone-base-url=http://gitea:3000 in secure mode, got %v", command)
	}
	if containsPrefixedArg(command, "--insecure-webhook") {
		t.Fatalf("ccg command must not enable insecure webhook mode in integration compose, got %v", command)
	}
}

func TestServeCmd_StdioDoesNotRequireWebhookValidation(t *testing.T) {
	deps, stdout, stderr := newTestDeps()
	called := false
	deps.ServeFunc = func(cfg ServeConfig) error {
		called = true
		return nil
	}

	err := executeCmd(deps, stdout, stderr, "serve", "--transport", "stdio", "--allow-repo", "org/*")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("expected ServeFunc to be called")
	}
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func containsPrefixedArg(args []string, prefix string) bool {
	for _, arg := range args {
		if arg == prefix || strings.HasPrefix(arg, prefix+"=") {
			return true
		}
	}
	return false
}
