// @index MCP handler for answering plain-language intent questions from recorded reasons.
package mcp

import (
	"context"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/app/search/intent"
)

// intentEntry is one declaration whose recorded reason answered the question.
// @intent give the caller a node id to walk from and the reason that earned the hit.
type intentEntry struct {
	NodeID        uint   `json:"node_id"`
	Name          string `json:"name"`
	QualifiedName string `json:"qualified_name"`
	Kind          string `json:"kind"`
	Reason        string `json:"reason"`
	Line          int    `json:"line,omitempty"`
	// MatchedTerms are the words of the question written in this reason. A row
	// carrying one very common word is a much weaker hit than one carrying three,
	// and without this the two rows look identical.
	MatchedTerms []string `json:"matched_terms,omitempty"`
}

// intentTerm is one word of the question and how many recorded reasons in this
// codebase hold it.
// @intent let a caller see that a long answer rests on a word everybody writes.
type intentTerm struct {
	Text      string `json:"text"`
	InReasons int    `json:"in_reasons"`
}

// intentFileGroup is one file and the answering declarations inside it.
// @intent hand back somewhere to start reading rather than a flat symbol list.
type intentFileGroup struct {
	FilePath string        `json:"file_path"`
	Entries  []intentEntry `json:"entries"`
}

// intentCoverage says how much of the searchable code had a reason recorded.
//
// It rides on every answer, not only empty ones. Three files out of a codebase
// where a third of declarations carry a reason is a partial answer, and the
// caller has no way to know that unless the numbers come back with it.
//
// @intent let a caller tell "no such reason exists" from "nobody wrote one down".
type intentCoverage struct {
	NodesWithReason int    `json:"nodes_with_reason"`
	NodesTotal      int    `json:"nodes_total"`
	Note            string `json:"note,omitempty"`
}

// intentResponse is the wire payload for findByIntent.
//
// There is no confidence score here, and that is deliberate. Any threshold would
// be a number fitted to whichever codebase it was measured on, and this tool has
// only ever been measured on one. Terms and ReasonsSearched are counted freshly
// against whatever index is being searched, so they say the same thing in a
// repository nobody has looked at: these words did the matching, and this is how
// ordinary each of them is here.
//
// @intent report what was found, how much could have been found, and what the finding rests on.
type intentResponse struct {
	Question string            `json:"question"`
	Files    []intentFileGroup `json:"files"`
	Coverage intentCoverage    `json:"coverage"`
	Terms    []intentTerm      `json:"terms,omitempty"`
	// ReasonsSearched is the denominator every InReasons is out of.
	ReasonsSearched int          `json:"reasons_searched,omitempty"`
	Next            []nextAction `json:"next,omitempty"`
}

// findByIntent answers a plain-language question from recorded reasons.
//
// It is a separate tool from `search` rather than a mode of it because the two
// take different input and are scored against different text. `search` takes an
// identifier and matches it against names; this takes a sentence and matches it
// against @intent and @domainRule only. Folding them together is what made the
// old shared index answer a question with whatever node happened to be named
// after one of its words.
//
// @param request question is the required plain-language question.
// @param request limit caps how many nodes are pulled from the index, not how many files come back.
// @requires Graph.Intent must be configured.
// @ensures an empty answer still reports coverage, and never falls back to name search.
func (h *handlers) findByIntent(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx = h.applyNamespace(ctx, request)
	log := h.logger()

	question, err := request.RequireString("question")
	if err != nil {
		return missingParamResult(err)
	}
	if strings.TrimSpace(question) == "" {
		return mcp.NewToolResultError("question must not be empty"), nil
	}
	limit := request.GetInt("limit", intent.DefaultLimit)
	if err := validateQueryGraphLimit(limit); err != nil {
		return finalizeToolResult("", err)
	}
	if h.deps.Graph.Intent == nil {
		return mcp.NewToolResultError("intent search is not configured"), nil
	}

	log.Info("find_by_intent called", "question", question, "limit", limit)

	return finalizeToolResult(h.cachedExecute(ctx, "find_by_intent:", map[string]any{"question": question, "limit": limit, "namespace": requestNamespace(request)}, func() (string, error) {
		answer, err := h.deps.Graph.Intent.Find(ctx, question, limit)
		if err != nil {
			log.Error("find_by_intent error", "question", question, trace.SlogError(err))
			return "", trace.Wrap(err, "find by intent")
		}
		log.Info("find_by_intent completed", "question", question, "file_count", len(answer.Files))
		result, err := marshalJSON(newIntentResponse(answer, question))
		if err != nil {
			return "", trace.Wrap(err, "marshal result")
		}
		return result, nil
	}))
}

// newIntentResponse converts an application answer into the wire payload.
// @ensures Next is empty exactly when the answer found something.
// @intent keep one conversion so the tool's shape cannot drift from the service's.
func newIntentResponse(answer intent.Answer, question string) intentResponse {
	files := make([]intentFileGroup, len(answer.Files))
	for i, f := range answer.Files {
		entries := make([]intentEntry, len(f.Entries))
		for j, e := range f.Entries {
			entries[j] = intentEntry{
				NodeID: e.NodeID, Name: e.Name, QualifiedName: e.QualifiedName,
				Kind: e.Kind, Reason: e.Reason, Line: e.Line, MatchedTerms: e.MatchedTerms,
			}
		}
		files[i] = intentFileGroup{FilePath: f.FilePath, Entries: entries}
	}
	terms := make([]intentTerm, len(answer.Terms))
	for i, term := range answer.Terms {
		terms[i] = intentTerm{Text: term.Text, InReasons: term.InReasons}
	}
	return intentResponse{
		Question:        question,
		Files:           files,
		Coverage:        newIntentCoverage(answer.Coverage),
		Terms:           terms,
		ReasonsSearched: answer.ReasonsSearched,
		Next:            intentNextActions(answer, question),
	}
}

// newIntentCoverage attaches the sentence that makes the two numbers mean something.
// @intent stop a caller reading raw counts as a quality score.
func newIntentCoverage(coverage intent.Coverage) intentCoverage {
	out := intentCoverage{NodesWithReason: coverage.NodesWithReason, NodesTotal: coverage.NodesTotal}
	if coverage.NodesTotal == 0 {
		out.Note = "nothing is indexed in this namespace; run a build first"
		return out
	}
	if coverage.NodesWithReason == 0 {
		out.Note = "no declaration in this namespace has a recorded reason, so no question can be answered here"
		return out
	}
	if coverage.NodesWithReason*2 < coverage.NodesTotal {
		out.Note = "fewer than half the searchable declarations have a recorded reason, so an empty or short answer may mean the reason was never written down"
	}
	return out
}

// intentNextActions names what to do when no recorded reason answered.
//
// It deliberately does not hand the question to `search`. Every term of a
// sentence has to appear in one node there, so it would almost certainly return
// nothing too, and suggesting it would read as if an answer were still coming.
// Annotating the code is the thing that actually fixes an empty answer.
//
// @ensures every returned action names a real tool or command with arguments that need no editing.
// @intent turn an empty answer into a step rather than a dead end.
func intentNextActions(answer intent.Answer, question string) []nextAction {
	if len(answer.Files) > 0 {
		return nil
	}
	actions := []nextAction{{
		Reason: "no recorded reason matched; if you can name the symbol instead, search matches identifiers",
		Tool:   "search",
		Args:   map[string]any{"query": question},
	}}
	if answer.Coverage.NodesTotal > 0 && answer.Coverage.NodesWithReason*2 < answer.Coverage.NodesTotal {
		actions = append(actions, nextAction{
			Reason: "most declarations here carry no recorded reason; annotating the area under investigation is what makes this tool answer",
			Tool:   "run `ccg annotate <file|dir>`",
			Args:   map[string]any{},
		})
	}
	return actions
}
