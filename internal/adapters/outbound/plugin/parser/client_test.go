package parser

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// TestHelperParserPlugin acts as a mock parser plugin subprocess when GO_WANT_HELPER_PROCESS=1.
// GO_HELPER_MODE selects misbehaviors used to exercise the client's robustness paths.
func TestHelperParserPlugin(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	switch os.Getenv("GO_HELPER_MODE") {
	case "sleep": // never respond and ignore stdin EOF
		time.Sleep(30 * time.Second)
		os.Exit(0)
	case "stderr_die": // emit diagnostics on stderr and exit non-zero without responding
		fmt.Fprintln(os.Stderr, "boom: parser crashed")
		os.Exit(1)
	}

	mode := os.Getenv("GO_HELPER_MODE")
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
			Edges: []wireEdge{{Kind: "contains", FilePath: req.FilePath, ToQN: "pkg.Foo"}},
		}
		if mode == "mismatch" {
			resp.FilePath = "OTHER/" + req.FilePath
		}
		out, _ := json.Marshal(resp)
		fmt.Println(string(out))
	}
	os.Exit(0)
}

func helperClientMode(t *testing.T, mode string) *client {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperParserPlugin")
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
	if mode != "" {
		cmd.Env = append(cmd.Env, "GO_HELPER_MODE="+mode)
	}
	c, err := newClient(cmd)
	if err != nil {
		t.Fatalf("newClient() error = %v", err)
	}
	return c
}

func helperClient(t *testing.T) *client { return helperClientMode(t, "") }

func TestClientParse(t *testing.T) {
	c := helperClient(t)
	defer c.close()

	nodes, edges, err := c.parse(context.Background(), "src/foo.go", "go")
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

// F1: a response whose file_path does not match the request must error and poison the client.
func TestClientRejectsMismatchedResponse(t *testing.T) {
	c := helperClientMode(t, "mismatch")
	defer c.close()

	if _, _, err := c.parse(context.Background(), "src/foo.go", "go"); err == nil {
		t.Fatal("parse() on mismatched response = nil error, want error")
	}
	// Poisoned: subsequent calls fail fast rather than misattributing.
	if _, _, err := c.parse(context.Background(), "src/bar.go", "go"); err == nil {
		t.Error("second parse() after desync = nil error, want fail-fast error")
	}
}

// F2: parse must honor context cancellation instead of blocking on a non-responding plugin.
func TestClientHonorsContextCancellation(t *testing.T) {
	c := helperClientMode(t, "sleep")
	defer c.close()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, _, err := c.parse(ctx, "src/foo.go", "go")
	if err == nil {
		t.Fatal("parse() with cancelled ctx = nil error, want error")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("parse() blocked %v, expected prompt cancellation", elapsed)
	}
}

// F3: close must not block forever when the plugin ignores stdin EOF.
func TestClientCloseKillsHungPlugin(t *testing.T) {
	c := helperClientMode(t, "sleep")
	c.closeTimeout = 100 * time.Millisecond

	start := time.Now()
	err := c.close()
	if err == nil {
		t.Error("close() on hung plugin = nil error, want kill/timeout error")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("close() blocked %v, expected timeout+kill near 100ms", elapsed)
	}
}

// F5: plugin stderr must surface in errors instead of being discarded.
func TestClientCapturesStderrOnFailure(t *testing.T) {
	c := helperClientMode(t, "stderr_die")

	err := c.close()
	if err == nil {
		t.Fatal("close() on crashed plugin = nil error, want exit error")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("close() error = %q, want it to include plugin stderr (\"boom\")", err.Error())
	}
}
