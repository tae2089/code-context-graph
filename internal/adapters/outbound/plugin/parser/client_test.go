package parser

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"testing"

	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// TestHelperParserPlugin acts as a mock parser plugin subprocess when GO_WANT_HELPER_PROCESS=1.
// It reads NDJSON parse requests on stdin and emits one NDJSON result per request on stdout.
func TestHelperParserPlugin(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var req parseRequest
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			continue
		}
		resp := parseResponse{
			FilePath: req.FilePath,
			Nodes: []wireNode{{
				QualifiedName: "pkg.Foo",
				Kind:          "class",
				Name:          "Foo",
				FilePath:      req.FilePath,
				StartLine:     1,
				EndLine:       5,
				Language:      req.Language,
			}},
			Edges: []wireEdge{{
				Kind:     "contains",
				FilePath: req.FilePath,
				ToQN:     "pkg.Foo",
			}},
		}
		out, _ := json.Marshal(resp)
		fmt.Println(string(out))
	}
	os.Exit(0)
}

func helperClient(t *testing.T) *client {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperParserPlugin")
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
	c, err := newClient(cmd)
	if err != nil {
		t.Fatalf("newClient() error = %v", err)
	}
	return c
}

func TestClientParse(t *testing.T) {
	c := helperClient(t)
	defer c.close()

	nodes, edges, err := c.parse("src/foo.go", "go")
	if err != nil {
		t.Fatalf("parse() error = %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(nodes))
	}
	if nodes[0].QualifiedName != "pkg.Foo" || nodes[0].Kind != graph.NodeKindClass {
		t.Errorf("node = %+v, want class pkg.Foo", nodes[0])
	}
	if nodes[0].Language != "go" {
		t.Errorf("node language = %q, want go", nodes[0].Language)
	}
	if len(edges) != 1 {
		t.Fatalf("edges = %d, want 1", len(edges))
	}
	if want := "contains:src/foo.go:pkg.Foo"; edges[0].Fingerprint != want {
		t.Errorf("edge fingerprint = %q, want %q", edges[0].Fingerprint, want)
	}
}
