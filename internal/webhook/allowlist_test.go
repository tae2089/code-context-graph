package webhook

import "testing"

func TestRepoAllowlist_ExactMatch(t *testing.T) {
	al := NewRepoFilter([]string{"org/svc"})

	if !al.IsAllowed("org/svc") {
		t.Error("expected org/svc to be allowed")
	}
	if al.IsAllowed("org/other") {
		t.Error("expected org/other to be denied")
	}
}

func TestRepoAllowlist_WildcardOrg(t *testing.T) {
	al := NewRepoFilter([]string{"org/*"})

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
	al := NewRepoFilter([]string{"org/*", "!org/private"})

	if !al.IsAllowed("org/svc") {
		t.Error("expected org/svc to be allowed")
	}
	if al.IsAllowed("org/private") {
		t.Error("expected org/private to be denied by negation")
	}
}

func TestRepoAllowlist_EmptyAllowsNothing(t *testing.T) {
	al := NewRepoFilter([]string{})

	if al.IsAllowed("org/svc") {
		t.Error("expected org/svc to be denied when allowlist is empty")
	}
	if al.IsAllowed("anything/at-all") {
		t.Error("expected anything/at-all to be denied when allowlist is empty")
	}
}

func TestRepoAllowlist_MultipleRules(t *testing.T) {
	al := NewRepoFilter([]string{
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

func TestRepoAllowlist_GlobalWildcard(t *testing.T) {
	al := NewRepoFilter([]string{"*/*"})

	if !al.IsAllowed("any-org/any-repo") {
		t.Error("expected any-org/any-repo to be allowed with */*")
	}
	if !al.IsAllowed("testadmin/sample-go") {
		t.Error("expected testadmin/sample-go to be allowed with */*")
	}
	if al.IsAllowed("no-slash") {
		t.Error("expected repo without org prefix to be denied")
	}
}

func TestRepoAllowlist_GlobalWildcardWithNegation(t *testing.T) {
	al := NewRepoFilter([]string{"*/*", "!secret/private"})

	if !al.IsAllowed("org/repo") {
		t.Error("expected org/repo to be allowed")
	}
	if al.IsAllowed("secret/private") {
		t.Error("expected secret/private to be denied by negation")
	}
}

func TestRepoFilter_PerRepoBranch_ExactRepo(t *testing.T) {
	f := NewRepoFilterFromRules([]RepoRule{
		{Pattern: "org/api", Branches: []string{"main", "develop"}},
	})

	if !f.IsAllowedRef("org/api", "refs/heads/main") {
		t.Error("expected org/api + refs/heads/main to be allowed")
	}
	if !f.IsAllowedRef("org/api", "refs/heads/develop") {
		t.Error("expected org/api + refs/heads/develop to be allowed")
	}
	if f.IsAllowedRef("org/api", "refs/heads/feature/x") {
		t.Error("expected org/api + refs/heads/feature/x to be denied")
	}
	if f.IsAllowedRef("org/other", "refs/heads/main") {
		t.Error("expected org/other to be denied (not in rules)")
	}
}

func TestRepoFilter_PerRepoBranch_WildcardRepo(t *testing.T) {
	f := NewRepoFilterFromRules([]RepoRule{
		{Pattern: "org/*", Branches: []string{"main"}},
	})

	if !f.IsAllowedRef("org/svc", "refs/heads/main") {
		t.Error("expected org/svc + main to be allowed")
	}
	if f.IsAllowedRef("org/svc", "refs/heads/develop") {
		t.Error("expected org/svc + develop to be denied")
	}
	if f.IsAllowedRef("other/svc", "refs/heads/main") {
		t.Error("expected other/svc to be denied")
	}
}

func TestRepoFilter_PerRepoBranch_DefaultBranches(t *testing.T) {
	f := NewRepoFilterFromRules([]RepoRule{
		{Pattern: "org/*"},
	})

	if !f.IsAllowedRef("org/svc", "refs/heads/main") {
		t.Error("expected default branch main to be allowed")
	}
	if !f.IsAllowedRef("org/svc", "refs/heads/master") {
		t.Error("expected default branch master to be allowed")
	}
	if f.IsAllowedRef("org/svc", "refs/heads/develop") {
		t.Error("expected develop to be denied when no branches configured")
	}
}

func TestRepoFilter_PerRepoBranch_GlobPattern(t *testing.T) {
	f := NewRepoFilterFromRules([]RepoRule{
		{Pattern: "org/web", Branches: []string{"release/*"}},
	})

	if !f.IsAllowedRef("org/web", "refs/heads/release/v1.0") {
		t.Error("expected release/v1.0 to match release/*")
	}
	if !f.IsAllowedRef("org/web", "refs/heads/release/hotfix") {
		t.Error("expected release/hotfix to match release/*")
	}
	if f.IsAllowedRef("org/web", "refs/heads/main") {
		t.Error("expected main to be denied when only release/* configured")
	}
}

func TestRepoFilter_PerRepoBranch_FirstMatchWins(t *testing.T) {
	f := NewRepoFilterFromRules([]RepoRule{
		{Pattern: "org/api", Branches: []string{"develop"}},
		{Pattern: "org/*", Branches: []string{"main"}},
	})

	if !f.IsAllowedRef("org/api", "refs/heads/develop") {
		t.Error("expected org/api to use first match: develop")
	}
	if f.IsAllowedRef("org/api", "refs/heads/main") {
		t.Error("expected org/api NOT to fall through to org/* rule")
	}
	if !f.IsAllowedRef("org/web", "refs/heads/main") {
		t.Error("expected org/web to match org/* rule with main")
	}
}

func TestRepoFilter_PerRepoBranch_Negation(t *testing.T) {
	f := NewRepoFilterFromRules([]RepoRule{
		{Pattern: "!org/private"},
		{Pattern: "org/*", Branches: []string{"main"}},
	})

	if f.IsAllowedRef("org/private", "refs/heads/main") {
		t.Error("expected org/private to be denied by negation")
	}
	if !f.IsAllowedRef("org/svc", "refs/heads/main") {
		t.Error("expected org/svc + main to be allowed")
	}
}

func TestRepoFilter_PerRepoBranch_EmptyRules(t *testing.T) {
	f := NewRepoFilterFromRules([]RepoRule{})

	if f.IsAllowedRef("org/svc", "refs/heads/main") {
		t.Error("expected empty rules to deny everything")
	}
}

func TestParseRepoRules(t *testing.T) {
	tests := []struct {
		input string
		want  RepoRule
	}{
		{"org/api:main,develop", RepoRule{Pattern: "org/api", Branches: []string{"main", "develop"}}},
		{"org/*", RepoRule{Pattern: "org/*", Branches: nil}},
		{"!org/private", RepoRule{Pattern: "!org/private", Branches: nil}},
		{"org/web:release/*", RepoRule{Pattern: "org/web", Branches: []string{"release/*"}}},
	}

	for _, tt := range tests {
		got := ParseRepoRule(tt.input)
		if got.Pattern != tt.want.Pattern {
			t.Errorf("ParseRepoRule(%q).Pattern = %q, want %q", tt.input, got.Pattern, tt.want.Pattern)
		}
		if len(got.Branches) != len(tt.want.Branches) {
			t.Errorf("ParseRepoRule(%q).Branches = %v, want %v", tt.input, got.Branches, tt.want.Branches)
			continue
		}
		for i, b := range got.Branches {
			if b != tt.want.Branches[i] {
				t.Errorf("ParseRepoRule(%q).Branches[%d] = %q, want %q", tt.input, i, b, tt.want.Branches[i])
			}
		}
	}
}
