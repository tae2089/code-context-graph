package mcpruntime

import (
	"context"
	"log/slog"
	"testing"

	"github.com/tae2089/code-context-graph/internal/adapters/inbound/mcp"
)

func TestInstanceCloseIsIdempotent(t *testing.T) {
	shutdowns := 0
	inst := &Instance{
		Cache:  mcp.NewCache(0),
		logger: slog.Default(),
		shutdown: func(context.Context) error {
			shutdowns++
			return nil
		},
	}

	inst.Close()
	inst.Close()

	if shutdowns != 1 {
		t.Fatalf("telemetry shutdowns = %d, want 1", shutdowns)
	}
}
