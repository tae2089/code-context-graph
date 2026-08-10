package wire

import (
	"testing"

	"github.com/tae2089/code-context-graph/internal/app/search/evidence"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

func file(path string, hits int) evidence.File {
	f := evidence.File{FilePath: path}
	for i := range hits {
		f.Hits = append(f.Hits, evidence.Result{
			Node:    graph.Node{ID: uint(i + 1), Name: "alpha", FilePath: path, Kind: "function"},
			Matched: []evidence.Match{evidence.MatchName},
		})
	}
	return f
}

func TestNewResponse_TruncatedStillCountsOnlyFiles(t *testing.T) {
	// A pool cut with every fetched file on the page: the completion signal is
	// about files, and it reached all of them, so it stays false.
	got := NewResponse(evidence.List{
		Files:         []evidence.File{file("a.go", 50)},
		PoolTruncated: true,
	}, "alpha", 10, 0, false)

	if got.Truncated {
		t.Errorf("Truncated = true, want false: no file went unreached, only the candidate pool was cut")
	}
	if !got.PoolTruncated {
		t.Errorf("PoolTruncated = false, want the pool cut reported")
	}
}

func TestNewResponse_PoolCutIsSeparateFromTheFileOverflow(t *testing.T) {
	// The two signals have to be settable independently, or a caller cannot
	// tell "more files remain" from "more candidates were never fetched".
	overflowOnly := NewResponse(evidence.List{
		Files:         []evidence.File{file("a.go", 1)},
		OverflowFiles: 7,
	}, "alpha", 1, 0, false)
	if !overflowOnly.Truncated || overflowOnly.PoolTruncated {
		t.Errorf("truncated=%v pool_truncated=%v, want true/false", overflowOnly.Truncated, overflowOnly.PoolTruncated)
	}

	poolOnly := NewResponse(evidence.List{
		Files:         []evidence.File{file("a.go", 1)},
		PoolTruncated: true,
	}, "alpha", 1, 0, false)
	if poolOnly.Truncated || !poolOnly.PoolTruncated {
		t.Errorf("truncated=%v pool_truncated=%v, want false/true", poolOnly.Truncated, poolOnly.PoolTruncated)
	}

	both := NewResponse(evidence.List{
		Files:         []evidence.File{file("a.go", 1)},
		OverflowFiles: 7,
		PoolTruncated: true,
	}, "alpha", 1, 0, false)
	if !both.Truncated || !both.PoolTruncated {
		t.Errorf("truncated=%v pool_truncated=%v, want true/true", both.Truncated, both.PoolTruncated)
	}

	neither := NewResponse(evidence.List{Files: []evidence.File{file("a.go", 1)}}, "alpha", 1, 0, false)
	if neither.Truncated || neither.PoolTruncated {
		t.Errorf("truncated=%v pool_truncated=%v, want false/false", neither.Truncated, neither.PoolTruncated)
	}
}

func TestNewResponse_OffersTheNextPageWhenOnlyThePoolRanOut(t *testing.T) {
	// The crowded-file case: one file used the whole pool, so nothing is left
	// to count as overflow — and without an action here the caller has no call
	// to make and the files behind it stay unreachable.
	got := NewResponse(evidence.List{
		Files:         []evidence.File{file("crowded.go", 50)},
		PoolTruncated: true,
	}, "alpha", 10, 0, false)

	if len(got.Next) != 1 {
		t.Fatalf("Next = %+v, want one call that moves past the pool's edge", got.Next)
	}
	if got.Next[0].Tool != "search" {
		t.Errorf("Next[0].Tool = %q, want search", got.Next[0].Tool)
	}
	if got.Next[0].Args["offset"] != 1 {
		t.Errorf("Next[0] offset = %v, want 1 — one file past this page", got.Next[0].Args["offset"])
	}
}

func TestNewResponse_DoesNotDoubleUpTheNextPageCall(t *testing.T) {
	// Both signals up is still one next page, not two identical calls.
	got := NewResponse(evidence.List{
		Files:         []evidence.File{file("a.go", 1), file("b.go", 1)},
		OverflowFiles: 7,
		PoolTruncated: true,
	}, "alpha", 2, 0, false)

	pages := 0
	for _, a := range got.Next {
		if a.Args["offset"] != nil {
			pages++
		}
	}
	if pages != 1 {
		t.Errorf("got %d next-page calls in %+v, want exactly 1", pages, got.Next)
	}
}
