// @index Intent-question answering: ports, answer DTOs, and the file-grouping service.
package intent

import (
	"context"
	"strings"

	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// DefaultLimit bounds how many files one question comes back with.
//
// It counts files rather than declarations because a file is what the caller
// picks from. A declaration budget let one talkative file spend it: on
// PostgreSQL the question "what decides which repositories and branches are
// allowed to sync" came back with nine files and admission.go was not among
// them, because the declarations ranked above it used all twenty slots before
// its file was ever reached.
const DefaultLimit = 20

// NodesPerFile is how many declarations are pulled per file the answer intends
// to keep.
//
// A file budget is only worth having if retrieval reaches past the files that
// fill it, and the index ranks declarations, not files. Five is a guess with a
// measurement behind it rather than a derivation: the golden corpus averages
// roughly two indexed declarations per answering file, so five leaves room for
// the crowded ones without asking for a page of rows nobody reads. A file whose
// declarations all rank below limit*NodesPerFile is still missed — this widens
// the window, it does not remove it.
const NodesPerFile = 5

// MaxEntriesPerFile bounds how many declarations one file shows.
//
// Retrieving deeper would otherwise turn a crowded file into a wall of prose.
// The caller picked the file; the best-ranked few declarations in it are enough
// to start reading, and the rest are reachable by walking the graph, which is
// the point of handing back node ids.
const MaxEntriesPerFile = 3

// Searcher answers a question from the recorded-reason index alone.
// @intent let the intent service consume a bound search implementation without a database handle.
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

// Result is what the recorded-reason index answered with: the ranked hits and
// the evidence that produced them.
// @intent keep the ranking and the evidence for it on one value, so neither can be reported without the other.
type Result struct {
	Hits   []Hit
	Terms  []Term
	Corpus int
}

// CoverageReader reports how much of the namespace has a reason recorded at all.
// @intent let an answer say how much of the code it could possibly have searched.
type CoverageReader interface {
	IntentCoverage(ctx context.Context) (Coverage, error)
}

// Coverage says how much of the namespace carries a recorded reason.
//
// This is not a quality score. It is the denominator the caller needs to read
// an answer honestly: a question that comes back empty in a namespace where
// only a third of declarations have a recorded reason has not established that
// the code is absent, only that nobody wrote down why it exists.
// @intent let a caller tell "no such reason" apart from "nobody wrote one down".
type Coverage struct {
	NodesWithReason int `json:"nodes_with_reason"`
	NodesTotal      int `json:"nodes_total"`
}

// Entry is one declaration whose recorded reason answered the question.
// @intent give the caller a graph node id it can walk from, plus the reason that earned the hit.
type Entry struct {
	NodeID        uint     `json:"node_id"`
	Name          string   `json:"name"`
	QualifiedName string   `json:"qualified_name"`
	Kind          string   `json:"kind"`
	Reason        string   `json:"reason"`
	Line          int      `json:"line,omitempty"`
	MatchedTerms  []string `json:"matched_terms,omitempty"`
}

// File groups the answering declarations that live in one file.
// @intent hand back a place to start reading rather than a flat list of symbols.
type File struct {
	FilePath string  `json:"file_path"`
	Entries  []Entry `json:"entries"`
}

// Answer is what one intent question produces.
//
// Terms and ReasonsSearched are there instead of a confidence score. A score
// would have to be calibrated against some codebase, and whatever number was
// chosen would be that codebase's number; the term counts are recounted against
// whichever corpus is in front of them, so they mean the same thing in a
// repository nobody has measured. They let the reader see that twenty files all
// came back on one word written in half the recorded reasons, which is the case
// this tool would otherwise present as a confident answer.
//
// @intent report what was found, how much could have been found, and what the finding rests on.
type Answer struct {
	Files    []File   `json:"files"`
	Coverage Coverage `json:"coverage"`
	Terms    []Term   `json:"terms,omitempty"`
	// ReasonsSearched is the denominator every Term.InReasons is out of: how
	// many recorded reasons the question was scored against.
	ReasonsSearched int `json:"reasons_searched,omitempty"`
}

// Service answers natural-language questions from recorded reasons.
// @intent provide one application entry point for finding an entry point by intent.
type Service struct {
	Search   Searcher
	Coverage CoverageReader
}

// New constructs an intent service from consumer-owned outbound ports.
// @intent make search and coverage dependencies explicit at composition time.
func New(search Searcher, coverage CoverageReader) *Service {
	return &Service{Search: search, Coverage: coverage}
}

// Find answers a question from recorded reasons and groups the answers by file.
//
// There is deliberately no fallback to name search when nothing matches. A name
// hit dressed up as an answer would tell the caller that somebody explained this
// code when nobody did, and that is the one mistake this tool cannot afford: it
// exists to be trusted during an incident.
//
// @requires question must contain at least one word.
// @return returns at most limit answering files in index-rank order, and coverage either way.
func (s *Service) Find(ctx context.Context, question string, limit int) (Answer, error) {
	if s == nil || s.Search == nil {
		return Answer{}, trace.New("intent service is not configured")
	}
	if strings.TrimSpace(question) == "" {
		return Answer{}, trace.New("intent question must not be empty")
	}
	if limit <= 0 {
		limit = DefaultLimit
	}

	result, err := s.Search.QueryIntent(ctx, question, limit*NodesPerFile)
	if err != nil {
		return Answer{}, err
	}

	answer := Answer{
		Files:           groupByFile(result.Hits, limit),
		Terms:           result.Terms,
		ReasonsSearched: result.Corpus,
	}
	if s.Coverage != nil {
		coverage, err := s.Coverage.IntentCoverage(ctx)
		if err != nil {
			return Answer{}, err
		}
		answer.Coverage = coverage
	}
	return answer, nil
}

// recordedReason returns the line that could have earned this node its place in
// the intent index.
//
// It mirrors document.BuildIntentContent, which indexes @intent and @domainRule.
// Reading back only @intent would drop a node indexed on a domain rule alone,
// and that node did not fail to record a reason — it recorded a different kind
// of one. @intent still wins when both are present, because it says why the code
// exists and a domain rule says what it must hold to.
// @intent read back the same tags the intent index was built from.
func recordedReason(node graph.Node) string {
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

// groupByFile collapses rank-ordered hits into at most maxFiles files, keeping
// the order the index returned and one group per file.
//
// A file's place in the list is decided by its best-ranked declaration, which is
// the first one seen. Once maxFiles files have been opened, a node from a file
// not among them is dropped rather than opening a file the answer will not show.
// @intent turn a ranked symbol list into the reading list the caller actually asked for.
// @domainRule a file enters the answer at the position of its best-ranked declaration.
func groupByFile(hits []Hit, maxFiles int) []File {
	files := make([]File, 0, min(maxFiles, len(hits)))
	indexByPath := make(map[string]int, len(hits))
	for _, hit := range hits {
		node := hit.Node
		reason := recordedReason(node)
		if reason == "" {
			continue
		}
		idx, seen := indexByPath[node.FilePath]
		if !seen {
			if len(files) >= maxFiles {
				continue
			}
			indexByPath[node.FilePath] = len(files)
			files = append(files, File{FilePath: node.FilePath})
			idx = len(files) - 1
		}
		if len(files[idx].Entries) >= MaxEntriesPerFile {
			continue
		}
		files[idx].Entries = append(files[idx].Entries, Entry{
			NodeID:        node.ID,
			Name:          node.Name,
			QualifiedName: node.QualifiedName,
			Kind:          string(node.Kind),
			Reason:        reason,
			Line:          node.StartLine,
			MatchedTerms:  hit.Terms,
		})
	}
	return files
}
