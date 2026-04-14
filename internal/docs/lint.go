package docs

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/imtaebin/code-context-graph/internal/model"
	"github.com/imtaebin/code-context-graph/internal/pathutil"
)

// Contradiction represents a symbol whose annotation is outdated relative to
// the code: the node was modified after the annotation was last written, and
// the annotation contains detail tags (e.g. @param) that may no longer be
// accurate.
type Contradiction struct {
	QualifiedName string
	Detail        string
}

// LintReport contains the results of a documentation lint check.
type LintReport struct {
	Orphans        []string       // doc files with no matching source in the graph
	Missing        []string       // source files in the graph with no doc file
	Stale          []string       // doc files older than the source's last update
	Unannotated    []string       // qualified names of symbols with no annotation
	Contradictions []Contradiction // annotated symbols whose code changed after the annotation
}

// Lint checks the documentation directory against the code graph and
// returns a report of orphan, missing, and stale documentation files.
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
	if err := g.DB.Select("id, qualified_name, kind, file_path").
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

	if len(ids) > 0 {
		var annotations []model.Annotation
		g.DB.Select("node_id").Where("node_id IN ?", ids).Find(&annotations)
		annotated := map[uint]bool{}
		for _, a := range annotations {
			annotated[a.NodeID] = true
		}
		for _, id := range ids {
			if !annotated[id] {
				report.Unannotated = append(report.Unannotated, nodeByID[id].QualifiedName)
			}
		}
	}

	// 5. Find contradictions: annotation has @param but node was updated after annotation.
	if len(ids) > 0 {
		var anns []model.Annotation
		if err := g.DB.Where("node_id IN ?", ids).Preload("Tags").Find(&anns).Error; err != nil {
			return nil, fmt.Errorf("query annotations for contradiction: %w", err)
		}
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
			var freshNode model.Node
			if err := g.DB.Select("updated_at").First(&freshNode, n.ID).Error; err != nil {
				continue
			}
			if freshNode.UpdatedAt.After(a.UpdatedAt) {
				report.Contradictions = append(report.Contradictions, Contradiction{
					QualifiedName: n.QualifiedName,
					Detail:        "@param exists but node updated since annotation",
				})
			}
		}
		sort.Slice(report.Contradictions, func(i, j int) bool {
			return report.Contradictions[i].QualifiedName < report.Contradictions[j].QualifiedName
		})
	}

	sort.Strings(report.Orphans)
	sort.Strings(report.Missing)
	sort.Strings(report.Stale)
	sort.Strings(report.Unannotated)

	return report, nil
}
