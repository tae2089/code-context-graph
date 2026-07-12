// @index Language package discovery and package-level semantic edge maintenance.
package workflow

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	ingestapp "github.com/tae2089/code-context-graph/internal/app/ingest"
	"github.com/tae2089/code-context-graph/internal/app/ingest/resolve"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// collectLanguagePackages discovers language-specific package information within the build directory.
// @intent identify package boundaries and file memberships to populate the graph's package structure.
func (s *Service) collectLanguagePackages(ctx context.Context, absDir string, opts BuildOptions) map[string]languagePackageInfo {
	merged := make(map[string]languagePackageInfo)
	ambiguous := make(map[string]struct{})
	for _, discovery := range s.packageDiscoverers() {
		packages, err := discovery.DiscoverPackages(ctx, ingestapp.PackageDiscoveryOptions{
			RootDir: absDir,
			WalkFiles: func(fn func(path, relPath string) error) error {
				return walkMatchingFiles(ctx, absDir, opts, fn)
			},
			HasParser: func(ext string) bool {
				_, ok := s.parserForExt(ext)
				return ok
			},
		})
		if err != nil {
			s.logger().Debug("skip language package context", "dir", absDir, "language", discovery.Language(), "error", err)
			continue
		}
		mergeLanguagePackages(merged, ambiguous, packages)
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

// languagePackageDiscoverer combines parser-owned discovery with a stable language identity.
// @intent select deterministic package discovery capabilities through the parser port.
type languagePackageDiscoverer interface {
	ingestapp.PackageDiscoverer
	Language() string
}

// packageDiscoverers collects unique discovery-capable parsers for all active languages.
// @intent discover packages through the ingest parser port without exposing adapter language specifications.
func (s *Service) packageDiscoverers() []languagePackageDiscoverer {
	if len(s.Walkers) == 0 && len(s.Parsers) == 0 {
		return nil
	}
	add := func(discoverers *[]languagePackageDiscoverer, seen map[string]struct{}, parser Parser) bool {
		discoverer, ok := parser.(languagePackageDiscoverer)
		if !ok || discoverer.Language() == "" {
			return false
		}
		if _, ok := seen[discoverer.Language()]; ok {
			return true
		}
		seen[discoverer.Language()] = struct{}{}
		*discoverers = append(*discoverers, discoverer)
		return true
	}

	exts := make([]string, 0, len(s.Walkers)+len(s.Parsers))
	for ext := range s.Parsers {
		exts = append(exts, ext)
	}
	for ext := range s.Walkers {
		exts = append(exts, ext)
	}
	slices.Sort(exts)
	seen := make(map[string]struct{}, len(exts))
	discoverers := make([]languagePackageDiscoverer, 0, len(exts))
	for _, ext := range exts {
		if add(&discoverers, seen, s.Parsers[ext]) {
			continue
		}
		add(&discoverers, seen, s.Walkers[ext])
	}
	return discoverers
}

// packageEdgeBuilder returns the first deterministic parser capability for one language.
// @intent keep package semantic enrichment behind the parser port instead of importing a parser adapter.
func (s *Service) packageEdgeBuilder(language string) ingestapp.PackageEdgeBuilder {
	if language == "" {
		return nil
	}
	exts := make([]string, 0, len(s.Parsers)+len(s.Walkers))
	for ext := range s.Parsers {
		exts = append(exts, ext)
	}
	for ext := range s.Walkers {
		exts = append(exts, ext)
	}
	slices.Sort(exts)
	for _, ext := range exts {
		if builder := packageEdgeBuilderForParser(s.Parsers[ext], language); builder != nil {
			return builder
		}
		if builder := packageEdgeBuilderForParser(s.Walkers[ext], language); builder != nil {
			return builder
		}
	}
	return nil
}

// packageEdgeBuilderForParser returns a matching optional semantic capability from one parser.
// @intent let explicit parsers and fallback walkers be evaluated independently for package semantics.
func packageEdgeBuilderForParser(parser Parser, language string) ingestapp.PackageEdgeBuilder {
	identified, ok := parser.(interface{ Language() string })
	if !ok || identified.Language() != language {
		return nil
	}
	builder, _ := parser.(ingestapp.PackageEdgeBuilder)
	return builder
}

// withImportPackageContext attaches discovered package names to the context for use during edge resolution.
// @intent ensure cross-package imports can be resolved using their semantic names.
func (s *Service) withImportPackageContext(ctx context.Context, packages map[string]languagePackageInfo) context.Context {
	ctx = ingestapp.WithImportPackages(ctx, importPackageContext(packages))
	return ingestapp.WithFilePackages(ctx, filePackageImportPaths(packages))
}

// mergeLanguagePackages aggregates discovered packages into a central map while filtering ambiguous paths.
// @intent consolidate package discovery results while discarding conflicting definitions.
func mergeLanguagePackages(dst map[string]languagePackageInfo, ambiguous map[string]struct{}, src map[string]languagePackageInfo) {
	for importPath, pkg := range src {
		if importPath == "" {
			continue
		}
		if _, blocked := ambiguous[importPath]; blocked {
			continue
		}
		if existing, ok := dst[importPath]; ok {
			if existing.Language != pkg.Language || existing.Name != pkg.Name || existing.Dir != pkg.Dir {
				delete(dst, importPath)
				ambiguous[importPath] = struct{}{}
				continue
			}
			existing.Files = appendUniqueStrings(existing.Files, pkg.Files...)
			dst[importPath] = existing
			continue
		}
		pkg.Files = appendUniqueStrings(nil, pkg.Files...)
		slices.Sort(pkg.Files)
		dst[importPath] = pkg
	}
}

// @intent normalize discovered package imports into the canonical names used when resolving cross-file imports during parsing.
func importPackageContext(packages map[string]languagePackageInfo) map[string]string {
	if len(packages) == 0 {
		return nil
	}
	values := make(map[string]string, len(packages))
	canonicalByDir := make(map[string]string, len(packages))
	for _, pkg := range packages {
		if pkg.Language != "typescript" && pkg.Language != "javascript" {
			continue
		}
		if pkg.Dir == "" || pkg.ImportPath == "" {
			continue
		}
		current := canonicalByDir[pkg.Dir]
		if current == "" || len(pkg.ImportPath) > len(current) {
			canonicalByDir[pkg.Dir] = pkg.ImportPath
		}
	}
	for importPath, pkg := range packages {
		if importPath == "" {
			continue
		}
		switch pkg.Language {
		case "typescript", "javascript":
			if canonical := canonicalByDir[pkg.Dir]; canonical != "" {
				values[importPath] = canonical
				continue
			}
			values[importPath] = pkg.ImportPath
		default:
			if pkg.Name != "" {
				values[importPath] = pkg.Name
			}
		}
	}
	if len(values) == 0 {
		return nil
	}
	return values
}

// filePackageImportPaths extracts a deterministic mapping of file paths to canonical import paths.
// @intent seed parser qualified names from discovered package ownership without depending on map iteration order.
func filePackageImportPaths(packages map[string]languagePackageInfo) map[string]string {
	if len(packages) == 0 {
		return nil
	}
	paths := sortedPackageImportPaths(packages)
	filePackages := make(map[string]string)
	for _, importPath := range paths {
		pkg := packages[importPath]
		if pkg.ImportPath == "" {
			continue
		}
		if pkg.Language != "typescript" && pkg.Language != "javascript" {
			continue
		}
		for _, filePath := range pkg.Files {
			if filePath == "" {
				continue
			}
			if _, exists := filePackages[filePath]; exists {
				continue
			}
			filePackages[filePath] = pkg.ImportPath
		}
	}
	if len(filePackages) == 0 {
		return nil
	}
	return filePackages
}

// packageNodes converts discovered package info into graph nodes.
// @intent project package metadata into the graph schema for persistence.
func packageNodes(packages map[string]languagePackageInfo) []graph.Node {
	if len(packages) == 0 {
		return nil
	}
	importPaths := sortedPackageImportPaths(packages)
	nodes := make([]graph.Node, 0, len(importPaths))
	for _, importPath := range importPaths {
		pkg := packages[importPath]
		nodes = append(nodes, graph.Node{
			QualifiedName: pkg.ImportPath,
			Kind:          graph.NodeKindPackage,
			Name:          pkg.Name,
			FilePath:      pkg.Dir,
			StartLine:     1,
			EndLine:       1,
			Language:      pkg.Language,
		})
	}
	return nodes
}

// packageContainsEdgeCount returns the total number of file-to-package containment relationships.
// @intent estimate the edge overhead for package structural nodes.
func packageContainsEdgeCount(packages map[string]languagePackageInfo) int {
	count := 0
	for _, pkg := range packages {
		count += len(pkg.Files)
	}
	return count
}

// upsertPackageNodes persists discovered package nodes into the graph store.
// @intent ensure package nodes exist before their member files are linked.
func upsertPackageNodes(ctx context.Context, txStore ingestapp.GraphStore, packages map[string]languagePackageInfo) error {
	nodes := packageNodes(packages)
	if len(nodes) == 0 {
		return nil
	}
	return txStore.UpsertNodes(ctx, nodes)
}

// upsertPackageContainsEdges links package nodes to their member file nodes.
// @intent populate the graph's structural hierarchy by connecting packages to their source files.
func upsertPackageContainsEdges(ctx context.Context, txStore ingestapp.GraphStore, packages map[string]languagePackageInfo) error {
	if len(packages) == 0 {
		return nil
	}
	importPaths := sortedPackageImportPaths(packages)
	pkgNodes, err := txStore.GetNodesByQualifiedNames(ctx, importPaths)
	if err != nil {
		return err
	}
	filePaths := packageFilePaths(packages)
	nodesByFile, err := txStore.GetNodesByFiles(ctx, filePaths)
	if err != nil {
		return err
	}
	var edges []graph.Edge
	for _, importPath := range importPaths {
		pkgNode := singleNodeOfKind(pkgNodes[importPath], graph.NodeKindPackage)
		if pkgNode == nil {
			continue
		}
		for _, filePath := range packages[importPath].Files {
			fileNode := singleNodeOfKind(nodesByFile[filePath], graph.NodeKindFile)
			if fileNode == nil {
				continue
			}
			edges = append(edges, graph.Edge{
				FromNodeID:  pkgNode.ID,
				ToNodeID:    fileNode.ID,
				Kind:        graph.EdgeKindContains,
				FilePath:    filePath,
				Line:        1,
				Fingerprint: packageContainsFingerprint(importPath, filePath),
			})
		}
	}
	return txStore.UpsertEdges(ctx, edges)
}

// sortedPackageImportPaths returns a deterministic list of all discovered import paths.
// @intent keep package-related database operations stable across build runs.
func sortedPackageImportPaths(packages map[string]languagePackageInfo) []string {
	paths := make([]string, 0, len(packages))
	for importPath := range packages {
		paths = append(paths, importPath)
	}
	slices.Sort(paths)
	return paths
}

// packageFilePaths extracts all unique file paths belonging to the discovered packages.
// @intent collect all files that need to be linked to their containing package nodes.
func packageFilePaths(packages map[string]languagePackageInfo) []string {
	seen := make(map[string]struct{})
	var paths []string
	for _, pkg := range packages {
		for _, filePath := range pkg.Files {
			if _, ok := seen[filePath]; ok {
				continue
			}
			seen[filePath] = struct{}{}
			paths = append(paths, filePath)
		}
	}
	slices.Sort(paths)
	return paths
}

// singleNodeOfKind returns the first node in a list that matches the specified kind, or nil if none or multiple exist.
// @intent ensure unambiguous node selection during structural edge linking.
func singleNodeOfKind(nodes []graph.Node, kind graph.NodeKind) *graph.Node {
	var found *graph.Node
	for i := range nodes {
		if nodes[i].Kind != kind {
			continue
		}
		if found != nil {
			return nil
		}
		found = &nodes[i]
	}
	return found
}

// packageContainsFingerprint generates a stable identifier for a package-to-file relationship.
// @intent ensure package structural edges can be upserted without duplication.
func packageContainsFingerprint(importPath, filePath string) string {
	sum := sha256.Sum256([]byte(importPath + "\x00" + filePath))
	return fmt.Sprintf("contains:package:%x", sum)
}

// refreshPackageSemanticEdges rebuilds package-level semantic edges for affected packages only.
// @intent keep synthesized package relationships in sync after incremental file changes without rebuilding every package.
// @sideEffect deletes stale package semantic edges and upserts regenerated ones through the graph store.
// @mutates graph edges
func (s *Service) refreshPackageSemanticEdges(ctx context.Context, graphStore ingestapp.GraphStore, absDir string, packages map[string]languagePackageInfo, changedFiles, deletedFiles []string, resolveOptions resolve.ResolveOptions) error {
	if graphStore == nil || len(packages) == 0 {
		return nil
	}
	batches, anchors, err := s.collectAffectedPackageSemanticBatches(ctx, graphStore, absDir, packages, changedFiles, deletedFiles)
	if err != nil {
		return err
	}
	if err := graphStore.DeletePackageSemanticEdges(ctx, anchors); err != nil {
		return err
	}
	edgeBatches := s.packageSemanticEdgeBatches(batches)
	if len(edgeBatches) == 0 {
		return nil
	}
	return s.flushBuildEdges(ctx, graphStore, edgeBatches, nil, resolveOptions)
}

// collectAffectedPackageSemanticBatches gathers node batches for packages touched by changed or deleted files.
// @intent limit package semantic edge refresh work to packages whose file sets overlap the current update.
func (s *Service) collectAffectedPackageSemanticBatches(ctx context.Context, graphStore ingestapp.GraphStore, absDir string, packages map[string]languagePackageInfo, changedFiles, deletedFiles []string) ([]parsedBuildNodeBatch, []string, error) {
	affected := affectedPackageImportPaths(packages, append(append([]string(nil), changedFiles...), deletedFiles...))
	if len(affected) == 0 {
		return nil, nil, nil
	}
	fileSet := make(map[string]struct{})
	for _, importPath := range affected {
		for _, filePath := range packages[importPath].Files {
			fileSet[filePath] = struct{}{}
		}
	}
	filePaths := make([]string, 0, len(fileSet))
	for filePath := range fileSet {
		filePaths = append(filePaths, filePath)
	}
	slices.Sort(filePaths)
	nodesByFile, err := graphStore.GetNodesByFiles(ctx, filePaths)
	if err != nil {
		return nil, nil, err
	}
	var batches []parsedBuildNodeBatch
	anchors := make([]string, 0, len(affected))
	for _, importPath := range affected {
		pkg := packages[importPath]
		files := append([]string(nil), pkg.Files...)
		slices.Sort(files)
		if len(files) == 0 {
			continue
		}
		anchors = append(anchors, files...)
		for _, filePath := range files {
			nodes := nodesByFile[filePath]
			if len(nodes) == 0 {
				continue
			}
			meta, language, err := s.packageSemanticMetadataForFile(ctx, absDir, filePath)
			if err != nil {
				return nil, nil, err
			}
			if language == "" {
				continue
			}
			packageName := meta.Package
			if packageName == "" {
				packageName = pkg.Name
			}
			batches = append(batches, parsedBuildNodeBatch{
				relPath:     filePath,
				nodes:       nodes,
				packageName: packageName,
				interfaces:  meta.Interfaces,
				language:    language,
			})
		}
	}
	return batches, anchors, nil
}

// packageSemanticMetadataForFile reparses one file to recover package-level semantic metadata.
// @intent reload package and interface metadata only for files participating in a package semantic refresh.
func (s *Service) packageSemanticMetadataForFile(ctx context.Context, absDir, relPath string) (ingestapp.ParseMetadata, string, error) {
	parser, ok := s.parserForExt(strings.ToLower(filepath.Ext(relPath)))
	if !ok {
		return ingestapp.ParseMetadata{}, "", nil
	}
	mp, ok := parser.(metadataParserWithLanguage)
	if !ok {
		return ingestapp.ParseMetadata{}, "", nil
	}
	content, err := os.ReadFile(filepath.Join(absDir, relPath))
	if err != nil {
		return ingestapp.ParseMetadata{}, "", err
	}
	_, _, _, meta, err := mp.ParseWithCommentsAndMetadata(ctx, relPath, content)
	if err != nil {
		return ingestapp.ParseMetadata{}, "", err
	}
	return meta, mp.Language(), nil
}

// affectedPackageImportPaths identifies packages whose file lists intersect the changed scope.
// @intent constrain package semantic refresh to import paths touched directly or by directory-level package splits.
func affectedPackageImportPaths(packages map[string]languagePackageInfo, affectedFiles []string) []string {
	if len(packages) == 0 || len(affectedFiles) == 0 {
		return nil
	}
	fileSet := make(map[string]struct{}, len(affectedFiles))
	dirSet := make(map[string]struct{}, len(affectedFiles))
	for _, filePath := range affectedFiles {
		filePath = filepath.ToSlash(filePath)
		fileSet[filePath] = struct{}{}
		dirSet[path.Dir(filePath)] = struct{}{}
	}
	var affected []string
	for importPath, pkg := range packages {
		for _, filePath := range pkg.Files {
			filePath = filepath.ToSlash(filePath)
			if _, ok := fileSet[filePath]; ok {
				affected = append(affected, importPath)
				break
			}
		}
		if len(affected) > 0 && affected[len(affected)-1] == importPath {
			continue
		}
		if _, ok := dirSet[filepath.ToSlash(pkg.Dir)]; ok {
			affected = append(affected, importPath)
		}
	}
	slices.Sort(affected)
	return affected
}

// appendUniqueString appends a string to a slice only if it is not already present.
// @intent maintain a unique set of strings while preserving insertion order for small sets.
func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

// appendUniqueStrings appends multiple strings to a slice, ensuring each is unique.
// @intent aggregate strings from multiple sources while filtering duplicates.
func appendUniqueStrings(values []string, add ...string) []string {
	for _, value := range add {
		values = appendUniqueString(values, value)
	}
	return values
}
