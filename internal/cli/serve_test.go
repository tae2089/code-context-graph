package cli

import (
	"testing"
)

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
