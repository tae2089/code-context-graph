package reposyncgraph_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tae2089/code-context-graph/internal/adapters/outbound/reposyncgraph"
	"github.com/tae2089/code-context-graph/internal/app/ingest"
	"github.com/tae2089/code-context-graph/internal/app/ingest/workflow"
	"github.com/tae2089/code-context-graph/internal/app/reposync"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

type recordingSyncer struct {
	files map[string]ingest.FileInfo
}

func (s *recordingSyncer) SyncWithExisting(_ context.Context, files map[string]ingest.FileInfo, _ []string) (*ingest.SyncStats, error) {
	s.files = make(map[string]ingest.FileInfo, len(files))
	for path, file := range files {
		s.files[path] = file
	}
	return &ingest.SyncStats{Added: len(files)}, nil
}

type sourceParser struct{}

func (sourceParser) Parse(string, []byte) ([]graph.Node, []graph.Edge, error) {
	return nil, nil, nil
}

func (sourceParser) ParseWithContext(context.Context, string, []byte) ([]graph.Node, []graph.Edge, error) {
	return nil, nil, nil
}

func TestUpdater_ExcludePatternsSkipMatchingFiles(t *testing.T) {
	repoDir := t.TempDir()
	for name, content := range map[string]string{
		"keep.go":     "package sample\n",
		"skip.gen.go": "package sample\n",
	} {
		if err := os.WriteFile(filepath.Join(repoDir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	syncer := &recordingSyncer{}
	updater := reposyncgraph.Updater{
		Service: &workflow.Service{Parsers: map[string]workflow.Parser{".go": sourceParser{}}},
		Syncer:  syncer,
	}

	if _, err := updater.Update(context.Background(), reposync.GraphRequest{
		RepoDir:         repoDir,
		Namespace:       "api",
		ExcludePatterns: []string{"*.gen.go"},
	}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if _, ok := syncer.files["keep.go"]; !ok {
		t.Fatalf("expected keep.go to be synced, got files=%v", syncer.files)
	}
	if _, ok := syncer.files["skip.gen.go"]; ok {
		t.Fatalf("expected skip.gen.go to be excluded, got files=%v", syncer.files)
	}
}
