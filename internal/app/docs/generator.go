package docs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/tae2089/code-context-graph/internal/domain/graph"
	"github.com/tae2089/code-context-graph/internal/pathspec"
)

// Generator reads the SQLite graph and writes markdown documentation.
// @intent 그래프에 저장된 심볼과 어노테이션을 문서 생성 단계로 전달한다.
type Generator struct {
	Repository Repository
	Files      RootedFiles
	OutDir     string
	Exclude    []string // path/glob patterns to exclude (see pathspec.MatchExcludes)
	Namespace  string
	Prune      bool
}

const generatorManagedMarker = "<!-- generated-by: code-context-graph docs -->\n"

type manifest struct {
	Namespace string   `json:"namespace"`
	Files     []string `json:"files"`
}

// Run generates index.md and per-file docs into g.OutDir.
// @intent 전체 문서 산출물을 한 번에 다시 생성한다.
// @sideEffect 파일별 Markdown과 index.md를 출력 디렉터리에 기록한다.
func (g *Generator) Run() error {
	nodes, annByID, err := g.loadNodes()
	if err != nil {
		return fmt.Errorf("load nodes: %w", err)
	}

	ids := symbolNodeIDs(nodes)
	edgesByFromID, err := g.loadEdges(ids)
	if err != nil {
		return fmt.Errorf("load edges: %w", err)
	}

	if len(g.Exclude) > 0 {
		filtered := nodes[:0]
		for _, n := range nodes {
			if !pathspec.MatchExcludes(g.Exclude, n.FilePath) {
				filtered = append(filtered, n)
			}
		}
		nodes = filtered
	}

	groups := groupByFile(nodes, annByID, edgesByFromID)
	if err := g.validateDocGroups(groups); err != nil {
		return err
	}
	current := generatedFiles(groups)

	var errs []error
	previous, _ := g.loadManifest()
	for _, grp := range groups {
		if err := g.writeFileDoc(grp); err != nil {
			errs = append(errs, fmt.Errorf("write file doc %s: %w", grp.FilePath, err))
		}
	}
	if err := g.writeIndex(groups); err != nil {
		errs = append(errs, err)
	}
	if g.Prune {
		if err := g.pruneManaged(previous.Files, current); err != nil {
			errs = append(errs, err)
		}
	}
	if err := g.saveManifest(current); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// validateDocGroups checks that every group's output path is safe before writing begins.
// @intent prevent path-traversal writes before any file I/O is attempted
func (g *Generator) validateDocGroups(groups []nodeGroup) error {
	for _, grp := range groups {
		if err := g.Files.Validate(grp.FilePath + ".md"); err != nil {
			return fmt.Errorf("invalid file doc path %s: %w", grp.FilePath, err)
		}
	}
	return nil
}

// loadNodes loads documentable graph nodes and their annotations.
// @intent 문서 렌더링에 필요한 노드와 태그를 한 번에 조회한다.
// @return 문서 대상 노드 목록과 node_id 기준 어노테이션 맵을 반환한다.
func (g *Generator) loadNodes() ([]graph.Node, map[uint]*graph.Annotation, error) {
	snapshot, err := g.Repository.Snapshot(context.Background(), g.Namespace, []graph.NodeKind{
		graph.NodeKindFunction, graph.NodeKindClass, graph.NodeKindType, graph.NodeKindTest, graph.NodeKindFile,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("query nodes: %w", err)
	}
	return snapshot.Nodes, snapshot.Annotations, nil
}

// loadEdges loads call and import edges keyed by source node.
// @intent 심볼 문서에 호출 관계를 표시할 최소 엣지 집합만 조회한다.
// @param nodeIDs 파일 노드를 제외한 심볼 노드 ID 목록이다.
// @return from_node_id 기준 엣지 맵을 반환한다.
func (g *Generator) loadEdges(nodeIDs []uint) (map[uint][]graph.Edge, error) {
	if len(nodeIDs) == 0 {
		return nil, nil
	}
	result, err := g.Repository.OutgoingDocEdges(context.Background(), g.Namespace, nodeIDs)
	if err != nil {
		return nil, fmt.Errorf("query edges: %w", err)
	}
	return result, nil
}

// generatedFiles builds the list of output paths that this run will produce.
// @intent track expected output files so the manifest and prune step stay consistent
func generatedFiles(groups []nodeGroup) []string {
	files := make([]string, 0, len(groups)+1)
	for _, grp := range groups {
		files = append(files, filepath.ToSlash(grp.FilePath+".md"))
	}
	files = append(files, "index.md")
	return files
}

// manifestPath returns the namespace-scoped path for the docs manifest file.
// @intent isolate manifest files per namespace so concurrent namespaces do not collide
func (g *Generator) manifestPath() string {
	name := ".ccg-docs-manifest.json"
	if g.Namespace != "" {
		name = ".ccg-docs-manifest." + strings.ReplaceAll(g.Namespace, string(filepath.Separator), "-") + ".json"
	}
	return name
}

// loadManifest reads the previously saved manifest from disk, returning an empty manifest if none exists.
// @intent restore the prior output file list so Run can compute stale files to prune
func (g *Generator) loadManifest() (*manifest, error) {
	m := &manifest{Namespace: g.Namespace}
	data, exists, err := g.Files.Read(g.manifestPath())
	if err != nil {
		return nil, err
	}
	if !exists {
		return m, nil
	}
	if err := json.Unmarshal(data, m); err != nil {
		return nil, err
	}
	return m, nil
}

// saveManifest persists the list of generated files to disk for use by the next run's prune step.
// @intent record which files were written so future runs can detect and remove stale docs
// @sideEffect writes .ccg-docs-manifest[.namespace].json to g.OutDir
func (g *Generator) saveManifest(files []string) error {
	data, err := json.MarshalIndent(manifest{Namespace: g.Namespace, Files: files}, "", "  ")
	if err != nil {
		return err
	}
	return g.Files.Write(g.manifestPath(), data)
}

// pruneManaged deletes previously generated docs that are no longer in the current output set,
// but only if the file starts with the managed marker to avoid removing user-edited files.
// @intent clean up stale generated docs without touching manually created files
// @sideEffect removes markdown files from g.OutDir that are no longer part of the current graph
func (g *Generator) pruneManaged(previous, current []string) error {
	keep := map[string]bool{}
	for _, p := range current {
		keep[p] = true
	}
	var errs []error
	for _, p := range previous {
		p = filepath.ToSlash(p)
		if keep[p] {
			continue
		}
		if err := g.Files.Validate(p); err != nil {
			errs = append(errs, err)
			continue
		}
		data, exists, err := g.Files.Read(p)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !exists {
			continue
		}
		if !strings.HasPrefix(string(data), generatorManagedMarker) {
			continue
		}
		if err := g.Files.Remove(p); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// symbolNodeIDs returns IDs of non-file nodes only.
// File nodes do not originate call/import edges, so excluding them
// keeps the loadEdges IN clause minimal.
// @intent 엣지 조회가 필요한 심볼 노드만 남겨 배치 크기를 줄인다.
func symbolNodeIDs(nodes []graph.Node) []uint {
	ids := make([]uint, 0, len(nodes))
	for _, n := range nodes {
		if n.Kind != graph.NodeKindFile {
			ids = append(ids, n.ID)
		}
	}
	return ids
}
