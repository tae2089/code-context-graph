package benchmark

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"
)

var validQueryID = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// LoadCorpus reads a queries.yaml file and validates its contents.
func LoadCorpus(path string) (*Corpus, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read corpus %q: %w", path, err)
	}
	var c Corpus
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse corpus %q: %w", path, err)
	}
	if err := ValidateCorpus(&c); err != nil {
		return nil, err
	}
	return &c, nil
}

// SaveCorpus writes a Corpus to a YAML file.
func SaveCorpus(path string, c *Corpus) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal corpus: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write corpus %q: %w", path, err)
	}
	return nil
}

// ValidateCorpus checks that all queries have required fields and no duplicate IDs.
func ValidateCorpus(c *Corpus) error {
	if len(c.Queries) == 0 {
		return fmt.Errorf("corpus has no queries")
	}
	seen := make(map[string]bool, len(c.Queries))
	for i, q := range c.Queries {
		if q.ID == "" {
			return fmt.Errorf("query[%d] missing id", i)
		}
		if !validQueryID.MatchString(q.ID) {
			return fmt.Errorf("query[%d] id %q contains invalid characters (use only [A-Za-z0-9_.-])", i, q.ID)
		}
		if q.Description == "" {
			return fmt.Errorf("query %q missing description", q.ID)
		}
		if seen[q.ID] {
			return fmt.Errorf("duplicate query id %q", q.ID)
		}
		seen[q.ID] = true
		if strings.Contains(q.Description, markerStart) || strings.Contains(q.Description, markerEnd) {
			return fmt.Errorf("query %q description contains reserved benchmark marker", q.ID)
		}
	}
	return nil
}
