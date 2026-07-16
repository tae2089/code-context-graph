// @index Immutable exact and longest-suffix file-node index shared by build and update resolution.
package resolve

import (
	"path"
	"strings"

	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// ImportFileIndex maps exact directories and their suffixes to persisted file nodes.
// @intent resolve many import paths from one immutable file-node snapshot without repeated store scans.
type ImportFileIndex struct {
	byDirectory map[string][]graph.Node
	bySuffix    map[string][]graph.Node
}

// NewImportFileIndex precomputes directory suffixes for the supplied file nodes.
// @intent share the exact-directory and longest-suffix import policy across build and staged update resolution.
func NewImportFileIndex(nodes []graph.Node) *ImportFileIndex {
	index := &ImportFileIndex{
		byDirectory: make(map[string][]graph.Node),
		bySuffix:    make(map[string][]graph.Node),
	}
	for _, node := range nodes {
		if node.Kind != graph.NodeKindFile {
			continue
		}
		dir := strings.Trim(path.Dir(node.FilePath), "/")
		if dir == "" || dir == "." {
			continue
		}
		index.byDirectory[dir] = append(index.byDirectory[dir], node)
		parts := strings.Split(dir, "/")
		for start := range parts {
			suffix := strings.Join(parts[start:], "/")
			index.bySuffix[suffix] = append(index.bySuffix[suffix], node)
		}
	}
	return index
}

// Find returns exact directory matches first, then all matches with the longest suffix.
// @intent preserve GraphStore import lookup precedence using bounded map reads.
func (i *ImportFileIndex) Find(importPath string) []graph.Node {
	if i == nil {
		return nil
	}
	importPath = strings.Trim(path.Clean(strings.TrimSpace(importPath)), "/")
	if importPath == "" || importPath == "." {
		return nil
	}
	if exact := i.byDirectory[importPath]; len(exact) > 0 {
		return exact
	}
	parts := strings.Split(importPath, "/")
	for start := range parts {
		if candidates := i.bySuffix[strings.Join(parts[start:], "/")]; len(candidates) > 0 {
			return candidates
		}
	}
	return nil
}
