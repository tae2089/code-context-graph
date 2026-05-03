package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/tae2089/code-context-graph/internal/analysis/flows"
	"github.com/tae2089/code-context-graph/internal/analysis/impact"
	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
	postprocesspolicy "github.com/tae2089/code-context-graph/internal/postprocess/policy"
	"github.com/tae2089/code-context-graph/internal/parse/treesitter"
	"github.com/tae2089/code-context-graph/internal/store/gormstore"
	"github.com/tae2089/code-context-graph/internal/store/search"
)

// simpleGoParser is a minimal Go parser for testing. It extracts package-level
// function declarations from simple Go files without depending on tree-sitter.
type simpleGoParser struct{}

func (p *simpleGoParser) ParseWithContext(ctx context.Context, filePath string, content []byte) ([]model.Node, []model.Edge, error) {
	return p.Parse(filePath, content)
}

func (p *simpleGoParser) Parse(filePath string, content []byte) ([]model.Node, []model.Edge, error) {
	var nodes []model.Node
	lines := strings.Split(string(content), "\n")

	var pkgName string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "package ") {
			pkgName = strings.TrimPrefix(trimmed, "package ")
			break
		}
	}

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "func ") {
			// Extract function name
			rest := strings.TrimPrefix(trimmed, "func ")
			parenIdx := strings.Index(rest, "(")
			if parenIdx > 0 {
				name := rest[:parenIdx]
				qn := pkgName + "." + name
				// Find end of function (next closing brace)
				endLine := i + 1
				braceCount := 0
				for j := i; j < len(lines); j++ {
					for _, ch := range lines[j] {
						if ch == '{' {
							braceCount++
						} else if ch == '}' {
							braceCount--
							if braceCount == 0 {
								endLine = j + 1
								goto done
							}
						}
					}
				}
			done:
				nodes = append(nodes, model.Node{
					QualifiedName: qn,
					Kind:          model.NodeKindFunction,
					Name:          name,
					FilePath:      filePath,
					StartLine:     i + 1,
					EndLine:       endLine,
					Language:      "go",
				})
			}
		}
	}
	return nodes, nil, nil
}

type commentAwareGoParser struct {
	simpleGoParser
}

func (p *commentAwareGoParser) ParseWithComments(ctx context.Context, filePath string, content []byte) ([]model.Node, []model.Edge, []treesitter.CommentBlock, error) {
	nodes, edges, err := p.Parse(filePath, content)
	if err != nil {
		return nil, nil, nil, err
	}

	lines := strings.Split(string(content), "\n")
	var comments []treesitter.CommentBlock

	i := 0
	for i < len(lines) {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "//") {
			start := i + 1
			var textLines []string
			for i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "//") {
				textLines = append(textLines, strings.TrimSpace(lines[i]))
				i++
			}
			end := i
			comments = append(comments, treesitter.CommentBlock{
				StartLine: start,
				EndLine:   end,
				Text:      strings.Join(textLines, "\n"),
			})
		} else {
			i++
		}
	}

	return nodes, edges, comments, nil
}

func (p *commentAwareGoParser) Language() string {
	return "go"
}

var handlerTestDBSeq atomic.Int64

func setupTestDeps(t *testing.T) *Deps {
	t.Helper()
	dsn := fmt.Sprintf("file:handlertest%d?mode=memory&cache=shared", handlerTestDBSeq.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.SearchDocument{}, &model.Flow{}, &model.FlowMembership{}); err != nil {
		t.Fatal(err)
	}
	sb := search.NewSQLiteBackend()
	if err := sb.Migrate(db); err != nil {
		if errors.Is(err, search.ErrFTS5NotAvailable) {
			t.Skip("fts5 module not available, skipping test")
		}
		t.Fatal(err)
	}

	goParser := &simpleGoParser{}
	return &Deps{
		Store:          st,
		DB:             db,
		Parser:         goParser,
		Walkers:        map[string]Parser{".go": goParser},
		SearchBackend:  sb,
		ImpactAnalyzer: impact.New(st),
		FlowTracer:     flows.New(st),
		FlowBuilder:    flows.NewBuilder(db, st),
		PostprocessPolicy: &stubPostprocessPolicy{
			resolvedPolicy: postprocesspolicy.PolicyDegraded,
			resolvedSource: postprocesspolicy.SourceAuto,
		},
		RepoRoot:       os.TempDir(),
	}
}

func setupGraphOnlyTestDeps(t *testing.T) *Deps {
	t.Helper()
	dsn := fmt.Sprintf("file:handlertest-graph-only%d?mode=memory&cache=shared", handlerTestDBSeq.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Flow{}, &model.FlowMembership{}); err != nil {
		t.Fatal(err)
	}

	goParser := &simpleGoParser{}
	return &Deps{
		Store:          st,
		DB:             db,
		Parser:         goParser,
		Walkers:        map[string]Parser{".go": goParser},
		ImpactAnalyzer: impact.New(st),
		FlowTracer:     flows.New(st),
		FlowBuilder:    flows.NewBuilder(db, st),
		RepoRoot:       os.TempDir(),
	}
}

type stubPostprocessPolicy struct {
	resolvedPolicy string
	resolvedSource string
	resolveErr     error
	recordErr      error
	resolvedInputs []postprocesspolicy.DecisionInput
	recordedRuns   []postprocesspolicy.RunRecord
}

func (s *stubPostprocessPolicy) Resolve(ctx context.Context, input postprocesspolicy.DecisionInput) (string, string, error) {
	s.resolvedInputs = append(s.resolvedInputs, input)
	if s.resolveErr != nil {
		return "", "", s.resolveErr
	}
	return s.resolvedPolicy, s.resolvedSource, nil
}

func (s *stubPostprocessPolicy) RecordRun(ctx context.Context, record postprocesspolicy.RunRecord) error {
	s.recordedRuns = append(s.recordedRuns, record)
	return s.recordErr
}

type realPostprocessPolicy struct {
	engine *postprocesspolicy.Engine
	store  *postprocesspolicy.Store
}

func newRealPostprocessPolicy(db *gorm.DB) *realPostprocessPolicy {
	return &realPostprocessPolicy{
		engine: &postprocesspolicy.Engine{},
		store:  postprocesspolicy.NewStore(db),
	}
}

func (p *realPostprocessPolicy) Resolve(ctx context.Context, input postprocesspolicy.DecisionInput) (string, string, error) {
	return p.engine.Resolve(ctx, p.store, input)
}

func (p *realPostprocessPolicy) RecordRun(ctx context.Context, record postprocesspolicy.RunRecord) error {
	return p.store.RecordRun(ctx, record)
}

func setupTestDepsWithRealPostprocessPolicy(t *testing.T) *Deps {
	t.Helper()
	deps := setupTestDeps(t)
	deps.PostprocessPolicy = newRealPostprocessPolicy(deps.DB)
	return deps
}

func callToolWithNamespace(t *testing.T, deps *Deps, namespace, toolName string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	srv := NewServer(deps)
	argsCopy := make(map[string]any, len(args)+1)
	for k, v := range args {
		argsCopy[k] = v
	}
	argsJSON, _ := json.Marshal(argsCopy)
	msg, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      toolName,
			"arguments": json.RawMessage(argsJSON),
		},
	})
	ctx := ctxns.WithNamespace(context.Background(), namespace)
	resp := srv.HandleMessage(ctx, msg)
	rpcResp, ok := resp.(mcp.JSONRPCResponse)
	if !ok {
		errResp, isErr := resp.(mcp.JSONRPCError)
		if isErr {
			t.Fatalf("JSON-RPC error: code=%d msg=%s", errResp.Error.Code, errResp.Error.Message)
		}
		t.Fatalf("unexpected response type: %T", resp)
	}
	resultJSON, err := json.Marshal(rpcResp.Result)
	if err != nil {
		t.Fatal(err)
	}
	var result mcp.CallToolResult
	if err := json.Unmarshal(resultJSON, &result); err != nil {
		t.Fatal(err)
	}
	return &result
}

func setupTestDepsWithComments(t *testing.T) *Deps {
	t.Helper()
	deps := setupTestDeps(t)
	goParser := &commentAwareGoParser{}
	deps.Parser = goParser
	deps.Walkers = map[string]Parser{".go": goParser}
	return deps
}

func callTool(t *testing.T, deps *Deps, toolName string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	srv := NewServer(deps)

	argsJSON, _ := json.Marshal(args)
	msg, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      toolName,
			"arguments": json.RawMessage(argsJSON),
		},
	})

	resp := srv.HandleMessage(context.Background(), msg)
	rpcResp, ok := resp.(mcp.JSONRPCResponse)
	if !ok {
		errResp, isErr := resp.(mcp.JSONRPCError)
		if isErr {
			t.Fatalf("JSON-RPC error: code=%d msg=%s", errResp.Error.Code, errResp.Error.Message)
		}
		t.Fatalf("unexpected response type: %T", resp)
	}

	resultJSON, err := json.Marshal(rpcResp.Result)
	if err != nil {
		t.Fatal(err)
	}
	var result mcp.CallToolResult
	if err := json.Unmarshal(resultJSON, &result); err != nil {
		t.Fatal(err)
	}
	return &result
}

func getTextContent(result *mcp.CallToolResult) string {
	if len(result.Content) == 0 {
		return ""
	}
	tc, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		return ""
	}
	return tc.Text
}
