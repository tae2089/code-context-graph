package cli

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/spf13/cobra"
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

func findSubCmd(root *cobra.Command, name string) *cobra.Command {
	for _, c := range root.Commands() {
		if c.Name() == name {
			return c
		}
	}
	return nil
}
