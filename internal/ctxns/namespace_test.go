package ctxns

import (
	"context"
	"testing"
)

func TestWithNamespace_setsNamespaceInContext(t *testing.T) {
	ctx := WithNamespace(context.Background(), "pay")
	got := FromContext(ctx)
	if got != "pay" {
		t.Errorf("FromContext() = %q, want %q", got, "pay")
	}
}

func TestFromContext_emptyWhenNotSet(t *testing.T) {
	ctx := context.Background()
	got := FromContext(ctx)
	if got != "" {
		t.Errorf("FromContext() = %q, want empty string", got)
	}
}
