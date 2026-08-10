package evidence

import (
	"fmt"
	"slices"
	"testing"

	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

func annotated(intent string) *graph.Annotation {
	return &graph.Annotation{Tags: []graph.DocTag{{Kind: graph.TagIntent, Value: intent}}}
}

func ids(list List) []uint {
	hits := list.Hits()
	out := make([]uint, len(hits))
	for i, r := range hits {
		out[i] = r.Node.ID
	}
	return out
}

func fileNode(id uint, name, path string) graph.Node {
	return graph.Node{ID: id, Name: name, QualifiedName: "reposync.SyncQueue." + name, FilePath: path}
}

// A candidate nobody can explain is worse than no candidate: an agent handed
// ten results tends to pick one, so filling the list with nodes full-text
// search matched on a word nobody meant turns "there is no answer" into
// "maybe this one".
func TestBuild_DropsCandidatesWithNoEvidence(t *testing.T) {
	nodes := []graph.Node{
		{ID: 1, Name: "Rerank", QualifiedName: "rank.Rerank", FilePath: "internal/app/search/rank/rank.go"},
		{ID: 2, Name: "unrelated", QualifiedName: "other.unrelated", FilePath: "internal/other/other.go"},
	}
	got := Build("rerank", nodes, Options{Limit: 10})

	if want := []uint{1}; !slices.Equal(ids(got), want) {
		t.Errorf("kept %v, want %v", ids(got), want)
	}
	if got.WeakFiltered != 1 {
		t.Errorf("WeakFiltered = %d, want 1", got.WeakFiltered)
	}
	if got.Note != "" {
		t.Errorf("Note = %q, want empty while results remain", got.Note)
	}
}

func TestBuild_MatchingIntentIsEvidenceOnItsOwn(t *testing.T) {
	nodes := []graph.Node{
		{
			ID: 1, Name: "SyncQueue", QualifiedName: "reposync.SyncQueue",
			FilePath:   "internal/app/reposync/queue.go",
			Annotation: annotated("hand webhook pushes to a bounded worker pool"),
		},
		{ID: 2, Name: "unrelated", QualifiedName: "other.unrelated", FilePath: "internal/other/other.go"},
	}
	got := Build("worker pool", nodes, Options{Limit: 10})

	if want := []uint{1}; !slices.Equal(ids(got), want) {
		t.Fatalf("kept %v, want %v", ids(got), want)
	}
	if !slices.Contains(got.Hits()[0].Matched, MatchIntent) {
		t.Errorf("Matched = %v, want it to include %q", got.Hits()[0].Matched, MatchIntent)
	}
	if got.Hits()[0].Intent == "" {
		t.Error("the intent that justified the hit was not carried into the result")
	}
}

func TestBuild_LabelsEverySignalThatMatched(t *testing.T) {
	node := graph.Node{
		ID: 1, Name: "Rerank", QualifiedName: "rank.Rerank",
		FilePath:   "internal/app/search/rank/rank.go",
		Annotation: annotated("order candidates by identifier-name and file-path evidence"),
	}
	got := Build("rank evidence", []graph.Node{node}, Options{Limit: 10})

	want := []Match{MatchName, MatchPath, MatchIntent}
	if !slices.Equal(got.Hits()[0].Matched, want) {
		t.Errorf("Matched = %v, want %v", got.Hits()[0].Matched, want)
	}
}

// A reader picks a file first and a declaration second, so the answer is a list
// of files, and each file arrives whole.
func TestBuild_GroupsEveryHitOfAFileTogether(t *testing.T) {
	nodes := []graph.Node{
		fileNode(1, "Add", "internal/app/reposync/queue.go"),
		fileNode(2, "handle", "internal/adapters/inbound/webhook/handler.go"),
		fileNode(3, "Shutdown", "internal/app/reposync/queue.go"),
	}
	got := Build("syncqueue", nodes, Options{Limit: 10})

	if len(got.Files) != 2 {
		t.Fatalf("got %d files, want 2", len(got.Files))
	}
	// The file whose best hit ranked highest comes first.
	if got.Files[0].FilePath != "internal/app/reposync/queue.go" {
		t.Errorf("first file = %q, want the one holding the best-ranked hit", got.Files[0].FilePath)
	}
	if want := []uint{1, 3, 2}; !slices.Equal(ids(got), want) {
		t.Errorf("hit order %v, want %v — a file's hits must be contiguous", ids(got), want)
	}
	if got.Files[0].HitCount() != 2 {
		t.Errorf("HitCount = %d, want 2", got.Files[0].HitCount())
	}
}

// Nothing inside a shown file is held back. A file that answers the query
// seventeen times is a file the reader wants to see seventeen times.
func TestBuild_ShowsEveryHitOfAShownFile(t *testing.T) {
	var nodes []graph.Node
	for i := 1; i <= 17; i++ {
		nodes = append(nodes, fileNode(uint(i), fmt.Sprintf("Step%d", i), "internal/app/reposync/queue.go"))
	}
	got := Build("syncqueue", nodes, Options{Limit: 10})

	if len(got.Files) != 1 {
		t.Fatalf("got %d files, want 1", len(got.Files))
	}
	if n := len(got.Files[0].Hits); n != 17 {
		t.Errorf("showed %d of 17 hits — a shown file must arrive whole", n)
	}
}

// Limit counts files. Ten hits in one file is one unit of the reader's
// attention, not ten.
func TestBuild_LimitCountsFilesNotHits(t *testing.T) {
	var nodes []graph.Node
	id := uint(0)
	for f := range 6 {
		for range 3 {
			id++
			nodes = append(nodes, fileNode(id, "Add", fmt.Sprintf("internal/app/reposync/queue%d.go", f)))
		}
	}
	got := Build("syncqueue", nodes, Options{Limit: 2})

	if len(got.Files) != 2 {
		t.Fatalf("got %d files, want 2", len(got.Files))
	}
	if len(got.Hits()) != 6 {
		t.Errorf("got %d hits, want 6 — both shown files must arrive whole", len(got.Hits()))
	}
	if got.OverflowFiles != 4 {
		t.Errorf("OverflowFiles = %d, want 4", got.OverflowFiles)
	}
}

// Paging moves by whole files, so no file is ever split across two answers.
func TestBuild_OffsetSkipsWholeFiles(t *testing.T) {
	var nodes []graph.Node
	id := uint(0)
	for f := range 4 {
		for range 2 {
			id++
			nodes = append(nodes, fileNode(id, "Add", fmt.Sprintf("internal/app/reposync/queue%d.go", f)))
		}
	}
	got := Build("syncqueue", nodes, Options{Limit: 2, Offset: 2})

	if len(got.Files) != 2 {
		t.Fatalf("got %d files, want 2", len(got.Files))
	}
	if got.Files[0].FilePath != "internal/app/reposync/queue2.go" {
		t.Errorf("first file = %q, want the third one", got.Files[0].FilePath)
	}
	if got.OverflowFiles != 0 {
		t.Errorf("OverflowFiles = %d, want 0 — this is the last page", got.OverflowFiles)
	}
}

// An offset past the end is a readable answer, not a bare empty list.
func TestBuild_ExplainsAnOffsetPastTheEnd(t *testing.T) {
	got := Build("syncqueue", []graph.Node{fileNode(1, "Add", "internal/app/reposync/queue.go")}, Options{Limit: 5, Offset: 9})
	if len(got.Files) != 0 {
		t.Fatalf("got %d files, want none", len(got.Files))
	}
	if got.Note == "" {
		t.Error("an empty page past the end must say so")
	}
}

// The budget decides whether another file joins the page; it never trims one.
// A file that alone exceeds the budget is still shown whole, because half a
// file is worse evidence than a long one.
func TestBuild_PageBudgetStopsAtAFileBoundary(t *testing.T) {
	var nodes []graph.Node
	id := uint(0)
	for range PageHitBudget + 10 {
		id++
		nodes = append(nodes, fileNode(id, "Add", "internal/app/reposync/queue.go"))
	}
	id++
	nodes = append(nodes, fileNode(id, "Add", "internal/app/reposync/other.go"))

	got := Build("syncqueue", nodes, Options{Limit: 10})

	if len(got.Files) != 1 {
		t.Fatalf("got %d files, want 1 — the second file did not fit the page budget", len(got.Files))
	}
	if n := len(got.Files[0].Hits); n != PageHitBudget+10 {
		t.Errorf("the first file lost hits to the budget: %d of %d", n, PageHitBudget+10)
	}
	if got.OverflowFiles != 1 {
		t.Errorf("OverflowFiles = %d, want 1", got.OverflowFiles)
	}
}

func TestBuild_LimitBoundsTheList(t *testing.T) {
	var nodes []graph.Node
	for i := 1; i <= 6; i++ {
		nodes = append(nodes, graph.Node{
			ID: uint(i), Name: "rank", QualifiedName: "rank.rank",
			FilePath: "file" + string(rune('a'+i)) + ".go",
		})
	}
	if got := Build("rank", nodes, Options{Limit: 3}); len(got.Files) != 3 {
		t.Errorf("returned %d files, want 3", len(got.Files))
	}
}

// An empty list is an answer, and it has to say which kind of empty it is.
func TestBuild_ExplainsAnEmptyList(t *testing.T) {
	weak := []graph.Node{
		{ID: 1, Name: "unrelated", QualifiedName: "other.unrelated", FilePath: "internal/other/other.go"},
		{ID: 2, Name: "alsoUnrelated", QualifiedName: "other.alsoUnrelated", FilePath: "internal/other/other.go"},
	}
	got := Build("webhook", weak, Options{Limit: 10})
	if len(got.Files) != 0 {
		t.Fatalf("returned %d files, want none", len(got.Files))
	}
	if got.WeakFiltered != 2 {
		t.Errorf("WeakFiltered = %d, want 2", got.WeakFiltered)
	}
	if got.Note == "" {
		t.Error("an empty list with filtered candidates must explain itself")
	}

	// Nothing retrieved at all is a different answer from everything filtered,
	// and a caller acts differently on each: one means rephrase, the other
	// means ask again with the weak candidates included.
	none := Build("webhook", nil, Options{Limit: 10})
	if none.Note == "" || none.Note == got.Note {
		t.Errorf("Note for an empty pool (%q) must differ from the filtered note (%q)", none.Note, got.Note)
	}
}

func TestBuild_IncludeWeakKeepsUnexplainableCandidatesLast(t *testing.T) {
	nodes := []graph.Node{
		{ID: 1, Name: "unrelated", QualifiedName: "other.unrelated", FilePath: "internal/other/other.go"},
		{ID: 2, Name: "Rerank", QualifiedName: "rank.Rerank", FilePath: "internal/app/search/rank/rank.go"},
	}
	got := Build("rerank", nodes, Options{Limit: 10, IncludeWeak: true})

	if want := []uint{2, 1}; !slices.Equal(ids(got), want) {
		t.Errorf("order %v, want %v — explainable results first", ids(got), want)
	}
	if got.WeakFiltered != 0 {
		t.Errorf("WeakFiltered = %d, want 0 when nothing was filtered", got.WeakFiltered)
	}
	if len(got.Hits()[1].Matched) != 0 {
		t.Errorf("a kept weak result must still report no matched signals, got %v", got.Hits()[1].Matched)
	}
}

// A hit the intent index returned is justified by the terms of the question
// written in its recorded reason, even when its name, path, and @intent share
// no token with the query — a prefix match like "네임스페이스가" or a
// @domainRule-only reason is exactly the case token overlap cannot see.
func TestBuild_IntentEvidenceJustifiesACandidateOnItsOwn(t *testing.T) {
	nodes := []graph.Node{
		{ID: 1, Name: "admitRepo", QualifiedName: "webhook.admitRepo", FilePath: "internal/adapters/inbound/webhook/admission.go"},
	}
	got := Build("which repositories are allowed to sync", nodes, Options{
		Limit: 10,
		Intent: map[NodeRef]IntentHit{
			{ID: 1}: {Reason: "decide which repository and branch a push may build", Terms: []string{"repositories", "sync"}},
		},
	})

	if want := []uint{1}; !slices.Equal(ids(got), want) {
		t.Fatalf("kept %v, want %v — an intent hit must never be filtered as weak", ids(got), want)
	}
	hit := got.Hits()[0]
	if !slices.Contains(hit.Matched, MatchIntent) {
		t.Errorf("Matched = %v, want it to include %q", hit.Matched, MatchIntent)
	}
	if hit.Reason != "decide which repository and branch a push may build" {
		t.Errorf("Reason = %q, want the recorded reason the index matched", hit.Reason)
	}
	if want := []string{"repositories", "sync"}; !slices.Equal(hit.MatchedTerms, want) {
		t.Errorf("MatchedTerms = %v, want %v", hit.MatchedTerms, want)
	}
	if got.WeakFiltered != 0 {
		t.Errorf("WeakFiltered = %d, want 0", got.WeakFiltered)
	}
}

// A name hit that the intent index also returned carries both kinds of
// evidence on one result, so the reader sees why it ranked and what the
// author said in one line.
func TestBuild_IntentEvidenceRidesOnANameHit(t *testing.T) {
	nodes := []graph.Node{
		{ID: 1, Name: "SyncQueue", QualifiedName: "reposync.SyncQueue", FilePath: "internal/app/reposync/queue.go"},
	}
	got := Build("syncqueue", nodes, Options{
		Limit: 10,
		Intent: map[NodeRef]IntentHit{
			{ID: 1}: {Reason: "hand webhook pushes to a bounded worker pool", Terms: []string{"syncqueue"}},
		},
	})

	hit := got.Hits()[0]
	want := []Match{MatchName, MatchIntent}
	if !slices.Equal(hit.Matched, want) {
		t.Errorf("Matched = %v, want %v", hit.Matched, want)
	}
	if hit.Reason == "" || len(hit.MatchedTerms) == 0 {
		t.Errorf("intent evidence lost on a name hit: Reason=%q MatchedTerms=%v", hit.Reason, hit.MatchedTerms)
	}
}

// Node ids are unique per repository, not across them: a federated answer can
// hold two nodes with the same id, and intent evidence must reach only the one
// it was measured on.
func TestBuild_IntentEvidenceKeysOnNamespaceAndID(t *testing.T) {
	nodes := []graph.Node{
		{ID: 1, Namespace: "repo-a", Name: "admit", QualifiedName: "a.admit", FilePath: "a/admission.go"},
		{ID: 1, Namespace: "repo-b", Name: "admit", QualifiedName: "b.admit", FilePath: "b/admission.go"},
	}
	got := Build("which repositories are allowed to sync", nodes, Options{
		Limit: 10,
		Intent: map[NodeRef]IntentHit{
			{Namespace: "repo-a", ID: 1}: {Reason: "decide which repository may sync", Terms: []string{"sync"}},
		},
	})

	if want := []uint{1}; !slices.Equal(ids(got), want) {
		t.Fatalf("kept %v, want %v — only repo-a's node", ids(got), want)
	}
	if got.Hits()[0].Node.Namespace != "repo-a" {
		t.Errorf("kept namespace %q, want repo-a", got.Hits()[0].Node.Namespace)
	}
	if got.WeakFiltered != 1 {
		t.Errorf("WeakFiltered = %d, want 1 — repo-b's node has no evidence of its own", got.WeakFiltered)
	}
}

// The answer has to say how many files it did not reach, or a reader has no way
// to tell a short answer from the first page of a long one.
func TestBuild_CountsTheFilesItDidNotReach(t *testing.T) {
	nodes := make([]graph.Node, 0, 7)
	for i := range 7 {
		nodes = append(nodes, graph.Node{
			ID: uint(i + 1), Name: "syncWorker", QualifiedName: "reposync.syncWorker",
			FilePath: fmt.Sprintf("internal/app/reposync/worker%d.go", i),
		})
	}
	list := Build("syncworker", nodes, Options{Limit: 3})
	if len(list.Files) != 3 {
		t.Fatalf("files = %d, want 3", len(list.Files))
	}
	if list.OverflowFiles != 4 {
		t.Errorf("OverflowFiles = %d, want 4: seven files, three shown", list.OverflowFiles)
	}
}

// Nothing was cut, so nothing may be claimed.
func TestBuild_ReportsNoOverflowWhenEverythingFits(t *testing.T) {
	nodes := []graph.Node{{ID: 1, Name: "syncWorker", QualifiedName: "reposync.syncWorker", FilePath: "a.go"}}
	if list := Build("syncworker", nodes, Options{Limit: 10}); list.OverflowFiles != 0 {
		t.Errorf("OverflowFiles = %d, want 0", list.OverflowFiles)
	}
}

// A negative offset is a caller's mistake, and every entry point turns it away.
// The paging still has to survive one: cutting a slice from a negative index
// panics, and a panic here names this package rather than the caller that was
// actually wrong. Skipping a negative number of files skips none.
func TestBuild_ANegativeOffsetStartsAtTheFirstFile(t *testing.T) {
	nodes := []graph.Node{
		fileNode(1, "Add", "internal/app/reposync/queue0.go"),
		fileNode(2, "Add", "internal/app/reposync/queue1.go"),
	}

	got := Build("syncqueue", nodes, Options{Limit: 5, Offset: -1})

	if len(got.Files) != 2 {
		t.Fatalf("files = %d, want both", len(got.Files))
	}
	if got.Files[0].FilePath != "internal/app/reposync/queue0.go" {
		t.Errorf("first file = %q, want the first one", got.Files[0].FilePath)
	}
	if got.OverflowFiles != 0 {
		t.Errorf("OverflowFiles = %d, want 0 — nothing was skipped and nothing was cut", got.OverflowFiles)
	}
}

// The empty answer is the case the upper-bound check misses: an offset past the
// end is caught by comparing against the file count, and a negative one is not.
func TestBuild_ANegativeOffsetOnAnEmptyAnswerDoesNotPanic(t *testing.T) {
	got := Build("syncqueue", nil, Options{Limit: 5, Offset: -1})

	if len(got.Files) != 0 {
		t.Fatalf("files = %d, want none", len(got.Files))
	}
	if got.Note == "" {
		t.Error("an empty answer must say which kind of empty it is")
	}
}

// namespacedFileNode is fileNode with a repository attached, which is the only
// way to reach the per-namespace paging path.
func namespacedFileNode(id uint, namespace, name, path string) graph.Node {
	n := fileNode(id, name, path)
	n.Namespace = namespace
	return n
}

// A negative offset is clamped where the window is cut, and the per-namespace
// path then does two more things with the offset it was handed: it filters the
// reassembled list against it, and it adds it to the step. Both read the
// unclamped number, so a clamped window of three files is filtered down to two
// and the file in between is dropped with nothing in the answer counting it.
func TestBuild_ANegativeOffsetStartsEveryNamespaceAtItsFirstFile(t *testing.T) {
	nodes := []graph.Node{
		namespacedFileNode(1, "repo-a", "Add", "internal/app/reposync/queue0.go"),
		namespacedFileNode(2, "repo-a", "Add", "internal/app/reposync/queue1.go"),
		namespacedFileNode(3, "repo-a", "Add", "internal/app/reposync/queue2.go"),
		namespacedFileNode(4, "repo-b", "Add", "internal/app/reposync/queue0.go"),
		namespacedFileNode(5, "repo-b", "Add", "internal/app/reposync/queue1.go"),
		namespacedFileNode(6, "repo-b", "Add", "internal/app/reposync/queue2.go"),
	}

	got := Build("syncqueue", nodes, Options{Limit: 5, Offset: -1, PerNamespace: true})

	perNamespace := map[string]int{}
	for _, f := range got.Files {
		perNamespace[f.Namespace]++
	}
	if perNamespace["repo-a"] != 3 || perNamespace["repo-b"] != 3 {
		t.Errorf("files per repository = %v, want three each — every file fits under the limit", perNamespace)
	}
	if got.OverflowFiles != 0 {
		t.Errorf("OverflowFiles = %d, want 0 — every file this query answers with is on the page", got.OverflowFiles)
	}
	if got.NextOffset != 3 {
		t.Errorf("NextOffset = %d, want 3 — three files were shown from the first one", got.NextOffset)
	}
}

// The same unclamped arithmetic sets the next offset on the single-repository
// path, where a page that was cut hands back a place behind its own last file.
func TestBuild_ANegativeOffsetDoesNotSuggestAnOffsetBehindThePage(t *testing.T) {
	nodes := make([]graph.Node, 0, 10)
	for i := range 10 {
		nodes = append(nodes, fileNode(uint(i+1), "Add", fmt.Sprintf("internal/app/reposync/queue%d.go", i)))
	}

	got := Build("syncqueue", nodes, Options{Limit: 3, Offset: -1})

	if len(got.Files) != 3 {
		t.Fatalf("files = %d, want 3", len(got.Files))
	}
	if got.NextOffset != 3 {
		t.Errorf("NextOffset = %d, want 3 — anything lower shows a file this page already did", got.NextOffset)
	}
}
