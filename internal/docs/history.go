package docs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// History tracks lint results across runs for the Twice Rule.
type History struct {
	Timestamp time.Time      `json:"timestamp"`
	Entries   map[string]int `json:"entries"` // "category:qualified_name" → consecutive count
}

// LoadHistory reads the history file. Returns empty history if file doesn't exist.
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
