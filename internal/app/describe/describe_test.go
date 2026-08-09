package describe_test

import (
	"context"
	"errors"
	"testing"

	"github.com/tae2089/code-context-graph/internal/app/describe"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// stubRepository serves fixed rows so the outline rules can be tested without a
// database.
type stubRepository struct {
	byFile      map[string][]graph.Node
	underPath   map[string][]graph.Node
	byName      map[string][]graph.Node
	annotations map[uint]*graph.Annotation
	err         error

	askedKinds []graph.NodeKind
	askedNames []string
}

func (s *stubRepository) NodesByFile(_ context.Context, filePath string) ([]graph.Node, error) {
	return s.byFile[filePath], s.err
}

func (s *stubRepository) PathNodes(_ context.Context, folderPath string, kinds []graph.NodeKind) ([]graph.Node, error) {
	s.askedKinds = kinds
	return s.underPath[folderPath], s.err
}

func (s *stubRepository) NodesByExactName(_ context.Context, name string, _ int) ([]graph.Node, error) {
	s.askedNames = append(s.askedNames, name)
	return s.byName[name], s.err
}

func (s *stubRepository) Annotations(_ context.Context, _ []uint) (map[uint]*graph.Annotation, error) {
	if s.annotations == nil {
		return map[uint]*graph.Annotation{}, s.err
	}
	return s.annotations, s.err
}

func node(id uint, kind graph.NodeKind, name, filePath string, startLine int) graph.Node {
	return graph.Node{
		ID:            id,
		Kind:          kind,
		Name:          name,
		QualifiedName: "pkg." + name,
		FilePath:      filePath,
		StartLine:     startLine,
		EndLine:       startLine + 5,
	}
}

func TestDescribe_ListsAFilesDeclarationsInTheOrderTheyAreWritten(t *testing.T) {
	repo := &stubRepository{byFile: map[string][]graph.Node{
		"internal/app/queue.go": {
			node(3, graph.NodeKindFunction, "Drain", "internal/app/queue.go", 90),
			node(1, graph.NodeKindFile, "queue.go", "internal/app/queue.go", 1),
			node(2, graph.NodeKindType, "Queue", "internal/app/queue.go", 20),
		},
	}}

	outline, err := describe.New(repo).Describe(t.Context(), "internal/app/queue.go")
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if outline.Scope != describe.ScopeFile {
		t.Fatalf("scope is %q, want %q", outline.Scope, describe.ScopeFile)
	}
	want := []string{"Queue", "Drain"}
	if len(outline.Declarations) != len(want) {
		t.Fatalf("got %d declarations, want %d: %+v", len(outline.Declarations), len(want), outline.Declarations)
	}
	for i, name := range want {
		if outline.Declarations[i].Name != name {
			t.Errorf("declaration %d is %q, want %q", i, outline.Declarations[i].Name, name)
		}
	}
}

func TestDescribe_LeavesTheFileNodeOutOfItsOwnDeclarations(t *testing.T) {
	repo := &stubRepository{byFile: map[string][]graph.Node{
		"internal/app/queue.go": {
			node(1, graph.NodeKindFile, "queue.go", "internal/app/queue.go", 1),
			node(2, graph.NodeKindPackage, "app", "internal/app/queue.go", 1),
		},
	}}

	outline, err := describe.New(repo).Describe(t.Context(), "internal/app/queue.go")
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if outline.Scope != describe.ScopeFile {
		t.Fatalf("scope is %q, want %q", outline.Scope, describe.ScopeFile)
	}
	if len(outline.Declarations) != 0 {
		t.Fatalf("got %+v, want no declarations", outline.Declarations)
	}
}

func TestDescribe_ShowsWhyEachDeclarationExists(t *testing.T) {
	repo := &stubRepository{
		byFile: map[string][]graph.Node{
			"internal/app/queue.go": {
				node(2, graph.NodeKindType, "Queue", "internal/app/queue.go", 20),
			},
		},
		annotations: map[uint]*graph.Annotation{
			2: {NodeID: 2, Tags: []graph.DocTag{{Kind: graph.TagIntent, Value: "hold pending syncs so a burst cannot drop one"}}},
		},
	}

	outline, err := describe.New(repo).Describe(t.Context(), "internal/app/queue.go")
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if len(outline.Declarations) != 1 {
		t.Fatalf("got %d declarations, want 1", len(outline.Declarations))
	}
	if got, want := outline.Declarations[0].Intent, "hold pending syncs so a burst cannot drop one"; got != want {
		t.Errorf("intent is %q, want %q", got, want)
	}
}

func TestDescribe_CollapsesAFolderToItsImmediateChildren(t *testing.T) {
	repo := &stubRepository{underPath: map[string][]graph.Node{
		"internal/app": {
			node(1, graph.NodeKindFile, "queue.go", "internal/app/queue.go", 1),
			node(2, graph.NodeKindFunction, "Drain", "internal/app/queue.go", 90),
			node(3, graph.NodeKindFile, "rank.go", "internal/app/search/rank/rank.go", 1),
			node(4, graph.NodeKindFunction, "Rank", "internal/app/search/rank/rank.go", 30),
			node(5, graph.NodeKindFile, "intent.go", "internal/app/search/intent/intent.go", 1),
		},
	}}

	outline, err := describe.New(repo).Describe(t.Context(), "internal/app")
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if outline.Scope != describe.ScopeDirectory {
		t.Fatalf("scope is %q, want %q", outline.Scope, describe.ScopeDirectory)
	}
	if len(outline.Children) != 2 {
		t.Fatalf("got %d children, want 2: %+v", len(outline.Children), outline.Children)
	}
	folder := outline.Children[0]
	if folder.Path != "internal/app/search" || folder.Kind != "directory" {
		t.Errorf("first child is %+v, want the search directory", folder)
	}
	if folder.FileCount != 2 || folder.DeclCount != 1 {
		t.Errorf("search holds %d files and %d declarations, want 2 and 1", folder.FileCount, folder.DeclCount)
	}
	file := outline.Children[1]
	if file.Path != "internal/app/queue.go" || file.Kind != "file" {
		t.Errorf("second child is %+v, want queue.go", file)
	}
	if file.DeclCount != 1 {
		t.Errorf("queue.go holds %d declarations, want 1", file.DeclCount)
	}
}

func TestDescribe_LoadsFilesAlongsideDeclarationsSoAnEmptyFileStillAppears(t *testing.T) {
	repo := &stubRepository{underPath: map[string][]graph.Node{
		"internal": {node(1, graph.NodeKindFile, "doc.go", "internal/doc.go", 1)},
	}}

	if _, err := describe.New(repo).Describe(t.Context(), "internal"); err != nil {
		t.Fatalf("Describe: %v", err)
	}
	var sawFile bool
	for _, kind := range repo.askedKinds {
		if kind == graph.NodeKindFile {
			sawFile = true
		}
		if kind == graph.NodeKindPackage {
			t.Errorf("the folder walk asked for package nodes, which would double-count a folder")
		}
	}
	if !sawFile {
		t.Errorf("the folder walk asked for %v, which never returns a file with nothing in it", repo.askedKinds)
	}
}

func TestDescribe_AnswersAMissedTargetWithWhereThatNameLives(t *testing.T) {
	repo := &stubRepository{byName: map[string][]graph.Node{
		"Service": {node(7, graph.NodeKindType, "Service", "internal/app/search/intent/intent.go", 135)},
	}}

	outline, err := describe.New(repo).Describe(t.Context(), "intent.Service")
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if outline.Scope != describe.ScopeUnknown {
		t.Fatalf("scope is %q, want %q", outline.Scope, describe.ScopeUnknown)
	}
	if len(outline.Suggestions) != 1 {
		t.Fatalf("got %d suggestions, want 1: %+v", len(outline.Suggestions), outline.Suggestions)
	}
	if got := outline.Suggestions[0].FilePath; got != "internal/app/search/intent/intent.go" {
		t.Errorf("suggestion points at %q", got)
	}
	if len(repo.askedNames) != 2 || repo.askedNames[0] != "intent.Service" || repo.askedNames[1] != "Service" {
		t.Errorf("looked up %v, want the whole target then its last segment", repo.askedNames)
	}
}

func TestDescribe_SaysNothingIsThereRatherThanFailing(t *testing.T) {
	outline, err := describe.New(&stubRepository{}).Describe(t.Context(), "internal/nope")
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if outline.Scope != describe.ScopeUnknown {
		t.Fatalf("scope is %q, want %q", outline.Scope, describe.ScopeUnknown)
	}
	if len(outline.Suggestions) != 0 {
		t.Errorf("got %+v, want no suggestions", outline.Suggestions)
	}
}

func TestDescribe_TreatsPaddedAndRelativeTargetsAsTheSamePlace(t *testing.T) {
	repo := &stubRepository{byFile: map[string][]graph.Node{
		"internal/app/queue.go": {node(2, graph.NodeKindType, "Queue", "internal/app/queue.go", 20)},
	}}
	service := describe.New(repo)

	for _, target := range []string{"internal/app/queue.go", "./internal/app/queue.go", " internal/app//queue.go "} {
		outline, err := service.Describe(t.Context(), target)
		if err != nil {
			t.Fatalf("Describe(%q): %v", target, err)
		}
		if outline.Scope != describe.ScopeFile {
			t.Errorf("%q resolved to %q, want %q", target, outline.Scope, describe.ScopeFile)
		}
		if outline.Target != "internal/app/queue.go" {
			t.Errorf("%q normalized to %q", target, outline.Target)
		}
	}
}

func TestDescribe_RefusesABlankTarget(t *testing.T) {
	if _, err := describe.New(&stubRepository{}).Describe(t.Context(), "   "); err == nil {
		t.Fatal("a blank target was accepted")
	}
}

func TestDescribe_ReportsAStoreFailureRatherThanAnEmptyOutline(t *testing.T) {
	repo := &stubRepository{err: errors.New("database is gone")}
	if _, err := describe.New(repo).Describe(t.Context(), "internal/app"); err == nil {
		t.Fatal("a store failure was reported as an empty outline")
	}
}
