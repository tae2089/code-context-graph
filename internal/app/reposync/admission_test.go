package reposync

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

func TestAllowRulesSpanMultipleOwners(t *testing.T) {
	tests := []struct {
		name       string
		rules      []RepoRule
		wantWarn   bool
		wantOwners []string
	}{
		{
			name:       "single owner wildcard",
			rules:      []RepoRule{{Pattern: "org/*"}, {Pattern: "!org/private"}},
			wantOwners: []string{"org"},
		},
		{
			name:       "single owner exact repos",
			rules:      []RepoRule{{Pattern: "org/api"}, {Pattern: "org/web"}},
			wantOwners: []string{"org"},
		},
		{
			name:       "multiple owners",
			rules:      []RepoRule{{Pattern: "org/*"}, {Pattern: "external/shared"}},
			wantWarn:   true,
			wantOwners: []string{"external", "org"},
		},
		{
			name:       "global wildcard",
			rules:      []RepoRule{{Pattern: "*/*"}},
			wantWarn:   true,
			wantOwners: []string{"*"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotWarn, gotOwners := AllowRulesSpanMultipleOwners(tt.rules)
			if gotWarn != tt.wantWarn {
				t.Fatalf("warning = %v, want %v", gotWarn, tt.wantWarn)
			}
			if len(gotOwners) != len(tt.wantOwners) {
				t.Fatalf("owners = %#v, want %#v", gotOwners, tt.wantOwners)
			}
			for i := range gotOwners {
				if gotOwners[i] != tt.wantOwners[i] {
					t.Fatalf("owners = %#v, want %#v", gotOwners, tt.wantOwners)
				}
			}
		})
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

func TestRepoFilter_PerRepoBranch_LaterRuleOverrides(t *testing.T) {
	f := NewRepoFilterFromRules([]RepoRule{
		{Pattern: "org/api", Branches: []string{"develop"}},
		{Pattern: "org/*", Branches: []string{"main"}},
	})

	if f.IsAllowedRef("org/api", "refs/heads/develop") {
		t.Error("expected later matching rule to override earlier develop-only rule")
	}
	if !f.IsAllowedRef("org/api", "refs/heads/main") {
		t.Error("expected org/api to use later org/* rule with main")
	}
	if !f.IsAllowedRef("org/web", "refs/heads/main") {
		t.Error("expected org/web to match org/* rule with main")
	}
}

func TestRepoFilter_PerRepoBranch_Negation(t *testing.T) {
	f := NewRepoFilterFromRules([]RepoRule{
		{Pattern: "org/*", Branches: []string{"main"}},
		{Pattern: "!org/private"},
	})

	if f.IsAllowedRef("org/private", "refs/heads/main") {
		t.Error("expected org/private to be denied by negation")
	}
	if !f.IsAllowedRef("org/svc", "refs/heads/main") {
		t.Error("expected org/svc + main to be allowed")
	}
}

func TestRepoFilter_IsAllowedAndIsAllowedRef_ShareOverrideSemantics(t *testing.T) {
	f := NewRepoFilterFromRules([]RepoRule{
		{Pattern: "org/*", Branches: []string{"main"}},
		{Pattern: "!org/private"},
		{Pattern: "external/shared", Branches: []string{"release/*"}},
	})

	tests := []struct {
		repo string
		ref  string
		want bool
	}{
		{repo: "org/svc", ref: "refs/heads/main", want: true},
		{repo: "org/private", ref: "refs/heads/main", want: false},
		{repo: "external/shared", ref: "refs/heads/release/v1", want: true},
		{repo: "external/shared", ref: "refs/heads/main", want: false},
		{repo: "other/repo", ref: "refs/heads/main", want: false},
	}

	for _, tt := range tests {
		if got := f.IsAllowedRef(tt.repo, tt.ref); got != tt.want {
			t.Errorf("IsAllowedRef(%q, %q) = %v, want %v", tt.repo, tt.ref, got, tt.want)
		}
		if got := f.IsAllowed(tt.repo); got != (tt.repo == "org/svc" || tt.repo == "external/shared") {
			t.Errorf("IsAllowed(%q) = %v, unexpected repo-level decision", tt.repo, got)
		}
	}
}

func TestRepoFilter_IsAllowedRef_LaterNegationOverridesEarlierAllow(t *testing.T) {
	f := NewRepoFilterFromRules([]RepoRule{
		{Pattern: "org/*", Branches: []string{"main"}},
		{Pattern: "!org/private"},
	})

	if f.IsAllowedRef("org/private", "refs/heads/main") {
		t.Error("expected org/private to be denied because later negation overrides earlier allow")
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
