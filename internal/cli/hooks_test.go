package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHooksInstall_CreatesPreCommitHook(t *testing.T) {
	repoDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoDir, ".git", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}

	deps, stdout, stderr := newTestDeps()
	if err := executeCmd(deps, stdout, stderr, "hooks", "install", "--git-dir", repoDir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hookPath := filepath.Join(repoDir, ".git", "hooks", "pre-commit")
	content, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("expected pre-commit hook at %s: %v", hookPath, err)
	}

	hook := string(content)
	for _, want := range []string{"#!/bin/sh", "ccg build", "ccg docs", "ccg lint"} {
		if !strings.Contains(hook, want) {
			t.Errorf("expected %q in hook, got:\n%s", want, hook)
		}
	}

	// Hook must be executable
	info, err := os.Stat(hookPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o111 == 0 {
		t.Errorf("expected hook to be executable, mode: %v", info.Mode())
	}
}

func TestHooksInstall_NoGitDir_Fails(t *testing.T) {
	repoDir := t.TempDir()
	// No .git directory

	deps, stdout, stderr := newTestDeps()
	err := executeCmd(deps, stdout, stderr, "hooks", "install", "--git-dir", repoDir)
	if err == nil {
		t.Fatal("expected error when .git dir does not exist")
	}
}

func TestHooksInstall_ExistingHook_AppendsWithGuard(t *testing.T) {
	repoDir := t.TempDir()
	hookDir := filepath.Join(repoDir, ".git", "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}

	existing := "#!/bin/sh\necho 'existing hook'\n"
	hookPath := filepath.Join(hookDir, "pre-commit")
	if err := os.WriteFile(hookPath, []byte(existing), 0o755); err != nil {
		t.Fatal(err)
	}

	deps, stdout, stderr := newTestDeps()
	if err := executeCmd(deps, stdout, stderr, "hooks", "install", "--git-dir", repoDir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatal(err)
	}

	hook := string(content)
	if !strings.Contains(hook, "existing hook") {
		t.Errorf("expected original hook content to be preserved")
	}
	if !strings.Contains(hook, "ccg build") {
		t.Errorf("expected ccg build to be appended")
	}
}

func TestHooksInstall_Idempotent(t *testing.T) {
	repoDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoDir, ".git", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}

	deps, stdout, stderr := newTestDeps()
	// Install twice
	for i := 0; i < 2; i++ {
		stdout.Reset()
		if err := executeCmd(deps, stdout, stderr, "hooks", "install", "--git-dir", repoDir); err != nil {
			t.Fatalf("install %d: unexpected error: %v", i+1, err)
		}
	}

	hookPath := filepath.Join(repoDir, ".git", "hooks", "pre-commit")
	content, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatal(err)
	}

	// Should not have duplicate ccg blocks
	hook := string(content)
	first := strings.Index(hook, "ccg build")
	last := strings.LastIndex(hook, "ccg build")
	if first != last {
		t.Errorf("ccg build appears multiple times in hook (not idempotent):\n%s", hook)
	}
}
