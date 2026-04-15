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

	err := executeCmd(deps, stdout, stderr, "serve", "--allow-repo", "org/*", "--allow-repo", "external/shared")
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
