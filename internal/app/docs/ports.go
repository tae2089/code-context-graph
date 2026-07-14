// @index Consumer-owned graph read contracts for generated documentation and lint policy.
package docs

import (
	"context"
	"time"

	"github.com/tae2089/code-context-graph/internal/domain/graph"
	"github.com/tae2089/code-context-graph/internal/domain/reference"
)

// RootedFiles provides safe, atomic access relative to one configured docs output root.
// @intent keep path containment, symlink checks, and filesystem mutation outside docs policy.
type RootedFiles interface {
	Validate(relPath string) error
	Read(relPath string) ([]byte, bool, error)
	Write(relPath string, data []byte) error
	Remove(relPath string) error
	ModTime(relPath string) (time.Time, bool, error)
	MarkdownFiles() (map[string]time.Time, error)
}

// Snapshot couples documentable nodes to their structured annotations.
// @intent keep generation and lint facts from drifting across separate persistence queries.
type Snapshot struct {
	Nodes       []graph.Node
	Annotations map[uint]*graph.Annotation
}

// Repository supplies namespace-scoped facts required by docs generation and lint.
// @intent isolate generated-format and lint policy from GORM query construction.
type Repository interface {
	Snapshot(ctx context.Context, namespace string, kinds []graph.NodeKind) (Snapshot, error)
	OutgoingDocEdges(ctx context.Context, namespace string, nodeIDs []uint) (map[uint][]graph.Edge, error)
	QualifiedNameExists(ctx context.Context, namespace, qualifiedName string) (bool, error)
	CCGRefExists(ctx context.Context, ref reference.Ref) (bool, error)
}
