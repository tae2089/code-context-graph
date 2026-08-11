package offsetrule

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The exact sentence, spelled out once. Every other test that cares about it
// compares against the constant, so this is the only place a reword shows up as
// a failure — which is the point: what the CLI prints and what an MCP tool
// answers with are user-visible text, and changing them should be a decision
// somebody made rather than a rename that slipped through.
//
// Pinning the constant only catches a reword. It does not catch a fresh copy of
// the same rule written somewhere else in its own words, which is how
// changes.AnalyzePage came to reject a negative offset with a different
// sentence than every search surface did. So the test also reads the tree: the
// sentence may appear in exactly the two files below and nowhere else.
func TestValidate_SaysOneSentence(t *testing.T) {
	if MustNotBeNegative != "offset must not be negative" {
		t.Fatalf("the shared sentence changed to %q; update every surface that shows it, then this test", MustNotBeNegative)
	}

	err := Validate(-1)
	if err == nil {
		t.Fatal("expected a negative offset to be rejected")
	}
	if !strings.Contains(err.Error(), MustNotBeNegative) {
		t.Fatalf("Validate(-1) = %q, want it to say %q", err.Error(), MustNotBeNegative)
	}
	if err := Validate(0); err != nil {
		t.Fatalf("Validate(0) = %v, want nil", err)
	}

	assertSentenceWrittenOnce(t)
}

// sentenceHomes are the only Go files allowed to contain the sentence as text:
// the declaration itself, and this test, which has to spell it out to pin it.
// Everywhere else names the constant.
var sentenceHomes = map[string]bool{
	filepath.Join("internal", "app", "search", "offsetrule", "offsetrule.go"):      true,
	filepath.Join("internal", "app", "search", "offsetrule", "offsetrule_test.go"): true,
}

// assertSentenceWrittenOnce walks every Go file in the repository and fails on
// any copy of the sentence outside its two homes.
func assertSentenceWrittenOnce(t *testing.T) {
	t.Helper()
	root := repositoryRootForSentenceScan(t)

	var copies []string
	scanned := 0
	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if sentenceHomes[relative] {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		scanned++
		if strings.Contains(string(content), MustNotBeNegative) {
			copies = append(copies, relative)
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("scan repository for copies of the sentence: %v", walkErr)
	}

	// A walk that reached almost nothing would find no copies and look like a
	// pass, so the scan has to prove it went somewhere. The floor is far below
	// the current count on purpose: it catches a root that stopped resolving,
	// not ordinary growth or shrinkage of the tree.
	if scanned < 100 {
		t.Fatalf("scanned only %d Go files under %s; the scan is not reaching the repository root", scanned, root)
	}

	if len(copies) > 0 {
		t.Fatalf("the sentence %q is written again in %v; name offsetrule.MustNotBeNegative instead so one reword reaches every surface", MustNotBeNegative, copies)
	}
}

// repositoryRootForSentenceScan locates the module root from this test's own
// source path, so the scan does not depend on the working directory the test
// binary happens to run in.
func repositoryRootForSentenceScan(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve the offset-rule test file path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", "..", "..", ".."))
}
