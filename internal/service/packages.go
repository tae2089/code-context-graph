// @index Language package discovery and package-level semantic edge maintenance.
package service

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"gorm.io/gorm"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/edgeresolve"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/parse/treesitter"
	"github.com/tae2089/code-context-graph/internal/store"
)

// collectLanguagePackages discovers language-specific package information within the build directory.
// @intent identify package boundaries and file memberships to populate the graph's package structure.
func (s *GraphService) collectLanguagePackages(ctx context.Context, absDir string, opts BuildOptions) map[string]languagePackageInfo {
	merged := make(map[string]languagePackageInfo)
	ambiguous := make(map[string]struct{})
	for _, spec := range s.packageDiscoverySpecs() {
		discovery := treesitter.PackageDiscoveryOrDefault(spec)
		packages, err := discovery.DiscoverPackages(ctx, treesitter.PackageDiscoveryOptions{
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
			s.logger().Debug("skip language package context", "dir", absDir, "language", spec.Name, "error", err)
			continue
		}
		mergeLanguagePackages(merged, ambiguous, packages)
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

// packageDiscoverySpecs collects language specifications for all active parsers and walkers.
// @intent provide a unique set of discovery rules for all supported source languages.
func (s *GraphService) packageDiscoverySpecs() []*treesitter.LangSpec {
	if len(s.Walkers) == 0 && len(s.Parsers) == 0 {
		return nil
	}
	// parserWithSpec exposes language metadata from parsers that own a LangSpec.
	// @intent let package discovery collect language specs without depending on a concrete parser type.
	type parserWithSpec interface {
		Spec() *treesitter.LangSpec
	}
	addSpec := func(specs *[]*treesitter.LangSpec, seen map[string]struct{}, spec *treesitter.LangSpec) {
		if spec == nil || spec.Name == "" {
			return
		}
		if _, ok := seen[spec.Name]; ok {
			return
		}
		seen[spec.Name] = struct{}{}
		*specs = append(*specs, spec)
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
	specs := make([]*treesitter.LangSpec, 0, len(exts))
	for _, ext := range exts {
		if parser, ok := s.Parsers[ext].(parserWithSpec); ok {
			addSpec(&specs, seen, parser.Spec())
			continue
		}
		walker := s.Walkers[ext]
		if walker == nil {
			continue
		}
		addSpec(&specs, seen, walker.Spec())
	}
	return specs
}

// withImportPackageContext attaches discovered package names to the context for use during edge resolution.
// @intent ensure cross-package imports can be resolved using their semantic names.
func (s *GraphService) withImportPackageContext(ctx context.Context, packages map[string]languagePackageInfo) context.Context {
	ctx = treesitter.WithImportPackages(ctx, importPackageContext(packages))
	return treesitter.WithFilePackages(ctx, filePackageImportPaths(packages))
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
func packageNodes(packages map[string]languagePackageInfo) []model.Node {
	if len(packages) == 0 {
		return nil
	}
	importPaths := sortedPackageImportPaths(packages)
	nodes := make([]model.Node, 0, len(importPaths))
	for _, importPath := range importPaths {
		pkg := packages[importPath]
		nodes = append(nodes, model.Node{
			QualifiedName: pkg.ImportPath,
			Kind:          model.NodeKindPackage,
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
func upsertPackageNodes(ctx context.Context, txStore store.GraphStore, packages map[string]languagePackageInfo) error {
	nodes := packageNodes(packages)
	if len(nodes) == 0 {
		return nil
	}
	return txStore.UpsertNodes(ctx, nodes)
}

// upsertPackageContainsEdges links package nodes to their member file nodes.
// @intent populate the graph's structural hierarchy by connecting packages to their source files.
func upsertPackageContainsEdges(ctx context.Context, txStore store.GraphStore, packages map[string]languagePackageInfo) error {
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
	var edges []model.Edge
	for _, importPath := range importPaths {
		pkgNode := singleNodeOfKind(pkgNodes[importPath], model.NodeKindPackage)
		if pkgNode == nil {
			continue
		}
		for _, filePath := range packages[importPath].Files {
			fileNode := singleNodeOfKind(nodesByFile[filePath], model.NodeKindFile)
			if fileNode == nil {
				continue
			}
			edges = append(edges, model.Edge{
				FromNodeID:  pkgNode.ID,
				ToNodeID:    fileNode.ID,
				Kind:        model.EdgeKindContains,
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
func singleNodeOfKind(nodes []model.Node, kind model.NodeKind) *model.Node {
	var found *model.Node
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
func (s *GraphService) refreshPackageSemanticEdges(ctx context.Context, graphStore store.GraphStore, db *gorm.DB, absDir string, packages map[string]languagePackageInfo, changedFiles, deletedFiles []string, resolveOptions edgeresolve.ResolveOptions) error {
	if graphStore == nil || len(packages) == 0 {
		return nil
	}
	batches, anchors, err := s.collectAffectedPackageSemanticBatches(ctx, graphStore, absDir, packages, changedFiles, deletedFiles)
	if err != nil {
		return err
	}
	if err := deletePackageSemanticEdges(ctx, db, anchors); err != nil {
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
func (s *GraphService) collectAffectedPackageSemanticBatches(ctx context.Context, graphStore store.GraphStore, absDir string, packages map[string]languagePackageInfo, changedFiles, deletedFiles []string) ([]parsedBuildNodeBatch, []string, error) {
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
func (s *GraphService) packageSemanticMetadataForFile(ctx context.Context, absDir, relPath string) (treesitter.ParseMetadata, string, error) {
	parser, ok := s.parserForExt(strings.ToLower(filepath.Ext(relPath)))
	if !ok {
		return treesitter.ParseMetadata{}, "", nil
	}
	mp, ok := parser.(metadataParserWithLanguage)
	if !ok {
		return treesitter.ParseMetadata{}, "", nil
	}
	content, err := os.ReadFile(filepath.Join(absDir, relPath))
	if err != nil {
		return treesitter.ParseMetadata{}, "", err
	}
	_, _, _, meta, err := mp.ParseWithCommentsAndMetadata(ctx, relPath, content)
	if err != nil {
		return treesitter.ParseMetadata{}, "", err
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

// deletePackageSemanticEdges removes stale synthesized package semantic edges for the given anchor files.
// @intent clear old package implements edges before rebuilding the affected package semantic snapshot.
// @sideEffect deletes package semantic edge rows from the edges table.
// @mutates graph edges
func deletePackageSemanticEdges(ctx context.Context, db *gorm.DB, anchors []string) error {
	if db == nil || len(anchors) == 0 {
		return nil
	}
	ns := ctxns.FromContext(ctx)
	return db.WithContext(ctx).
		Where("namespace = ? AND kind = ? AND line = 0 AND file_path IN ?", ns, model.EdgeKindImplements, anchors).
		Delete(&model.Edge{}).Error
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
