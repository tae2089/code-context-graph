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
	// Reason is the recorded reason the intent index matched — the node's
	// @intent, or its @domainRule when no @intent exists. It is set only for
	// hits the intent query returned, so an empty Reason means the hit earned
	// its place on name, path, or token overlap alone.
	Reason string
	// MatchedTerms are the terms of the query written in Reason, as the intent
	// scorer counted them. They are the proof behind MatchIntent when token
	// overlap alone cannot see the match.
	MatchedTerms []string
}

// NodeRef names one node across repositories. Node ids are unique only within
// a namespace, so a federated answer needs both to address a node.
// @intent key per-node intent evidence so it cannot leak onto another repository's node.
type NodeRef struct {
	Namespace string
	ID        uint
}

// IntentHit is what the intent index said about one candidate: the recorded
// reason it matched and the query terms written in it.
// @intent carry the intent query's evidence into the list without the list depending on the intent packages.
type IntentHit struct {
	Reason string
	Terms  []string
}

// Coverage is how much of the searched repositories ever recorded a reason: how
// many declarations carry at least one @intent or @domainRule, out of how many
// declarations were indexed at all.
//
// It is a fact about the index rather than about this query, and it is what
// separates the two empty answers that used to read alike. An empty answer in a
// repository with 1900 recorded reasons means the reasons do not cover this
// question; an empty answer in a repository with none means nobody has written
// anything down yet, and only the second one is answered by annotating.
//
// WithReason counts declarations, never reasons. One reason is one indexed
// document, so counting documents would report a declaration whose author wrote
// three of them three times.
//
// This is declared here, rather than reused from the intent package, for the same
// reason IntentHit is: a list has to be describable without the retrieval ports
// that filled it.
//
// @domainRule WithReason never exceeds Declarations.
// @intent let an empty answer say whether anyone ever recorded a reason to search.
type Coverage struct {
	WithReason   int `json:"with_reason"`
	Declarations int `json:"declarations"`
}

// Known reports whether these numbers were measured at all.
//
// A surface that never reached the recorded-reason index leaves the zero value
// here, and "0 of 0 declarations recorded a reason" reads as a finding when it is
// the absence of one. Nothing may state the fraction without asking this first.
// @intent keep an unmeasured coverage from being reported as a measured zero.
func (c Coverage) Known() bool { return c.Declarations > 0 }

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
	// PoolTruncated says the candidate pool this list was built from came back
	// full: the backend had at least as many candidates as the pool could hold
	// and may have had more. Build never sets it — it is a fact about the fetch,
	// which happens before this package sees anything — so whoever fetched the
	// pool sets it.
	//
	// It is deliberately not folded into OverflowFiles, which counts files and
	// only ever counts files. Read together they separate the two ways a page
	// can end: OverflowFiles == 0 with PoolTruncated false is the whole answer,
	// while OverflowFiles == 0 with PoolTruncated true is only the end of the
	// candidates that were fetched.
	PoolTruncated bool
	// NextOffset is the offset the page after this one starts at. It is set
	// whether or not another page exists; OverflowFiles and PoolTruncated are
	// what say whether asking for it is worth anything.
	//
	// It is carried rather than recomputed by the caller because "offset plus
	// the files on this page" is only right when the page is one contiguous run
	// of the answer. Whoever cut the page is the only one who knows that.
	NextOffset int
	// Coverage is how much of the searched repositories recorded a reason at all.
	// Build does not measure it — it is a fact about the index, which is read
	// before this package sees anything — so it arrives on Options and is carried
	// through unchanged.
	Coverage Coverage
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
	// PerNamespace makes Limit and Offset a budget every namespace gets on its
	// own rather than one budget the namespaces compete for. Federated search
	// sets it; a single-repository search has one namespace and cannot tell the
	// difference.
	//
	// Sharing one budget meant a limit smaller than the namespace count silenced
	// whole repositories, and the page ended up an arbitrary subset of the
	// answer rather than a run of it — so the offset that would resume it did
	// not exist.
	PerNamespace bool
	// IncludeWeak keeps candidates no signal explains, after the explainable
	// ones. Off by default, because a list padded with unexplainable results
	// reads as "here are ten answers" when there were two.
	IncludeWeak bool
	// Intent is what the intent query said about each node it returned. A node
	// with an entry here is justified by it — the terms are the proof — even
	// when its name, path, and @intent share no token with the query.
	Intent map[NodeRef]IntentHit
	// Coverage is how much of the searched repositories recorded a reason at all.
	// It is an input rather than something set on the returned list afterwards
	// because the note depends on it: which kind of empty an empty answer is
	// cannot be decided without knowing whether anybody wrote a reason down.
	Coverage Coverage
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
// @ensures a negative Offset is read as zero everywhere the page is cut, counted, and stepped from.
// @intent give a reader or an agent a file list where every line states why it is there.
func Build(query string, nodes []graph.Node, opts Options) List {
	// Cutting the window is not the only thing done with the offset: the page is
	// also filtered against it and the next one is stepped from it. Clamping it
	// once here is what keeps those three agreeing, since a clamp further down
	// fixes the window and leaves the other two reading a number no page ever
	// started at.
	opts.Offset = max(opts.Offset, 0)
	qTokens := identtoken.Split(query)
	kept := make([]Result, 0, len(nodes))
	weak := make([]Result, 0)
	for _, node := range nodes {
		intent := node.Intent()
		fromIntent, viaIntent := opts.Intent[NodeRef{Namespace: node.Namespace, ID: node.ID}]
		matched := matchedSignals(query, qTokens, node, intent, viaIntent)
		result := Result{Node: node, Intent: intent, Matched: matched, Reason: fromIntent.Reason, MatchedTerms: fromIntent.Terms}
		if len(matched) > 0 {
			kept = append(kept, result)
			continue
		}
		result.Matched = nil
		weak = append(weak, result)
	}

	list := List{WeakFiltered: len(weak), Coverage: opts.Coverage}
	if opts.IncludeWeak {
		kept = append(kept, weak...)
		list.WeakFiltered = 0
	}

	files := groupByFile(kept)
	if opts.PerNamespace {
		list.Files, list.OverflowFiles, list.NextOffset = pagePerNamespace(files, opts)
	} else {
		list.Files, list.OverflowFiles = page(files, opts)
		list.NextOffset = opts.Offset + len(list.Files)
	}
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
//
// viaIntent marks a node the intent query returned. It counts as the intent
// signal even when token overlap sees nothing, because the intent scorer
// matches ways overlap cannot — a non-ASCII prefix, or a @domainRule reason
// that is not the node's @intent.
// @intent state a candidate's evidence in the same terms the ranker ordered it by.
func matchedSignals(query string, qTokens []string, node graph.Node, intent string, viaIntent bool) []Match {
	signals := rank.Signals(query, node)
	matched := make([]Match, 0, 3)
	if signals.Name > 0 {
		matched = append(matched, MatchName)
	}
	if signals.Path > 0 {
		matched = append(matched, MatchPath)
	}
	if viaIntent || intentOverlaps(qTokens, intent) {
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
// A negative offset skips no files rather than panicking. Every entry point
// rejects one before the pipeline runs, so this is not how a caller finds out
// they were wrong; it is here because a slice cut from a negative index takes
// the process down, and a crash inside this package names it instead of the
// caller that was actually at fault. The bounds are checked at both ends for
// that reason: the upper one alone still panics on an empty answer, where there
// is no file count large enough to catch a negative start.
//
// @ensures every returned file carries all the hits groupByFile gave it.
// @ensures the returned count plus the returned files covers every file from the start position on.
// @intent bound an answer by files, so paging through it never lands a reader mid-file.
func page(files []File, opts Options) ([]File, int) {
	start := max(opts.Offset, 0)
	if start >= len(files) {
		return nil, 0
	}
	remaining := files[start:]

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

// pagePerNamespace cuts one page per namespace instead of one page for the
// whole answer, and returns the files, how many it did not reach, and the
// offset all of those pages resume at.
//
// Every namespace is windowed at the same Offset under the same Limit, so no
// namespace can spend another's slots and none is silenced by a limit smaller
// than the namespace count. That also makes each namespace's page a run of its
// own list rather than a scattering of it, which is what lets one shared offset
// resume all of them.
//
// The catch is the hit budget: it can stop one namespace's page earlier than
// another's, and one offset cannot resume lists that moved by different
// amounts. So the answer walks the repositories in lockstep — every page is cut
// back to the shortest page any namespace with files left produced, and the
// offset handed back is that step. A namespace that ran out does not set the
// step, because holding every repository down to the smallest one's size would
// let a repository with two files pace a repository with two thousand.
//
// @requires files arrive in the order groupByFile left them.
// @ensures every returned namespace's files are one run of that namespace's list starting at Offset.
// @ensures the returned offset steps over no file of any namespace that still has files left.
// @intent give federated search one offset that resumes every repository at once, so the next call it suggests is a call that works.
func pagePerNamespace(files []File, opts Options) ([]File, int, int) {
	order, byNamespace := groupByNamespace(files)

	taken := make(map[string]int, len(order))
	overflow := 0
	// step is the shortest page among namespaces that still have files left;
	// shortest is the shortest page of all, the fallback for when none do.
	step, shortest := 0, 0
	for _, ns := range order {
		kept, left := page(byNamespace[ns], opts)
		taken[ns] = len(kept)
		overflow += left
		if left > 0 && (step == 0 || len(kept) < step) {
			step = len(kept)
		}
		if len(kept) > 0 && (shortest == 0 || len(kept) < shortest) {
			shortest = len(kept)
		}
	}

	next := step
	if next == 0 {
		// Every namespace showed all it had from Offset on, so there is no next
		// page to line up. The offset still gets read when the candidate pool was
		// cut, and then the shortest page is the only place all of them can
		// resume from without stepping over a file.
		next = shortest
	}
	for ns, n := range taken {
		if step > 0 && n > step {
			overflow += n - step
			taken[ns] = step
		}
	}

	out := make([]File, 0, len(files))
	at := make(map[string]int, len(order))
	for _, f := range files {
		i := at[f.Namespace]
		at[f.Namespace]++
		if i >= opts.Offset && i < opts.Offset+taken[f.Namespace] {
			out = append(out, f)
		}
	}
	return out, overflow, opts.Offset + next
}

// groupByNamespace splits the file list per repository, keeping each
// repository's files in the order the whole list had them so a page still reads
// in rank order once the windows are put back together.
// @ensures the returned namespaces are in the order they first appear in files.
// @intent let each repository be paged through its own list without losing the shared ranking.
func groupByNamespace(files []File) ([]string, map[string][]File) {
	order := make([]string, 0, len(files))
	byNamespace := make(map[string][]File, len(files))
	for _, f := range files {
		if _, seen := byNamespace[f.Namespace]; !seen {
			order = append(order, f.Namespace)
		}
		byNamespace[f.Namespace] = append(byNamespace[f.Namespace], f)
	}
	return order, byNamespace
}
