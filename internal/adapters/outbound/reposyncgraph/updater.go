package reposyncgraph

import (
	"context"

	"github.com/tae2089/code-context-graph/internal/app/ingest/workflow"
	"github.com/tae2089/code-context-graph/internal/app/reposync"
	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
)

// Updater maps repository sync requests to the ingest incremental workflow.
// @intent preserve ingest workflow composition behind the repository sync graph port.
type Updater struct {
	Service *workflow.Service
	Syncer  workflow.IncrementalSyncer
}

// Update preserves replace, size limit, namespace, and unreadable-file semantics.
// @intent replace one synchronized repository namespace using the existing incremental ingest contract.
func (u Updater) Update(ctx context.Context, req reposync.GraphRequest) (reposync.UpdateStats, error) {
	stats, err := u.Service.Update(requestctx.WithNamespace(ctx, req.Namespace), workflow.UpdateOptions{BuildOptions: workflow.BuildOptions{Dir: req.RepoDir, IncludePaths: req.IncludePaths, MaxFileBytes: req.MaxFileBytes, MaxTotalParsedBytes: req.MaxTotalParsedBytes}, Syncer: u.Syncer, Replace: true, FailOnUnreadable: req.FailOnUnreadable})
	if err != nil {
		return reposync.UpdateStats{}, err
	}
	return reposync.UpdateStats{Added: stats.Added, Modified: stats.Modified, Skipped: stats.Skipped, Deleted: stats.Deleted}, nil
}
