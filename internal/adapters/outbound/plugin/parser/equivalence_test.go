package parser

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"testing"

	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

const fixtureFile = "src/foo.go"

// richPayload is the response the rich mock plugin returns for any request: two nodes and one
// edge of every plugin-emittable kind. It stands in for the full shape treesitter produces.
func richPayload(filePath string) parseResponse {
	return parseResponse{
		FilePath: filePath,
		Nodes: []wireNode{
			{QualifiedName: "pkg.Foo", Kind: "class", Name: "Foo", FilePath: filePath, StartLine: 1, EndLine: 20, Language: "go"},
			{QualifiedName: "pkg.Foo.bar", Kind: "function", Name: "bar", FilePath: filePath, StartLine: 5, EndLine: 12, Language: "go"},
		},
		Edges: []wireEdge{
			{Kind: "contains", FilePath: filePath, ToQN: "pkg.Foo"},
			{Kind: "calls", FilePath: filePath, Line: 12, ToName: "helper"},
			{Kind: "imports_from", FilePath: filePath, Line: 3, ImportPath: "github.com/acme/bar"},
			{Kind: "tested_by", FilePath: filePath, ProdName: "pkg.Foo", TestQN: "pkg.TestFoo"},
			{Kind: "implements", FilePath: filePath, ImplQN: "pkg.Foo", IfaceName: "Reader"},
			{Kind: "inherits", FilePath: filePath, ChildQN: "pkg.Foo", ParentName: "Base"},
		},
	}
}

// TestHelperRichPlugin is a mock plugin subprocess emitting the full node/edge shape.
func TestHelperRichPlugin(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var req parseRequest
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			continue
		}
		out, _ := json.Marshal(richPayload(req.FilePath))
		fmt.Println(string(out))
	}
	os.Exit(0)
}

// TestEquivalenceWireLossless proves the subprocess round-trip faithfully reconstructs every
// node field and every edge-kind fingerprint in the exact format the built-in treesitter emits.
func TestEquivalenceWireLossless(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperRichPlugin")
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
	c, err := newClient(cmd)
	if err != nil {
		t.Fatalf("newClient() error = %v", err)
	}
	a := New(c, "go")
	defer a.Close()

	nodes, edges, err := a.ParseWithContext(context.Background(), fixtureFile, nil)
	if err != nil {
		t.Fatalf("ParseWithContext() error = %v", err)
	}

	// Nodes reconstruct exactly.
	wantNodes := []graph.Node{
		{QualifiedName: "pkg.Foo", Kind: graph.NodeKindClass, Name: "Foo", FilePath: fixtureFile, StartLine: 1, EndLine: 20, Language: "go"},
		{QualifiedName: "pkg.Foo.bar", Kind: graph.NodeKindFunction, Name: "bar", FilePath: fixtureFile, StartLine: 5, EndLine: 12, Language: "go"},
	}
	if len(nodes) != len(wantNodes) {
		t.Fatalf("nodes = %d, want %d", len(nodes), len(wantNodes))
	}
	for i, want := range wantNodes {
		if nodes[i] != want {
			t.Errorf("node[%d] = %+v, want %+v", i, nodes[i], want)
		}
	}

	// Every edge kind reconstructs with the exact treesitter-format fingerprint.
	wantFingerprints := map[graph.EdgeKind]string{
		graph.EdgeKindContains:    "contains:" + fixtureFile + ":pkg.Foo",
		graph.EdgeKindCalls:       "calls:" + fixtureFile + ":helper:12",
		graph.EdgeKindImportsFrom: "imports_from:" + fixtureFile + ":github.com/acme/bar:3",
		graph.EdgeKindTestedBy:    "tested_by:" + fixtureFile + ":pkg.Foo:pkg.TestFoo",
		graph.EdgeKindImplements:  "implements:" + fixtureFile + ":pkg.Foo:Reader",
		graph.EdgeKindInherits:    graph.BuildInheritsFingerprintV2(fixtureFile, "pkg.Foo", "Base"),
	}
	if len(edges) != len(wantFingerprints) {
		t.Fatalf("edges = %d, want %d", len(edges), len(wantFingerprints))
	}
	gotByKind := make(map[graph.EdgeKind]graph.Edge, len(edges))
	for _, e := range edges {
		gotByKind[e.Kind] = e
	}
	for kind, wantFP := range wantFingerprints {
		got, ok := gotByKind[kind]
		if !ok {
			t.Errorf("missing edge kind %q", kind)
			continue
		}
		if got.Fingerprint != wantFP {
			t.Errorf("edge %q fingerprint = %q, want %q", kind, got.Fingerprint, wantFP)
		}
		if got.FilePath != fixtureFile {
			t.Errorf("edge %q FilePath = %q, want %q", kind, got.FilePath, fixtureFile)
		}
	}
}
