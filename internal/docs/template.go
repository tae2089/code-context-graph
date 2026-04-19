package docs

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tae2089/code-context-graph/internal/model"
)

// nodeGroup holds everything needed to render a single file's doc.
// @intent 한 소스 파일의 문서 렌더링에 필요한 노드·어노테이션·엣지를 묶는다.
type nodeGroup struct {
	FilePath  string
	FileAnn   *model.Annotation
	Nodes     []model.Node
	AnnByID   map[uint]*model.Annotation
	EdgesByID map[uint][]model.Edge
}

// groupByFile groups graph nodes into per-file render units.
// @intent 문서 렌더러가 파일 단위로 반복할 수 있게 입력 데이터를 재구성한다.
// @return 파일 경로 기준으로 정렬된 nodeGroup 목록을 반환한다.
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

// writeFileDoc writes one rendered markdown document.
// @intent 단일 소스 파일 문서를 실제 산출물로 저장한다.
// @sideEffect 출력 디렉터리를 만들고 해당 .md 파일을 기록한다.
func (g *Generator) writeFileDoc(grp nodeGroup) error {
	content := renderFileDoc(grp)
	outPath := filepath.Join(g.OutDir, filepath.FromSlash(grp.FilePath+".md"))
	if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}
	return os.WriteFile(outPath, []byte(content), 0644)
}

// writeIndex writes the aggregated markdown index.
// @intent 전체 파일 문서에 대한 탐색용 index.md를 저장한다.
// @sideEffect 출력 디렉터리를 만들고 index.md를 기록한다.
func (g *Generator) writeIndex(groups []nodeGroup) error {
	content := renderIndex(groups)
	if err := os.MkdirAll(g.OutDir, 0755); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}
	return os.WriteFile(filepath.Join(g.OutDir, "index.md"), []byte(content), 0644)
}

// renderFileDoc renders one file documentation page as markdown.
// @intent 파일 수준 어노테이션과 심볼 정보를 사람이 읽는 Markdown으로 직렬화한다.
// @return 파일 문서의 전체 Markdown 문자열을 반환한다.
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

// renderSymbol renders a single symbol section into the builder.
// @intent 심볼의 메타데이터와 태그를 문서 섹션으로 풀어쓴다.
// @param edges 호출 및 import 관계 표시에 사용할 엣지 목록이다.
func renderSymbol(b *strings.Builder, n model.Node, ann *model.Annotation, edges []model.Edge) {
	fmt.Fprintf(b, "\n### %s\n", n.Name)
	fmt.Fprintf(b, "- **Lines:** %d\u2013%d\n", n.StartLine, n.EndLine)

	if ann == nil {
		return
	}

	if ann.Summary != "" {
		fmt.Fprintf(b, "\n%s\n", ann.Summary)
	}
	if ann.Context != "" {
		// Summary가 없으면 Lines 뒤에 빈 줄이 필요; Summary가 있으면 이미 줄바꿈 있음
		if ann.Summary == "" {
			fmt.Fprintln(b)
		}
		for _, l := range strings.Split(ann.Context, "\n") {
			fmt.Fprintf(b, "> %s\n", l)
		}
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
	if mutates := tagValues(ann, model.TagMutates); len(mutates) > 0 {
		fmt.Fprintf(b, "- **Mutates:** %s\n", strings.Join(mutates, "; "))
	}
	if reqs := tagValues(ann, model.TagRequires); len(reqs) > 0 {
		fmt.Fprintf(b, "- **Requires:**\n")
		for _, r := range reqs {
			fmt.Fprintf(b, "  - %s\n", r)
		}
	}
	if ensures := tagValues(ann, model.TagEnsures); len(ensures) > 0 {
		fmt.Fprintf(b, "- **Ensures:**\n")
		for _, e := range ensures {
			fmt.Fprintf(b, "  - %s\n", e)
		}
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
	if sees := tagValues(ann, model.TagSee); len(sees) > 0 {
		fmt.Fprintf(b, "- **See:** %s\n", strings.Join(sees, ", "))
	}

	var callNames []string
	for _, e := range edges {
		callNames = append(callNames, e.ToNode.Name)
	}
	if len(callNames) > 0 {
		fmt.Fprintf(b, "- **Calls:** %s\n", strings.Join(callNames, ", "))
	}
}

// renderIndex renders the top-level documentation index.
// @intent 생성된 모든 파일 문서와 심볼에 대한 탐색용 표를 만든다.
// @return index.md에 기록할 Markdown 문자열을 반환한다.
func renderIndex(groups []nodeGroup) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Code Context Index\n\nGenerated: %s\n\n## Files\n\n", time.Now().Format("2006-01-02 15:04"))
	fmt.Fprintf(&b, "| File | Symbols | Description |\n|------|---------|-------------|\n")

	for _, grp := range groups {
		desc := "\u2014"
		if grp.FileAnn != nil {
			if v := tagValue(grp.FileAnn, model.TagIndex); v != "" {
				desc = v
			}
		}
		slashPath := filepath.ToSlash(grp.FilePath)
		link := slashPath + ".md"
		fmt.Fprintf(&b, "| [%s](%s) | %d | %s |\n", slashPath, link, len(grp.Nodes), desc)
	}

	kindOrder := []model.NodeKind{
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
		slashPath := filepath.ToSlash(n.FilePath)
		anchor := markdownAnchor(n.Name)
		link := fmt.Sprintf("[%s](%s.md#%s)", n.Name, slashPath, anchor)
		fmt.Fprintf(&b, "| %s | %s | %s |\n", link, string(n.Kind), slashPath)
	}

	return b.String()
}

// markdownAnchor converts a symbol name to a GitHub-flavored Markdown anchor:
// lowercase, spaces to hyphens, non-alphanumeric/hyphen/underscore stripped.
// @intent 심볼 이름을 GitHub Markdown 헤더 링크 형식으로 정규화한다.
func markdownAnchor(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('-')
		}
	}
	return b.String()
}

// kindIdx returns the display order index for a node kind.
// @intent 심볼 표를 미리 정의한 kind 순서대로 정렬할 기준값을 준다.
func kindIdx(order []model.NodeKind, k model.NodeKind) int {
	for i, o := range order {
		if o == k {
			return i
		}
	}
	return len(order)
}

// tagValue returns the first tag value for a kind.
// @intent 단일값 태그를 렌더링할 때 첫 번째 항목만 간단히 조회한다.
func tagValue(ann *model.Annotation, kind model.TagKind) string {
	for _, t := range ann.Tags {
		if t.Kind == kind {
			return t.Value
		}
	}
	return ""
}

// tagValues returns all tag values for a kind.
// @intent 다중 허용 태그를 순서대로 렌더링할 수 있게 값을 모은다.
func tagValues(ann *model.Annotation, kind model.TagKind) []string {
	var vals []string
	for _, t := range ann.Tags {
		if t.Kind == kind {
			vals = append(vals, t.Value)
		}
	}
	return vals
}

// tagsWithName returns named tags of a specific kind.
// @intent @param 같이 이름과 값을 함께 출력해야 하는 태그를 보존해 전달한다.
func tagsWithName(ann *model.Annotation, kind model.TagKind) []model.DocTag {
	var tags []model.DocTag
	for _, t := range ann.Tags {
		if t.Kind == kind {
			tags = append(tags, t)
		}
	}
	return tags
}
