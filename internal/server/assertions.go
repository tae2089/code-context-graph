// @index Compile-time MCP dependency checks owned by the server runtime.
package server

import (
	"github.com/tae2089/code-context-graph/internal/analysis/coupling"
	"github.com/tae2089/code-context-graph/internal/analysis/coverage"
	"github.com/tae2089/code-context-graph/internal/analysis/deadcode"
	"github.com/tae2089/code-context-graph/internal/analysis/flows"
	"github.com/tae2089/code-context-graph/internal/analysis/impact"
	"github.com/tae2089/code-context-graph/internal/analysis/incremental"
	"github.com/tae2089/code-context-graph/internal/analysis/largefunc"
	"github.com/tae2089/code-context-graph/internal/analysis/query"
	"github.com/tae2089/code-context-graph/internal/mcp"
)

// @intent keep MCP adapter interface checks next to the self-hosted/stdout server assembly.
var (
	_ mcp.ImpactAnalyzer    = (*impact.Analyzer)(nil)
	_ mcp.FlowTracer        = (*flows.Tracer)(nil)
	_ mcp.QueryService      = (*query.Service)(nil)
	_ mcp.LargefuncAnalyzer = (*largefunc.Service)(nil)
	_ mcp.DeadcodeAnalyzer  = (*deadcode.Service)(nil)
	_ mcp.CouplingAnalyzer  = (*coupling.Service)(nil)
	_ mcp.CoverageAnalyzer  = (*coverage.Service)(nil)
	_ mcp.IncrementalSyncer = (*incremental.Syncer)(nil)
)
