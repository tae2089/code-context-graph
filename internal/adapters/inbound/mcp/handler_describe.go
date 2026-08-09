// @index MCP handler for structural outlines: what the graph holds under one path.
package mcp

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/app/describe"
)

// describeReplacedPatterns are the query_graph patterns describe took over.
//
// They are answered with a sentence rather than "unknown pattern" because both
// were reachable by any agent holding an older tool description, and a caller
// that gets told its pattern does not exist has no way to learn that the
// capability moved rather than disappeared.
//
// Both only ever reported a file's own declarations: the graph records a
// contains edge from a file to each declaration in it and nowhere else, so
// children_of on a type always came back empty, and file_summary returned
// counts of the same rows describe now lists.
//
// @intent tell a caller where a removed capability went instead of that it is gone.
var describeReplacedPatterns = map[string]string{
	"children_of":  `call describe with the file path as "target" to list its declarations`,
	"file_summary": `call describe with the file path as "target"; it lists the declarations the counts were taken from`,
}

// describeDecl is one declaration written in the described file.
// @intent give a reader a name, a line to open, and why it exists.
type describeDecl struct {
	NodeID        uint   `json:"node_id"`
	Name          string `json:"name"`
	QualifiedName string `json:"qualified_name"`
	Kind          string `json:"kind"`
	StartLine     int    `json:"start_line"`
	EndLine       int    `json:"end_line"`
	Intent        string `json:"intent,omitempty"`
}

// describeChild is one folder or file directly inside the described folder.
// @intent let a caller descend one step at a time instead of reading a whole subtree.
type describeChild struct {
	Path      string `json:"path"`
	Kind      string `json:"kind"`
	FileCount int    `json:"file_count"`
	DeclCount int    `json:"decl_count"`
}

// describeSuggestion is one place a missed target's name actually lives.
// @intent turn a wrong path into the right one instead of into an empty answer.
type describeSuggestion struct {
	QualifiedName string `json:"qualified_name"`
	Kind          string `json:"kind"`
	FilePath      string `json:"file_path"`
	StartLine     int    `json:"start_line"`
}

// describeResponse is the wire payload for describe.
//
// There is no limit, no offset, and no relevance here. An outline that dropped
// rows to fit a page would be a worse answer than a search result, because a
// caller reading it has no way to tell a folder with eight files from one whose
// ninth was trimmed. Folders are collapsed to one level instead, which bounds
// the answer by structure rather than by an arbitrary count.
//
// @intent answer "what is in here" exactly, so the ranked tools never have to guess.
type describeResponse struct {
	Target       string               `json:"target"`
	Scope        string               `json:"scope"`
	Children     []describeChild      `json:"children,omitempty"`
	Declarations []describeDecl       `json:"declarations,omitempty"`
	Suggestions  []describeSuggestion `json:"suggestions,omitempty"`
	Next         []nextAction         `json:"next,omitempty"`
}

// describe lists what the graph holds under one path.
//
// It is the tool the ranked ones hand off to. `search` and `find_by_intent`
// guess which declarations matter and can be wrong about it; this one only
// reports what exists, so it cannot be. Once a caller has a path from either of
// them, everything after that is a lookup rather than a query.
//
// @param request target is a folder path, a file path, or a name that turns out to be neither.
// @requires Graph.Describe must be configured.
// @ensures a target the graph does not hold comes back as scope "unknown" with places that name lives, never as an error.
func (h *handlers) describe(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx = h.applyNamespace(ctx, request)
	log := h.logger()

	target, err := request.RequireString("target")
	if err != nil {
		return missingParamResult(err)
	}
	if h.deps.Graph.Describe == nil {
		return mcp.NewToolResultError("describe is not configured"), nil
	}

	log.Info("describe called", "target", target)

	return finalizeToolResult(h.cachedExecute(ctx, "describe:", map[string]any{
		"target":    target,
		"namespace": requestNamespace(request),
	}, func() (string, error) {
		outline, err := h.deps.Graph.Describe.Describe(ctx, target)
		if err != nil {
			log.Error("describe error", "target", target, trace.SlogError(err))
			return "", newToolResultErr(fmt.Sprintf("describe error: %v", err))
		}
		log.Info("describe completed", "target", outline.Target, "scope", string(outline.Scope),
			"children", len(outline.Children), "declarations", len(outline.Declarations))
		result, err := marshalJSON(newDescribeResponse(outline))
		if err != nil {
			return "", trace.Wrap(err, "marshal result")
		}
		return result, nil
	}))
}

// newDescribeResponse converts an application outline into the wire payload.
// @intent keep one conversion so the tool's shape cannot drift from the service's.
func newDescribeResponse(outline describe.Outline) describeResponse {
	response := describeResponse{Target: outline.Target, Scope: string(outline.Scope)}
	for _, child := range outline.Children {
		response.Children = append(response.Children, describeChild{
			Path: child.Path, Kind: child.Kind, FileCount: child.FileCount, DeclCount: child.DeclCount,
		})
	}
	for _, decl := range outline.Declarations {
		response.Declarations = append(response.Declarations, describeDecl{
			NodeID: decl.NodeID, Name: decl.Name, QualifiedName: decl.QualifiedName,
			Kind: decl.Kind, StartLine: decl.StartLine, EndLine: decl.EndLine, Intent: decl.Intent,
		})
	}
	for _, suggestion := range outline.Suggestions {
		response.Suggestions = append(response.Suggestions, describeSuggestion{
			QualifiedName: suggestion.QualifiedName, Kind: suggestion.Kind,
			FilePath: suggestion.FilePath, StartLine: suggestion.StartLine,
		})
	}
	response.Next = describeNextActions(outline)
	return response
}

// describeNextActions names what to do when the target was not found.
//
// A found outline gets none. Its rows already carry paths and node ids, and a
// caller holding those does not need to be told what a path is for; printing a
// call per row would be longer than the answer.
//
// @ensures every returned action names a real tool with arguments that need no editing.
func describeNextActions(outline describe.Outline) []nextAction {
	if outline.Scope != describe.ScopeUnknown {
		return nil
	}
	if len(outline.Suggestions) > 0 {
		return []nextAction{{
			Reason: fmt.Sprintf("%q is not a path in this graph, but that name is declared in %s", outline.Target, outline.Suggestions[0].FilePath),
			Tool:   "describe",
			Args:   map[string]any{"target": outline.Suggestions[0].FilePath},
		}}
	}
	return []nextAction{
		{
			Reason: fmt.Sprintf("nothing is stored under %q; if it is an identifier, search for it by name", outline.Target),
			Tool:   "search",
			Args:   map[string]any{"query": outline.Target},
		},
		{
			Reason: "if you cannot name the symbol, ask why it exists instead",
			Tool:   "find_by_intent",
			Args:   map[string]any{"question": fmt.Sprintf("what handles %s", outline.Target)},
		},
	}
}
