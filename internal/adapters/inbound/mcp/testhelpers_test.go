package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/tae2089/code-context-graph/internal/adapters/outbound/graphgorm"
	search "github.com/tae2089/code-context-graph/internal/adapters/outbound/searchsql"
	"github.com/tae2089/code-context-graph/internal/adapters/outbound/treesitter"
	"github.com/tae2089/code-context-graph/internal/app/analyze/changes"
	flows "github.com/tae2089/code-context-graph/internal/app/analyze/flow"
	"github.com/tae2089/code-context-graph/internal/app/analyze/impact"
	"github.com/tae2089/code-context-graph/internal/app/analyze/query"
	"github.com/tae2089/code-context-graph/internal/app/search/retrieval"
	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// simpleGoParser is a minimal Go parser for testing. It extracts package-level
// function declarations from simple Go files without depending on tree-sitter.
type simpleGoParser struct{}

func (p *simpleGoParser) ParseWithContext(ctx context.Context, filePath string, content []byte) ([]graph.Node, []graph.Edge, error) {
	return p.Parse(filePath, content)
}

func (p *simpleGoParser) Parse(filePath string, content []byte) ([]graph.Node, []graph.Edge, error) {
	var nodes []graph.Node
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
				nodes = append(nodes, graph.Node{
					QualifiedName: qn,
					Kind:          graph.NodeKindFunction,
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

func (p *commentAwareGoParser) ParseWithComments(ctx context.Context, filePath string, content []byte) ([]graph.Node, []graph.Edge, []treesitter.CommentBlock, error) {
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

func (p *commentAwareGoParser) ParseWithCommentsAndMetadata(ctx context.Context, filePath string, content []byte) ([]graph.Node, []graph.Edge, []treesitter.CommentBlock, treesitter.ParseMetadata, error) {
	nodes, edges, comments, err := p.ParseWithComments(ctx, filePath, content)
	return nodes, edges, comments, treesitter.ParseMetadata{}, err
}

func (p *commentAwareGoParser) Language() string {
	return "go"
}

var handlerTestDBSeq atomic.Int64

type testDepsState struct {
	db      *gorm.DB
	backend search.Backend
}

var testDepsStates sync.Map

func registerTestDeps(t *testing.T, deps *Deps, db *gorm.DB, backend search.Backend) *Deps {
	t.Helper()
	testDepsStates.Store(deps, testDepsState{db: db, backend: backend})
	t.Cleanup(func() { testDepsStates.Delete(deps) })
	return deps
}

func testDBFor(deps *Deps) *gorm.DB {
	state, _ := testDepsStates.Load(deps)
	return state.(testDepsState).db
}

func testGraphStoreFor(deps *Deps) *graphgorm.Store {
	return graphgorm.New(testDBFor(deps))
}

func testSearchBackendFor(deps *Deps) search.Backend {
	state, _ := testDepsStates.Load(deps)
	return state.(testDepsState).backend
}

func setTestSearchBackend(deps *Deps, backend search.Backend) {
	state, _ := testDepsStates.Load(deps)
	current := state.(testDepsState)
	current.backend = backend
	testDepsStates.Store(deps, current)
	reader := search.NewReader(current.db, backend)
	writer := search.NewSearchWriter(current.db, backend, deps.Runtime.Logger)
	deps.Graph.Search = reader
	deps.Docs.Retrieval = retrieval.New(reader, reader)
	deps.Build.Search = writer
	deps.Build.Maintenance = writer
}

func setTestChangesClient(deps *Deps, client changes.GitClient) {
	deps.Analysis.Changes = changes.New(graphgorm.New(testDBFor(deps)), client)
}

func groupedTestDeps(st *graphgorm.Store, db *gorm.DB, sb search.Backend, parser Parser, log *slog.Logger) *Deps {
	reader := search.NewReader(db, sb)
	writer := search.NewSearchWriter(db, sb, log)
	return &Deps{
		Build: BuildToolsDeps{
			Store:       st,
			Walkers:     map[string]Parser{".go": parser},
			UnitOfWork:  search.NewIngestUnitOfWork(db, sb, log),
			Search:      writer,
			Maintenance: writer,
			FlowBuilder: flows.NewBuilder(st),
		},
		Graph: GraphToolsDeps{
			Store:      st,
			Query:      query.New(st),
			Search:     reader,
			Statistics: st,
			Reader:     st,
		},
		Analysis: AnalysisToolsDeps{
			Impact: impact.New(st), Flow: flows.New(st), Reader: st,
			CrossImpact: impact.New(st.CrossNamespaceReader()),
			CrossFlow:   flows.New(st.CrossNamespaceReader()),
			CrossRefs:   st,
		},
		Docs:    DocsToolsDeps{Retrieval: retrieval.New(reader, reader)},
		Runtime: RuntimeToolsDeps{Logger: log, RepoRoot: os.TempDir()},
	}
}

func setupTestDeps(t *testing.T) *Deps {
	t.Helper()
	dsn := fmt.Sprintf("file:handlertest%d?mode=memory&cache=shared", handlerTestDBSeq.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&graph.SearchDocument{}, &graph.Flow{}, &graph.FlowMembership{}); err != nil {
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
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	return registerTestDeps(t, groupedTestDeps(st, db, sb, goParser, logger), db, sb)
}

// setupTestDepsMinimal creates a Deps with only the core fields initialized.
// Used to test backward compatibility - that old tools work even when new interfaces are nil.
func setupTestDepsMinimal(t *testing.T) *Deps {
	t.Helper()
	dsn := fmt.Sprintf("file:handlertest-minimal%d?mode=memory&cache=shared", handlerTestDBSeq.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&graph.SearchDocument{}, &graph.Flow{}, &graph.FlowMembership{}); err != nil {
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
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	deps := groupedTestDeps(st, db, sb, goParser, logger)
	deps.Build.FlowBuilder = nil
	deps.Graph.Query = nil
	return registerTestDeps(t, deps, db, sb)
}

func setupGraphOnlyTestDeps(t *testing.T) *Deps {
	t.Helper()
	dsn := fmt.Sprintf("file:handlertest-graph-only%d?mode=memory&cache=shared", handlerTestDBSeq.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&graph.Flow{}, &graph.FlowMembership{}); err != nil {
		t.Fatal(err)
	}

	goParser := &simpleGoParser{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	deps := groupedTestDeps(st, db, nil, goParser, logger)
	return registerTestDeps(t, deps, db, nil)
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
	ctx := requestctx.WithNamespace(context.Background(), namespace)
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
	deps.Build.Walkers = map[string]Parser{".go": goParser}
	return deps
}

func callTool(t *testing.T, deps *Deps, toolName string, args map[string]any) *mcp.CallToolResult {
	return callToolWithContext(t, context.Background(), deps, toolName, args)
}

func callToolWithContext(t *testing.T, ctx context.Context, deps *Deps, toolName string, args map[string]any) *mcp.CallToolResult {
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
