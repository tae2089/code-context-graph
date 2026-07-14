// @index Namespace and git provenance evidence builders for namespace-scoped MCP responses.
package mcp

import (
	"context"
	"os"
	"path/filepath"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"

	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
)

// namespaceEvidenceBlock captures stable namespace provenance fields shared across MCP responses.
// @intent keep evidence payloads typed while exposing namespace and git provenance.
type namespaceEvidenceBlock struct {
	Namespace     string                     `json:"namespace"`
	NamespacePath string                     `json:"namespace_path,omitempty"`
	Git           *namespaceGitEvidenceBlock `json:"git,omitempty"`
}

// namespaceGitEvidenceBlock captures git provenance attached to namespace evidence.
// @intent preserve git evidence keys while making nil-versus-false behavior explicit.
type namespaceGitEvidenceBlock struct {
	Branch string `json:"branch"`
	Commit string `json:"commit"`
	Dirty  *bool  `json:"dirty,omitempty"`
	Remote string `json:"remote,omitempty"`
}

// namespaceEvidenceFromContext builds evidence metadata for namespace-scoped graph queries.
// @intent include namespace path and git state when available so LLM has traceable provenance.
func (h *handlers) namespaceEvidenceFromContext(ctx context.Context) namespaceEvidenceBlock {
	ns := requestctx.FromContext(ctx)
	return h.namespaceEvidence(ns)
}

// @intent collect namespace-scoped path and git provenance so MCP responses can explain where graph evidence came from.
func (h *handlers) namespaceEvidence(namespace string) namespaceEvidenceBlock {
	ns := requestctx.Normalize(namespace)
	evidence := namespaceEvidenceBlock{Namespace: ns}

	root := h.deps.Runtime.NamespaceRoot
	if root == "" {
		return evidence
	}

	namespacePath := filepath.Join(root, ns)
	stat, err := os.Stat(namespacePath)
	if err != nil || !stat.IsDir() {
		return evidence
	}

	evidence.NamespacePath = namespacePath

	gitInfo := namespaceGitEvidence(namespacePath)
	if gitInfo != nil {
		evidence.Git = gitInfo
	}
	return evidence
}

// @intent summarize git branch, commit, remote, and dirty state for namespace-scoped evidence blocks.
func namespaceGitEvidence(path string) *namespaceGitEvidenceBlock {
	repo, err := git.PlainOpenWithOptions(path, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return nil
	}

	head, err := repo.Head()
	if err != nil {
		return nil
	}

	info := &namespaceGitEvidenceBlock{
		Branch: branchNameForRef(head.Name()),
		Commit: head.Hash().String(),
	}

	wt, err := repo.Worktree()
	if err == nil {
		if status, err := wt.Status(); err == nil {
			dirty := !status.IsClean()
			info.Dirty = &dirty
		}
	}

	cfg, err := repo.Config()
	if err == nil {
		var remoteURL string
		for _, r := range cfg.Remotes {
			if r.Name == "origin" && len(r.URLs) > 0 {
				remoteURL = r.URLs[0]
				break
			}
		}
		if remoteURL == "" {
			for _, r := range cfg.Remotes {
				if len(r.URLs) > 0 {
					remoteURL = r.URLs[0]
					break
				}
			}
		}
		if remoteURL != "" {
			info.Remote = remoteURL
		}
	}

	return info
}

// @intent normalize git reference names into human-readable branch labels inside evidence metadata.
func branchNameForRef(ref plumbing.ReferenceName) string {
	if ref.IsBranch() {
		return ref.Short()
	}
	return string(ref)
}
