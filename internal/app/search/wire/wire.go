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
	PoolTruncated bool   `json:"pool_truncated"`
	Limits        Limits `json:"limits"`
	// AnnotationCoverage is how many of the searched declarations recorded a
	// reason — an @intent or a @domainRule — out of how many were indexed.
	//
	// It is on every answer, not only the empty ones, because it is what makes an
	// answer's size readable: two files out of a repository where four hundred
	// declarations recorded a reason is a thin answer, and two out of a repository
	// where six did is most of what there was to find. `with_reason: 0` says the
	// question was put to an index nobody has written anything into yet.
	AnnotationCoverage evidence.Coverage `json:"annotation_coverage"`
	Next               []NextAction      `json:"next,omitempty"`
	Note               string            `json:"note,omitempty"`
}

// CompactResultItem keeps the evidence an agent needs to choose and open a hit,
// without repeating data already present in its file group.
// @intent reduce search-response context while preserving rank evidence and exact source bounds.
type CompactResultItem struct {
	QualifiedName string           `json:"qualified_name"`
	Kind          graph.NodeKind   `json:"kind"`
	StartLine     int              `json:"start_line"`
	EndLine       int              `json:"end_line"`
	Intent        string           `json:"intent,omitempty"`
	Matched       []evidence.Match `json:"matched,omitempty"`
	Reason        string           `json:"reason,omitempty"`
	MatchedTerms  []string         `json:"matched_terms,omitempty"`
}

// CompactFileGroup groups compact hits under the one copy of their file path.
// @intent avoid repeating file identity on every hit while keeping federated namespace labels.
type CompactFileGroup struct {
	FilePath  string              `json:"file_path"`
	Namespace string              `json:"namespace,omitempty"`
	Hits      []CompactResultItem `json:"hits"`
}

// CompactResponse is the token-efficient search view used by agents that do
// not need storage ids or redundant per-hit names and paths.
// @intent preserve search decisions, completion signals, and continuations in a smaller wire payload.
type CompactResponse struct {
	Files              []CompactFileGroup `json:"files"`
	WeakFiltered       int                `json:"weak_filtered"`
	Truncated          bool               `json:"truncated"`
	PoolTruncated      bool               `json:"pool_truncated"`
	Limits             Limits             `json:"limits"`
	AnnotationCoverage evidence.Coverage  `json:"annotation_coverage"`
	Next               []NextAction       `json:"next,omitempty"`
	Note               string             `json:"note,omitempty"`
}

// Compact returns a smaller view without changing the full response contract.
// Search continuations retain compact mode so later pages do not grow again.
// @ensures every search action in Next carries compact=true.
// @intent let CLI and MCP share one loss-aware compact representation.
func (r Response) Compact() CompactResponse {
	files := make([]CompactFileGroup, len(r.Files))
	for i, file := range r.Files {
		hits := make([]CompactResultItem, len(file.Hits))
		for j, hit := range file.Hits {
			intent := hit.Intent
			if intent == hit.Reason {
				intent = ""
			}
			hits[j] = CompactResultItem{
				QualifiedName: hit.QualifiedName,
				Kind:          hit.Kind,
				StartLine:     hit.StartLine,
				EndLine:       hit.EndLine,
				Intent:        intent,
				Matched:       hit.Matched,
				Reason:        hit.Reason,
				MatchedTerms:  hit.MatchedTerms,
			}
		}
		files[i] = CompactFileGroup{
			FilePath:  file.FilePath,
			Namespace: file.Namespace,
			Hits:      hits,
		}
	}

	next := make([]NextAction, len(r.Next))
	for i, action := range r.Next {
		next[i] = action
		if action.Tool != "search" {
			continue
		}
		args := make(map[string]any, len(action.Args)+1)
		for key, value := range action.Args {
			args[key] = value
		}
		args["compact"] = true
		next[i].Args = args
	}

	return CompactResponse{
		Files:              files,
		WeakFiltered:       r.WeakFiltered,
		Truncated:          r.Truncated,
		PoolTruncated:      r.PoolTruncated,
		Limits:             r.Limits,
		AnnotationCoverage: r.AnnotationCoverage,
		Next:               next,
		Note:               r.Note,
	}
}

// Limits states the bounds that shaped this page, all counted in files
// except the budget, which only decides whether one more file joins.
// @intent let a caller tell a short answer from the first page of a long one.
type Limits struct {
	Files     int `json:"files"`
	Offset    int `json:"offset"`
	HitBudget int `json:"hit_budget"`
}

// NextAction is one step that widens this answer, written out so an agent can
// take it without inventing anything.
//
// Before this existed a response could say it withheld things and leave the
// caller with no way to reach them. Naming the tool and its arguments turns a
// dead-end count into a step.
//
// Most steps are a call: Tool and Args. One is not, and cannot be — when the
// answer was empty because nobody ever recorded a reason, the fix is to write
// those reasons, which is reading and judgement rather than a query. That step
// names a Skill instead, and exactly one of the two forms is ever filled in.
//
// @domainRule an action names either a Tool with its Args or a Skill, never both, so a caller never has to guess which one to act on.
// @intent turn what a search withheld into a step the caller can actually take.
type NextAction struct {
	Reason string         `json:"reason"`
	Tool   string         `json:"tool,omitempty"`
	Args   map[string]any `json:"args,omitempty"`
	// Skill names a packaged workflow to run rather than a tool to call. It is
	// set only for the step no tool can perform.
	Skill string `json:"skill,omitempty"`
}

// NewResponse converts an evidence list into the wire payload.
// @requires withNamespace is true only for federated searches, where a caller needs to know which repository a hit came from.
// @requires query, limit and offset are the ones this list was built with, so the suggested calls repeat the caller's own request.
// @ensures Next is empty exactly when the answer withheld nothing and had nothing to explain about coming back empty.
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

		AnnotationCoverage: list.Coverage,
		Next:               nextActions(list, query, limit),
		Note:               list.Note,
	}
}

// annotateSkill is the workflow that writes the reasons a why-question is
// answered from. Named as a constant because the empty-answer step and the tests
// that guard it must agree on the string.
const annotateSkill = "ccg-annotate"

// noteWriteTheReasons is why the annotate step is offered, with the coverage that
// justifies offering it. The fraction is in the reason on purpose: "go annotate"
// with no number behind it reads like generic advice, and the caller has no way
// to judge whether it would help.
const noteWriteTheReasons = "nothing on this page could say why it is here, and only %d of %d indexed declarations have ever recorded a reason; annotate the area you are asking about, rebuild the graph, then ask this question again"

// nextActions writes one step per thing this answer withheld.
//
// An empty answer used to get none. That was wrong in the one case that matters
// most: a question about why code exists is answered out of recorded reasons, so
// where nobody wrote any, the empty answer was reported as a fact about the
// codebase and the caller concluded the code was not there. It now gets the step
// that fixes the cause. A page whose every shown hit justified nothing gets the
// same step, for the same reason.
//
// The step is withheld when coverage was never measured. A surface that did not
// reach the recorded-reason index leaves the fraction at zero, and "0 of 0" would
// state a measurement that was never taken.
//
// A cut candidate pool earns the same next-page call as unreached files, and
// only one of the two is ever written: they are the same call, and the reason
// differs only in what the caller is being told they might find. Without it the
// crowded-file case — one file whose hits fill the whole pool — would report a
// pool cut and hand the caller nowhere to go with it.
//
// The offset it hands back is the list's own NextOffset, not this page's offset
// plus the files on it. The two agree whenever a page is one contiguous run of
// the answer, and only the code that cut the page knows whether it was.
//
// @ensures every returned action names either a tool every search surface offers, with arguments that need no editing, or a skill every surface ships.
// @ensures at most one next-page action is returned, however many bounds stopped this page.
// @ensures the annotate step is returned only when no shown hit justified itself and coverage was actually measured.
// @intent make the follow-up step obvious enough that an agent does not have to invent one.
func nextActions(list evidence.List, query string, limit int) []NextAction {
	actions := make([]NextAction, 0, 3)
	switch {
	case list.OverflowFiles > 0:
		actions = append(actions, NextAction{
			Reason: fmt.Sprintf("%d more files answered this query and are not on this page", list.OverflowFiles),
			Tool:   "search",
			Args:   map[string]any{"query": query, "limit": limit, "offset": list.NextOffset},
		})
	case list.PoolTruncated && len(list.Files) > 0:
		actions = append(actions, NextAction{
			Reason: "this page reached the end of the candidates that were fetched, not the end of the answer; more files may follow",
			Tool:   "search",
			Args:   map[string]any{"query": query, "limit": limit, "offset": list.NextOffset},
		})
	}
	if list.WeakFiltered > 0 {
		actions = append(actions, NextAction{
			Reason: fmt.Sprintf("%d candidates had nothing in their name, path, or @intent to justify them", list.WeakFiltered),
			Tool:   "search",
			Args:   map[string]any{"query": query, "include_weak": true},
		})
	}
	// Last, because it is the slowest move and the only one that changes the
	// codebase. The cheap retries above are worth trying first, and a caller
	// reading top to bottom meets them in that order.
	if !list.Justified() && list.Coverage.Known() {
		actions = append(actions, NextAction{
			Reason: fmt.Sprintf(noteWriteTheReasons, list.Coverage.WithReason, list.Coverage.Declarations),
			Skill:  annotateSkill,
		})
	}
	return actions
}
