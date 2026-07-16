package main

import (
	"log/slog"
	"testing"

	ccgruntime "github.com/tae2089/code-context-graph/internal/runtime"
)

func TestWebhookSecretFlagUsesEnvironmentDefaultAndAllowsExplicitOverride(t *testing.T) {
	t.Setenv("CCG_WEBHOOK_SECRET", "environment-test-value")
	rt := ccgruntime.NewRuntime(slog.Default())
	defer rt.Close()
	cmd := newRootCmd(rt, "test")

	got, err := cmd.Flags().GetString("webhook-secret")
	if err != nil {
		t.Fatalf("get webhook-secret flag: %v", err)
	}
	if got != "environment-test-value" {
		t.Fatal("webhook-secret flag did not preserve the environment default")
	}
	if err := cmd.Flags().Set("webhook-secret", "explicit-test-value"); err != nil {
		t.Fatalf("set webhook-secret flag: %v", err)
	}
	got, err = cmd.Flags().GetString("webhook-secret")
	if err != nil {
		t.Fatalf("get overridden webhook-secret flag: %v", err)
	}
	if got != "explicit-test-value" {
		t.Fatal("explicit webhook-secret flag did not override the environment default")
	}
}
