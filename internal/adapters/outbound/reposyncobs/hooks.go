// @index OpenTelemetry and trace-log adapter for repository sync queue lifecycles.
package reposyncobs

import (
	"context"

	"go.opentelemetry.io/otel/attribute"

	"github.com/tae2089/code-context-graph/internal/app/reposync"
	"github.com/tae2089/code-context-graph/internal/obs"
)

// @intent adapt OpenTelemetry spans and trace log fields to reposync observability hooks.
type Hooks struct{}

// @intent attach repository and branch attributes to app-owned queue operations.
func (Hooks) Start(ctx context.Context, operation, repo, branch string) (context.Context, func()) {
	ctx, span := obs.StartChildSpan(ctx, operation, attribute.String("repo.full_name", repo), attribute.String("git.branch", branch))
	return ctx, func() { span.End() }
}

// @intent preserve trace correlation fields on repository sync queue logs.
func (Hooks) LogArgs(ctx context.Context) []any { return obs.TraceLogArgs(ctx) }

var _ reposync.Observability = Hooks{}
