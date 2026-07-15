// @index Consumer-owned transaction ports for graph ingest application workflows.
package ingest

import (
	"context"

	"github.com/tae2089/code-context-graph/internal/app/ingest/resolve"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// CommentBlock is parser-neutral documentation text associated with source lines.
// @intent carry comments and docstring ownership from parser adapters into ingest binding policy.
type CommentBlock struct {
	StartLine      int
	EndLine        int
	Text           string
	IsDocstring    bool
	OwnerStartLine int
}

// PackageInterfaceInfo describes one parsed interface and its declared methods.
// @intent preserve package-level implementation inference without exposing parser implementation types.
type PackageInterfaceInfo struct {
	Name    string
	Methods []string
}

// ParseMetadata carries package-level enrichment inputs produced by a parser.
// @intent let ingest coordinate package semantics through parser-owned metadata.
type ParseMetadata struct {
	Package    string
	Interfaces []PackageInterfaceInfo
}

// PackageInfo describes one source package discovered by a parser adapter.
// @intent let ingest create package nodes and membership edges without knowing language-specific discovery details.
type PackageInfo struct {
	ImportPath string
	Name       string
	Dir        string
	Language   string
	Files      []string
}

// PackageDiscoveryOptions supplies traversal policy to parser package discovery.
// @intent reuse ingest include/exclude and parser-registration policy during language-specific package discovery.
type PackageDiscoveryOptions struct {
	RootDir   string
	WalkFiles func(func(path, relPath string) error) error
	HasParser func(ext string) bool
}

// PackageContext contains the package-wide graph state used to derive semantic edges.
// @intent let parser adapters enrich multi-file packages without leaking AST types into ingest.
type PackageContext struct {
	Package        string
	Language       string
	Files          []string
	Nodes          []graph.Node
	Interfaces     []PackageInterfaceInfo
	ImportPackages map[string]string
}

// Parser is the minimum source parser consumed by build and update workflows.
// @intent parse source into domain graph values without exposing Tree-sitter or another parser implementation.
// @domainRule full builds may invoke one Parser instance concurrently, so ParseWithContext implementations must isolate mutable parser state.
type Parser interface {
	Parse(filePath string, content []byte) ([]graph.Node, []graph.Edge, error)
	ParseWithContext(ctx context.Context, filePath string, content []byte) ([]graph.Node, []graph.Edge, error)
}

// AnnotatingParser adds comments and language identity for annotation restoration.
// @intent make comment-aware parsing an optional ingest capability.
type AnnotatingParser interface {
	Parser
	ParseWithComments(ctx context.Context, filePath string, content []byte) ([]graph.Node, []graph.Edge, []CommentBlock, error)
	Language() string
}

// MetadataParser adds package-level parse metadata used by full and incremental semantic refreshes.
// @intent expose package/interface metadata without coupling ingest to parser adapter structs.
type MetadataParser interface {
	AnnotatingParser
	ParseWithCommentsAndMetadata(ctx context.Context, filePath string, content []byte) ([]graph.Node, []graph.Edge, []CommentBlock, ParseMetadata, error)
}

// PackageDiscoverer is an optional parser capability for repository-level package discovery.
// @intent delegate language-specific package discovery while ingest owns traversal policy.
type PackageDiscoverer interface {
	DiscoverPackages(ctx context.Context, opts PackageDiscoveryOptions) (map[string]PackageInfo, error)
}

// PackageEdgeBuilder is an optional parser capability for multi-file semantic relationships.
// @intent derive language-specific package edges through a parser-neutral ingest contract.
type PackageEdgeBuilder interface {
	PackageEdges(ctx PackageContext) []graph.Edge
}

// GraphStore is the transaction-scoped graph persistence surface required by build and update workflows.
// @intent keep ingest graph reads and writes inside the unit-of-work boundary without exposing a persistence implementation.
type GraphStore interface {
	GetNodesByIDs(ctx context.Context, ids []uint) ([]graph.Node, error)
	GetNodesByFile(ctx context.Context, filePath string) ([]graph.Node, error)
	GetNodesByFiles(ctx context.Context, filePaths []string) (map[string][]graph.Node, error)
	GetNodesByQualifiedNames(ctx context.Context, names []string) (map[string][]graph.Node, error)
	ListFileNodes(ctx context.Context) ([]graph.Node, error)
	ListImportFileNodes(ctx context.Context) ([]graph.Node, error)
	GetFileNodesByPathSuffix(ctx context.Context, suffix string) ([]graph.Node, error)
	GetEdgesFromNodes(ctx context.Context, nodeIDs []uint) ([]graph.Edge, error)
	GetEdgesToNodes(ctx context.Context, nodeIDs []uint) ([]graph.Edge, error)
	UpsertNodes(ctx context.Context, nodes []graph.Node) error
	UpsertEdges(ctx context.Context, edges []graph.Edge) error
	UpsertAnnotation(ctx context.Context, annotation *graph.Annotation) error
	DeleteNodesByFile(ctx context.Context, filePath string) error
	DeleteEdgesByFile(ctx context.Context, filePath string) error
	DeletePackageSemanticEdges(ctx context.Context, anchorFiles []string) error
	DeleteGraph(ctx context.Context) error
}

// SearchWriter updates derived search state inside the same transaction as graph mutations.
// @intent expose full and scoped search rebuilds as indivisible application operations.
// @domainRule implementations must combine search-document refresh and backend index rebuild so callers cannot commit only one half.
type SearchWriter interface {
	RebuildAll(ctx context.Context) error
	RebuildNodes(ctx context.Context, nodeIDs []uint) error
}

// Transaction exposes the graph and search capabilities participating in one atomic ingest operation.
// @intent give an ingest callback transaction-scoped capabilities without exposing a raw database handle.
type Transaction interface {
	Graph() GraphStore
	Search() SearchWriter
}

// UnitOfWork owns the atomic boundary for graph persistence and derived search updates.
// @intent commit graph and search changes together only when the callback succeeds.
// @ensures a callback error rolls back both graph and search changes.
type UnitOfWork interface {
	WithinTransaction(ctx context.Context, fn func(Transaction) error) error
}

// FileInfo carries one incremental source snapshot and its change-detection metadata.
// @intent keep incremental update inputs owned by ingest rather than a concrete sync implementation.
type FileInfo struct {
	Hash    string
	Content []byte
	Force   bool
}

// SyncStats reports incremental file outcomes and unresolved-edge diagnostics.
// @intent expose update results without coupling callers to the incremental implementation package.
type SyncStats struct {
	Added      int
	Modified   int
	Skipped    int
	Deleted    int
	Unresolved resolve.FilterResolvedDiagnostics
}

// IncrementalSyncer reconciles one current source snapshot against known files.
// @intent let ingest orchestrate batching and deletion policy through an implementation-neutral sync seam.
type IncrementalSyncer interface {
	SyncWithExisting(ctx context.Context, files map[string]FileInfo, existingFiles []string) (*SyncStats, error)
}

// TransactionalIncrementalSyncer runs incremental reconciliation against the active graph transaction.
// @intent keep incremental graph mutations inside the same unit of work as package and search updates.
type TransactionalIncrementalSyncer interface {
	SyncWithExistingStore(ctx context.Context, graphStore GraphStore, files map[string]FileInfo, existingFiles []string) (*SyncStats, error)
}
