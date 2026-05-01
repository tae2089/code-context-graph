package webhook

import "testing"

func TestResolveCloneURL_UsesConfiguredBaseURL(t *testing.T) {
	got, err := ResolveCloneURL("org/svc", "https://evil.example/repo.git", "https://github.com", false)
	if err != nil {
		t.Fatalf("ResolveCloneURL returned error: %v", err)
	}
	if got != "https://github.com/org/svc.git" {
		t.Fatalf("clone URL = %q, want %q", got, "https://github.com/org/svc.git")
	}
}

func TestResolveCloneURL_AllowsPayloadOnlyInExplicitInsecureMode(t *testing.T) {
	got, err := ResolveCloneURL("org/svc", "file:///tmp/repo.git", "", true)
	if err != nil {
		t.Fatalf("ResolveCloneURL returned error: %v", err)
	}
	if got != "file:///tmp/repo.git" {
		t.Fatalf("clone URL = %q, want %q", got, "file:///tmp/repo.git")
	}
}

func TestResolveCloneURL_RejectsMissingBaseURLOutsideInsecureMode(t *testing.T) {
	if _, err := ResolveCloneURL("org/svc", "https://github.com/org/svc.git", "", false); err == nil {
		t.Fatal("expected error when secure mode has no clone base URL")
	}
}

func TestResolveCloneURL_RejectsInvalidRepoName(t *testing.T) {
	if _, err := ResolveCloneURL("org/../svc", "https://github.com/org/svc.git", "https://github.com", false); err == nil {
		t.Fatal("expected invalid repo name to be rejected")
	}
}
