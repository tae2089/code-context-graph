package parser

import (
	"context"
	"testing"

	"github.com/tae2089/code-context-graph/internal/app/ingest"
)

// The plugin parser adapter must satisfy the ingest.Parser port.
var _ ingest.Parser = (*Adapter)(nil)

func TestAdapterImplementsParserPort(t *testing.T) {
	c := helperClient(t)
	a := New(c, "go")
	defer a.Close()

	nodes, edges, err := a.ParseWithContext(context.Background(), "src/foo.go", nil)
	if err != nil {
		t.Fatalf("ParseWithContext() error = %v", err)
	}
	if len(nodes) != 1 || nodes[0].QualifiedName != "pkg.Foo" {
		t.Errorf("nodes = %+v, want one pkg.Foo", nodes)
	}
	if len(edges) != 1 || edges[0].Fingerprint != "contains:src/foo.go:pkg.Foo" {
		t.Errorf("edges = %+v, want one contains edge", edges)
	}
}
