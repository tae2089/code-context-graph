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

func TestFromContext_emptyWhenNotSet_returnsDefault(t *testing.T) {
	ctx := context.Background()
	got := FromContext(ctx)
	if got != "default" {
		t.Errorf("FromContext() = %q, want %q", got, "default")
	}
}

func TestWithNamespace_emptyStringNormalizesToDefault(t *testing.T) {
	ctx := WithNamespace(context.Background(), "")
	got := FromContext(ctx)
	if got != "default" {
		t.Errorf("FromContext() = %q, want %q", got, "default")
	}
}
