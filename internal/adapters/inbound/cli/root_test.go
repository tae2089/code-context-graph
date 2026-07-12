package cli

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func newTestDeps() (*Deps, *bytes.Buffer, *bytes.Buffer) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return &Deps{Logger: logger}, stdout, stderr
}

func executeCmd(deps *Deps, stdout, stderr *bytes.Buffer, args ...string) error {
	cmd := NewRootCmd(deps)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs(args)
	return cmd.Execute()
}

func executeCmdWithContext(ctx context.Context, deps *Deps, stdout, stderr *bytes.Buffer, args ...string) error {
	cmd := NewRootCmd(deps)
	cmd.SetContext(ctx)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs(args)
	return cmd.Execute()
}

func TestRoot_UnknownCommand(t *testing.T) {
	deps, _, stderr := newTestDeps()

	err := executeCmd(deps, &bytes.Buffer{}, stderr, "frobnicate")
	if err == nil {
		t.Fatal("expected error for unknown command, got nil")
	}
}

func TestRoot_NoCommand(t *testing.T) {
	deps, stdout, stderr := newTestDeps()

	cmd := NewRootCmd(deps)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{})
	_ = cmd.Execute()

	out := stdout.String()
	if len(out) == 0 {
		t.Fatal("expected usage/help output")
	}
}

func TestRoot_CommandContract(t *testing.T) {
	cmd := NewRootCmd(&Deps{})
	want := []string{
		"build",
		"docs",
		"hooks",
		"init",
		"lint",
		"migrate",
		"search",
		"serve",
		"status",
		"update",
		"version",
	}

	got := make([]string, 0, len(cmd.Commands()))
	for _, subcommand := range cmd.Commands() {
		got = append(got, subcommand.Name())
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("root command contract changed\ngot:  %v\nwant: %v", got, want)
	}
}

func TestRoot_MetadataCommandsAreNotRegistered(t *testing.T) {
	for _, name := range []string{"languages", "example", "tags"} {
		t.Run(name, func(t *testing.T) {
			deps, stdout, stderr := newTestDeps()
			err := executeCmd(deps, stdout, stderr, name)
			if err == nil || !strings.Contains(err.Error(), "unknown command") {
				t.Fatalf("execute removed command %q error = %v, want unknown command", name, err)
			}
		})
	}
}

func TestRoot_ServeCommand(t *testing.T) {
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

func TestRoot_SkipDBInitCommandsDoNotCallInitFunc(t *testing.T) {
	commands := []string{"version", "hooks"}

	for _, name := range commands {
		t.Run(name, func(t *testing.T) {
			deps, stdout, stderr := newTestDeps()
			called := 0
			deps.InitFunc = func(dbDriver, dsn string) error {
				called++
				return nil
			}

			if err := executeCmd(deps, stdout, stderr, name); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if called != 0 {
				t.Fatalf("InitFunc called %d times, want 0", called)
			}
		})
	}
}

func TestRoot_DBDependentCommandsStillCallInitFunc(t *testing.T) {
	deps, stdout, stderr := newTestDeps()
	called := 0
	deps.InitFunc = func(dbDriver, dsn string) error {
		called++
		return nil
	}
	deps.ServeFunc = func(cfg ServeConfig) error { return nil }

	if err := executeCmd(deps, stdout, stderr, "serve"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called != 1 {
		t.Fatalf("InitFunc called %d times, want 1", called)
	}
}

func TestRoot_LintMigrateAutoRulesSkipsDBInit(t *testing.T) {
	deps, stdout, stderr := newTestDeps()
	initCalled := 0
	deps.InitFunc = func(dbDriver, dsn string) error {
		initCalled++
		return nil
	}

	dir := t.TempDir()
	cfgFile := filepath.Join(dir, ".ccg.yaml")
	if err := os.WriteFile(cfgFile, []byte(`rules:
  - pattern: "pkg/auto.go::Move"
    category: unannotated
    action: warn
    auto: true
    created: "2026-05-03"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := executeCmd(deps, stdout, stderr, "--config", cfgFile, "lint", "--history-dir", dir, "--migrate-auto-rules"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if initCalled != 0 {
		t.Fatalf("InitFunc called %d times, want 0", initCalled)
	}
	if _, err := os.Stat(filepath.Join(dir, "auto-rules.yaml")); err != nil {
		t.Fatalf("expected auto-rules.yaml to be created: %v", err)
	}
}

func TestRoot_MigrateCommandCallsMigrateFuncOnly(t *testing.T) {
	deps, stdout, stderr := newTestDeps()
	initCalled := 0
	migrateCalled := 0
	deps.InitFunc = func(dbDriver, dsn string) error {
		initCalled++
		return nil
	}
	deps.MigrateFunc = func(cfg MigrateConfig) error {
		migrateCalled++
		if cfg.DBDriver != "sqlite" {
			t.Fatalf("dbDriver = %q, want sqlite", cfg.DBDriver)
		}
		if cfg.DBDSN != "ccg.db" {
			t.Fatalf("dsn = %q, want ccg.db", cfg.DBDSN)
		}
		if cfg.MigrationsDir != "" {
			t.Fatalf("migrationsDir = %q, want empty default", cfg.MigrationsDir)
		}
		return nil
	}

	if err := executeCmd(deps, stdout, stderr, "migrate"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if initCalled != 0 {
		t.Fatalf("InitFunc called %d times, want 0", initCalled)
	}
	if migrateCalled != 1 {
		t.Fatalf("MigrateFunc called %d times, want 1", migrateCalled)
	}
	if got := stdout.String(); got != "Migration complete\n" {
		t.Fatalf("stdout = %q, want migration completion", got)
	}
}

func TestRoot_MigrateCommandPassesMigrationsDirFlag(t *testing.T) {
	deps, stdout, stderr := newTestDeps()
	var got MigrateConfig
	deps.MigrateFunc = func(cfg MigrateConfig) error {
		got = cfg
		return nil
	}

	if err := executeCmd(deps, stdout, stderr, "migrate", "--migrations-dir", "/tmp/ccg-migrations"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.MigrationsDir != "/tmp/ccg-migrations" {
		t.Fatalf("migrationsDir = %q, want flag value", got.MigrationsDir)
	}
}

func TestRoot_HooksInstallSkipsInitFuncViaParentAnnotation(t *testing.T) {
	deps, stdout, stderr := newTestDeps()
	called := 0
	deps.InitFunc = func(dbDriver, dsn string) error {
		called++
		return nil
	}
	repoDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoDir, ".git", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := executeCmd(deps, stdout, stderr, "hooks", "install", "--git-dir", repoDir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called != 0 {
		t.Fatalf("InitFunc called %d times, want 0", called)
	}
}

func TestRoot_MissingConfigFileIsIgnored(t *testing.T) {
	viper.Reset()
	defer viper.Reset()

	deps, stdout, stderr := newTestDeps()
	if err := executeCmd(deps, stdout, stderr, "--config", filepath.Join(t.TempDir(), "missing.yaml"), "version"); err != nil {
		t.Fatalf("expected missing config to be ignored, got %v", err)
	}
}

func TestRoot_MalformedConfigFails(t *testing.T) {
	viper.Reset()
	defer viper.Reset()

	deps, stdout, stderr := newTestDeps()
	configPath := filepath.Join(t.TempDir(), ".ccg.yaml")
	if err := os.WriteFile(configPath, []byte("db:\n  driver: [unterminated\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := executeCmd(deps, stdout, stderr, "--config", configPath, "version"); err == nil {
		t.Fatal("expected malformed config to fail")
	}
}

func TestRoot_DefaultNamespaceFlagValue(t *testing.T) {
	deps, _, _ := newTestDeps()
	cmd := NewRootCmd(deps)

	got, err := cmd.PersistentFlags().GetString("namespace")
	if err != nil {
		t.Fatalf("get namespace flag: %v", err)
	}
	if got != "default" {
		t.Fatalf("namespace default = %q, want %q", got, "default")
	}
}
