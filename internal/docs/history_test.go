package docs

import (
	"path/filepath"
	"testing"
)

func TestHistory_FirstRun_AllCountsOne(t *testing.T) {
	dir := t.TempDir()
	histPath := filepath.Join(dir, "lint-history.json")

	current := []string{"unannotated:pkg/a.go::Foo", "missing:pkg/b.go"}

	h, err := LoadHistory(histPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	triggered := h.Update(current)

	if len(triggered) != 0 {
		t.Errorf("expected 0 triggered on first run, got %v", triggered)
	}

	// All entries should have count 1
	for _, key := range current {
		if h.Entries[key] != 1 {
			t.Errorf("expected count 1 for %q, got %d", key, h.Entries[key])
		}
	}

	// Persist and re-read
	if err := h.Save(histPath); err != nil {
		t.Fatalf("save: %v", err)
	}

	h2, err := LoadHistory(histPath)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if h2.Entries["unannotated:pkg/a.go::Foo"] != 1 {
		t.Errorf("expected persisted count 1, got %d", h2.Entries["unannotated:pkg/a.go::Foo"])
	}
}

func TestHistory_SecondRun_TwiceRuleTriggered(t *testing.T) {
	dir := t.TempDir()
	histPath := filepath.Join(dir, "lint-history.json")

	// First run
	h1, _ := LoadHistory(histPath)
	h1.Update([]string{"unannotated:pkg/a.go::Foo", "missing:pkg/b.go"})
	h1.Save(histPath)

	// Second run — same items (plus one new, one resolved)
	h2, _ := LoadHistory(histPath)
	triggered := h2.Update([]string{"unannotated:pkg/a.go::Foo", "incomplete:pkg/c.go::Bar"})

	// "unannotated:pkg/a.go::Foo" appeared twice → triggered
	if len(triggered) != 1 || triggered[0] != "unannotated:pkg/a.go::Foo" {
		t.Errorf("expected [unannotated:pkg/a.go::Foo] triggered, got %v", triggered)
	}

	// "missing:pkg/b.go" resolved → removed
	if _, ok := h2.Entries["missing:pkg/b.go"]; ok {
		t.Error("resolved item should be removed from entries")
	}

	// "incomplete:pkg/c.go::Bar" is new → count 1
	if h2.Entries["incomplete:pkg/c.go::Bar"] != 1 {
		t.Errorf("expected count 1 for new item, got %d", h2.Entries["incomplete:pkg/c.go::Bar"])
	}
}

func TestHistory_ResolvedItem_Removed(t *testing.T) {
	dir := t.TempDir()
	histPath := filepath.Join(dir, "lint-history.json")

	h, _ := LoadHistory(histPath)
	h.Update([]string{"unannotated:pkg/a.go::Foo"})
	h.Save(histPath)

	// Second run — item resolved (not in current)
	h2, _ := LoadHistory(histPath)
	h2.Update([]string{}) // empty = all resolved

	if len(h2.Entries) != 0 {
		t.Errorf("expected empty entries after resolution, got %v", h2.Entries)
	}
}
