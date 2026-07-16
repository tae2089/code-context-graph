// @index Unresolved-edge index compatibility identity for safe semi-naive updates.
package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"

	ingestapp "github.com/tae2089/code-context-graph/internal/app/ingest"
)

const unresolvedIndexAlgorithmVersion = "missing-target-v1"

// unresolvedIndexVersion fingerprints the candidate algorithm and every parser that can influence the built graph.
// @intent prevent semi-naive replay from consuming unresolved candidates produced by incompatible parser/query or resolution behavior.
// @domainRule bump unresolvedIndexAlgorithmVersion whenever unresolved candidate selection or endpoint resolution semantics change.
func (s *Service) unresolvedIndexVersion() (string, bool) {
	parsers := make(map[string]Parser, len(s.Parsers)+len(s.Walkers))
	for ext, parser := range s.Parsers {
		parsers["parser:"+ext] = parser
	}
	for ext, parser := range s.Walkers {
		parsers["walker:"+ext] = parser
	}
	if len(parsers) == 0 {
		return "", false
	}
	keys := make([]string, 0, len(parsers))
	for key := range parsers {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	h := sha256.New()
	h.Write([]byte(unresolvedIndexAlgorithmVersion))
	h.Write([]byte{0})
	for _, key := range keys {
		versioned, ok := parsers[key].(ingestapp.VersionedParser)
		if !ok || versioned.ParseCacheVersion() == "" {
			return "", false
		}
		h.Write([]byte(key))
		h.Write([]byte{0})
		h.Write([]byte(versioned.ParseCacheVersion()))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), true
}
