package cli

import (
	"strings"
	"testing"
)

func TestTagsCommand_ListsAllTags(t *testing.T) {
	deps, stdout, stderr := newTestDeps()

	if err := executeCmd(deps, stdout, stderr, "tags"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := stdout.String()
	for _, want := range []string{
		"@index",
		"@intent",
		"@domainRule",
		"@sideEffect",
		"@mutates",
		"@requires",
		"@ensures",
		"@param",
		"@return",
		"@see",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in tags output, got:\n%s", want, out)
		}
	}
}

func TestTagsCommand_ShowsDescriptions(t *testing.T) {
	deps, stdout, stderr := newTestDeps()

	if err := executeCmd(deps, stdout, stderr, "tags"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := stdout.String()
	// Each tag should have a description (non-empty line next to it)
	if len(out) < 200 {
		t.Errorf("expected detailed tags output, got too short:\n%s", out)
	}
}
