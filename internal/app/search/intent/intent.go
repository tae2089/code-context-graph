// @index Recorded-reason retrieval: the intent index port and its answer types.
package intent

import (
	"context"
	"strings"

	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// Searcher answers a question from the recorded-reason index alone.
// @intent let search consume a bound intent-index implementation without a database handle.
type Searcher interface {
	QueryIntent(ctx context.Context, query string, limit int) (Result, error)
}

// Hit is one declaration the index returned, with the terms of the question that
// are written in its recorded reason.
// @intent carry the reason a declaration ranked, not only that it ranked.
type Hit struct {
	Node  graph.Node
	Terms []string
}

// Term is one term of the question and how many recorded reasons hold it.
//
// A term nobody wrote down comes back with a count of zero rather than being
// left out: that zero is the reader's answer to why the question came back thin.
// @intent let a reader weigh a match by how common the word that earned it is.
type Term struct {
	Text      string `json:"text"`
	InReasons int    `json:"in_reasons"`
}

// Coverage is how much of a repository ever recorded a reason: how many
// declarations carry at least one @intent or @domainRule, out of how many
// declarations were indexed at all.
//
// WithReason counts declarations, not reasons, and that is the whole point of
// the type. One reason is one indexed document, so a declaration whose author
// wrote three of them is three documents; counting documents would report that
// declaration three times and say a repository is better annotated than it is.
//
// @domainRule WithReason never exceeds Declarations, because both are counted from the same derived index.
// @intent let an answer say whether it came back empty because nobody wrote a reason down.
type Coverage struct {
	WithReason   int `json:"with_reason"`
	Declarations int `json:"declarations"`
}

// Known reports whether the coverage numbers were measured at all. A caller that
// never asked the index — or asked one that holds nothing — must not be told "0
// of 0 declarations recorded a reason", which reads as a finding when it is an
// absence of one.
// @intent keep an unmeasured coverage from being reported as a measured zero.
func (c Coverage) Known() bool { return c.Declarations > 0 }

// Result is what the recorded-reason index answered with: the ranked hits and
// the evidence that produced them.
// @intent keep the ranking and the evidence for it on one value, so neither can be reported without the other.
type Result struct {
	Hits  []Hit
	Terms []Term
	// Corpus is how many recorded reasons the scorer weighed a word's commonness
	// against — a count of reasons, so it is not Coverage.WithReason.
	Corpus int
	// Coverage is the state of the whole recorded-reason index, not of this
	// query. It is reported even when nothing matched, because that is the case
	// it exists for.
	Coverage Coverage
}

// CanAnswer reports whether the recorded reasons speak this question's language
// at all: at least half of its scored terms are written in some reason.
//
// It gates membership, not ranking. The index admits a document on any shared
// word, which is right for a tool that reports term counts alongside its answer
// — but search reports membership. A question mostly made of words nobody ever
// wrote down ("zzz nonexistent symbol qqq") is not answered by the one common
// word it happens to share with fifty reasons; a real question keeps its hits
// even when each one matched only the couple of words that mattered. The
// fraction is over the question's own terms, so no corpus-fitted cutoff hides
// in it.
// @domainRule intent hits justify membership only when at least half of the question's scored terms appear in some recorded reason.
func (r Result) CanAnswer() bool {
	known := 0
	for _, term := range r.Terms {
		if term.InReasons > 0 {
			known++
		}
	}
	return known*2 >= len(r.Terms)
}

// RecordedReason returns the line that could have earned this node its place in
// the intent index.
//
// It mirrors document.BuildReasons, which indexes @intent and @domainRule.
// Reading back only @intent would drop a node indexed on a domain rule alone,
// and that node did not fail to record a reason — it recorded a different kind
// of one. @intent still wins when both are present, because it says why the code
// exists and a domain rule says what it must hold to.
// @intent read back the same tags the intent index was built from.
func RecordedReason(node graph.Node) string {
	if reason := node.Intent(); reason != "" {
		return reason
	}
	if node.Annotation == nil {
		return ""
	}
	for _, tag := range node.Annotation.Tags {
		if tag.Kind == graph.TagDomainRule && strings.TrimSpace(tag.Value) != "" {
			return tag.Value
		}
	}
	return ""
}
