package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/tae2089/code-context-graph/internal/pathspec"
)

// TestProjectExcludesKeepOwnSourceInTheGraph holds this repository's own
// `.ccg.yaml` to the rule that it must not hide this repository's own code.
//
// It exists because a pattern already did. `docs/.*` was written to drop the
// generated `./docs` output directory, but a pattern containing `.*` is treated
// as a regular expression and a regular expression matches anywhere in the
// string, so it also dropped `internal/app/docs/`. Four files left the graph and
// nothing said so: an excluded file produces no node, no error, and no warning,
// so the only visible symptom was that questions about documentation generation
// had no answer. Anchoring the pattern with `^` is what confines it to the
// project root.
//
// The check runs against the shipped config rather than against a fixture on
// purpose. A fixture would prove the matcher works, which it already did — the
// defect was in what this project asked the matcher for.
//
// Only `exclude` is checked. The `rules` list is written in the same pattern
// language and carries the same trap, but it decides which lint findings are
// ignored rather than which files exist, and this project ignores findings for
// `cmd/.*` deliberately. There is no way to tell that choice apart from an
// accident by reading the pattern, so asserting on it would only encode the
// config back into a test.
func TestProjectExcludesKeepOwnSourceInTheGraph(t *testing.T) {
	var parsed struct {
		Exclude []string `yaml:"exclude"`
	}
	if err := yaml.Unmarshal([]byte(readRepositoryFile(t, ".ccg.yaml")), &parsed); err != nil {
		t.Fatalf("parse .ccg.yaml: %v", err)
	}
	if len(parsed.Exclude) == 0 {
		t.Fatal(".ccg.yaml declares no exclude patterns")
	}

	for _, rel := range projectSourceFiles(t) {
		if pathspec.MatchExcludes(parsed.Exclude, rel) {
			t.Errorf("exclude drops project source %s", rel)
		}
	}
}

// projectSourceFiles lists the first-party Go files that belong in the graph:
// everything under internal/ and cmd/ that is neither a test nor generated.
func projectSourceFiles(t *testing.T) []string {
	t.Helper()
	root := repositoryRoot(t)
	var files []string
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			rel = filepath.ToSlash(rel)
			switch {
			case !strings.HasSuffix(rel, ".go"),
				strings.HasSuffix(rel, "_test.go"),
				strings.HasSuffix(rel, ".gen.go"),
				strings.HasSuffix(rel, ".pb.go"):
				return nil
			}
			files = append(files, rel)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	if len(files) == 0 {
		t.Fatal("found no project source files to check")
	}
	return files
}
