// @index Repository/branch admission, ref normalization, and namespace policy for repository sync.
package reposync

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// NormalizeBranchRef extracts a branch name only from refs/heads references.
// @intent reject tags and other Git refs before repository sync admission.
func NormalizeBranchRef(ref string) (string, bool) {
	branch, ok := strings.CutPrefix(ref, "refs/heads/")
	return branch, ok && branch != ""
}

// ExtractNamespace derives the existing repo-name namespace strategy.
// @intent preserve repository-backed namespace compatibility while removing owner segments.
func ExtractNamespace(repoFullName string) string {
	idx := strings.Index(repoFullName, "/")
	if idx < 0 {
		return repoFullName
	}
	return strings.ReplaceAll(repoFullName[idx+1:], "/", "-")
}

// allowRule is one compiled allow or deny pattern with optional negation and wildcard prefix.
// @intent represent a single Atlantis-style repo pattern in a form cheap to evaluate per webhook.
type allowRule struct {
	pattern  string
	negate   bool
	wildcard bool
	prefix   string
}

// RepoRule is the user-facing config form pairing a repo pattern with optional branch globs.
// @intent expose a stable shape for CLI/YAML config without leaking the internal compiled rule layout.
type RepoRule struct {
	Pattern  string
	Branches []string
}

// repoFilterRule binds a compiled allow/deny pattern to its per-rule branch allowlist.
// @intent keep branch policy attached to the rule that allowed the repo so order-sensitive evaluation stays local.
type repoFilterRule struct {
	allowRule
	branches []string
}

// RepoFilter holds the ordered rule list used to decide repo and branch admission.
// @intent provide a single matcher whose result depends on rule declaration order, where later matching rules override earlier ones.
type RepoFilter struct {
	rulesFull []repoFilterRule
}

// NewRepoFilter expands simple string patterns into webhook repo rules.
// @intent keep CLI-style allowlist configuration compatible with the richer rule matcher.
// @param patterns is the ordered allow/deny rule list in compact string form.
// @ensures returns a filter whose rule order matches the caller input order.
func NewRepoFilter(patterns []string) *RepoFilter {
	rules := make([]RepoRule, 0, len(patterns))
	for _, p := range patterns {
		rules = append(rules, RepoRule{Pattern: p})
	}
	return NewRepoFilterFromRules(rules)
}

// NewRepoFilterFromRules compiles repository and branch rules into a matcher.
// @intent centralize Atlantis-style repo filtering so webhook dispatch can make one consistent allow decision.
// @param rules is the ordered repo rule list whose later matches override earlier ones.
// @ensures returns a filter that preserves the source rule order for evaluation.
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

// IsAllowed reports whether a repository matches the configured allow and deny rules.
// @intent let callers gate repository-level sync before looking at branch-specific restrictions.
// @domainRule rules are evaluated in declaration order and the last matching rule wins; a later allow rule overrides an earlier deny match and vice versa.
// @domainRule a repository with no matching rule is denied (default-deny).
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

// IsAllowedRef resolves a git ref to a branch and applies branch allowlist rules.
// @intent reject non-branch webhook refs before they can enter the sync pipeline.
// @return returns false for non-branch refs and otherwise defers to branch policy evaluation.
func (f *RepoFilter) IsAllowedRef(repoFullName, ref string) bool {
	branch, ok := NormalizeBranchRef(ref)
	if !ok {
		return false
	}
	return f.IsAllowedBranch(repoFullName, branch)
}

// IsAllowedBranch reports whether a repository and branch pair should be processed.
// @intent combine repo allowlist and branch policy so webhook handlers can skip unsupported pushes cheaply.
// @domainRule with no rules configured the result is always false (default-deny).
// @domainRule rules are evaluated in declaration order; each later matching rule replaces the prior decision, so a negate rule clears any previous allow and a subsequent allow rule re-enables the repo.
// @domainRule when a non-negate rule matches but specifies no branches, the built-in defaults ("main", "master") are used.
func (f *RepoFilter) IsAllowedBranch(repoFullName, branch string) bool {
	if len(f.rulesFull) == 0 {
		return false
	}

	allowed := false
	for _, r := range f.rulesFull {
		if !r.match(repoFullName) {
			continue
		}
		if r.negate {
			allowed = false
			continue
		}
		branches := r.branches
		if len(branches) == 0 {
			branches = defaultBranches
		}
		allowed = matchBranchPatterns(branch, branches)
	}
	return allowed
}

// @intent evaluate branch globs consistently across repo rules without duplicating path-match logic at call sites.
func matchBranchPatterns(branch string, patterns []string) bool {
	for _, p := range patterns {
		if matched, _ := path.Match(p, branch); matched {
			return true
		}
	}
	return false
}

// ParseRepoRule decodes a repo rule string with optional comma-separated branch patterns.
// @intent preserve compact CLI config while still supporting per-repository branch restrictions.
// @param s follows pattern or pattern:branch1,branch2 syntax.
// @return returns a RepoRule with branch restrictions only when the colon form is present.
func ParseRepoRule(s string) RepoRule {
	pattern, branchStr, found := strings.Cut(s, ":")
	if !found {
		return RepoRule{Pattern: s}
	}
	branches := strings.Split(branchStr, ",")
	return RepoRule{Pattern: pattern, Branches: branches}
}

// AllowRuleOwners returns the positive repository owner prefixes present in allow rules.
// @intent let server startup warn when repo-name namespace extraction is used with multi-owner webhook allowlists.
// @domainRule deny rules are ignored because they only narrow the positive admission surface.
func AllowRuleOwners(rules []RepoRule) []string {
	owners := make(map[string]struct{})
	for _, rule := range rules {
		pattern := strings.TrimSpace(rule.Pattern)
		if pattern == "" || strings.HasPrefix(pattern, "!") {
			continue
		}
		owner, _, ok := strings.Cut(pattern, "/")
		if !ok || owner == "" {
			continue
		}
		owners[owner] = struct{}{}
	}
	out := make([]string, 0, len(owners))
	for owner := range owners {
		out = append(out, owner)
	}
	sort.Strings(out)
	return out
}

// AllowRulesSpanMultipleOwners reports whether webhook rules admit repositories under more than one owner prefix.
// @intent identify configurations that can collide under the current repo-name namespace strategy.
func AllowRulesSpanMultipleOwners(rules []RepoRule) (bool, []string) {
	owners := AllowRuleOwners(rules)
	return len(owners) > 1 || (len(owners) == 1 && owners[0] == "*"), owners
}

// ValidateRepoNameNamespaceRules rejects admission surfaces that can map distinct repositories to one repo-name namespace.
// @intent fail webhook startup before equal repo names from different owners can share checkout and graph state.
// @domainRule repo-name namespaces are safe only when all positive allow rules are constrained to one non-wildcard owner.
func ValidateRepoNameNamespaceRules(rules []RepoRule) error {
	spans, owners := AllowRulesSpanMultipleOwners(rules)
	if !spans {
		return nil
	}
	return fmt.Errorf("webhook allow-repo owners %v can collide under repo-name namespaces; configure one non-wildcard owner", owners)
}

// @intent apply one compiled allow or deny pattern to a repository full name during filter evaluation.
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
