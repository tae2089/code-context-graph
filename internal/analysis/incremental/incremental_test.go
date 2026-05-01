package incremental

import (
	"context"
	"errors"
	"testing"

	"github.com/tae2089/code-context-graph/internal/model"
)

type recordingStore struct {
	nodes        map[string]*model.Node
	upserted     []string
	deleted      []string
	upsertedEdge int
}

func (r *recordingStore) GetNodesByFile(_ context.Context, filePath string) ([]model.Node, error) {
	var result []model.Node
	for _, n := range r.nodes {
		if n.FilePath == filePath {
			result = append(result, *n)
		}
	}
	return result, nil
}

func (r *recordingStore) GetNodesByFiles(_ context.Context, filePaths []string) (map[string][]model.Node, error) {
	set := make(map[string]struct{}, len(filePaths))
	for _, fp := range filePaths {
		set[fp] = struct{}{}
	}
	result := make(map[string][]model.Node)
	for _, n := range r.nodes {
		if _, ok := set[n.FilePath]; ok {
			result[n.FilePath] = append(result[n.FilePath], *n)
		}
	}
	return result, nil
}

func (r *recordingStore) UpsertNodes(_ context.Context, nodes []model.Node) error {
	for _, n := range nodes {
		r.upserted = append(r.upserted, n.FilePath)
		r.nodes[n.QualifiedName] = &n
	}
	return nil
}

func (r *recordingStore) UpsertEdges(_ context.Context, edges []model.Edge) error {
	r.upsertedEdge += len(edges)
	return nil
}

func (r *recordingStore) DeleteNodesByFile(_ context.Context, filePath string) error {
	r.deleted = append(r.deleted, filePath)
	toDelete := []string{}
	for qn, n := range r.nodes {
		if n.FilePath == filePath {
			toDelete = append(toDelete, qn)
		}
	}
	for _, qn := range toDelete {
		delete(r.nodes, qn)
	}
	return nil
}

type staticParser struct {
	result map[string]parseResult
	called []string
}

type parseResult struct {
	nodes []model.Node
	edges []model.Edge
	err   error
}

func (p *staticParser) Parse(filePath string, _ []byte) ([]model.Node, []model.Edge, error) {
	p.called = append(p.called, filePath)
	r, ok := p.result[filePath]
	if !ok {
		return nil, nil, nil
	}
	if r.err != nil {
		return nil, nil, r.err
	}
	return r.nodes, r.edges, nil
}

func newStore() *recordingStore {
	return &recordingStore{nodes: map[string]*model.Node{}}
}

func TestIncremental_NewFile(t *testing.T) {
	st := newStore()
	parser := &staticParser{result: map[string]parseResult{
		"new.go": {
			nodes: []model.Node{{QualifiedName: "pkg.New", Kind: model.NodeKindFunction, Name: "New", FilePath: "new.go", StartLine: 1, EndLine: 2, Hash: "abc123", Language: "go"}},
		},
	}}

	syncer := New(st, parser)
	files := map[string]FileInfo{
		"new.go": {Hash: "abc123", Content: []byte("package pkg")},
	}
	stats, err := syncer.Sync(context.Background(), files)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Added != 1 {
		t.Errorf("expected 1 added, got %d", stats.Added)
	}
	if len(st.upserted) != 1 {
		t.Errorf("expected 1 upsert call, got %d", len(st.upserted))
	}
}

func TestIncremental_UnchangedFile(t *testing.T) {
	st := newStore()
	st.nodes["pkg.Existing"] = &model.Node{QualifiedName: "pkg.Existing", Kind: model.NodeKindFunction, Name: "Existing", FilePath: "exist.go", Hash: "same123", Language: "go"}

	parser := &staticParser{result: map[string]parseResult{}}

	syncer := New(st, parser)
	files := map[string]FileInfo{
		"exist.go": {Hash: "same123", Content: []byte("package pkg")},
	}
	stats, err := syncer.Sync(context.Background(), files)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Skipped != 1 {
		t.Errorf("expected 1 skipped, got %d", stats.Skipped)
	}
	if len(st.upserted) != 0 {
		t.Errorf("expected 0 upserts for unchanged file, got %d", len(st.upserted))
	}
}

func TestIncremental_ModifiedFile(t *testing.T) {
	st := newStore()
	st.nodes["pkg.Old"] = &model.Node{QualifiedName: "pkg.Old", Kind: model.NodeKindFunction, Name: "Old", FilePath: "mod.go", Hash: "old_hash", Language: "go"}

	parser := &staticParser{result: map[string]parseResult{
		"mod.go": {
			nodes: []model.Node{{QualifiedName: "pkg.Updated", Kind: model.NodeKindFunction, Name: "Updated", FilePath: "mod.go", StartLine: 1, EndLine: 5, Hash: "new_hash", Language: "go"}},
		},
	}}

	syncer := New(st, parser)
	files := map[string]FileInfo{
		"mod.go": {Hash: "new_hash", Content: []byte("package pkg")},
	}
	stats, err := syncer.Sync(context.Background(), files)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Modified != 1 {
		t.Errorf("expected 1 modified, got %d", stats.Modified)
	}
	if len(st.deleted) != 1 || st.deleted[0] != "mod.go" {
		t.Errorf("expected delete for mod.go, got %v", st.deleted)
	}
}

func TestIncremental_ModifiedFileParseFailurePreservesExistingNodes(t *testing.T) {
	st := newStore()
	st.nodes["pkg.Old"] = &model.Node{QualifiedName: "pkg.Old", Kind: model.NodeKindFunction, Name: "Old", FilePath: "mod.go", Hash: "old_hash", Language: "go"}
	parseErr := errors.New("parse failed")
	parser := &staticParser{result: map[string]parseResult{
		"mod.go": {err: parseErr},
	}}

	syncer := New(st, parser)
	files := map[string]FileInfo{
		"mod.go": {Hash: "new_hash", Content: []byte("package pkg")},
	}

	_, err := syncer.Sync(context.Background(), files)
	if !errors.Is(err, parseErr) {
		t.Fatalf("expected parse error, got %v", err)
	}
	if _, ok := st.nodes["pkg.Old"]; !ok {
		t.Fatalf("expected existing node to be preserved after parse failure")
	}
	if len(st.deleted) != 0 {
		t.Fatalf("expected no delete before successful parse, got %v", st.deleted)
	}
}

func TestIncremental_DeletedFile(t *testing.T) {
	st := newStore()
	st.nodes["pkg.Gone"] = &model.Node{QualifiedName: "pkg.Gone", Kind: model.NodeKindFunction, Name: "Gone", FilePath: "gone.go", Hash: "h1", Language: "go"}

	parser := &staticParser{result: map[string]parseResult{}}

	syncer := New(st, parser)
	files := map[string]FileInfo{}
	existing := []string{"gone.go"}

	stats, err := syncer.SyncWithExisting(context.Background(), files, existing)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Deleted != 1 {
		t.Errorf("expected 1 deleted, got %d", stats.Deleted)
	}
	if len(st.deleted) != 1 || st.deleted[0] != "gone.go" {
		t.Errorf("expected delete for gone.go, got %v", st.deleted)
	}
}

func TestIncremental_DispatchesParserByExtension(t *testing.T) {
	st := newStore()
	goParser := &staticParser{result: map[string]parseResult{
		"a.go": {
			nodes: []model.Node{{QualifiedName: "pkg.A", Kind: model.NodeKindFunction, Name: "A", FilePath: "a.go", StartLine: 1, EndLine: 2, Hash: "go1", Language: "go"}},
		},
	}}
	pyParser := &staticParser{result: map[string]parseResult{
		"b.py": {
			nodes: []model.Node{{QualifiedName: "pkg.b", Kind: model.NodeKindFunction, Name: "b", FilePath: "b.py", StartLine: 1, EndLine: 2, Hash: "py1", Language: "python"}},
		},
	}}

	syncer := NewWithRegistry(st, map[string]Parser{
		".go": goParser,
		".py": pyParser,
	})

	files := map[string]FileInfo{
		"a.go": {Hash: "go1", Content: []byte("package a")},
		"b.py": {Hash: "py1", Content: []byte("def b(): pass")},
	}

	stats, err := syncer.Sync(context.Background(), files)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Added != 2 {
		t.Fatalf("expected 2 added, got %d", stats.Added)
	}
	if len(goParser.called) != 1 || goParser.called[0] != "a.go" {
		t.Fatalf("go parser called with %v, want [a.go]", goParser.called)
	}
	if len(pyParser.called) != 1 || pyParser.called[0] != "b.py" {
		t.Fatalf("py parser called with %v, want [b.py]", pyParser.called)
	}
	if len(st.upserted) != 2 {
		t.Fatalf("expected 2 upserts, got %d", len(st.upserted))
	}
}

func TestIncremental_UnknownExtensionIsSkipped(t *testing.T) {
	st := newStore()
	goParser := &staticParser{result: map[string]parseResult{}}

	syncer := NewWithRegistry(st, map[string]Parser{
		".go": goParser,
	})

	files := map[string]FileInfo{
		"note.txt": {Hash: "txt1", Content: []byte("hello")},
	}

	stats, err := syncer.Sync(context.Background(), files)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Skipped != 1 {
		t.Fatalf("expected 1 skipped, got %d", stats.Skipped)
	}
	if stats.Added != 0 {
		t.Fatalf("expected 0 added, got %d", stats.Added)
	}
	if len(goParser.called) != 0 {
		t.Fatalf("expected go parser not to be called, got %v", goParser.called)
	}
	if len(st.upserted) != 0 {
		t.Fatalf("expected no upserts, got %v", st.upserted)
	}
}
