package webhook

import "testing"

func TestRepoAllowlist_ExactMatch(t *testing.T) {
	al := NewRepoAllowlist([]string{"org/svc"})

	if !al.IsAllowed("org/svc") {
		t.Error("expected org/svc to be allowed")
	}
	if al.IsAllowed("org/other") {
		t.Error("expected org/other to be denied")
	}
}

func TestRepoAllowlist_WildcardOrg(t *testing.T) {
	al := NewRepoAllowlist([]string{"org/*"})

	if !al.IsAllowed("org/svc") {
		t.Error("expected org/svc to be allowed")
	}
	if !al.IsAllowed("org/another") {
		t.Error("expected org/another to be allowed")
	}
	if al.IsAllowed("other/svc") {
		t.Error("expected other/svc to be denied")
	}
}

func TestRepoAllowlist_Negation(t *testing.T) {
	al := NewRepoAllowlist([]string{"org/*", "!org/private"})

	if !al.IsAllowed("org/svc") {
		t.Error("expected org/svc to be allowed")
	}
	if al.IsAllowed("org/private") {
		t.Error("expected org/private to be denied by negation")
	}
}

func TestRepoAllowlist_EmptyAllowsNothing(t *testing.T) {
	al := NewRepoAllowlist([]string{})

	if al.IsAllowed("org/svc") {
		t.Error("expected org/svc to be denied when allowlist is empty")
	}
	if al.IsAllowed("anything/at-all") {
		t.Error("expected anything/at-all to be denied when allowlist is empty")
	}
}

func TestRepoAllowlist_MultipleRules(t *testing.T) {
	al := NewRepoAllowlist([]string{
		"acme/*",
		"!acme/internal",
		"external/shared",
	})

	if !al.IsAllowed("acme/api") {
		t.Error("expected acme/api to be allowed")
	}
	if !al.IsAllowed("acme/web") {
		t.Error("expected acme/web to be allowed")
	}

	if al.IsAllowed("acme/internal") {
		t.Error("expected acme/internal to be denied by negation")
	}

	if !al.IsAllowed("external/shared") {
		t.Error("expected external/shared to be allowed")
	}

	if al.IsAllowed("external/other") {
		t.Error("expected external/other to be denied")
	}
	if al.IsAllowed("random/repo") {
		t.Error("expected random/repo to be denied")
	}
}
