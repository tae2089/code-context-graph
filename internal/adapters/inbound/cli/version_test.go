package cli

import (
	"strings"
	"testing"
)

func TestVersion_PrintsVersionInfo(t *testing.T) {
	deps, stdout, stderr := newTestDeps()
	deps.Version = VersionInfo{
		Version: "v1.2.3",
		Commit:  "abc1234",
		Date:    "2025-01-01T00:00:00Z",
	}

	err := executeCmd(deps, stdout, stderr, "version")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "v1.2.3") {
		t.Errorf("expected version in output, got: %s", out)
	}
	if !strings.Contains(out, "abc1234") {
		t.Errorf("expected commit in output, got: %s", out)
	}
	if !strings.Contains(out, "2025-01-01T00:00:00Z") {
		t.Errorf("expected date in output, got: %s", out)
	}
}

func TestVersion_DefaultsWhenEmpty(t *testing.T) {
	deps, stdout, stderr := newTestDeps()

	err := executeCmd(deps, stdout, stderr, "version")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "dev") {
		t.Errorf("expected 'dev' default version, got: %s", out)
	}
}
