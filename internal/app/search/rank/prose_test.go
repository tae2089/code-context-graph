// A standing check that `wiki_search` still answers questions written the way a
// person asks them.
//
// The scan declines a query whose words mostly name nothing here
// (retrieval.mostlyNamesNothing). Refusing an invented query is the point of
// that rule; refusing a real question is the way it goes wrong, and it goes
// wrong quietly — the caller sees an empty answer and cannot tell whether the
// thing is missing or the rule misjudged. The golden set has only five
// question-shaped queries, too few to notice. These thirty are here to notice.
//
// They carry no judgment about which file is right; the golden set does that.
// This asks the cheaper question the rule can break: did anything come back.
package rank_test

import (
	"testing"
)

// proseQuestions are questions a developer or agent could plausibly ask this
// repository, written as sentences rather than as symbol names.
var proseQuestions = []string{
	"how does the graph get built",
	"what happens when a webhook arrives",
	"where do search results get ranked",
	"how do i follow a call chain",
	"what stops the server accepting new work",
	"why is the queue draining slowly",
	"how are annotations attached to nodes",
	"what happens if a parse fails halfway",
	"where is the database schema defined",
	"how do i add support for a new language",
	"what decides which files get skipped",
	"how does incremental update know what changed",
	"where does the mcp server register its tools",
	"why would a cross reference stay unresolved",
	"how is a namespace isolated from another",
	"what runs when a push event comes in",
	"how does the wiki page get rendered",
	"where do i configure exclude patterns",
	"what happens to the graph when a file is deleted",
	"how are duplicate nodes avoided",
	"why does the scan have a row cap",
	"what makes a candidate weak",
	"how does the impact radius get computed",
	"where is the tree sitter parser invoked",
	"what does the postprocess step actually do",
	"how do i clone a repository from gitea",
	"why is my search returning nothing",
	"what tells the server to shut down",
	"how are flows detected in the graph",
	"where does the annotation text come from",
}

// These queries were never captured, so the fixture's full-text half returns
// nothing for them and the scan answers alone. That is the half the rule
// governs, which makes it the half worth testing.
func TestProseQuestionsStillGetAnswered(t *testing.T) {
	_, service, _ := loadDocsFixture(t)

	refused := make([]string, 0)
	for _, q := range proseQuestions {
		response, err := service.FromDB(t.Context(), "ccg", q, goldenLimit, 0, nil)
		if err != nil {
			t.Fatalf("FromDB(%q): %v", q, err)
		}
		if len(response.Results) == 0 {
			refused = append(refused, q)
		}
	}
	if len(refused) > 0 {
		t.Errorf("the scan refused %d of %d real questions; a rule meant to reject invented queries is rejecting askable ones:\n  %s",
			len(refused), len(proseQuestions), joinLines(refused))
	}
}

func joinLines(items []string) string {
	out := ""
	for i, item := range items {
		if i > 0 {
			out += "\n  "
		}
		out += item
	}
	return out
}
