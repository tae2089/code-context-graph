package docs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// History tracks lint results across runs for the Twice Rule.
// @intent 반복적으로 발생하는 문서 이슈를 누적 추적해 자동 규칙 승격에 활용한다.
type History struct {
	Timestamp time.Time      `json:"timestamp"`
	Entries   map[string]int `json:"entries"` // "category:qualified_name" → consecutive count
}

// LoadHistory reads the history file. Returns empty history if file doesn't exist.
// @intent 이전 lint 실행 이력을 복원해 연속 발생 여부를 판단할 수 있게 한다.
// @return 파일이 없으면 비어 있는 History를 반환한다.
func LoadHistory(path string) (*History, error) {
	h := &History{Entries: map[string]int{}}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return h, nil
		}
		return nil, err
	}

	if err := json.Unmarshal(data, h); err != nil {
		return nil, err
	}
	if h.Entries == nil {
		h.Entries = map[string]int{}
	}
	return h, nil
}

// Update compares current lint keys against stored history.
// Returns keys that reached count >= 2 (Twice Rule triggered).
// Mutates h.Entries in place.
// @intent 현재 이슈 집합으로 연속 발생 카운트를 갱신한다.
// @domainRule 같은 키가 두 번 이상 연속 관측되면 Twice Rule 대상으로 간주한다.
// @mutates h.Entries, h.Timestamp
func (h *History) Update(currentKeys []string) []string {
	currentSet := map[string]bool{}
	for _, k := range currentKeys {
		currentSet[k] = true
	}

	var triggered []string

	// Remove resolved items (in history but not in current)
	for key := range h.Entries {
		if !currentSet[key] {
			delete(h.Entries, key)
		}
	}

	// Increment counts for current items
	for _, key := range currentKeys {
		h.Entries[key]++
		if h.Entries[key] >= 2 {
			triggered = append(triggered, key)
		}
	}

	h.Timestamp = time.Now()
	sort.Strings(triggered)
	return triggered
}

// Save writes the history to the given path, creating parent dirs if needed.
// @intent 다음 lint 실행이 이전 상태를 참조할 수 있도록 이력을 영속화한다.
// @sideEffect 대상 경로의 부모 디렉터리를 만들고 JSON 파일을 기록한다.
func (h *History) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// WriteYamlRules appends Twice-Rule-triggered entries to .ccg.yaml rules section.
// Idempotent: skips rules whose pattern already exists in the file.
// Creates the file if it doesn't exist.
// @intent 반복 발생 이슈를 설정 파일의 warn 규칙으로 반영한다.
// @domainRule 이미 존재하는 pattern은 중복 규칙으로 다시 추가하지 않는다.
// @sideEffect .ccg.yaml 파일을 읽고 rules 섹션을 갱신한다.
func WriteYamlRules(cfgPath string, triggered []string) error {
	data, err := os.ReadFile(cfgPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	content := string(data)

	// Parse triggered keys ("category:qualified_name")
	var newRules []string
	for _, key := range triggered {
		parts := strings.SplitN(key, ":", 2)
		if len(parts) != 2 {
			continue
		}
		category, pattern := parts[0], parts[1]

		// Idempotency: skip if pattern already in file
		if strings.Contains(content, pattern) {
			continue
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("  - pattern: %q\n", pattern))
		sb.WriteString(fmt.Sprintf("    category: %s\n", category))
		sb.WriteString("    action: warn\n")
		sb.WriteString("    auto: true\n")
		sb.WriteString(fmt.Sprintf("    created: %q\n", time.Now().Format("2006-01-02")))
		newRules = append(newRules, sb.String())
	}

	if len(newRules) == 0 {
		return nil
	}

	var out strings.Builder
	out.WriteString(content)
	if !strings.Contains(content, "rules:") {
		out.WriteString("\nrules:\n")
	}
	for _, r := range newRules {
		out.WriteString(r)
	}

	return os.WriteFile(cfgPath, []byte(out.String()), 0o644)
}
