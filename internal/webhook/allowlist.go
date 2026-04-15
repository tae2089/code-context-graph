package webhook

import "strings"

type allowRule struct {
	pattern  string
	negate   bool
	wildcard bool
	prefix   string
}

type RepoAllowlist struct {
	rules []allowRule
}

func NewRepoAllowlist(patterns []string) *RepoAllowlist {
	rules := make([]allowRule, 0, len(patterns))
	for _, p := range patterns {
		r := allowRule{}
		if strings.HasPrefix(p, "!") {
			r.negate = true
			p = p[1:]
		}
		if strings.HasSuffix(p, "/*") {
			r.wildcard = true
			r.prefix = p[:len(p)-2]
		}
		r.pattern = p
		rules = append(rules, r)
	}
	return &RepoAllowlist{rules: rules}
}

func (a *RepoAllowlist) IsAllowed(repoFullName string) bool {
	allowed := false
	for _, r := range a.rules {
		matched := r.match(repoFullName)
		if !matched {
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

func (r *allowRule) match(repoFullName string) bool {
	if r.wildcard {
		parts := strings.SplitN(repoFullName, "/", 2)
		return len(parts) == 2 && parts[0] == r.prefix
	}
	return repoFullName == r.pattern
}
