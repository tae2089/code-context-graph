package incremental

import (
	"context"
	"errors"
	"path"
	"strings"
	"testing"

	"github.com/tae2089/code-context-graph/internal/adapters/outbound/treesitter"
	"github.com/tae2089/code-context-graph/internal/app/ingest"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
	"github.com/tae2089/code-context-graph/internal/domain/reference"
)

type recordingStore struct {
	nodes           map[string]*graph.Node
	upserted        []string
	deleted         []string
	upsertedEdges   []graph.Edge
	annotations     []*graph.Annotation
	nextID          uint
	operations      []string
	deleteCalls     int
	annotationCalls int
}

func (r *recordingStore) GetNodesByFile(_ context.Context, filePath string) ([]graph.Node, error) {
	var result []graph.Node
	for _, n := range r.nodes {
		if n.FilePath == filePath {
			result = append(result, *n)
		}
	}
	return result, nil
}

func (r *recordingStore) GetNodesByIDs(_ context.Context, ids []uint) ([]graph.Node, error) {
	set := make(map[uint]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	var result []graph.Node
	for _, n := range r.nodes {
		if _, ok := set[n.ID]; ok {
			result = append(result, *n)
		}
	}
	return result, nil
}

func (r *recordingStore) GetNodesByFiles(_ context.Context, filePaths []string) (map[string][]graph.Node, error) {
	set := make(map[string]struct{}, len(filePaths))
	for _, fp := range filePaths {
		set[fp] = struct{}{}
	}
	result := make(map[string][]graph.Node)
	for _, n := range r.nodes {
		if _, ok := set[n.FilePath]; ok {
			result[n.FilePath] = append(result[n.FilePath], *n)
		}
	}
	return result, nil
}

func (r *recordingStore) GetNodesByQualifiedNames(_ context.Context, names []string) (map[string][]graph.Node, error) {
	set := make(map[string]struct{}, len(names))
	for _, name := range names {
		set[name] = struct{}{}
	}
	result := make(map[string][]graph.Node)
	for _, n := range r.nodes {
		if _, ok := set[n.QualifiedName]; ok {
			result[n.QualifiedName] = append(result[n.QualifiedName], *n)
		}
	}
	return result, nil
}

func (r *recordingStore) GetFileNodesByPathSuffix(_ context.Context, suffix string) ([]graph.Node, error) {
	suffix = strings.Trim(suffix, "/")
	var exact []graph.Node
	var result []graph.Node
	bestDepth := -1
	for _, n := range r.nodes {
		if n.Kind != graph.NodeKindFile {
			continue
		}
		dir := strings.Trim(path.Dir(n.FilePath), "/")
		if dir == "." || dir == "" {
			continue
		}
		if dir == suffix {
			exact = append(exact, *n)
			continue
		}
		if depth := reference.CommonSuffixDepth(suffix, dir); depth > 0 {
			if depth > bestDepth {
				bestDepth = depth
				result = []graph.Node{*n}
				continue
			}
			if depth == bestDepth {
				result = append(result, *n)
			}
		}
	}
	if len(exact) > 0 {
		return exact, nil
	}
	return result, nil
}

func (r *recordingStore) UpsertNodes(_ context.Context, nodes []graph.Node) error {
	r.operations = append(r.operations, "upsert_nodes")
	for _, n := range nodes {
		if n.ID == 0 {
			r.nextID++
			n.ID = r.nextID
		}
		r.upserted = append(r.upserted, n.FilePath)
		copy := n
		r.nodes[n.QualifiedName] = &copy
	}
	return nil
}

func (r *recordingStore) UpsertEdges(_ context.Context, edges []graph.Edge) error {
	r.operations = append(r.operations, "upsert_edges")
	r.upsertedEdges = append(r.upsertedEdges, edges...)
	return nil
}

func (r *recordingStore) DeleteNodesByFiles(_ context.Context, filePaths []string) error {
	r.deleteCalls++
	r.operations = append(r.operations, "delete_nodes")
	r.deleted = append(r.deleted, filePaths...)
	fileSet := make(map[string]struct{}, len(filePaths))
	for _, filePath := range filePaths {
		fileSet[filePath] = struct{}{}
	}
	toDelete := []string{}
	for qn, n := range r.nodes {
		if _, ok := fileSet[n.FilePath]; ok {
			toDelete = append(toDelete, qn)
		}
	}
	for _, qn := range toDelete {
		delete(r.nodes, qn)
	}
	return nil
}

func (r *recordingStore) UpsertAnnotations(_ context.Context, annotations []*graph.Annotation) error {
	r.annotationCalls++
	r.annotations = append(r.annotations, annotations...)
	return nil
}

func (r *recordingStore) GetEdgesToNodes(_ context.Context, nodeIDs []uint) ([]graph.Edge, error) {
	set := make(map[uint]struct{}, len(nodeIDs))
	for _, id := range nodeIDs {
		set[id] = struct{}{}
	}
	var result []graph.Edge
	for _, edge := range r.upsertedEdges {
		if _, ok := set[edge.ToNodeID]; ok {
			result = append(result, edge)
		}
	}
	return result, nil
}

type staticParser struct {
	result map[string]parseResult
	called []string
}

type parseResult struct {
	nodes []graph.Node
	edges []graph.Edge
	err   error
}

type commentParseResult struct {
	parseResult
	comments []treesitter.CommentBlock
	language string
}

func (p *staticParser) Parse(filePath string, _ []byte) ([]graph.Node, []graph.Edge, error) {
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

type commentAwareParser struct {
	result map[string]commentParseResult
	called []string
}

func (p *commentAwareParser) Parse(filePath string, _ []byte) ([]graph.Node, []graph.Edge, error) {
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

func (p *commentAwareParser) ParseWithComments(_ context.Context, filePath string, _ []byte) ([]graph.Node, []graph.Edge, []treesitter.CommentBlock, error) {
	p.called = append(p.called, filePath)
	r, ok := p.result[filePath]
	if !ok {
		return nil, nil, nil, nil
	}
	if r.err != nil {
		return nil, nil, nil, r.err
	}
	return r.nodes, r.edges, r.comments, nil
}

func (p *commentAwareParser) Language() string {
	for _, r := range p.result {
		if r.language != "" {
			return r.language
		}
	}
	return ""
}

func newStore() *recordingStore {
	return &recordingStore{nodes: map[string]*graph.Node{}}
}

func TestIncremental_NewFile(t *testing.T) {
	st := newStore()
	parser := &staticParser{result: map[string]parseResult{
		"new.go": {
			nodes: []graph.Node{{QualifiedName: "pkg.New", Kind: graph.NodeKindFunction, Name: "New", FilePath: "new.go", StartLine: 1, EndLine: 2, Hash: "abc123", Language: "go"}},
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

func TestIncremental_NewFileResolvesCallEdges(t *testing.T) {
	st := newStore()
	parser := &staticParser{result: map[string]parseResult{
		"new.go": {
			nodes: []graph.Node{
				{QualifiedName: "pkg.A", Kind: graph.NodeKindFunction, Name: "A", FilePath: "new.go", StartLine: 3, EndLine: 5, Hash: "abc123", Language: "go"},
				{QualifiedName: "pkg.B", Kind: graph.NodeKindFunction, Name: "B", FilePath: "new.go", StartLine: 7, EndLine: 7, Hash: "abc123", Language: "go"},
			},
			edges: []graph.Edge{{
				Kind:        graph.EdgeKindCalls,
				FilePath:    "new.go",
				Line:        4,
				Fingerprint: "calls:new.go:B:4",
			}},
		},
	}}

	syncer := New(st, parser)
	files := map[string]FileInfo{
		"new.go": {Hash: "abc123", Content: []byte("package pkg")},
	}
	if _, err := syncer.Sync(context.Background(), files); err != nil {
		t.Fatal(err)
	}
	if len(st.upsertedEdges) != 1 {
		t.Fatalf("upserted edges=%d, want 1", len(st.upsertedEdges))
	}
	if got := st.upsertedEdges[0].FromNodeID; got == 0 {
		t.Fatal("expected FromNodeID to be resolved")
	}
	if got := st.upsertedEdges[0].ToNodeID; got == 0 {
		t.Fatal("expected ToNodeID to be resolved")
	}
}

func TestIncremental_UnchangedFile(t *testing.T) {
	st := newStore()
	st.nodes["pkg.Existing"] = &graph.Node{QualifiedName: "pkg.Existing", Kind: graph.NodeKindFunction, Name: "Existing", FilePath: "exist.go", Hash: "same123", Language: "go"}

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

func TestIncremental_ForceReparsesUnchangedFile(t *testing.T) {
	st := newStore()
	st.nodes["pkg.Existing"] = &graph.Node{QualifiedName: "pkg.Existing", Kind: graph.NodeKindFunction, Name: "Existing", FilePath: "exist.go", Hash: "same123", Language: "go"}

	parser := &staticParser{result: map[string]parseResult{
		"exist.go": {
			nodes: []graph.Node{{QualifiedName: "pkg.Existing", Kind: graph.NodeKindFunction, Name: "Existing", FilePath: "exist.go", StartLine: 1, EndLine: 2, Hash: "same123", Language: "go"}},
		},
	}}

	syncer := New(st, parser)
	files := map[string]FileInfo{
		"exist.go": {Hash: "same123", Content: []byte("package pkg"), Force: true},
	}
	stats, err := syncer.Sync(context.Background(), files)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Modified != 1 {
		t.Errorf("expected 1 modified, got %d", stats.Modified)
	}
	if len(st.deleted) != 1 || st.deleted[0] != "exist.go" {
		t.Errorf("expected forced delete/reparse for exist.go, got %v", st.deleted)
	}
	if len(parser.called) != 1 || parser.called[0] != "exist.go" {
		t.Errorf("expected parser to be called for forced file, got %v", parser.called)
	}
}

func TestIncremental_ModifiedFile(t *testing.T) {
	st := newStore()
	st.nodes["pkg.Old"] = &graph.Node{QualifiedName: "pkg.Old", Kind: graph.NodeKindFunction, Name: "Old", FilePath: "mod.go", Hash: "old_hash", Language: "go"}

	parser := &staticParser{result: map[string]parseResult{
		"mod.go": {
			nodes: []graph.Node{{QualifiedName: "pkg.Updated", Kind: graph.NodeKindFunction, Name: "Updated", FilePath: "mod.go", StartLine: 1, EndLine: 5, Hash: "new_hash", Language: "go"}},
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

func TestIncremental_BatchesModifiedFileDeletion(t *testing.T) {
	st := newStore()
	st.nodes["pkg.OldA"] = &graph.Node{QualifiedName: "pkg.OldA", Kind: graph.NodeKindFunction, Name: "OldA", FilePath: "a.go", Hash: "old_a", Language: "go"}
	st.nodes["pkg.OldB"] = &graph.Node{QualifiedName: "pkg.OldB", Kind: graph.NodeKindFunction, Name: "OldB", FilePath: "b.go", Hash: "old_b", Language: "go"}
	parser := &staticParser{result: map[string]parseResult{
		"a.go": {nodes: []graph.Node{{QualifiedName: "pkg.NewA", Kind: graph.NodeKindFunction, Name: "NewA", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"}}},
		"b.go": {nodes: []graph.Node{{QualifiedName: "pkg.NewB", Kind: graph.NodeKindFunction, Name: "NewB", FilePath: "b.go", StartLine: 1, EndLine: 2, Language: "go"}}},
	}}

	syncer := New(st, parser)
	stats, err := syncer.Sync(context.Background(), map[string]FileInfo{
		"a.go": {Hash: "new_a", Content: []byte("package pkg")},
		"b.go": {Hash: "new_b", Content: []byte("package pkg")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Modified != 2 {
		t.Fatalf("expected 2 modified files, got %d", stats.Modified)
	}
	if st.deleteCalls != 1 {
		t.Fatalf("expected one batched deletion call, got %d", st.deleteCalls)
	}
}

func TestIncremental_ModifiedFileParseFailurePreservesExistingNodes(t *testing.T) {
	st := newStore()
	st.nodes["pkg.Old"] = &graph.Node{QualifiedName: "pkg.Old", Kind: graph.NodeKindFunction, Name: "Old", FilePath: "mod.go", Hash: "old_hash", Language: "go"}
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

func TestIncremental_MultiFileParseFailurePreservesEarlierExistingNodes(t *testing.T) {
	st := newStore()
	st.nodes["pkg.OldA"] = &graph.Node{QualifiedName: "pkg.OldA", Kind: graph.NodeKindFunction, Name: "OldA", FilePath: "a.go", Hash: "old_a", Language: "go"}
	st.nodes["pkg.OldB"] = &graph.Node{QualifiedName: "pkg.OldB", Kind: graph.NodeKindFunction, Name: "OldB", FilePath: "b.go", Hash: "old_b", Language: "go"}
	parseErr := errors.New("parse failed")
	parser := &staticParser{result: map[string]parseResult{
		"a.go": {
			nodes: []graph.Node{{QualifiedName: "pkg.NewA", Kind: graph.NodeKindFunction, Name: "NewA", FilePath: "a.go", StartLine: 1, EndLine: 2, Hash: "new_a", Language: "go"}},
		},
		"b.go": {err: parseErr},
	}}

	syncer := New(st, parser)
	files := map[string]FileInfo{
		"a.go": {Hash: "new_a", Content: []byte("package pkg")},
		"b.go": {Hash: "new_b", Content: []byte("package pkg")},
	}

	_, err := syncer.Sync(context.Background(), files)
	if !errors.Is(err, parseErr) {
		t.Fatalf("expected parse error, got %v", err)
	}
	if _, ok := st.nodes["pkg.OldA"]; !ok {
		t.Fatalf("expected first file existing node to be preserved after later parse failure")
	}
	if _, ok := st.nodes["pkg.OldB"]; !ok {
		t.Fatalf("expected second file existing node to be preserved after parse failure")
	}
	if len(st.deleted) != 0 {
		t.Fatalf("expected no delete before all parses succeed, got %v", st.deleted)
	}
}

func TestIncremental_DeletedFile(t *testing.T) {
	st := newStore()
	st.nodes["pkg.Gone"] = &graph.Node{QualifiedName: "pkg.Gone", Kind: graph.NodeKindFunction, Name: "Gone", FilePath: "gone.go", Hash: "h1", Language: "go"}

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

func TestSyncWithExisting_RestoresAnnotationsForModifiedFile(t *testing.T) {
	st := newStore()
	st.nodes["pkg.Old"] = &graph.Node{ID: 1, QualifiedName: "pkg.Old", Kind: graph.NodeKindFunction, Name: "Old", FilePath: "mod.go", StartLine: 3, EndLine: 5, Hash: "old_hash", Language: "go"}

	parser := &commentAwareParser{result: map[string]commentParseResult{
		"mod.go": {
			parseResult: parseResult{
				nodes: []graph.Node{{QualifiedName: "mod.go", Kind: graph.NodeKindFile, Name: "mod.go", FilePath: "mod.go", StartLine: 1, EndLine: 5, Hash: "new_hash", Language: "go"}, {QualifiedName: "pkg.Updated", Kind: graph.NodeKindFunction, Name: "Updated", FilePath: "mod.go", StartLine: 3, EndLine: 5, Hash: "new_hash", Language: "go"}},
			},
			comments: []treesitter.CommentBlock{{StartLine: 1, EndLine: 1, Text: "// @intent 복원 테스트"}},
			language: "go",
		},
	}}

	syncer := New(st, parser)
	files := map[string]FileInfo{
		"mod.go": {Hash: "new_hash", Content: []byte("// @intent 복원 테스트\nfunc Updated() {}")},
	}

	stats, err := syncer.Sync(context.Background(), files)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Modified != 1 {
		t.Fatalf("expected 1 modified, got %d", stats.Modified)
	}
	if len(st.annotations) != 1 {
		t.Fatalf("expected 1 restored annotation, got %d", len(st.annotations))
	}
	if len(st.annotations[0].Tags) != 1 || st.annotations[0].Tags[0].Kind != graph.TagIntent {
		t.Fatalf("expected restored @intent tag, got %#v", st.annotations[0].Tags)
	}
}

func TestSyncWithExisting_BatchesAnnotationsAcrossFiles(t *testing.T) {
	st := newStore()
	parser := &commentAwareParser{result: map[string]commentParseResult{
		"a.go": {
			parseResult: parseResult{nodes: []graph.Node{
				{QualifiedName: "a.go", Kind: graph.NodeKindFile, Name: "a.go", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
				{QualifiedName: "pkg.A", Kind: graph.NodeKindFunction, Name: "A", FilePath: "a.go", StartLine: 2, EndLine: 2, Language: "go"},
			}},
			comments: []treesitter.CommentBlock{{StartLine: 1, EndLine: 1, Text: "// @intent A"}},
			language: "go",
		},
		"b.go": {
			parseResult: parseResult{nodes: []graph.Node{
				{QualifiedName: "b.go", Kind: graph.NodeKindFile, Name: "b.go", FilePath: "b.go", StartLine: 1, EndLine: 2, Language: "go"},
				{QualifiedName: "pkg.B", Kind: graph.NodeKindFunction, Name: "B", FilePath: "b.go", StartLine: 2, EndLine: 2, Language: "go"},
			}},
			comments: []treesitter.CommentBlock{{StartLine: 1, EndLine: 1, Text: "// @intent B"}},
			language: "go",
		},
	}}

	syncer := New(st, parser)
	_, err := syncer.Sync(context.Background(), map[string]FileInfo{
		"a.go": {Hash: "new_a", Content: []byte("// @intent A\nfunc A() {}")},
		"b.go": {Hash: "new_b", Content: []byte("// @intent B\nfunc B() {}")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.annotations) < 2 {
		t.Fatalf("expected annotations from both files, got %d", len(st.annotations))
	}
	if st.annotationCalls != 1 {
		t.Fatalf("expected one batched annotation call, got %d", st.annotationCalls)
	}
}

func TestIncremental_DispatchesParserByExtension(t *testing.T) {
	st := newStore()
	goParser := &staticParser{result: map[string]parseResult{
		"a.go": {
			nodes: []graph.Node{{QualifiedName: "pkg.A", Kind: graph.NodeKindFunction, Name: "A", FilePath: "a.go", StartLine: 1, EndLine: 2, Hash: "go1", Language: "go"}},
		},
	}}
	pyParser := &staticParser{result: map[string]parseResult{
		"b.py": {
			nodes: []graph.Node{{QualifiedName: "pkg.b", Kind: graph.NodeKindFunction, Name: "b", FilePath: "b.py", StartLine: 1, EndLine: 2, Hash: "py1", Language: "python"}},
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

func TestSyncWithExisting_ReleasesContentAfterProcessing(t *testing.T) {
	st := newStore()
	st.nodes["pkg.Old"] = &graph.Node{QualifiedName: "pkg.Old", Kind: graph.NodeKindFunction, Name: "Old", FilePath: "mod.go", Hash: "old_hash", Language: "go"}
	parser := &staticParser{result: map[string]parseResult{
		"new.go": {
			nodes: []graph.Node{{QualifiedName: "pkg.New", Kind: graph.NodeKindFunction, Name: "New", FilePath: "new.go", StartLine: 1, EndLine: 2, Hash: "new_hash", Language: "go"}},
		},
		"mod.go": {
			nodes: []graph.Node{{QualifiedName: "pkg.Updated", Kind: graph.NodeKindFunction, Name: "Updated", FilePath: "mod.go", StartLine: 1, EndLine: 2, Hash: "mod_hash", Language: "go"}},
		},
	}}

	syncer := New(st, parser)
	files := map[string]FileInfo{
		"new.go": {Hash: "new_hash", Content: []byte("package pkg\nfunc New() {}")},
		"mod.go": {Hash: "mod_hash", Content: []byte("package pkg\nfunc Updated() {}")},
	}
	stats, err := syncer.Sync(context.Background(), files)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Added != 1 || stats.Modified != 1 {
		t.Fatalf("expected 1 added and 1 modified, got %+v", stats)
	}
	if files["new.go"].Content != nil {
		t.Fatalf("expected new.go content released, got %d bytes", len(files["new.go"].Content))
	}
	if files["mod.go"].Content != nil {
		t.Fatalf("expected mod.go content released, got %d bytes", len(files["mod.go"].Content))
	}
	if files["new.go"].Hash != "new_hash" || files["mod.go"].Hash != "mod_hash" {
		t.Fatalf("expected hashes preserved, got new=%q mod=%q", files["new.go"].Hash, files["mod.go"].Hash)
	}
}

func TestSyncWithExisting_DoesNotPersistUnresolvedEdges(t *testing.T) {
	st := newStore()
	parser := &staticParser{result: map[string]parseResult{
		"main.go": {
			nodes: []graph.Node{{
				QualifiedName: "pkg.Main",
				Kind:          graph.NodeKindFunction,
				Name:          "Main",
				FilePath:      "main.go",
				StartLine:     1,
				EndLine:       3,
				Hash:          "hash",
				Language:      "go",
			}},
			edges: []graph.Edge{{
				Kind:        graph.EdgeKindCalls,
				FilePath:    "main.go",
				Line:        2,
				Fingerprint: "calls:main.go:pkg.Missing:2",
			}},
		},
	}}
	syncer := New(st, parser)
	_, err := syncer.Sync(context.Background(), map[string]FileInfo{
		"main.go": {Hash: "hash", Content: []byte("package pkg\nfunc Main(){ Missing() }")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.upsertedEdges) != 0 {
		t.Fatalf("expected unresolved edge to be skipped, got %+v", st.upsertedEdges)
	}
}

func TestSyncWithExisting_ReleasesContentForUnchangedAndUnparsedFiles(t *testing.T) {
	st := newStore()
	st.nodes["pkg.Existing"] = &graph.Node{QualifiedName: "pkg.Existing", Kind: graph.NodeKindFunction, Name: "Existing", FilePath: "exist.go", Hash: "same123", Language: "go"}
	st.nodes["pkg.Old"] = &graph.Node{QualifiedName: "pkg.Old", Kind: graph.NodeKindFunction, Name: "Old", FilePath: "mod.go", Hash: "old_hash", Language: "go"}
	parser := &staticParser{result: map[string]parseResult{
		"mod.go": {
			nodes: []graph.Node{{QualifiedName: "pkg.Updated", Kind: graph.NodeKindFunction, Name: "Updated", FilePath: "mod.go", StartLine: 1, EndLine: 2, Hash: "new_hash", Language: "go"}},
		},
	}}

	syncer := NewWithRegistry(st, map[string]Parser{".go": parser})
	files := map[string]FileInfo{
		"exist.go": {Hash: "same123", Content: []byte("package pkg")},
		"note.txt": {Hash: "txt1", Content: []byte("hello")},
		"mod.go":   {Hash: "new_hash", Content: []byte("package pkg\nfunc Updated() {}")},
	}
	stats, err := syncer.Sync(context.Background(), files)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Skipped != 2 || stats.Modified != 1 {
		t.Fatalf("expected 2 skipped and 1 modified, got %+v", stats)
	}
	for _, fp := range []string{"exist.go", "note.txt", "mod.go"} {
		if files[fp].Content != nil {
			t.Fatalf("expected %s content released, got %d bytes", fp, len(files[fp].Content))
		}
	}
}

func TestIncremental_ResolvesCallEdgesAcrossMultipleFilesInBatch(t *testing.T) {
	st := newStore()
	st.nodes["mcp.FlowTracer"] = &graph.Node{ID: 10, QualifiedName: "mcp.FlowTracer", Kind: graph.NodeKindType, Name: "FlowTracer", FilePath: "mcp/deps.go", StartLine: 1, EndLine: 3, Hash: "iface", Language: "go"}
	st.nodes["mcp/deps.go"] = &graph.Node{ID: 11, QualifiedName: "mcp/deps.go", Kind: graph.NodeKindFile, Name: "mcp/deps.go", FilePath: "mcp/deps.go", StartLine: 1, EndLine: 3, Hash: "iface", Language: "go"}
	parser := &staticParser{result: map[string]parseResult{
		"flows/tracer.go": {
			nodes: []graph.Node{
				{QualifiedName: "flows/tracer.go", Kind: graph.NodeKindFile, Name: "flows/tracer.go", FilePath: "flows/tracer.go", StartLine: 1, EndLine: 20, Hash: "impl", Language: "go"},
				{QualifiedName: "flows.Tracer", Kind: graph.NodeKindClass, Name: "Tracer", FilePath: "flows/tracer.go", StartLine: 3, EndLine: 7, Hash: "impl", Language: "go"},
				{QualifiedName: "flows.Tracer.TraceFlow", Kind: graph.NodeKindFunction, Name: "TraceFlow", FilePath: "flows/tracer.go", StartLine: 5, EndLine: 6, Hash: "impl", Language: "go"},
			},
			edges: []graph.Edge{{Kind: graph.EdgeKindImplements, FilePath: "flows/tracer.go", Line: 3, Fingerprint: "implements:flows/tracer.go:flows.Tracer:mcp.FlowTracer"}},
		},
		"cmd/main.go": {
			nodes: []graph.Node{
				{QualifiedName: "cmd/main.go", Kind: graph.NodeKindFile, Name: "cmd/main.go", FilePath: "cmd/main.go", StartLine: 1, EndLine: 20, Hash: "main", Language: "go"},
				{QualifiedName: "main.Run", Kind: graph.NodeKindFunction, Name: "Run", FilePath: "cmd/main.go", StartLine: 3, EndLine: 8, Hash: "main", Language: "go"},
			},
			edges: []graph.Edge{
				{Kind: graph.EdgeKindImportsFrom, FilePath: "cmd/main.go", Line: 1, Fingerprint: "imports_from:cmd/main.go:github.com/example/project/mcp:1"},
				{Kind: graph.EdgeKindCalls, FilePath: "cmd/main.go", Line: 4, Fingerprint: "calls:cmd/main.go:h.deps.FlowTracer.TraceFlow:4"},
			},
		},
	}}

	syncer := New(st, parser)
	_, err := syncer.Sync(context.Background(), map[string]FileInfo{
		"flows/tracer.go": {Hash: "impl", Content: []byte("package flows")},
		"cmd/main.go":     {Hash: "main", Content: []byte("package main")},
	})
	if err != nil {
		t.Fatal(err)
	}
	var foundCall bool
	for _, edge := range st.upsertedEdges {
		if edge.Kind != graph.EdgeKindCalls {
			continue
		}
		foundCall = true
		if edge.FromNodeID == 0 || edge.ToNodeID == 0 {
			t.Fatalf("expected call edge to resolve in one batch, got %+v", edge)
		}
	}
	if !foundCall {
		t.Fatalf("expected resolved call edge in %+v", st.upsertedEdges)
	}
}

func TestSyncBatchesWithExisting_ResolvesImplementsBeforeEarlierCallBatch(t *testing.T) {
	st := newStore()
	st.nodes["mcp.FlowTracer"] = &graph.Node{ID: 10, QualifiedName: "mcp.FlowTracer", Kind: graph.NodeKindType, Name: "FlowTracer", FilePath: "mcp/deps.go", StartLine: 1, EndLine: 3, Hash: "iface", Language: "go"}
	st.nodes["mcp/deps.go"] = &graph.Node{ID: 11, QualifiedName: "mcp/deps.go", Kind: graph.NodeKindFile, Name: "mcp/deps.go", FilePath: "mcp/deps.go", StartLine: 1, EndLine: 3, Hash: "iface", Language: "go"}
	parser := &staticParser{result: map[string]parseResult{
		"cmd/main.go": {
			nodes: []graph.Node{
				{QualifiedName: "cmd/main.go", Kind: graph.NodeKindFile, Name: "cmd/main.go", FilePath: "cmd/main.go", StartLine: 1, EndLine: 20, Hash: "main", Language: "go"},
				{QualifiedName: "main.Run", Kind: graph.NodeKindFunction, Name: "Run", FilePath: "cmd/main.go", StartLine: 3, EndLine: 8, Hash: "main", Language: "go"},
			},
			edges: []graph.Edge{
				{Kind: graph.EdgeKindImportsFrom, FilePath: "cmd/main.go", Line: 1, Fingerprint: "imports_from:cmd/main.go:github.com/example/project/mcp:1"},
				{Kind: graph.EdgeKindCalls, FilePath: "cmd/main.go", Line: 4, Fingerprint: "calls:cmd/main.go:h.deps.FlowTracer.TraceFlow:4"},
			},
		},
		"flows/tracer.go": {
			nodes: []graph.Node{
				{QualifiedName: "flows/tracer.go", Kind: graph.NodeKindFile, Name: "flows/tracer.go", FilePath: "flows/tracer.go", StartLine: 1, EndLine: 20, Hash: "impl", Language: "go"},
				{QualifiedName: "flows.Tracer", Kind: graph.NodeKindClass, Name: "Tracer", FilePath: "flows/tracer.go", StartLine: 3, EndLine: 7, Hash: "impl", Language: "go"},
				{QualifiedName: "flows.Tracer.TraceFlow", Kind: graph.NodeKindFunction, Name: "TraceFlow", FilePath: "flows/tracer.go", StartLine: 5, EndLine: 6, Hash: "impl", Language: "go"},
			},
			edges: []graph.Edge{{Kind: graph.EdgeKindImplements, FilePath: "flows/tracer.go", Line: 3, Fingerprint: "implements:flows/tracer.go:flows.Tracer:mcp.FlowTracer"}},
		},
	}}

	source := ingest.FileBatchSource(func(visitor ingest.FileBatchVisitor) error {
		for _, files := range []map[string]ingest.FileInfo{
			{"cmd/main.go": {Hash: "main", Content: []byte("package main")}},
			{"flows/tracer.go": {Hash: "impl", Content: []byte("package flows")}},
		} {
			if err := visitor(files); err != nil {
				return err
			}
		}
		return nil
	})

	syncer := New(st, parser)
	if _, err := syncer.SyncBatchesWithExisting(context.Background(), source, nil); err != nil {
		t.Fatal(err)
	}
	for _, edge := range st.upsertedEdges {
		if edge.Kind == graph.EdgeKindCalls && edge.FromNodeID != 0 && edge.ToNodeID != 0 {
			return
		}
	}
	t.Fatalf("expected earlier call batch to resolve after later implements batch, got %+v", st.upsertedEdges)
}

func TestSyncBatchesWithExisting_BatchesDeletedFiles(t *testing.T) {
	st := newStore()
	st.nodes["pkg.A"] = &graph.Node{QualifiedName: "pkg.A", Kind: graph.NodeKindFunction, FilePath: "a.go"}
	st.nodes["pkg.B"] = &graph.Node{QualifiedName: "pkg.B", Kind: graph.NodeKindFunction, FilePath: "b.go"}
	source := ingest.FileBatchSource(func(ingest.FileBatchVisitor) error { return nil })

	syncer := New(st, &staticParser{})
	stats, err := syncer.SyncBatchesWithExisting(context.Background(), source, []string{"a.go", "b.go"})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Deleted != 2 {
		t.Fatalf("expected 2 deleted files, got %d", stats.Deleted)
	}
	if st.deleteCalls != 1 {
		t.Fatalf("expected one batched deletion call, got %d", st.deleteCalls)
	}
}

func TestIncremental_BatchesNodesBeforeResolvingEdges(t *testing.T) {
	st := newStore()
	st.nodes["mcp.FlowTracer"] = &graph.Node{ID: 10, QualifiedName: "mcp.FlowTracer", Kind: graph.NodeKindType, Name: "FlowTracer", FilePath: "mcp/deps.go", StartLine: 1, EndLine: 3, Hash: "iface", Language: "go"}
	st.nodes["mcp/deps.go"] = &graph.Node{ID: 11, QualifiedName: "mcp/deps.go", Kind: graph.NodeKindFile, Name: "mcp/deps.go", FilePath: "mcp/deps.go", StartLine: 1, EndLine: 3, Hash: "iface", Language: "go"}
	parser := &staticParser{result: map[string]parseResult{
		"flows/tracer.go": {
			nodes: []graph.Node{{QualifiedName: "flows.Tracer", Kind: graph.NodeKindClass, Name: "Tracer", FilePath: "flows/tracer.go", StartLine: 3, EndLine: 7, Hash: "impl", Language: "go"}},
			edges: []graph.Edge{{Kind: graph.EdgeKindImplements, FilePath: "flows/tracer.go", Line: 3, Fingerprint: "implements:flows/tracer.go:flows.Tracer:mcp.FlowTracer"}},
		},
		"cmd/main.go": {
			nodes: []graph.Node{{QualifiedName: "main.Run", Kind: graph.NodeKindFunction, Name: "Run", FilePath: "cmd/main.go", StartLine: 3, EndLine: 8, Hash: "main", Language: "go"}},
			edges: []graph.Edge{{Kind: graph.EdgeKindCalls, FilePath: "cmd/main.go", Line: 4, Fingerprint: "calls:cmd/main.go:h.deps.FlowTracer.TraceFlow:4"}},
		},
	}}

	syncer := New(st, parser)
	_, err := syncer.Sync(context.Background(), map[string]FileInfo{
		"flows/tracer.go": {Hash: "impl", Content: []byte("package flows")},
		"cmd/main.go":     {Hash: "main", Content: []byte("package main")},
	})
	if err != nil {
		t.Fatal(err)
	}
	firstEdge := -1
	nodeOps := 0
	for i, op := range st.operations {
		if op == "upsert_nodes" {
			nodeOps++
		}
		if op == "upsert_edges" && firstEdge == -1 {
			firstEdge = i
		}
	}
	if firstEdge == -1 {
		t.Fatalf("expected edge upsert operation, got %v", st.operations)
	}
	if nodeOps != 1 {
		t.Fatalf("expected one batched node upsert before edges, got ops %v", st.operations)
	}
	for i := 0; i < firstEdge; i++ {
		if st.operations[i] == "upsert_edges" {
			t.Fatalf("edge upsert happened before all node upserts: %v", st.operations)
		}
	}
}

func TestSyncWithExisting_DoesNotReleaseContentBeforeAnnotations(t *testing.T) {
	st := newStore()
	st.nodes["pkg.Old"] = &graph.Node{ID: 1, QualifiedName: "pkg.Old", Kind: graph.NodeKindFunction, Name: "Old", FilePath: "mod.go", StartLine: 3, EndLine: 5, Hash: "old_hash", Language: "go"}
	parser := &commentAwareParser{result: map[string]commentParseResult{
		"mod.go": {
			parseResult: parseResult{
				nodes: []graph.Node{{QualifiedName: "mod.go", Kind: graph.NodeKindFile, Name: "mod.go", FilePath: "mod.go", StartLine: 1, EndLine: 5, Hash: "new_hash", Language: "go"}, {QualifiedName: "pkg.Updated", Kind: graph.NodeKindFunction, Name: "Updated", FilePath: "mod.go", StartLine: 3, EndLine: 5, Hash: "new_hash", Language: "go"}},
			},
			comments: []treesitter.CommentBlock{{StartLine: 1, EndLine: 1, Text: "// @intent restore test"}},
			language: "go",
		},
	}}

	syncer := New(st, parser)
	files := map[string]FileInfo{
		"mod.go": {Hash: "new_hash", Content: []byte("// @intent restore test\nfunc Updated() {}")},
	}
	stats, err := syncer.Sync(context.Background(), files)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Modified != 1 {
		t.Fatalf("expected 1 modified, got %d", stats.Modified)
	}
	if len(st.annotations) != 1 {
		t.Fatalf("expected 1 restored annotation, got %d", len(st.annotations))
	}
	if files["mod.go"].Content != nil {
		t.Fatalf("expected mod.go content released after annotation restore, got %d bytes", len(files["mod.go"].Content))
	}
}
