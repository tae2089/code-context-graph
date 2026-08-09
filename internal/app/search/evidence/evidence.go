// @index Turns ranked search candidates into a list a reader can judge.
package evidence

import (
	"strings"

	"github.com/tae2089/code-context-graph/internal/app/search/identtoken"
	"github.com/tae2089/code-context-graph/internal/app/search/rank"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// PageHitBudget is the point past which one answer stops taking on more files.
//
// It bounds a page without ever trimming a file: a file is added whole or left
// for the next page, and the first file of a page is always added. So a file
// holding sixty hits is still answered with sixty hits — it just travels alone.
// Half a file is worse evidence than a long one, because the reader cannot tell
// which half they got.
const PageHitBudget = 50

// Match names one reason a candidate is in the list.
// @intent let a reader see which part of a result the query actually touched.
type Match string

const (
	MatchName   Match = "name"
	MatchPath   Match = "path"
	MatchIntent Match = "intent"
)

// Result is one candidate and the case for it.
// @intent carry a search hit together with the evidence that justifies showing it.
type Result struct {
	Node   graph.Node
	Intent string
	// Matched lists every signal the query touched, in a fixed order: name,
	// path, intent. It is empty only for a weak result kept by IncludeWeak.
	Matched []Match
}

// File is every hit one file answered the query with.
//
// The file is the unit of a search answer because it is the unit of reading: a
// reader picks a file first and a declaration inside it second. Grouping also
// removes the old reason to hide hits — ten hits in one file cost one decision,
// not ten — so a shown file arrives whole.
//
// @intent make the file, not the declaration, the thing a caller chooses between.
type File struct {
	// Namespace is empty outside federated search. Two repositories can each
	// hold an internal/app/main.go, and those are two files, not one.
	Namespace string
	FilePath  string
	Hits      []Result
}

// HitCount is how many hits this file answered with.
// @intent let a caller weigh a file before reading any of its hits.
func (f File) HitCount() int { return len(f.Hits) }

// List is a whole search answer, including what it decided not to show.
// @intent make "nothing to show" a readable answer rather than an empty array.
type List struct {
	Files        []File
	WeakFiltered int
	// OverflowFiles is how many further files this page did not reach, whether
	// the Limit or the page budget stopped it. It separates "this is
	// everything" from "this is the first ten files of thirty".
	OverflowFiles int
	// Note is set only when Files is empty, and says which kind of empty it is:
	// nothing retrieved, nothing explainable, or a page past the end.
	Note string
}

// Hits flattens the answer back into one ranked sequence, a file at a time.
// @ensures hits of one file stay contiguous and keep the order Build was handed.
// @intent give renderers and measurements one sequence without losing the grouping.
func (l List) Hits() []Result {
	out := make([]Result, 0, len(l.Files))
	for _, f := range l.Files {
		out = append(out, f.Hits...)
	}
	return out
}

// Options are the caller's choices about how wide the list may be.
// @domainRule Limit and Offset are both counted in files, never in hits, so paging never splits a file.
// @intent keep the bounds a caller controls — page size, page position, strictness — in one argument.
type Options struct {
	Limit  int
	Offset int
	// IncludeWeak keeps candidates no signal explains, after the explainable
	// ones. Off by default, because a list padded with unexplainable results
	// reads as "here are ten answers" when there were two.
	IncludeWeak bool
}

const (
	noteNothingRetrieved = "Full-text search matched no indexed node. Every term of the query has to appear in the same document, so a rarer or shorter query usually helps."
	noteAllWeak          = "Every candidate matched the query only inside indexed text, with nothing in its name, file path, or @intent to justify it. Ask again with weak candidates included to see them anyway."
	notePastTheEnd       = "The offset is past the last file this query answered with. Ask again from a lower offset."
)

// Build turns the reranked candidate pool into a list whose every entry can be
// justified, and reports what it left out.
//
// The order it is handed is the order it keeps. Ranking was measured against
// the golden set and its job turned out to be membership, not sequence: a
// reader who reads all ten lines is unaffected by which is third. So this
// changes who is in the list, never who is first — except that weak results
// kept by IncludeWeak go last, since they are there to be scrolled past.
//
// @requires nodes arrive in the order rank.Rerank left them, and carry their loaded Annotation.
// @ensures the returned Files never exceed Options.Limit, and every returned file carries all of its justified hits.
// @ensures Note is non-empty exactly when Files is empty.
// @intent give a reader or an agent a file list where every line states why it is there.
func Build(query string, nodes []graph.Node, opts Options) List {
	qTokens := identtoken.Split(query)
	kept := make([]Result, 0, len(nodes))
	weak := make([]Result, 0)
	for _, node := range nodes {
		intent := node.Intent()
		matched := matchedSignals(query, qTokens, node, intent)
		result := Result{Node: node, Intent: intent, Matched: matched}
		if len(matched) > 0 {
			kept = append(kept, result)
			continue
		}
		result.Matched = nil
		weak = append(weak, result)
	}

	list := List{WeakFiltered: len(weak)}
	if opts.IncludeWeak {
		kept = append(kept, weak...)
		list.WeakFiltered = 0
	}

	files := groupByFile(kept)
	list.Files, list.OverflowFiles = page(files, opts)
	if len(list.Files) == 0 {
		list.Note = noteNothingRetrieved
		switch {
		case len(files) > 0:
			list.Note = notePastTheEnd
		case len(nodes) > 0:
			list.Note = noteAllWeak
		}
	}
	return list
}

// matchedSignals collects every reason this node is worth showing, in a fixed
// order so two results are comparable at a glance.
// @intent state a candidate's evidence in the same terms the ranker ordered it by.
func matchedSignals(query string, qTokens []string, node graph.Node, intent string) []Match {
	signals := rank.Signals(query, node)
	matched := make([]Match, 0, 3)
	if signals.Name > 0 {
		matched = append(matched, MatchName)
	}
	if signals.Path > 0 {
		matched = append(matched, MatchPath)
	}
	if intentOverlaps(qTokens, intent) {
		matched = append(matched, MatchIntent)
	}
	return matched
}

// intentOverlaps reports whether the author's stated purpose shares a word with
// the query.
//
// Plain word overlap, not a weighted score. Three ways of scoring this were
// measured against the golden set — overlap, rarity-weighted, and BM25 — and
// overlap was the one that invented no preferences of its own: it and the
// rarity-weighted version ranked identically on every query, while BM25's
// length correction promoted a data-transfer object over the function it
// described. Here the question is only whether there is a reason at all, so the
// simplest answer is also the whole answer.
//
// @domainRule a single shared word is evidence; the query and the intent are compared as identifier tokens, so camelCase splits the same way on both sides.
// @intent treat an author-written purpose as a reason to show a result even when the name and path say nothing.
func intentOverlaps(qTokens []string, intent string) bool {
	if intent == "" || len(qTokens) == 0 {
		return false
	}
	words := map[string]bool{}
	for _, w := range identtoken.Split(intent) {
		words[strings.ToLower(w)] = true
	}
	for _, tok := range qTokens {
		if words[strings.ToLower(tok)] {
			return true
		}
	}
	return false
}

// groupByFile collects each file's hits into one entry, in the order the ranker
// put their best hit.
//
// A file is identified by its namespace as well as its path, because a
// federated search covers several repositories and two of them can each hold an
// `internal/app/main.go`. Namespace is empty outside federated search, so this
// changes nothing for a single repository.
//
// @ensures files appear in the order of their best-ranked hit, and hits keep their order within a file.
// @intent turn a ranked list of declarations into a ranked list of files to read.
func groupByFile(results []Result) []File {
	at := map[string]int{}
	out := make([]File, 0, len(results))
	for _, r := range results {
		key := r.Node.Namespace + "\x00" + r.Node.FilePath
		i, ok := at[key]
		if !ok {
			at[key] = len(out)
			out = append(out, File{Namespace: r.Node.Namespace, FilePath: r.Node.FilePath})
			i = len(out) - 1
		}
		out[i].Hits = append(out[i].Hits, r)
	}
	return out
}

// page cuts one page out of the file list and counts the files it did not reach.
//
// Two bounds stop it, and neither ever splits a file: the caller's Limit, and
// PageHitBudget. The budget is checked before a file is added, never after, and
// the first file of a page is added whatever its size — so the answer is always
// at least one whole file.
//
// @ensures every returned file carries all the hits groupByFile gave it.
// @ensures the returned count plus the returned files plus Offset covers the whole input.
// @intent bound an answer by files, so paging through it never lands a reader mid-file.
func page(files []File, opts Options) ([]File, int) {
	if opts.Offset >= len(files) {
		return nil, 0
	}
	remaining := files[opts.Offset:]

	out := make([]File, 0, len(remaining))
	hits := 0
	for _, f := range remaining {
		if opts.Limit > 0 && len(out) == opts.Limit {
			break
		}
		if len(out) > 0 && hits+f.HitCount() > PageHitBudget {
			break
		}
		out = append(out, f)
		hits += f.HitCount()
	}
	return out, len(remaining) - len(out)
}
