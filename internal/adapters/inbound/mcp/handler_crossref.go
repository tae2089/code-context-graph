// @index MCP handler exposing materialized cross-namespace references as a repository dependency map.
package mcp

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tae2089/trace"

	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// crossRefItem serializes one materialized cross-namespace reference.
// @intent expose symbolic target identity and derived resolution state without internal row metadata.
type crossRefItem struct {
	FromNamespace  string `json:"from_namespace"`
	FromNodeID     uint   `json:"from_node_id"`
	Raw            string `json:"raw"`
	ToNamespace    string `json:"to_namespace"`
	ToPath         string `json:"to_path,omitempty"`
	ToSymbol       string `json:"to_symbol,omitempty"`
	ResolvedNodeID *uint  `json:"resolved_node_id,omitempty"`
	Status         string `json:"status"`
	Source         string `json:"source"`
}

// listCrossRefsResponse is the typed wire payload for listCrossRefs.
// @intent keep the requested namespace and direction visible next to the reference list.
type listCrossRefsResponse struct {
	Namespace string         `json:"namespace"`
	Direction string         `json:"direction"`
	Refs      []crossRefItem `json:"refs"`
}

// listCrossRefs lists cross-namespace references declared by or targeting one namespace.
// @intent give agents a repository-level dependency map derived from ccg:// annotations.
// @param request direction selects outbound, inbound, or both; status optionally filters by resolution state.
// @requires the cross-ref lister must be configured.
// @ensures returns refs sorted as stored (outbound before inbound for direction both).
func (h *handlers) listCrossRefs(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx = h.applyNamespace(ctx, request)
	log := h.logger()

	direction := request.GetString("direction", "both")
	if direction != "outbound" && direction != "inbound" && direction != "both" {
		return mcp.NewToolResultError(fmt.Sprintf("unknown direction: %q (want outbound, inbound, or both)", direction)), nil
	}
	status := request.GetString("status", "")
	if status != "" && status != string(graph.CrossRefStatusResolved) && status != string(graph.CrossRefStatusDead) {
		return mcp.NewToolResultError(fmt.Sprintf("unknown status: %q (want resolved or dead)", status)), nil
	}
	ns := requestctx.FromContext(ctx)

	log.Info("list_cross_refs called", "namespace", ns, "direction", direction, "status", status)

	if h.deps.Analysis.CrossRefs == nil {
		return mcp.NewToolResultError("cross-ref lister not configured"), nil
	}

	return finalizeToolResult(h.cachedExecute(ctx, "list_cross_refs:", map[string]any{"namespace": ns, "direction": direction, "status": status}, func() (string, error) {
		var rows []graph.CrossRef
		if direction == "outbound" || direction == "both" {
			outbound, err := h.deps.Analysis.CrossRefs.ListOutboundCrossRefs(ctx, ns)
			if err != nil {
				return "", trace.Wrap(err, "list outbound cross refs")
			}
			rows = append(rows, outbound...)
		}
		if direction == "inbound" || direction == "both" {
			inbound, err := h.deps.Analysis.CrossRefs.ListInboundCrossRefs(ctx, ns)
			if err != nil {
				return "", trace.Wrap(err, "list inbound cross refs")
			}
			rows = append(rows, inbound...)
		}

		refs := make([]crossRefItem, 0, len(rows))
		for _, row := range rows {
			if status != "" && string(row.Status) != status {
				continue
			}
			refs = append(refs, crossRefItem{
				FromNamespace:  row.FromNamespace,
				FromNodeID:     row.FromNodeID,
				Raw:            row.Raw,
				ToNamespace:    row.ToNamespace,
				ToPath:         row.ToPath,
				ToSymbol:       row.ToSymbol,
				ResolvedNodeID: row.ResolvedNodeID,
				Status:         string(row.Status),
				Source:         string(row.Source),
			})
		}
		return marshalJSON(listCrossRefsResponse{Namespace: ns, Direction: direction, Refs: refs})
	}))
}
