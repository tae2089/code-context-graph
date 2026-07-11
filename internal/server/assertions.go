// @index Compile-time MCP dependency checks owned by the server runtime.
package server

import (
	"github.com/tae2089/code-context-graph/internal/analysis/flows"
	"github.com/tae2089/code-context-graph/internal/analysis/impact"
	"github.com/tae2089/code-context-graph/internal/analysis/incremental"
	"github.com/tae2089/code-context-graph/internal/analysis/query"
	"github.com/tae2089/code-context-graph/internal/mcp"
)

// @intent keep MCP adapter interface checks next to the self-hosted/stdout server assembly.
var (
	_ mcp.ImpactAnalyzer    = (*impact.Analyzer)(nil)
	_ mcp.FlowTracer        = (*flows.Tracer)(nil)
	_ mcp.QueryService      = (*query.Service)(nil)
	_ mcp.IncrementalSyncer = (*incremental.Syncer)(nil)
)
