package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestLoadAutoRules_MissingReturnsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auto-rules.yaml")

	set, err := LoadAutoRules(testStateFiles, path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if set == nil {
		t.Fatal("expected non-nil set")
	}
	if len(set.Rules) != 0 {
		t.Fatalf("expected empty rules, got %v", set.Rules)
	}
}

func TestAutoRuleSet_RoundTripPreservesFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auto-rules.yaml")
	set := &AutoRuleSet{Rules: []AutoRule{{Pattern: "pkg/a.go::Foo", Category: "unannotated", Action: "warn", Auto: true, Created: "2026-05-03"}}}

	if err := set.Save(testStateFiles, path); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := LoadAutoRules(testStateFiles, path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(loaded.Rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(loaded.Rules))
	}
	rule := loaded.Rules[0]
	if rule.Pattern != "pkg/a.go::Foo" || rule.Category != "unannotated" || rule.Action != "warn" || !rule.Auto || rule.Created != "2026-05-03" {
		t.Fatalf("unexpected round-trip rule: %+v", rule)
	}
}

func TestAutoRuleSet_UpsertAddsNew(t *testing.T) {
	set := &AutoRuleSet{}
	added := set.Upsert([]string{"unannotated:pkg/a.go::Foo"})

	if len(added) != 1 {
		t.Fatalf("expected 1 added rule, got %d", len(added))
	}
	if len(set.Rules) != 1 {
		t.Fatalf("expected 1 stored rule, got %d", len(set.Rules))
	}
	rule := set.Rules[0]
	if rule.Pattern != "pkg/a.go::Foo" {
		t.Fatalf("pattern = %q, want pkg/a.go::Foo", rule.Pattern)
	}
	if rule.Category != "unannotated" || rule.Action != "warn" || !rule.Auto {
		t.Fatalf("unexpected rule metadata: %+v", rule)
	}
	if rule.Created == "" {
		t.Fatal("expected created date to be populated")
	}
}

func TestAutoRuleSet_UpsertIsIdempotent(t *testing.T) {
	set := &AutoRuleSet{}
	set.Upsert([]string{"unannotated:pkg/a.go::Foo"})
	added := set.Upsert([]string{"unannotated:pkg/a.go::Foo"})

	if len(added) != 0 {
		t.Fatalf("expected no new rules on duplicate upsert, got %d", len(added))
	}
	if len(set.Rules) != 1 {
		t.Fatalf("expected 1 stored rule, got %d", len(set.Rules))
	}
}

func TestAutoRuleSet_SaveCreatesParentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "state", "auto-rules.yaml")
	set := &AutoRuleSet{}
	set.Upsert([]string{"missing:pkg/b.go"})

	if err := set.Save(testStateFiles, path); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected saved file to exist: %v", err)
	}
}

func TestAutoRuleSet_SaveUsesYamlPolicyShape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auto-rules.yaml")
	set := &AutoRuleSet{}
	set.Upsert([]string{"dead-ref:pkg/c.go::Bar"})

	if err := set.Save(testStateFiles, path); err != nil {
		t.Fatalf("save: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	content := string(data)
	for _, want := range []string{"rules:", "pattern: pkg/c.go::Bar", "category: dead-ref", "action: warn", "auto: true"} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected %q in yaml, got:\n%s", want, content)
		}
	}

	var loaded AutoRuleSet
	if err := yaml.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("expected valid yaml shape, got error: %v", err)
	}
	if len(loaded.Rules) != 1 || loaded.Rules[0].Pattern != "pkg/c.go::Bar" {
		t.Fatalf("unexpected unmarshaled rules: %+v", loaded.Rules)
	}
}
