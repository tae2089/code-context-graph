package reposync

import "testing"

func TestResolveCloneURL_UsesConfiguredBaseURL(t *testing.T) {
	got, err := ResolveCloneURL("org/svc", "https://evil.example/repo.git", []string{"https://github.com"}, false)
	if err != nil {
		t.Fatalf("ResolveCloneURL returned error: %v", err)
	}
	if got != "https://github.com/org/svc.git" {
		t.Fatalf("clone URL = %q, want %q", got, "https://github.com/org/svc.git")
	}
}

func TestResolveCloneURL_UsesFirstConfiguredBaseURLAndIgnoresPayload(t *testing.T) {
	got, err := ResolveCloneURL("org/svc", "file:///tmp/evil.git", []string{"https://gitea.local/base", "https://github.com"}, false)
	if err != nil {
		t.Fatalf("ResolveCloneURL returned error: %v", err)
	}
	if got != "https://gitea.local/base/org/svc.git" {
		t.Fatalf("clone URL = %q, want %q", got, "https://gitea.local/base/org/svc.git")
	}
}

func TestResolveCloneURL_AllowsPayloadOnlyInExplicitInsecureMode(t *testing.T) {
	got, err := ResolveCloneURL("org/svc", "file:///tmp/repo.git", nil, true)
	if err != nil {
		t.Fatalf("ResolveCloneURL returned error: %v", err)
	}
	if got != "file:///tmp/repo.git" {
		t.Fatalf("clone URL = %q, want %q", got, "file:///tmp/repo.git")
	}
}

func TestResolveCloneURL_RejectsMissingBaseURLOutsideInsecureMode(t *testing.T) {
	if _, err := ResolveCloneURL("org/svc", "https://github.com/org/svc.git", nil, false); err == nil {
		t.Fatal("expected error when secure mode has no clone base URL")
	}
}

func TestResolveCloneURL_RejectsInvalidRepoName(t *testing.T) {
	if _, err := ResolveCloneURL("org/../svc", "https://github.com/org/svc.git", []string{"https://github.com"}, false); err == nil {
		t.Fatal("expected invalid repo name to be rejected")
	}
}

func TestResolveCloneURL_RejectsInvalidConfiguredBaseURL(t *testing.T) {
	if _, err := ResolveCloneURL("org/svc", "https://github.com/org/svc.git", []string{"https://github.com", "://bad"}, false); err == nil {
		t.Fatal("expected invalid configured base URL to be rejected")
	}
}
