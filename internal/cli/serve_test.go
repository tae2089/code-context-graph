package cli

import (
	"strings"
	"testing"
	"time"
)

func TestServeCommand_ExecutesServeFunc(t *testing.T) {
	deps, stdout, stderr := newTestDeps()

	called := false
	deps.ServeFunc = func(cfg ServeConfig) error {
		called = true
		if cfg.Transport != "stdio" {
			t.Fatalf("Transport = %q, want stdio", cfg.Transport)
		}
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

func TestServeCmd_RejectsHTTPTransport(t *testing.T) {
	deps, stdout, stderr := newTestDeps()
	deps.ServeFunc = func(cfg ServeConfig) error { return nil }

	err := executeCmd(deps, stdout, stderr, "serve", "--transport", "streamable-http")
	if err == nil || !strings.Contains(err.Error(), "ccg-server") {
		t.Fatalf("expected ccg-server guidance for HTTP transport, got %v", err)
	}
}

func TestServeCmdFlags_StdioOptions(t *testing.T) {
	deps, stdout, stderr := newTestDeps()

	var got ServeConfig
	deps.ServeFunc = func(cfg ServeConfig) error {
		got = cfg
		return nil
	}

	err := executeCmd(deps, stdout, stderr,
		"serve",
		"--cache-ttl", "2m",
		"--no-cache",
		"--namespace-root", "/var/namespaces",
		"--max-file-bytes", "128",
		"--max-total-parsed-bytes", "256",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.CacheTTL != 2*time.Minute || !got.NoCache {
		t.Fatalf("unexpected cache config: %+v", got)
	}
	if got.NamespaceRoot != "/var/namespaces" || got.WorkspaceRoot != "/var/namespaces" {
		t.Fatalf("unexpected namespace/workspace roots: %+v", got)
	}
	if got.MaxFileBytes != 128 || got.MaxTotalParsedBytes != 256 {
		t.Fatalf("unexpected parse limits: %+v", got)
	}
}

func TestServeCmdFlags_WorkspaceRootAlias(t *testing.T) {
	deps, stdout, stderr := newTestDeps()

	var got ServeConfig
	deps.ServeFunc = func(cfg ServeConfig) error {
		got = cfg
		return nil
	}

	err := executeCmd(deps, stdout, stderr, "serve", "--workspace-root", "/var/workspaces")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.NamespaceRoot != "/var/workspaces" || got.WorkspaceRoot != "/var/workspaces" {
		t.Fatalf("workspace alias not applied: %+v", got)
	}
}
