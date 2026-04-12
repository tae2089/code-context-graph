// @index Apache AGE 그래프 저장소. PostgreSQL SQL로 Cypher 쿼리를 실행하여 코드 그래프를 관리한다.
package agestore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	_ "github.com/lib/pq"

	"github.com/imtaebin/code-context-graph/internal/model"
)

const graphName = "code_graph"

// Store wraps a PostgreSQL connection with Apache AGE extension.
// Cypher queries are executed as plain SQL — no AGE driver needed.
type Store struct {
	db *sql.DB
}

// New creates a new AGE graph store from a PostgreSQL DSN.
// dsn example: "host=127.0.0.1 port=5455 dbname=ccg user=ccg password=ccg sslmode=disable"
func New(dsn string) (*Store, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return &Store{db: db}, nil
}

// Init loads the AGE extension and creates the graph if it doesn't exist.
func (s *Store) Init(ctx context.Context) error {
	stmts := []string{
		"CREATE EXTENSION IF NOT EXISTS age",
		"LOAD 'age'",
		`SET search_path = ag_catalog, "$user", public`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("exec %q: %w", stmt, err)
		}
	}

	var count int
	err := s.db.QueryRowContext(ctx,
		"SELECT count(*) FROM ag_catalog.ag_graph WHERE name = $1", graphName).Scan(&count)
	if err != nil {
		return fmt.Errorf("check graph: %w", err)
	}
	if count == 0 {
		_, err := s.db.ExecContext(ctx,
			fmt.Sprintf("SELECT * FROM ag_catalog.create_graph('%s')", graphName))
		if err != nil {
			return fmt.Errorf("create graph: %w", err)
		}
	}
	return nil
}

// ClearGraph drops and recreates the graph.
func (s *Store) ClearGraph(ctx context.Context) error {
	s.ensureAGE(ctx)
	_, _ = s.db.ExecContext(ctx,
		fmt.Sprintf("SELECT * FROM ag_catalog.drop_graph('%s', true)", graphName))
	_, err := s.db.ExecContext(ctx,
		fmt.Sprintf("SELECT * FROM ag_catalog.create_graph('%s')", graphName))
	return err
}

// SyncNodes creates vertices in the AGE graph.
func (s *Store) SyncNodes(ctx context.Context, nodes []model.Node) error {
	s.ensureAGE(ctx)
	for _, n := range nodes {
		label := labelFor(n.Kind)
		cypher := fmt.Sprintf(
			"CREATE (:%s {node_id: %d, qualified_name: '%s', name: '%s', kind: '%s', file_path: '%s', language: '%s', start_line: %d, end_line: %d})",
			label, n.ID, esc(n.QualifiedName), esc(n.Name), esc(string(n.Kind)),
			esc(n.FilePath), esc(n.Language), n.StartLine, n.EndLine,
		)
		if err := s.execCypher(ctx, cypher); err != nil {
			return fmt.Errorf("create node %s: %w", n.QualifiedName, err)
		}
	}
	return nil
}

// SyncEdges creates edges in the AGE graph.
func (s *Store) SyncEdges(ctx context.Context, edges []model.Edge) error {
	s.ensureAGE(ctx)
	for _, e := range edges {
		if e.FromNodeID == 0 || e.ToNodeID == 0 {
			continue
		}
		label := strings.ToUpper(string(e.Kind))
		cypher := fmt.Sprintf(
			"MATCH (a {node_id: %d}), (b {node_id: %d}) CREATE (a)-[:%s {line: %d}]->(b)",
			e.FromNodeID, e.ToNodeID, label, e.Line,
		)
		// Skip duplicate edge errors silently
		_ = s.execCypher(ctx, cypher)
	}
	return nil
}

// ExecuteCypher runs an arbitrary Cypher query and returns results as JSON strings.
// This is the MCP-facing method — Claude writes the Cypher, this method executes it.
func (s *Store) ExecuteCypher(ctx context.Context, cypher string, columnCount int) ([][]string, error) {
	s.ensureAGE(ctx)

	if columnCount < 1 {
		columnCount = 1
	}

	// Build column definitions: (v0 agtype, v1 agtype, ...)
	cols := make([]string, columnCount)
	for i := 0; i < columnCount; i++ {
		cols[i] = fmt.Sprintf("v%d ag_catalog.agtype", i)
	}
	colDef := strings.Join(cols, ", ")

	query := fmt.Sprintf(
		"SELECT * FROM ag_catalog.cypher('%s', $$ %s $$) AS (%s)",
		graphName, cypher, colDef,
	)

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("cypher: %w", err)
	}
	defer rows.Close()

	var results [][]string
	scanArgs := make([]interface{}, columnCount)
	scanPtrs := make([]string, columnCount)
	for i := range scanArgs {
		scanArgs[i] = &scanPtrs[i]
	}

	for rows.Next() {
		if err := rows.Scan(scanArgs...); err != nil {
			continue
		}
		row := make([]string, columnCount)
		copy(row, scanPtrs)
		results = append(results, row)
	}
	return results, nil
}

// ExecuteCypherJSON runs a Cypher query and returns the result as a JSON string.
func (s *Store) ExecuteCypherJSON(ctx context.Context, cypher string, columnCount int) (string, error) {
	results, err := s.ExecuteCypher(ctx, cypher, columnCount)
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(map[string]any{
		"cypher":  cypher,
		"results": results,
		"count":   len(results),
	})
	return string(b), nil
}

// Close closes the connection.
func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) execCypher(ctx context.Context, cypher string) error {
	query := fmt.Sprintf(
		"SELECT * FROM ag_catalog.cypher('%s', $$ %s $$) AS (v ag_catalog.agtype)",
		graphName, cypher,
	)
	_, err := s.db.ExecContext(ctx, query)
	return err
}

func (s *Store) ensureAGE(ctx context.Context) {
	s.db.ExecContext(ctx, "LOAD 'age'")
	s.db.ExecContext(ctx, `SET search_path = ag_catalog, "$user", public`)
}

func labelFor(kind model.NodeKind) string {
	switch kind {
	case model.NodeKindFunction:
		return "Function"
	case model.NodeKindClass:
		return "Class"
	case model.NodeKindType:
		return "Type"
	case model.NodeKindTest:
		return "Test"
	case model.NodeKindFile:
		return "File"
	default:
		return "Node"
	}
}

func esc(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "'", "\\'")
	return s
}
