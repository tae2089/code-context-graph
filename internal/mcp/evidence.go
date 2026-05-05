// @index Workspace and git provenance evidence builders for namespace-scoped MCP responses.
package mcp

import (
	"context"
	"os"
	"path/filepath"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"

	"github.com/tae2089/code-context-graph/internal/ctxns"
)

// workspaceEvidenceFromContext builds evidence metadata for namespace-scoped graph queries.
// @intent include workspace path and git state when available so LLM has traceable provenance.
func (h *handlers) workspaceEvidenceFromContext(ctx context.Context) workspaceEvidenceBlock {
	ns := ctxns.FromContext(ctx)
	return h.workspaceEvidence(ns)
}

// workspaceEvidenceBlock captures stable workspace provenance fields shared across MCP responses.
// @intent keep evidence payloads typed while preserving the legacy namespace/workspace/git JSON shape.
type workspaceEvidenceBlock struct {
	Namespace     string                    `json:"namespace"`
	WorkspacePath string                    `json:"workspace_path,omitempty"`
	Git           *workspaceGitEvidenceBlock `json:"git,omitempty"`
}

// workspaceGitEvidenceBlock captures git provenance attached to workspace evidence.
// @intent preserve the legacy git evidence keys while making nil-versus-false behavior explicit.
type workspaceGitEvidenceBlock struct {
	Branch string `json:"branch"`
	Commit string `json:"commit"`
	Dirty  *bool  `json:"dirty,omitempty"`
	Remote string `json:"remote,omitempty"`
}

// @intent collect namespace-scoped workspace and git provenance so MCP responses can explain where graph evidence came from.
func (h *handlers) workspaceEvidence(namespace string) workspaceEvidenceBlock {
	ns := ctxns.Normalize(namespace)
	evidence := workspaceEvidenceBlock{Namespace: ns}

	root := h.deps.WorkspaceRoot
	if root == "" {
		return evidence
	}

	workspacePath := filepath.Join(root, ns)
	stat, err := os.Stat(workspacePath)
	if err != nil || !stat.IsDir() {
		return evidence
	}

	evidence.WorkspacePath = workspacePath

	gitInfo := workspaceGitEvidence(workspacePath)
	if gitInfo != nil {
		evidence.Git = gitInfo
	}
	return evidence
}

// @intent summarize git branch, commit, remote, and dirty state for workspace-scoped evidence blocks.
func workspaceGitEvidence(path string) *workspaceGitEvidenceBlock {
	repo, err := git.PlainOpenWithOptions(path, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return nil
	}

	head, err := repo.Head()
	if err != nil {
		return nil
	}

	info := &workspaceGitEvidenceBlock{
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
