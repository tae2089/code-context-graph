package workflow

import (
	"context"
	"testing"

	ingestapp "github.com/tae2089/code-context-graph/internal/app/ingest"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

type parserWithoutPackageCapabilities struct {
	language string
}

func (p parserWithoutPackageCapabilities) Parse(string, []byte) ([]graph.Node, []graph.Edge, error) {
	return nil, nil, nil
}

func (p parserWithoutPackageCapabilities) ParseWithContext(context.Context, string, []byte) ([]graph.Node, []graph.Edge, error) {
	return nil, nil, nil
}

func (p parserWithoutPackageCapabilities) Language() string { return p.language }

type packageCapableParser struct {
	parserWithoutPackageCapabilities
}

func (p packageCapableParser) DiscoverPackages(context.Context, ingestapp.PackageDiscoveryOptions) (map[string]ingestapp.PackageInfo, error) {
	return map[string]ingestapp.PackageInfo{"pkg": {ImportPath: "pkg", Language: p.language}}, nil
}

func (p packageCapableParser) PackageEdges(ingestapp.PackageContext) []graph.Edge {
	return []graph.Edge{{Kind: graph.EdgeKindImplements, Fingerprint: "fallback"}}
}

func TestPackageDiscoverers_FallsBackToWalkerOptionalCapability(t *testing.T) {
	fallback := packageCapableParser{parserWithoutPackageCapabilities{language: "go"}}
	svc := &Service{
		Parsers: map[string]Parser{".go": parserWithoutPackageCapabilities{language: "go"}},
		Walkers: map[string]Parser{".go": fallback},
	}

	discoverers := svc.packageDiscoverers()
	if len(discoverers) != 1 {
		t.Fatalf("packageDiscoverers returned %d capabilities, want walker fallback", len(discoverers))
	}
	if _, ok := discoverers[0].(packageCapableParser); !ok {
		t.Fatalf("packageDiscoverers selected %T, want packageCapableParser", discoverers[0])
	}
}

func TestPackageEdgeBuilder_FallsBackToWalkerOptionalCapability(t *testing.T) {
	fallback := packageCapableParser{parserWithoutPackageCapabilities{language: "go"}}
	svc := &Service{
		Parsers: map[string]Parser{".go": parserWithoutPackageCapabilities{language: "go"}},
		Walkers: map[string]Parser{".go": fallback},
	}

	builder := svc.packageEdgeBuilder("go")
	if builder == nil {
		t.Fatal("packageEdgeBuilder returned nil, want walker fallback")
	}
	if got := builder.PackageEdges(ingestapp.PackageContext{}); len(got) != 1 || got[0].Fingerprint != "fallback" {
		t.Fatalf("walker fallback returned unexpected edges: %#v", got)
	}
}
