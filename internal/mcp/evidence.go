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
func (h *handlers) workspaceEvidenceFromContext(ctx context.Context) map[string]any {
	ns := ctxns.FromContext(ctx)
	return h.workspaceEvidence(ns)
}

// @intent collect namespace-scoped workspace and git provenance so MCP responses can explain where graph evidence came from.
func (h *handlers) workspaceEvidence(namespace string) map[string]any {
	ns := ctxns.Normalize(namespace)
	evidence := map[string]any{"namespace": ns}

	root := h.deps.WorkspaceRoot
	if root == "" {
		return evidence
	}

	workspacePath := filepath.Join(root, ns)
	stat, err := os.Stat(workspacePath)
	if err != nil || !stat.IsDir() {
		return evidence
	}

	evidence["workspace_path"] = workspacePath

	gitInfo := workspaceGitEvidence(workspacePath)
	if len(gitInfo) > 0 {
		evidence["git"] = gitInfo
	}
	return evidence
}

// @intent summarize git branch, commit, remote, and dirty state for workspace-scoped evidence blocks.
func workspaceGitEvidence(path string) map[string]any {
	repo, err := git.PlainOpenWithOptions(path, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return nil
	}

	head, err := repo.Head()
	if err != nil {
		return nil
	}

	info := map[string]any{
		"branch": branchNameForRef(head.Name()),
		"commit": head.Hash().String(),
	}

	wt, err := repo.Worktree()
	if err == nil {
		if status, err := wt.Status(); err == nil {
			info["dirty"] = !status.IsClean()
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
			info["remote"] = remoteURL
		}
	}

	if len(info) == 0 {
		return nil
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
