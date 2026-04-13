package cli

import (
	"strings"
	"testing"
)

func TestExampleCommand_DefaultsToGo(t *testing.T) {
	deps, stdout, stderr := newTestDeps()

	if err := executeCmd(deps, stdout, stderr, "example"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := stdout.String()
	for _, want := range []string{"@intent", "@param", "func "} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in Go example output, got:\n%s", want, out)
		}
	}
}

func TestExampleCommand_GoLanguage(t *testing.T) {
	deps, stdout, stderr := newTestDeps()

	if err := executeCmd(deps, stdout, stderr, "example", "go"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := stdout.String()
	for _, want := range []string{"//", "@intent", "@param", "@return", "@index", "func "} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in Go example output, got:\n%s", want, out)
		}
	}
}

func TestExampleCommand_PythonLanguage(t *testing.T) {
	deps, stdout, stderr := newTestDeps()

	if err := executeCmd(deps, stdout, stderr, "example", "python"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := stdout.String()
	for _, want := range []string{"# @intent", "# @param", "def "} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in Python example output, got:\n%s", want, out)
		}
	}
}

func TestExampleCommand_JavaLanguage(t *testing.T) {
	deps, stdout, stderr := newTestDeps()

	if err := executeCmd(deps, stdout, stderr, "example", "java"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := stdout.String()
	for _, want := range []string{"// @intent", "// @param", "public "} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in Java example output, got:\n%s", want, out)
		}
	}
}

func TestExampleCommand_UnknownLanguageFails(t *testing.T) {
	deps, stdout, stderr := newTestDeps()

	err := executeCmd(deps, stdout, stderr, "example", "brainfuck")
	if err == nil {
		t.Fatalf("expected error for unknown language, got output:\n%s", stdout.String())
	}
}
