package docs

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/imtaebin/code-context-graph/internal/model"
)

// nodeGroup holds everything needed to render a single file's doc.
type nodeGroup struct {
	FilePath  string
	FileAnn   *model.Annotation
	Nodes     []model.Node
	AnnByID   map[uint]*model.Annotation
	EdgesByID map[uint][]model.Edge
}

func groupByFile(nodes []model.Node, annByID map[uint]*model.Annotation, edgesByFromID map[uint][]model.Edge) []nodeGroup {
	fileAnns := map[string]*model.Annotation{}
	fileNodeMap := map[string][]model.Node{}

	for _, n := range nodes {
		if n.Kind == model.NodeKindFile {
			fileAnns[n.FilePath] = annByID[n.ID]
		} else {
			fileNodeMap[n.FilePath] = append(fileNodeMap[n.FilePath], n)
		}
	}

	var paths []string
	for fp := range fileNodeMap {
		paths = append(paths, fp)
	}
	sort.Strings(paths)

	groups := make([]nodeGroup, 0, len(paths))
	for _, fp := range paths {
		groups = append(groups, nodeGroup{
			FilePath:  fp,
			FileAnn:   fileAnns[fp],
			Nodes:     fileNodeMap[fp],
			AnnByID:   annByID,
			EdgesByID: edgesByFromID,
		})
	}
	return groups
}

func (g *Generator) writeFileDoc(grp nodeGroup) error {
	content := renderFileDoc(grp)
	outPath := filepath.Join(g.OutDir, filepath.FromSlash(grp.FilePath+".md"))
	if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
		return err
	}
	return os.WriteFile(outPath, []byte(content), 0644)
}

func (g *Generator) writeIndex(groups []nodeGroup) error {
	content := renderIndex(groups)
	if err := os.MkdirAll(g.OutDir, 0755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(g.OutDir, "index.md"), []byte(content), 0644)
}

func renderFileDoc(grp nodeGroup) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n", grp.FilePath)

	if grp.FileAnn != nil {
		if idx := tagValue(grp.FileAnn, model.TagIndex); idx != "" {
			fmt.Fprintf(&b, "\n> %s\n", idx)
		}
	}

	kindOrder := []model.NodeKind{
		model.NodeKindFunction,
		model.NodeKindClass,
		model.NodeKindType,
		model.NodeKindTest,
	}
	kindTitle := map[model.NodeKind]string{
		model.NodeKindFunction: "Functions",
		model.NodeKindClass:    "Classes",
		model.NodeKindType:     "Types",
		model.NodeKindTest:     "Tests",
	}

	byKind := map[model.NodeKind][]model.Node{}
	for _, n := range grp.Nodes {
		byKind[n.Kind] = append(byKind[n.Kind], n)
	}

	for _, k := range kindOrder {
		ns := byKind[k]
		if len(ns) == 0 {
			continue
		}
		sort.Slice(ns, func(i, j int) bool { return ns[i].StartLine < ns[j].StartLine })
		fmt.Fprintf(&b, "\n## %s\n", kindTitle[k])
		for _, n := range ns {
			renderSymbol(&b, n, grp.AnnByID[n.ID], grp.EdgesByID[n.ID])
		}
	}

	return b.String()
}

func renderSymbol(b *strings.Builder, n model.Node, ann *model.Annotation, edges []model.Edge) {
	fmt.Fprintf(b, "\n### %s\n", n.Name)
	fmt.Fprintf(b, "- **Lines:** %d\u2013%d\n", n.StartLine, n.EndLine)

	if ann == nil {
		return
	}

	if v := tagValue(ann, model.TagIntent); v != "" {
		fmt.Fprintf(b, "- **Intent:** %s\n", v)
	}
	if rules := tagValues(ann, model.TagDomainRule); len(rules) > 0 {
		fmt.Fprintf(b, "- **Domain Rules:**\n")
		for _, r := range rules {
			fmt.Fprintf(b, "  - %s\n", r)
		}
	}
	if effects := tagValues(ann, model.TagSideEffect); len(effects) > 0 {
		fmt.Fprintf(b, "- **Side Effects:** %s\n", strings.Join(effects, "; "))
	}
	if params := tagsWithName(ann, model.TagParam); len(params) > 0 {
		fmt.Fprintf(b, "- **Params:**\n")
		for _, p := range params {
			fmt.Fprintf(b, "  - `%s` \u2014 %s\n", p.Name, p.Value)
		}
	}
	if v := tagValue(ann, model.TagReturn); v != "" {
		fmt.Fprintf(b, "- **Returns:** %s\n", v)
	}

	var callNames []string
	for _, e := range edges {
		callNames = append(callNames, e.ToNode.Name)
	}
	if len(callNames) > 0 {
		fmt.Fprintf(b, "- **Calls:** %s\n", strings.Join(callNames, ", "))
	}
}

func renderIndex(groups []nodeGroup) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Code Context Index\n\nGenerated: %s\n\n## Files\n\n", time.Now().Format("2006-01-02"))
	fmt.Fprintf(&b, "| File | Symbols | Description |\n|------|---------|-------------|\n")

	for _, grp := range groups {
		desc := "\u2014"
		if grp.FileAnn != nil {
			if v := tagValue(grp.FileAnn, model.TagIndex); v != "" {
				desc = v
			}
		}
		link := filepath.ToSlash(grp.FilePath) + ".md"
		fmt.Fprintf(&b, "| [%s](%s) | %d | %s |\n", grp.FilePath, link, len(grp.Nodes), desc)
	}

	kindOrder := []model.NodeKind{
		model.NodeKindFile,
		model.NodeKindClass,
		model.NodeKindFunction,
		model.NodeKindType,
		model.NodeKindTest,
	}

	var allNodes []model.Node
	for _, grp := range groups {
		allNodes = append(allNodes, grp.Nodes...)
	}
	sort.Slice(allNodes, func(i, j int) bool {
		ki := kindIdx(kindOrder, allNodes[i].Kind)
		kj := kindIdx(kindOrder, allNodes[j].Kind)
		if ki != kj {
			return ki < kj
		}
		return allNodes[i].Name < allNodes[j].Name
	})

	fmt.Fprintf(&b, "\n## All Symbols\n\n| Symbol | Kind | File |\n|--------|------|------|\n")
	for _, n := range allNodes {
		fmt.Fprintf(&b, "| %s | %s | %s |\n", n.Name, string(n.Kind), n.FilePath)
	}

	return b.String()
}

func kindIdx(order []model.NodeKind, k model.NodeKind) int {
	for i, o := range order {
		if o == k {
			return i
		}
	}
	return len(order)
}

func tagValue(ann *model.Annotation, kind model.TagKind) string {
	for _, t := range ann.Tags {
		if t.Kind == kind {
			return t.Value
		}
	}
	return ""
}

func tagValues(ann *model.Annotation, kind model.TagKind) []string {
	var vals []string
	for _, t := range ann.Tags {
		if t.Kind == kind {
			vals = append(vals, t.Value)
		}
	}
	return vals
}

func tagsWithName(ann *model.Annotation, kind model.TagKind) []model.DocTag {
	var tags []model.DocTag
	for _, t := range ann.Tags {
		if t.Kind == kind {
			tags = append(tags, t)
		}
	}
	return tags
}
