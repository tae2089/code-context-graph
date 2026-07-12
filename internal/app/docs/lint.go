package docs

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
	"github.com/tae2089/code-context-graph/internal/domain/reference"
	"github.com/tae2089/code-context-graph/internal/pathspec"
)

// Contradiction represents a symbol whose annotation is outdated relative to
// the code: the node was modified after the annotation was last written, and
// the annotation contains detail tags (e.g. @param) that may no longer be
// accurate.
// @intent 코드 변경으로 세부 어노테이션 신뢰성이 깨진 심볼을 보고한다.
type Contradiction struct {
	QualifiedName string
	Detail        string
}

// DeadRef represents an @see tag whose target qualified name does not exist in
// the graph. This indicates a broken cross-reference that should be updated or
// removed.
// @intent 해석되지 않는 @see 참조를 수집해 문서 링크 정합성을 점검한다.
type DeadRef struct {
	QualifiedName string // the symbol that contains the @see tag
	SeeTarget     string // the @see value that could not be resolved
}

// LintReport contains the results of a documentation lint check.
// @intent 문서 생성물과 어노테이션 품질 점검 결과를 카테고리별로 반환한다.
type LintReport struct {
	Orphans        []string        // doc files with no matching source in the graph
	Missing        []string        // source files in the graph with no doc file
	Stale          []string        // doc files older than the source's last update
	Unannotated    []string        // qualified names of symbols with no annotation
	Contradictions []Contradiction // annotated symbols whose code changed after the annotation
	DeadRefs       []DeadRef       // @see targets that do not exist in the graph
	Incomplete     []string        // annotated symbols with no @intent tag
	Drifted        []string        // annotated symbols whose node was updated after the annotation
}

// Lint checks the documentation directory against the code graph and
// returns a report of orphan, missing, and stale documentation files.
// @intent 문서 파일, 그래프 노드, 어노테이션을 교차 검증해 문서 건강 상태를 계산한다.
// @sideEffect 출력 디렉터리와 데이터베이스를 읽는다.
func (g *Generator) Lint() (*LintReport, error) {
	report := &LintReport{}

	// 1. Collect all .md doc files from the output directory.
	docFiles, err := g.lintDocFiles()
	if err != nil {
		return nil, err
	}

	// 2. Collect all source file paths from the graph (distinct file_path
	// values for non-file nodes, plus file nodes themselves).
	type fileEntry struct {
		FilePath  string
		UpdatedAt int64 // unix timestamp of the most recent node update
	}
	graphFiles := map[string]*fileEntry{}

	fileSnapshot, err := g.Repository.Snapshot(context.Background(), g.Namespace, []graph.NodeKind{graph.NodeKindFunction, graph.NodeKindClass, graph.NodeKindType, graph.NodeKindTest})
	if err != nil {
		return nil, fmt.Errorf("query nodes: %w", err)
	}
	nodes := fileSnapshot.Nodes

	for _, n := range nodes {
		if len(g.Exclude) > 0 && pathspec.MatchExcludes(g.Exclude, n.FilePath) {
			continue
		}
		ts := n.UpdatedAt.Unix()
		if e, ok := graphFiles[n.FilePath]; ok {
			if ts > e.UpdatedAt {
				e.UpdatedAt = ts
			}
		} else {
			graphFiles[n.FilePath] = &fileEntry{FilePath: n.FilePath, UpdatedAt: ts}
		}
	}

	// 3. Cross-reference.
	for srcPath := range docFiles {
		if _, ok := graphFiles[srcPath]; !ok {
			report.Orphans = append(report.Orphans, srcPath)
		}
	}

	for srcPath, entry := range graphFiles {
		docInfo, ok := docFiles[srcPath]
		if !ok {
			report.Missing = append(report.Missing, srcPath)
			continue
		}
		if docInfo.Unix() < entry.UpdatedAt {
			report.Stale = append(report.Stale, srcPath)
		}
	}

	// 4. Find unannotated symbols (functions, classes, types — skip tests).
	symbolSnapshot, err := g.Repository.Snapshot(context.Background(), g.Namespace, []graph.NodeKind{graph.NodeKindFunction, graph.NodeKindClass, graph.NodeKindType})
	if err != nil {
		return nil, fmt.Errorf("query symbol nodes: %w", err)
	}
	symbolNodes := symbolSnapshot.Nodes

	// Collect IDs to batch-query annotations.
	ids := make([]uint, 0, len(symbolNodes))
	nodeByID := map[uint]*graph.Node{}
	for i := range symbolNodes {
		n := &symbolNodes[i]
		if len(g.Exclude) > 0 && pathspec.MatchExcludes(g.Exclude, n.FilePath) {
			continue
		}
		ids = append(ids, n.ID)
		nodeByID[n.ID] = n
	}

	// 4a. Load all annotations for symbol nodes (single query with tags).
	var anns []graph.Annotation
	annotated := map[uint]*graph.Annotation{}
	for _, id := range ids {
		if annotation := symbolSnapshot.Annotations[id]; annotation != nil {
			annotated[id] = annotation
			anns = append(anns, *annotation)
		}
	}

	// 4b. Unannotated: symbol nodes with no annotation at all.
	for _, id := range ids {
		if _, ok := annotated[id]; !ok {
			report.Unannotated = append(report.Unannotated, nodeByID[id].QualifiedName)
		}
	}

	// 5. Find contradictions: annotation has @param but node was updated after annotation.
	for _, a := range anns {
		hasParam := false
		for _, tag := range a.Tags {
			if tag.Kind == graph.TagParam {
				hasParam = true
				break
			}
		}
		if !hasParam {
			continue
		}
		n := nodeByID[a.NodeID]
		if n == nil {
			continue
		}
		if n.UpdatedAt.After(a.UpdatedAt) {
			report.Contradictions = append(report.Contradictions, Contradiction{
				QualifiedName: n.QualifiedName,
				Detail:        "@param exists but node updated since annotation",
			})
		}
	}
	sort.Slice(report.Contradictions, func(i, j int) bool {
		return report.Contradictions[i].QualifiedName < report.Contradictions[j].QualifiedName
	})

	// 6. Find dead refs: @see targets that don't exist in the graph.
	for _, a := range anns {
		n := nodeByID[a.NodeID]
		if n == nil {
			continue
		}
		for _, tag := range a.Tags {
			if tag.Kind != graph.TagSee {
				continue
			}
			if reference.Is(tag.Value) {
				ref, err := reference.Parse(tag.Value)
				if err != nil {
					report.DeadRefs = append(report.DeadRefs, DeadRef{
						QualifiedName: n.QualifiedName,
						SeeTarget:     tag.Value,
					})
					continue
				}
				ok, err := g.ccgRefExists(*ref)
				if err != nil {
					return nil, fmt.Errorf("query dead ccg ref for %q → %q: %w", n.QualifiedName, tag.Value, err)
				}
				if !ok {
					report.DeadRefs = append(report.DeadRefs, DeadRef{
						QualifiedName: n.QualifiedName,
						SeeTarget:     tag.Value,
					})
				}
				continue
			}
			exists, err := g.Repository.QualifiedNameExists(context.Background(), g.Namespace, tag.Value)
			if err != nil {
				return nil, fmt.Errorf("query dead ref for %q → %q: %w", n.QualifiedName, tag.Value, err)
			}
			if !exists {
				report.DeadRefs = append(report.DeadRefs, DeadRef{
					QualifiedName: n.QualifiedName,
					SeeTarget:     tag.Value,
				})
			}
		}
	}
	sort.Slice(report.DeadRefs, func(i, j int) bool {
		return report.DeadRefs[i].QualifiedName < report.DeadRefs[j].QualifiedName
	})

	// 7. Find incomplete annotations: annotation exists but no @intent tag.
	for _, a := range anns {
		n := nodeByID[a.NodeID]
		if n == nil {
			continue
		}
		hasIntent := false
		for _, tag := range a.Tags {
			if tag.Kind == graph.TagIntent {
				hasIntent = true
				break
			}
		}
		if !hasIntent {
			report.Incomplete = append(report.Incomplete, n.QualifiedName)
		}
	}
	sort.Strings(report.Incomplete)

	// 8. Find drifted annotations: node updated after annotation.
	for _, a := range anns {
		n := nodeByID[a.NodeID]
		if n == nil {
			continue
		}
		if n.UpdatedAt.After(a.UpdatedAt) {
			report.Drifted = append(report.Drifted, n.QualifiedName)
		}
	}
	sort.Strings(report.Drifted)

	sort.Strings(report.Orphans)
	sort.Strings(report.Missing)
	sort.Strings(report.Stale)
	sort.Strings(report.Unannotated)

	return report, nil
}

// @intent collect only the Markdown files that belong to the active docs namespace.
// @domainRule named namespaces trust their scoped manifest; without one, foreign docs in the shared output dir are ignored.
func (g *Generator) lintDocFiles() (map[string]time.Time, error) {
	docFiles := map[string]time.Time{}
	if m, ok, err := g.loadLintManifest(); err != nil {
		return nil, err
	} else if ok {
		for _, rel := range m.Files {
			rel = filepath.ToSlash(rel)
			if rel == "index.md" || !strings.HasSuffix(rel, ".md") {
				continue
			}
			srcPath := strings.TrimSuffix(rel, ".md")
			if len(g.Exclude) > 0 && pathspec.MatchExcludes(g.Exclude, srcPath) {
				continue
			}
			modTime, exists, err := g.Files.ModTime(rel)
			if err != nil {
				return nil, err
			}
			if exists {
				docFiles[srcPath] = modTime
			}
		}
		return docFiles, nil
	}
	if g.Namespace != "" && requestctx.Normalize(g.Namespace) != requestctx.DefaultNamespace {
		return docFiles, nil
	}
	files, err := g.Files.MarkdownFiles()
	if err != nil {
		return nil, fmt.Errorf("walk docs dir: %w", err)
	}
	for rel, modTime := range files {
		if rel == "index.md" {
			continue
		}
		srcPath := strings.TrimSuffix(rel, ".md")
		if len(g.Exclude) > 0 && pathspec.MatchExcludes(g.Exclude, srcPath) {
			continue
		}
		docFiles[srcPath] = modTime
	}
	return docFiles, nil
}

// @intent load the active namespace manifest for lint without hiding whether it exists.
func (g *Generator) loadLintManifest() (*manifest, bool, error) {
	data, exists, err := g.Files.Read(g.manifestPath())
	if err != nil {
		return nil, false, err
	}
	if !exists {
		return nil, false, nil
	}
	m := &manifest{}
	if err := json.Unmarshal(data, m); err != nil {
		return nil, false, err
	}
	return m, true, nil
}

// ccgRefExists checks whether a parsed ccg:// @see ref resolves to graph data.
// @intent let docs lint validate cross-namespace refs while keeping local @see lookup semantics unchanged.
func (g *Generator) ccgRefExists(ref reference.Ref) (bool, error) {
	return g.Repository.CCGRefExists(context.Background(), ref)
}
