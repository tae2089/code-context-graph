package docs

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/pathutil"
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
	docFiles := map[string]os.FileInfo{} // source path → FileInfo of .md
	if _, err := os.Stat(g.OutDir); err == nil {
		err := filepath.Walk(g.OutDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return err
			}
			if !strings.HasSuffix(path, ".md") {
				return nil
			}

			rel, _ := filepath.Rel(g.OutDir, path)
			rel = filepath.ToSlash(rel)

			// Skip index.md — it's not a per-file doc.
			if rel == "index.md" {
				return nil
			}

			// Strip .md suffix to get the source path.
			srcPath := strings.TrimSuffix(rel, ".md")
			docFiles[srcPath] = info
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walk docs dir: %w", err)
		}
	}

	// 2. Collect all source file paths from the graph (distinct file_path
	// values for non-file nodes, plus file nodes themselves).
	type fileEntry struct {
		FilePath  string
		UpdatedAt int64 // unix timestamp of the most recent node update
	}
	graphFiles := map[string]*fileEntry{}

	var nodes []model.Node
	if err := g.DB.Select("file_path, updated_at").
		Where("kind IN ?", []string{
			string(model.NodeKindFunction),
			string(model.NodeKindClass),
			string(model.NodeKindType),
			string(model.NodeKindTest),
		}).Find(&nodes).Error; err != nil {
		return nil, fmt.Errorf("query nodes: %w", err)
	}

	for _, n := range nodes {
		if len(g.Exclude) > 0 && pathutil.MatchExcludes(g.Exclude, n.FilePath) {
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
		if docInfo.ModTime().Unix() < entry.UpdatedAt {
			report.Stale = append(report.Stale, srcPath)
		}
	}

	// 4. Find unannotated symbols (functions, classes, types — skip tests).
	var symbolNodes []model.Node
	if err := g.DB.Select("id, qualified_name, kind, file_path, updated_at").
		Where("kind IN ?", []string{
			string(model.NodeKindFunction),
			string(model.NodeKindClass),
			string(model.NodeKindType),
		}).Find(&symbolNodes).Error; err != nil {
		return nil, fmt.Errorf("query symbol nodes: %w", err)
	}

	// Collect IDs to batch-query annotations.
	ids := make([]uint, 0, len(symbolNodes))
	nodeByID := map[uint]*model.Node{}
	for i := range symbolNodes {
		n := &symbolNodes[i]
		if len(g.Exclude) > 0 && pathutil.MatchExcludes(g.Exclude, n.FilePath) {
			continue
		}
		ids = append(ids, n.ID)
		nodeByID[n.ID] = n
	}

	// 4a. Load all annotations for symbol nodes (single query with tags).
	var anns []model.Annotation
	annotated := map[uint]*model.Annotation{}
	if len(ids) > 0 {
		if err := g.DB.Where("node_id IN ?", ids).Preload("Tags").Find(&anns).Error; err != nil {
			return nil, fmt.Errorf("query annotations: %w", err)
		}
		for i := range anns {
			annotated[anns[i].NodeID] = &anns[i]
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
			if tag.Kind == model.TagParam {
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
			if tag.Kind != model.TagSee {
				continue
			}
			var count int64
			if err := g.DB.Model(&model.Node{}).Where("qualified_name = ?", tag.Value).Count(&count).Error; err != nil {
				return nil, fmt.Errorf("query dead ref for %q → %q: %w", n.QualifiedName, tag.Value, err)
			}
			if count == 0 {
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
			if tag.Kind == model.TagIntent {
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
