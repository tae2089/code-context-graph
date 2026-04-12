// @index PostgreSQL + pgvector 저장소. 코드 그래프 동기화와 시맨틱 검색용 임베딩 문서를 관리한다.
package pgstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	_ "github.com/lib/pq"

	"github.com/imtaebin/code-context-graph/internal/model"
)

// Store wraps a PostgreSQL connection for pgvector operations.
type Store struct {
	db *sql.DB
}

// New creates a new PostgreSQL store.
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

// InitPGVector creates the pgvector extension and documents table.
func (s *Store) InitPGVector(ctx context.Context) error {
	stmts := []string{
		"CREATE EXTENSION IF NOT EXISTS vector",
		`CREATE TABLE IF NOT EXISTS ccg_documents (
			id SERIAL PRIMARY KEY,
			node_id INTEGER NOT NULL,
			content TEXT NOT NULL,
			metadata JSONB,
			embedding vector(1536),
			created_at TIMESTAMP DEFAULT NOW()
		)`,
		"CREATE INDEX IF NOT EXISTS idx_ccg_documents_node_id ON ccg_documents(node_id)",
	}
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("pgvector init: %w", err)
		}
	}
	return nil
}

// SyncNodes stores nodes in a relational table for reference.
func (s *Store) SyncNodes(ctx context.Context, nodes []model.Node) error {
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS ccg_nodes (
		id INTEGER PRIMARY KEY,
		qualified_name TEXT NOT NULL,
		name TEXT NOT NULL,
		kind TEXT NOT NULL,
		file_path TEXT NOT NULL,
		language TEXT,
		start_line INTEGER,
		end_line INTEGER
	)`)
	if err != nil {
		return fmt.Errorf("create ccg_nodes: %w", err)
	}

	s.db.ExecContext(ctx, "DELETE FROM ccg_nodes")

	for _, n := range nodes {
		_, err := s.db.ExecContext(ctx,
			"INSERT INTO ccg_nodes (id, qualified_name, name, kind, file_path, language, start_line, end_line) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)",
			n.ID, n.QualifiedName, n.Name, string(n.Kind), n.FilePath, n.Language, n.StartLine, n.EndLine,
		)
		if err != nil {
			return fmt.Errorf("insert node %s: %w", n.QualifiedName, err)
		}
	}
	return nil
}

// SyncEdges stores edges in a relational table for reference.
func (s *Store) SyncEdges(ctx context.Context, edges []model.Edge) error {
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS ccg_edges (
		id INTEGER PRIMARY KEY,
		from_node_id INTEGER NOT NULL,
		to_node_id INTEGER NOT NULL,
		kind TEXT NOT NULL,
		line INTEGER
	)`)
	if err != nil {
		return fmt.Errorf("create ccg_edges: %w", err)
	}

	s.db.ExecContext(ctx, "DELETE FROM ccg_edges")

	for _, e := range edges {
		if e.FromNodeID == 0 || e.ToNodeID == 0 {
			continue
		}
		_, err := s.db.ExecContext(ctx,
			"INSERT INTO ccg_edges (id, from_node_id, to_node_id, kind, line) VALUES ($1, $2, $3, $4, $5)",
			e.ID, e.FromNodeID, e.ToNodeID, string(e.Kind), e.Line,
		)
		if err != nil {
			continue // skip duplicates
		}
	}
	return nil
}

// PGVectorDocument represents a document for pgvector embedding.
type PGVectorDocument struct {
	NodeID   uint
	Content  string
	Metadata map[string]string
}

// SyncPGVectorDocuments clears and inserts documents for pgvector embedding.
func (s *Store) SyncPGVectorDocuments(ctx context.Context, docs []PGVectorDocument) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM ccg_documents"); err != nil {
		return fmt.Errorf("clear pgvector docs: %w", err)
	}

	for _, d := range docs {
		metaJSON, _ := json.Marshal(d.Metadata)
		_, err := s.db.ExecContext(ctx,
			"INSERT INTO ccg_documents (node_id, content, metadata) VALUES ($1, $2, $3)",
			d.NodeID, d.Content, string(metaJSON),
		)
		if err != nil {
			return fmt.Errorf("insert pgvector doc node_id=%d: %w", d.NodeID, err)
		}
	}
	return nil
}

// ClearGraph drops all ccg tables.
func (s *Store) ClearGraph(ctx context.Context) error {
	for _, t := range []string{"ccg_documents", "ccg_edges", "ccg_nodes"} {
		s.db.ExecContext(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s CASCADE", t))
	}
	return nil
}

// Close closes the connection.
func (s *Store) Close() error {
	return s.db.Close()
}
