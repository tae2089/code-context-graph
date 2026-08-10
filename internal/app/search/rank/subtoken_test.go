package rank

import (
	"testing"

	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// Cutting the query into sub-tokens hands each piece to the scorer as evidence
// on its own, and one rune is a piece almost every identifier happens to hold.
// ExecuteC cuts to execute and c; tmplFunc shares the c and nothing else, which
// is a coincidence of spelling rather than a reason to show it. Everything that
// spells execute keeps its score, so the camelCase query still reaches the
// identifiers it names.
func TestNameSim_OneSharedRuneIsNotEvidence(t *testing.T) {
	q := newQueryTokens("ExecuteC")

	coincidences := []string{"tmplFunc", "InitDefaultHelpCmd"}
	for _, name := range coincidences {
		node := graph.Node{Name: name, QualifiedName: "cobra." + name}
		if got := nameSim(q, node); got != 0 {
			t.Errorf("nameSim(\"ExecuteC\", %q) = %.4f, want 0 — the only rune they share is the c", name, got)
		}
	}

	named := []string{"Execute", "ExecuteContext", "ExecuteContextC", "ExecuteC"}
	for _, name := range named {
		node := graph.Node{Name: name, QualifiedName: "cobra.Command." + name}
		if got := nameSim(q, node); got <= 0 {
			t.Errorf("nameSim(\"ExecuteC\", %q) = %.4f, want positive — the name spells execute", name, got)
		}
	}
}

// The rule may not turn into "short names are unsearchable". A searcher who
// types a one- or two-rune identifier means that identifier, and the query has
// nothing longer to offer, so its runes are the whole of what was typed and
// still have to match. The rule only withholds a rune the query itself treats
// as a leftover.
func TestNameSim_AQueryWithNothingLongerStillFindsTheNameItSpells(t *testing.T) {
	cases := []struct{ query, name string }{
		{"c", "c"},
		{"db", "db"},
		{"T", "T"},
		{"x y", "xy"},
	}
	for _, c := range cases {
		node := graph.Node{Name: c.name, QualifiedName: "pkg." + c.name}
		if got := nameSim(newQueryTokens(c.query), node); got <= 0 {
			t.Errorf("nameSim(%q, %q) = %.4f, want positive — the query has no longer piece to be judged on", c.query, c.name, got)
		}
	}
}

// A longer piece that does match still carries the candidate on its own. The
// rule asks for one piece worth more than a coincidence, not for every piece.
func TestNameSim_OneLongSubTokenIsStillEnough(t *testing.T) {
	node := graph.Node{Name: "Store", QualifiedName: "graphgorm.Store"}

	if got := nameSim(newQueryTokens("graphStore"), node); got <= 0 {
		t.Errorf("nameSim(\"graphStore\", \"Store\") = %.4f, want positive — store is a whole word they share", got)
	}
}
