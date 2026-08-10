// Package wire is the serialized form of a search answer — the JSON contract
// MCP's search tool and the CLI's --json output both speak. It lives beside
// the search service for the same reason the service exists: two surfaces
// that answer the same query must not drift into describing that answer
// differently.
package wire

import (
	"fmt"

	"github.com/tae2089/code-context-graph/internal/app/search/evidence"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// ResultItem summarizes one node hit returned by full-text search.
// @intent preserve a stable per-item DTO for search responses.
// @domainRule Namespace is set only in federated (multi-namespace) mode so single-namespace responses stay unchanged.
type ResultItem struct {
	ID            uint           `json:"id"`
	QualifiedName string         `json:"qualified_name"`
	Kind          graph.NodeKind `json:"kind"`
	Name          string         `json:"name"`
	FilePath      string         `json:"file_path"`
	StartLine     int            `json:"start_line"`
	EndLine       int            `json:"end_line"`
	// Intent is the node's own @intent tag, empty when nobody wrote one.
	Intent string `json:"intent,omitempty"`
	// Matched names the signals this query touched — name, path, intent — so a
	// caller can tell an exact identifier hit from a hit on a written purpose.
	Matched []evidence.Match `json:"matched"`
	// Reason is the recorded reason the intent index answered with — set only
	// when this hit came from the intent query, so its absence means the hit
	// earned its place on name, path, or token overlap alone.
	Reason string `json:"reason,omitempty"`
	// MatchedTerms are the query terms written in Reason — the proof behind an
	// intent match that token overlap alone cannot see.
	MatchedTerms []string `json:"matched_terms,omitempty"`
	Namespace    string   `json:"namespace,omitempty"`
}

// FileGroup is one file and every hit it answered the query with.
//
// The file is the unit of a search answer because it is the unit of reading: a
// caller picks a file first and a declaration inside it second. Grouping is also
// what let the old per-file cap go — ten hits in one file cost the reader one
// decision, not ten — so a file that appears here appears whole.
//
// @intent let a caller choose between files, then read inside the one it chose.
type FileGroup struct {
	FilePath  string       `json:"file_path"`
	Namespace string       `json:"namespace,omitempty"`
	HitCount  int          `json:"hit_count"`
	Hits      []ResultItem `json:"hits"`
}

// Response wraps the file list so an empty answer can still say why.
//
// It replaced a bare JSON array. An array has nowhere to put the reason a list
// came back short, and a caller that reads `[]` cannot tell "nothing was
// indexed under those words" from "everything found was unjustifiable".
//
// @intent make a search answer self-describing, including when it is empty.
type Response struct {
	Files        []FileGroup `json:"files"`
	FileCount    int         `json:"file_count"`
	WeakFiltered int         `json:"weak_filtered"`
	// Truncated is true when this answer did not reach every file the query
	// answered with. It is never about hits: a shown file is shown whole.
	Truncated bool `json:"truncated"`
	// PoolTruncated is true when the candidate pool behind this answer came back
	// full, so the backend held at least as many candidates as the pool could
	// take and may have held more.
	//
	// It is a second signal rather than part of Truncated because the two say
	// different things and call for different moves. Truncated counts files this
	// page did not reach, and stays about files. PoolTruncated says the page
	// stopped at the edge of what was fetched. `truncated: false` with
	// `pool_truncated: true` is the case that used to read as "that is
	// everything" when it was not: ask for the next page anyway.
	PoolTruncated bool         `json:"pool_truncated"`
	Limits        Limits       `json:"limits"`
	Next          []NextAction `json:"next,omitempty"`
	Note          string       `json:"note,omitempty"`
}

// Limits states the bounds that shaped this page, all counted in files
// except the budget, which only decides whether one more file joins.
// @intent let a caller tell a short answer from the first page of a long one.
type Limits struct {
	Files     int `json:"files"`
	Offset    int `json:"offset"`
	HitBudget int `json:"hit_budget"`
}

// NextAction is one call that widens this answer, written out so an agent can
// make it verbatim.
//
// Before this existed a response could say it withheld things and leave the
// caller with no way to reach them. Naming the tool and its arguments turns a
// dead-end count into a step.
//
// @intent turn what a search withheld into a call the caller can actually make.
type NextAction struct {
	Reason string         `json:"reason"`
	Tool   string         `json:"tool"`
	Args   map[string]any `json:"args"`
}

// NewResponse converts an evidence list into the wire payload.
// @requires withNamespace is true only for federated searches, where a caller needs to know which repository a hit came from.
// @requires query, limit and offset are the ones this list was built with, so the suggested calls repeat the caller's own request.
// @ensures Next is empty exactly when the answer withheld nothing.
// @intent keep one conversion so no two search surfaces can drift apart.
func NewResponse(list evidence.List, query string, limit, offset int, withNamespace bool) Response {
	files := make([]FileGroup, len(list.Files))
	for i, f := range list.Files {
		hits := make([]ResultItem, len(f.Hits))
		for j, r := range f.Hits {
			n := r.Node
			hits[j] = ResultItem{
				ID: n.ID, QualifiedName: n.QualifiedName, Kind: n.Kind, Name: n.Name,
				FilePath: n.FilePath, StartLine: n.StartLine, EndLine: n.EndLine,
				Intent: r.Intent, Matched: r.Matched,
				Reason: r.Reason, MatchedTerms: r.MatchedTerms,
			}
			if withNamespace {
				hits[j].Namespace = n.Namespace
			}
		}
		files[i] = FileGroup{FilePath: f.FilePath, HitCount: f.HitCount(), Hits: hits}
		if withNamespace {
			files[i].Namespace = f.Namespace
		}
	}
	return Response{
		Files:         files,
		FileCount:     len(files),
		WeakFiltered:  list.WeakFiltered,
		Truncated:     list.OverflowFiles > 0,
		PoolTruncated: list.PoolTruncated,
		Limits:        Limits{Files: limit, Offset: offset, HitBudget: evidence.PageHitBudget},
		Next:          nextActions(list, query, limit, offset),
		Note:          list.Note,
	}
}

// nextActions writes one call per thing this answer withheld. An empty answer
// gets none: search already scored the query against names and recorded
// reasons both, so there is no second index left to hand the query to.
//
// A cut candidate pool earns the same next-page call as unreached files, and
// only one of the two is ever written: they are the same call, and the reason
// differs only in what the caller is being told they might find. Without it the
// crowded-file case — one file whose hits fill the whole pool — would report a
// pool cut and hand the caller nowhere to go with it.
//
// @ensures every returned action names a tool every search surface offers, with arguments that need no editing.
// @ensures at most one next-page action is returned, however many bounds stopped this page.
// @intent make the follow-up call obvious enough that an agent does not have to invent one.
func nextActions(list evidence.List, query string, limit, offset int) []NextAction {
	actions := make([]NextAction, 0, 2)
	switch {
	case list.OverflowFiles > 0:
		actions = append(actions, NextAction{
			Reason: fmt.Sprintf("%d more files answered this query and are not on this page", list.OverflowFiles),
			Tool:   "search",
			Args:   map[string]any{"query": query, "limit": limit, "offset": offset + len(list.Files)},
		})
	case list.PoolTruncated && len(list.Files) > 0:
		actions = append(actions, NextAction{
			Reason: "this page reached the end of the candidates that were fetched, not the end of the answer; more files may follow",
			Tool:   "search",
			Args:   map[string]any{"query": query, "limit": limit, "offset": offset + len(list.Files)},
		})
	}
	if list.WeakFiltered > 0 {
		actions = append(actions, NextAction{
			Reason: fmt.Sprintf("%d candidates had nothing in their name, path, or @intent to justify them", list.WeakFiltered),
			Tool:   "search",
			Args:   map[string]any{"query": query, "include_weak": true},
		})
	}
	return actions
}
