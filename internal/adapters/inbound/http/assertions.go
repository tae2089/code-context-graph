// @index Compile-time MCP dependency checks owned by the server runtime.
package server

import (
	"github.com/tae2089/code-context-graph/internal/adapters/inbound/mcp"
	flows "github.com/tae2089/code-context-graph/internal/app/analyze/flow"
	"github.com/tae2089/code-context-graph/internal/app/analyze/impact"
	"github.com/tae2089/code-context-graph/internal/app/analyze/query"
	"github.com/tae2089/code-context-graph/internal/app/ingest/incremental"
)

// @intent keep MCP adapter interface checks next to the self-hosted/stdout server assembly.
var (
	_ mcp.ImpactAnalyzer    = (*impact.Analyzer)(nil)
	_ mcp.FlowTracer        = (*flows.Tracer)(nil)
	_ mcp.QueryService      = (*query.Service)(nil)
	_ mcp.IncrementalSyncer = (*incremental.Syncer)(nil)
)
