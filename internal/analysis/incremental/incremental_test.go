package incremental

import (
	"context"
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
}

type parseResult struct {
	nodes []model.Node
	edges []model.Edge
}

func (p *staticParser) Parse(filePath string, _ []byte) ([]model.Node, []model.Edge, error) {
	r, ok := p.result[filePath]
	if !ok {
		return nil, nil, nil
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
