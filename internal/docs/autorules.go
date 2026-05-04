package docs

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// AutoRule stores generated warn-only lint state separate from human policy.
// @intent Twice Rule이 만든 자동 warn 규칙을 사람이 관리하는 설정 파일과 분리 저장한다.
type AutoRule struct {
	Pattern  string `yaml:"pattern"`
	Category string `yaml:"category"`
	Action   string `yaml:"action"`
	Auto     bool   `yaml:"auto"`
	Created  string `yaml:"created,omitempty"`
}

// AutoRuleSet is the persisted generated-rule document written under .ccg/.
// @intent generated lint state를 rules YAML shape로 직렬화해 기존 규칙 형태와 호환되게 유지한다.
type AutoRuleSet struct {
	Rules []AutoRule `yaml:"rules"`
}

// LoadAutoRules reads generated auto rules. Missing files return an empty set.
// @intent generated lint rule 상태 파일이 없을 때도 lint가 정상적으로 동작하게 한다.
func LoadAutoRules(path string) (*AutoRuleSet, error) {
	set := &AutoRuleSet{Rules: []AutoRule{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return set, nil
		}
		return nil, err
	}
	if err := yaml.Unmarshal(data, set); err != nil {
		return nil, err
	}
	if set.Rules == nil {
		set.Rules = []AutoRule{}
	}
	return set, nil
}

// Upsert adds new generated warn rules for triggered lint keys.
// @intent 같은 category+pattern 규칙을 중복 기록하지 않고 새 자동 규칙만 추가한다.
func (s *AutoRuleSet) Upsert(triggered []string) []AutoRule {
	if s.Rules == nil {
		s.Rules = []AutoRule{}
	}
	existing := make(map[string]struct{}, len(s.Rules))
	for _, rule := range s.Rules {
		existing[autoRuleKey(rule.Category, rule.Pattern)] = struct{}{}
	}

	var added []AutoRule
	for _, key := range triggered {
		category, pattern, ok := strings.Cut(key, ":")
		if !ok || category == "" || pattern == "" {
			continue
		}
		lookup := autoRuleKey(category, pattern)
		if _, ok := existing[lookup]; ok {
			continue
		}
		rule := AutoRule{
			Pattern:  pattern,
			Category: category,
			Action:   "warn",
			Auto:     true,
			Created:  time.Now().Format("2006-01-02"),
		}
		s.Rules = append(s.Rules, rule)
		added = append(added, rule)
		existing[lookup] = struct{}{}
	}

	sort.Slice(s.Rules, func(i, j int) bool {
		if s.Rules[i].Category == s.Rules[j].Category {
			return s.Rules[i].Pattern < s.Rules[j].Pattern
		}
		return s.Rules[i].Category < s.Rules[j].Category
	})
	return added
}

// Save writes generated auto rules, creating parent dirs if needed.
// @intent generated lint state를 원자적으로 기록해 수동 정책 파일과 분리 유지한다.
func (s *AutoRuleSet) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(s)
	if err != nil {
		return err
	}
	return atomicWriteFile(path, data, 0o644)
}

// autoRuleKey builds a stable map key for one generated lint rule.
// @intent deduplicate auto-generated rules by category and pattern before persisting them.
func autoRuleKey(category, pattern string) string {
	return category + "\x00" + pattern
}
