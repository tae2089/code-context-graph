package webhook

import (
	"path"
	"strings"
)

type allowRule struct {
	pattern  string
	negate   bool
	wildcard bool
	prefix   string
}

type RepoRule struct {
	Pattern  string
	Branches []string
}

type repoFilterRule struct {
	allowRule
	branches []string
}

type RepoFilter struct {
	rulesFull []repoFilterRule
}

func NewRepoFilter(patterns []string) *RepoFilter {
	rules := make([]RepoRule, 0, len(patterns))
	for _, p := range patterns {
		rules = append(rules, RepoRule{Pattern: p})
	}
	return NewRepoFilterFromRules(rules)
}

func NewRepoFilterFromRules(rules []RepoRule) *RepoFilter {
	full := make([]repoFilterRule, 0, len(rules))
	for _, r := range rules {
		pat := r.Pattern
		ar := allowRule{}
		if strings.HasPrefix(pat, "!") {
			ar.negate = true
			pat = pat[1:]
		}
		if strings.HasSuffix(pat, "/*") {
			ar.wildcard = true
			ar.prefix = pat[:len(pat)-2]
		}
		ar.pattern = pat
		full = append(full, repoFilterRule{allowRule: ar, branches: r.Branches})
	}
	return &RepoFilter{rulesFull: full}
}

var defaultBranches = []string{"main", "master"}

func (f *RepoFilter) IsAllowed(repoFullName string) bool {
	allowed := false
	for _, r := range f.rulesFull {
		if !r.match(repoFullName) {
			continue
		}
		if r.negate {
			allowed = false
		} else {
			allowed = true
		}
	}
	return allowed
}

func (f *RepoFilter) IsAllowedRef(repoFullName, ref string) bool {
	if len(f.rulesFull) == 0 {
		return false
	}

	for _, r := range f.rulesFull {
		if !r.match(repoFullName) {
			continue
		}
		if r.negate {
			return false
		}
		branches := r.branches
		if len(branches) == 0 {
			branches = defaultBranches
		}
		return matchBranchPatterns(ref, branches)
	}
	return false
}

func matchBranchPatterns(ref string, patterns []string) bool {
	branch := strings.TrimPrefix(ref, "refs/heads/")
	for _, p := range patterns {
		if matched, _ := path.Match(p, branch); matched {
			return true
		}
	}
	return false
}

func ParseRepoRule(s string) RepoRule {
	pattern, branchStr, found := strings.Cut(s, ":")
	if !found {
		return RepoRule{Pattern: s}
	}
	branches := strings.Split(branchStr, ",")
	return RepoRule{Pattern: pattern, Branches: branches}
}

func (r *allowRule) match(repoFullName string) bool {
	if r.wildcard {
		parts := strings.SplitN(repoFullName, "/", 2)
		if len(parts) != 2 {
			return false
		}
		return r.prefix == "*" || parts[0] == r.prefix
	}
	return repoFullName == r.pattern
}
