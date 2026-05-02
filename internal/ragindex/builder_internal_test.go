package ragindex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBuilder_WriteIndex_UsesUniqueTempFileForConcurrentBuilds(t *testing.T) {
	indexDir := filepath.Join(t.TempDir(), ".ccg")
	b := &Builder{IndexDir: indexDir}

	makeIndex := func(label string) *Index {
		return &Index{
			Version: 1,
			BuiltAt: time.Now(),
			Root: &TreeNode{
				ID:      "root",
				Label:   label,
				Summary: strings.Repeat(label+"-payload-", 50000),
				Children: []*TreeNode{
					{ID: "child", Label: label + "-child", Summary: "summary"},
				},
			},
		}
	}

	const writers = 8
	start := make(chan struct{})
	errCh := make(chan error, writers)
	var wg sync.WaitGroup

	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errCh <- b.writeIndex(makeIndex(string(rune('a' + i))))
		}(i)
	}

	close(start)
	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent writeIndex returned error: %v", err)
		}
	}

	target := filepath.Join(indexDir, "doc-index.json")
	raw, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read final index: %v", err)
	}

	var idx Index
	if err := json.Unmarshal(raw, &idx); err != nil {
		t.Fatalf("final index must be valid JSON: %v", err)
	}
	if idx.Root == nil || idx.Root.Label == "" {
		t.Fatalf("final index missing root payload: %#v", idx.Root)
	}

	matches, err := filepath.Glob(filepath.Join(indexDir, "doc-index*.tmp"))
	if err != nil {
		t.Fatalf("glob temp files: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected temp files to be cleaned up, got %v", matches)
	}
}
