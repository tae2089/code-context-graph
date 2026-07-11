// @index ragindex 패키지는 DB의 커뮤니티/어노테이션 데이터를 읽어 doc-index.json을 빌드한다.
package ragindex

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/ccgref"
	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
)

// TreeNode는 doc-index.json의 단일 노드이다.
// @intent RAG 탐색 트리에서 커뮤니티, 파일, 심볼 노드를 동일 구조로 표현한다.
type TreeNode struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Kind        string `json:"kind"`
	Summary     string `json:"summary"`
	DocPath     string `json:"doc_path,omitempty"` // file 노드만 설정
	SearchText  string `json:"search_text,omitempty"`
	HasChildren bool   `json:"has_children,omitempty"`
	// @intent expose annotation-derived text bucketed by retrieval field so Retrieve scoring weights @intent/@domainRule/etc. independently of flat SearchText recall.
	FieldTexts map[string]string `json:"field_texts,omitempty"`
	Details    *NodeDetails      `json:"details,omitempty"`
	Children   []*TreeNode       `json:"children"`
}

// NodeDetails carries browser-facing metadata for a graph node in the Wiki tree.
// @intent let presentation indexes expose symbol annotations without requiring a generated file doc.
type NodeDetails struct {
	QualifiedName string            `json:"qualified_name"`
	FilePath      string            `json:"file_path"`
	StartLine     int               `json:"start_line"`
	EndLine       int               `json:"end_line"`
	Language      string            `json:"language"`
	Annotation    *AnnotationDetail `json:"annotation,omitempty"`
}

// AnnotationDetail preserves structured annotation data for Wiki symbol detail views.
// @intent serialize annotation summary, context, and tags in a UI-friendly shape.
type AnnotationDetail struct {
	Summary string         `json:"summary"`
	Context string         `json:"context"`
	Tags    []DocTagDetail `json:"tags"`
}

// DocTagDetail describes one annotation tag in a JSON-safe index payload.
// @intent keep tag kind, type, name, and ordering available to browser renderers.
type DocTagDetail struct {
	Kind    model.TagKind `json:"kind"`
	Type    string        `json:"type"`
	Name    string        `json:"name"`
	Value   string        `json:"value"`
	Ordinal int           `json:"ordinal"`
	Ref     *ccgref.Ref   `json:"ref,omitempty"`
}

// DocTagDetailFromModel converts a stored annotation tag into an index-safe DTO.
// @intent attach parsed ccg:// metadata to @see tags without changing stored annotation rows.
func DocTagDetailFromModel(tag model.DocTag) DocTagDetail {
	detail := DocTagDetail{
		Kind:    tag.Kind,
		Type:    tag.Type,
		Name:    tag.Name,
		Value:   tag.Value,
		Ordinal: tag.Ordinal,
	}
	if tag.Kind == model.TagSee && ccgref.Is(tag.Value) {
		if ref, err := ccgref.Parse(tag.Value); err == nil {
			detail.Ref = ref
		}
	}
	return detail
}

// Index는 .ccg/doc-index.json 전체 포맷이다.
// @intent 디스크에 저장되는 문서 인덱스 루트 페이로드를 정의한다.
type Index struct {
	Version int       `json:"version"`
	BuiltAt time.Time `json:"built_at"`
	Root    *TreeNode `json:"root"`
}

// Builder는 DB에서 인덱스를 빌드하는 구조체이다.
// @intent 그래프 DB와 문서 출력 경로를 연결해 doc-index.json 생성을 조율한다.
type Builder struct {
	DB          *gorm.DB
	OutDir      string // docs 디렉토리 (기본: "docs")
	IndexDir    string // .ccg 디렉토리 (기본: ".ccg")
	ProjectDesc string // root 노드 summary (기본: "")
}

// indexDir는 IndexDir 필드의 기본값을 반환한다.
// @intent 인덱스 출력 디렉터리가 비어 있을 때 기본 경로를 일관되게 제공한다.
func (b *Builder) indexDir() string {
	if b.IndexDir == "" {
		return ".ccg"
	}
	return b.IndexDir
}

// nodeInfo는 Builder 내부에서 노드의 파일 정보를 담는 구조체이다.
// @intent 커뮤니티 멤버를 파일 및 심볼 메타데이터로 역조회할 때 필요한 최소 정보를 담는다.
type nodeInfo struct {
	FilePath      string
	Name          string
	QualifiedName string
	Kind          model.NodeKind
}

// BuildTree는 DB에서 커뮤니티와 멤버 노드를 읽어 파일 쓰기 없이 RAG 탐색 트리를 합성한다.
// 반환값: (루트 노드, 커뮤니티 수, 파일 수, 에러)
// @intent 커뮤니티 구조와 문서 요약을 TreeNode JSON shape으로 합성해 파일 인덱스와 DB fallback이 같은 트리 의미를 공유한다.
// @ensures 성공 시 반환된 파일 수는 트리에 포함된 고유 파일 수와 같다.
func (b *Builder) BuildTree(ctx context.Context) (*TreeNode, int, int, error) {
	slog.Debug("ragindex.Builder.BuildTree 시작", "outDir", b.OutDir, "indexDir", b.IndexDir)
	if b.DB == nil {
		return nil, 0, 0, trace.New("DB not configured")
	}

	ns := ctxns.FromContext(ctx)

	// 1. 모든 커뮤니티와 멤버 로드
	var communities []model.Community
	q := b.DB.WithContext(ctx).Preload("Members").Where("namespace = ?", ns).Order("key ASC, id ASC")
	if err := q.Find(&communities).Error; err != nil {
		return nil, 0, 0, trace.Wrap(err, "load communities")
	}
	slog.Debug("커뮤니티 로드 완료", "count", len(communities))

	// 2. 1-pass: 모든 커뮤니티의 고유 node ID 수집
	allNodeIDs := make([]uint, 0)
	for _, comm := range communities {
		for _, m := range comm.Members {
			allNodeIDs = append(allNodeIDs, m.NodeID)
		}
	}

	// 노드 ID → nodeInfo 매핑
	nodeInfoMap := make(map[uint]nodeInfo)
	if len(allNodeIDs) > 0 {
		var nodes []model.Node
		nq := b.DB.WithContext(ctx).Where("id IN ?", allNodeIDs).Where("namespace = ?", ns)
		if err := nq.Find(&nodes).Error; err != nil {
			return nil, 0, 0, trace.Wrap(err, "load all nodes")
		}
		for _, n := range nodes {
			nodeInfoMap[n.ID] = nodeInfo{
				FilePath:      n.FilePath,
				Name:          n.Name,
				QualifiedName: n.QualifiedName,
				Kind:          n.Kind,
			}
		}
	}

	// 고유 파일 경로 목록 수집
	filePathSet := make(map[string]struct{})
	for _, info := range nodeInfoMap {
		if info.Kind == model.NodeKindPackage {
			continue
		}
		filePathSet[info.FilePath] = struct{}{}
	}
	allFilePaths := make([]string, 0, len(filePathSet))
	for fp := range filePathSet {
		allFilePaths = append(allFilePaths, fp)
	}
	sort.Strings(allFilePaths)

	// 3. 배치 fileSummary 조회
	summaries, err := b.batchFileSummaries(ctx, allFilePaths)
	if err != nil {
		return nil, 0, 0, trace.Wrap(err, "batchFileSummaries")
	}
	fileSearchTexts, fileFieldTexts, err := b.batchFileSearchTexts(ctx, allFilePaths)
	if err != nil {
		return nil, 0, 0, trace.Wrap(err, "batchFileSearchTexts")
	}

	// 4. annotation text를 가진 symbol 노드 배치 조회
	symbolsByFile, err := b.batchSymbolNodes(ctx, allNodeIDs)
	if err != nil {
		return nil, 0, 0, trace.Wrap(err, "batchSymbolNodes")
	}

	root := &TreeNode{
		ID:       "root",
		Label:    "Root",
		Kind:     "root",
		Summary:  b.ProjectDesc,
		Children: []*TreeNode{},
	}

	uniqueFiles := make(map[string]struct{})

	// 5. 2-pass: 커뮤니티별 TreeNode 구성
	for _, comm := range communities {
		slog.Debug("커뮤니티 처리 중", "key", comm.Key, "members", len(comm.Members))

		commNode := &TreeNode{
			ID:       fmt.Sprintf("community:%s", comm.Key),
			Label:    comm.Label,
			Kind:     "community",
			Summary:  comm.Description,
			Children: []*TreeNode{},
		}

		if len(comm.Members) > 0 {
			// 이 커뮤니티의 고유 파일 경로 수집
			commFilePathSet := make(map[string]struct{})
			for _, m := range comm.Members {
				if info, ok := nodeInfoMap[m.NodeID]; ok {
					if info.Kind == model.NodeKindPackage {
						continue
					}
					commFilePathSet[info.FilePath] = struct{}{}
				}
			}
			slog.Debug("파일 경로 그룹 완료", "community", comm.Key, "files", len(commFilePathSet))

			// 각 파일 경로별 TreeNode 생성
			commFilePaths := make([]string, 0, len(commFilePathSet))
			for filePath := range commFilePathSet {
				commFilePaths = append(commFilePaths, filePath)
			}
			sort.Strings(commFilePaths)

			for _, filePath := range commFilePaths {
				summary := summaries[filePath]
				fileNode := &TreeNode{
					ID:         fmt.Sprintf("file:%s", filePath),
					Label:      filepath.Base(filePath),
					Kind:       "file",
					Summary:    summary,
					DocPath:    b.docPath(filePath),
					SearchText: fileSearchTexts[filePath],
					FieldTexts: fileFieldTexts[filePath],
					Children:   symbolsByFile[filePath],
				}
				uniqueFiles[filePath] = struct{}{}
				commNode.Children = append(commNode.Children, fileNode)
			}
		}

		root.Children = append(root.Children, commNode)
	}

	slog.Debug("ragindex.Builder.BuildTree 완료", "communities", len(communities), "files", len(uniqueFiles))
	return root, len(communities), len(uniqueFiles), nil
}

// Build는 DB에서 커뮤니티와 멤버 노드를 읽어 doc-index.json을 생성한다.
// 반환값: (커뮤니티 수, 파일 수, 에러)
// @intent 커뮤니티 구조와 문서 요약을 트리 형태 인덱스로 합성한다.
// @sideEffect 데이터베이스를 읽고 doc-index.json 파일을 기록한다.
// @ensures 성공 시 반환된 파일 수는 인덱스에 포함된 고유 파일 수와 같다.
func (b *Builder) Build(ctx context.Context) (int, int, error) {
	slog.Debug("ragindex.Builder.Build 시작", "outDir", b.OutDir, "indexDir", b.IndexDir)

	root, communities, files, err := b.BuildTree(ctx)
	if err != nil {
		return 0, 0, err
	}

	// Index 구조체 구성
	idx := &Index{
		Version: 1,
		BuiltAt: time.Now().UTC(),
		Root:    root,
	}

	// doc-index.json 파일 기록 (원자적 쓰기)
	if err := b.writeIndex(idx); err != nil {
		return 0, 0, trace.Wrap(err, "writeIndex")
	}

	slog.Debug("ragindex.Builder.Build 완료", "communities", communities, "files", files)
	return communities, files, nil
}

// batchFileSummaries는 주어진 파일 경로 목록에 대해 filePath → summary 맵을 한 번에 반환한다.
// @index 태그를 우선하고, 없으면 @intent 태그로 폴백한다. 둘 다 없으면 빈 문자열이다.
// @intent 파일 문서 노드에 붙일 요약문을 태그 우선순위대로 조회한다.
// @domainRule 파일 요약은 @index를 우선하고 없을 때만 @intent로 대체한다.
func (b *Builder) batchFileSummaries(ctx context.Context, filePaths []string) (map[string]string, error) {
	result := make(map[string]string, len(filePaths))
	if len(filePaths) == 0 {
		return result, nil
	}

	ns := ctxns.FromContext(ctx)

	// @intent 파일별 첫 번째 index 또는 intent 태그 값을 담는 배치 조회 결과다.
	type row struct {
		FilePath string
		Value    string
	}

	// @index 태그를 파일 경로별로 일괄 조회
	var indexRows []row
	iq := b.DB.WithContext(ctx).Table("doc_tags").
		Select("nodes.file_path, doc_tags.value").
		Joins("JOIN annotations ON annotations.id = doc_tags.annotation_id").
		Joins("JOIN nodes ON nodes.id = annotations.node_id").
		Where("nodes.file_path IN ? AND doc_tags.kind = ?", filePaths, string(model.TagIndex)).
		Where("nodes.namespace = ?", ns)
	if err := iq.Order("doc_tags.ordinal ASC, doc_tags.id ASC").
		Scan(&indexRows).Error; err != nil {
		return nil, trace.Wrap(err, "batch index tags")
	}
	for _, r := range indexRows {
		if _, exists := result[r.FilePath]; !exists {
			result[r.FilePath] = r.Value
		}
	}

	// @index 태그가 없는 파일 경로만 @intent로 폴백 조회
	missing := make([]string, 0)
	for _, fp := range filePaths {
		if result[fp] == "" {
			missing = append(missing, fp)
		}
	}
	if len(missing) > 0 {
		var intentRows []row
		fq := b.DB.WithContext(ctx).Table("doc_tags").
			Select("nodes.file_path, doc_tags.value").
			Joins("JOIN annotations ON annotations.id = doc_tags.annotation_id").
			Joins("JOIN nodes ON nodes.id = annotations.node_id").
			Where("nodes.file_path IN ? AND doc_tags.kind = ?", missing, string(model.TagIntent)).
			Where("nodes.namespace = ?", ns)
		if err := fq.Order("doc_tags.ordinal ASC, doc_tags.id ASC").
			Scan(&intentRows).Error; err != nil {
			return nil, trace.Wrap(err, "batch intent tags")
		}
		for _, r := range intentRows {
			if result[r.FilePath] == "" {
				result[r.FilePath] = r.Value
			}
		}
	}

	return result, nil
}

// batchFileSearchTexts returns annotation-derived search text for file nodes.
// @intent keep file-level retrieval searchable by structured file annotations without exposing the text in tree responses, and supply per-field bucketed text for Retrieve scoring.
func (b *Builder) batchFileSearchTexts(ctx context.Context, filePaths []string) (map[string]string, map[string]map[string]string, error) {
	result := make(map[string]string, len(filePaths))
	fields := make(map[string]map[string]string, len(filePaths))
	if len(filePaths) == 0 {
		return result, fields, nil
	}
	ns := ctxns.FromContext(ctx)
	var nodes []model.Node
	q := b.DB.WithContext(ctx).
		Where("namespace = ? AND kind = ? AND file_path IN ?", ns, model.NodeKindFile, filePaths).
		Preload("Annotation.Tags")
	if err := q.Find(&nodes).Error; err != nil {
		return nil, nil, trace.Wrap(err, "batch file search text")
	}
	for i := range nodes {
		text := SearchTextForAnnotation(nodes[i].Annotation)
		if text != "" && result[nodes[i].FilePath] == "" {
			result[nodes[i].FilePath] = text
		}
		if _, exists := fields[nodes[i].FilePath]; !exists {
			if ft := fieldTextsForAnnotation(nodes[i].Annotation); ft != nil {
				fields[nodes[i].FilePath] = ft
			}
		}
	}
	return result, fields, nil
}

// batchSymbolNodes returns annotated symbol nodes grouped by file path.
// @intent attach symbol subtrees with full annotation search text so RAG retrieval is not limited to @intent summaries.
// @domainRule symbols without annotation text are omitted to keep the PageIndex tree compact.
func (b *Builder) batchSymbolNodes(ctx context.Context, nodeIDs []uint) (map[string][]*TreeNode, error) {
	result := make(map[string][]*TreeNode)
	if len(nodeIDs) == 0 {
		return result, nil
	}

	ns := ctxns.FromContext(ctx)
	var nodes []model.Node
	sq := b.DB.WithContext(ctx).
		Where("id IN ?", nodeIDs).
		Where("namespace = ?", ns).
		Where("kind IN ?", []model.NodeKind{model.NodeKindFunction, model.NodeKindClass, model.NodeKindType, model.NodeKindTest}).
		Preload("Annotation.Tags").
		Order("file_path ASC, start_line ASC, qualified_name ASC")
	if err := sq.Find(&nodes).Error; err != nil {
		return nil, trace.Wrap(err, "batch symbol nodes")
	}

	for i := range nodes {
		summary := summaryForAnnotation(nodes[i].Annotation)
		searchText := SearchTextForAnnotation(nodes[i].Annotation)
		fieldTexts := fieldTextsForAnnotation(nodes[i].Annotation)
		if summary == "" && searchText == "" {
			continue
		}
		if fieldTexts == nil {
			fieldTexts = map[string]string{}
		}
		if qn := strings.TrimSpace(nodes[i].QualifiedName); qn != "" {
			fieldTexts[fieldQualifiedName] = strings.ToLower(qn)
		}
		if len(fieldTexts) == 0 {
			fieldTexts = nil
		}
		symNode := &TreeNode{
			ID:         fmt.Sprintf("symbol:%s", nodes[i].QualifiedName),
			Label:      nodes[i].Name,
			Kind:       "symbol",
			Summary:    summary,
			SearchText: searchText,
			FieldTexts: fieldTexts,
			Children:   []*TreeNode{},
		}
		result[nodes[i].FilePath] = append(result[nodes[i].FilePath], symNode)
	}

	return result, nil
}

// summaryForAnnotation chooses compact display text while search text keeps all annotation tags.
// @intent keep tree labels concise without discarding non-intent tags from retrieval scoring.
func summaryForAnnotation(annotation *model.Annotation) string {
	if annotation == nil {
		return ""
	}
	for _, tag := range annotation.Tags {
		if tag.Kind == model.TagIndex {
			return strings.TrimSpace(tag.Value)
		}
	}
	for _, tag := range annotation.Tags {
		if tag.Kind == model.TagIntent {
			return strings.TrimSpace(tag.Value)
		}
	}
	return strings.TrimSpace(annotation.Summary)
}

// SearchTextForAnnotation assembles non-displayed text used by docs search and retrieval.
// @intent include annotation summary, context, tag kinds, names, types, and values without indexing source or generic node metadata.
func SearchTextForAnnotation(annotation *model.Annotation) string {
	var parts []string
	add := func(values ...string) {
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value != "" {
				parts = append(parts, value)
			}
		}
	}
	if annotation != nil {
		add(annotation.Summary, annotation.Context)
		for _, tag := range annotation.Tags {
			add(string(tag.Kind), tag.Type, tag.Name, tag.Value)
		}
	}
	return strings.Join(parts, " ")
}

// @intent bucket annotation tag values into structured retrieval fields so Retrieve can weight @intent / @domainRule / @sideEffect / @mutates / @requires / @ensures / @see independently from generic hidden text, while unbucketed tags (param/return/throws/typedef) stay in the generic fallback to preserve recall.
func fieldTextsForAnnotation(annotation *model.Annotation) map[string]string {
	if annotation == nil {
		return nil
	}
	buckets := map[string][]string{}
	push := func(field, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		buckets[field] = append(buckets[field], value)
	}
	if s := strings.TrimSpace(annotation.Summary); s != "" {
		push(fieldAnnotationText, s)
	}
	if c := strings.TrimSpace(annotation.Context); c != "" {
		push(fieldAnnotationText, c)
	}
	for _, tag := range annotation.Tags {
		val := strings.TrimSpace(tag.Value)
		switch tag.Kind {
		case model.TagIntent:
			push(fieldIntent, val)
		case model.TagIndex:
			push(fieldIndexSummary, val)
		case model.TagDomainRule:
			push(fieldDomainRule, val)
		case model.TagRequires:
			push(fieldRequires, val)
		case model.TagEnsures:
			push(fieldEnsures, val)
		case model.TagSideEffect:
			push(fieldSideEffect, val)
		case model.TagMutates:
			push(fieldMutates, val)
		case model.TagSee:
			push(fieldSee, val)
		default:
			push(fieldGenericHidden, strings.TrimSpace(string(tag.Kind)+" "+tag.Type+" "+tag.Name+" "+val))
		}
	}
	if len(buckets) == 0 {
		return nil
	}
	out := make(map[string]string, len(buckets))
	for k, vs := range buckets {
		out[k] = strings.ToLower(strings.Join(vs, " "))
	}
	return out
}

// docPath는 파일 경로를 기반으로 docs 디렉토리 내의 문서 경로를 반환한다.
// 전체 상대 경로 구조를 유지하여 basename 충돌을 방지한다.
// 예: "internal/mcp/handlers.go" → "docs/internal/mcp/handlers.go.md"
// @intent 인덱스 노드가 실제 Markdown 문서를 안정적으로 가리키는 경로를 계산한다.
// @domainRule 원본 상대 경로 구조를 유지해 동명 파일 충돌을 피한다.
func (b *Builder) docPath(filePath string) string {
	outDir := b.OutDir
	if outDir == "" {
		outDir = "docs"
	}
	rel := filePath
	if filepath.IsAbs(filePath) {
		rel = strings.TrimPrefix(filePath, "/")
	}
	return filepath.Join(outDir, rel+".md")
}

// writeIndex는 IndexDir/doc-index.json 파일에 Index를 JSON으로 원자적으로 기록한다.
// 임시 파일에 먼저 쓴 후 rename하여 중간에 프로세스가 중단되어도 파일이 손상되지 않는다.
// @intent RAG 인덱스를 중간 손상 없이 안전하게 디스크에 저장한다.
// @sideEffect 인덱스 디렉터리를 만들고 임시 파일 작성 후 최종 파일로 rename한다.
func (b *Builder) writeIndex(idx *Index) error {
	dir := b.indexDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return trace.Wrap(err, "create index dir")
	}

	target := filepath.Join(dir, "doc-index.json")
	f, err := os.CreateTemp(dir, "doc-index-*.tmp")
	if err != nil {
		return trace.Wrap(err, "create temp file")
	}
	tmp := f.Name()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(idx); err != nil {
		f.Close()
		os.Remove(tmp)
		return trace.Wrap(err, "encode index")
	}

	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return trace.Wrap(err, "sync temp file")
	}

	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return trace.Wrap(err, "close temp file")
	}

	if err := os.Rename(tmp, target); err != nil {
		os.Remove(tmp)
		return trace.Wrap(err, "rename to doc-index.json")
	}

	slog.Debug("doc-index.json 기록 완료", "path", target)
	return nil
}

// LoadIndex는 주어진 경로에서 doc-index.json을 읽어 Index를 반환한다.
// @intent 저장된 RAG 인덱스를 도구나 서버가 다시 로드할 수 있게 한다.
// @sideEffect 대상 JSON 파일을 읽는다.
func LoadIndex(path string) (*Index, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, trace.Wrap(err, "LoadIndex open "+path)
	}
	defer f.Close()

	var idx Index
	if err := json.NewDecoder(f).Decode(&idx); err != nil {
		return nil, trace.Wrap(err, "LoadIndex decode")
	}
	return &idx, nil
}

// SearchResult는 Search 함수가 반환하는 단일 매칭 결과이다.
// @intent 검색 UI나 MCP 응답에서 표시할 최소 결과 정보를 담는다.
type SearchResult struct {
	ID      string       `json:"id"`
	Label   string       `json:"label"`
	Kind    string       `json:"kind"`
	Summary string       `json:"summary"`
	DocPath string       `json:"doc_path,omitempty"`
	Details *NodeDetails `json:"details,omitempty"`
	Path    []string     `json:"path"` // root부터 해당 노드까지의 Label 경로
}

// RetrieveResult represents one document candidate selected from tree-aware query matching.
// @intent return file-level RAG retrieval candidates with the matched tree evidence that caused the hit, including which annotation buckets contributed. Optional Phase 2 diagnostics (ExpandedTerms, FieldScores, LiteralScore, ExpansionScore) are emitted only when RetrieveOptions.Explain is true and stay omitempty so default responses keep the shape backward-compatible.
type RetrieveResult struct {
	ID             string         `json:"id"`
	Label          string         `json:"label"`
	Kind           string         `json:"kind"`
	Summary        string         `json:"summary"`
	DocPath        string         `json:"doc_path"`
	Path           []string       `json:"path"`
	Score          int            `json:"score"`
	MatchedTerms   []string       `json:"matched_terms"`
	MatchedFields  []string       `json:"matched_fields"`
	Matches        []SearchResult `json:"matches,omitempty"`
	ExpandedTerms  []string       `json:"expanded_terms,omitempty"`
	FieldScores    map[string]int `json:"field_scores,omitempty"`
	LiteralScore   int            `json:"literal_score,omitempty"`
	ExpansionScore int            `json:"expansion_score,omitempty"`

	wholeWordHits int `json:"-"`
}

// @intent canonical retrieval field names used both for FieldTexts bucket keys and matched_fields evidence so downstream consumers see a stable vocabulary.
const (
	fieldLabel          = "label"
	fieldLabelContains  = "label_contains"
	fieldQualifiedName  = "qualified_name"
	fieldIntent         = "intent"
	fieldIndexSummary   = "index_summary"
	fieldDomainRule     = "domainRule"
	fieldRequires       = "requires"
	fieldEnsures        = "ensures"
	fieldSideEffect     = "sideEffect"
	fieldMutates        = "mutates"
	fieldSee            = "see"
	fieldAnnotationText = "annotation_text"
	fieldGenericHidden  = "generic"
)

// Search는 root 트리를 DFS로 순회하며 query를 label, summary, search_text에서
// case-insensitive 검색하여 최대 maxResults개의 결과를 반환한다.
// root 노드 자체는 결과에 포함하지 않는다.
// @intent 문서 인덱스 트리에서 제목, 요약, 구조화 annotation 기반 키워드 탐색을 제공한다.
// @requires query가 비어 있지 않아야 의미 있는 결과가 나온다.
func Search(root *TreeNode, query string, maxResults int) []SearchResult {
	if root == nil || query == "" {
		return nil
	}
	q := strings.ToLower(query)
	results := make([]SearchResult, 0)
	searchNode(root, q, []string{root.Label}, &results, maxResults)
	return results
}

// searchNode traverses descendants and appends matches to results.
// @intent DFS 탐색의 재귀 단위를 담당해 검색 결과 수 제한을 지킨다.
// @mutates results
func searchNode(n *TreeNode, query string, path []string, results *[]SearchResult, maxResults int) {
	for _, child := range n.Children {
		if len(*results) >= maxResults {
			return
		}
		// 슬라이스 공유 방지를 위해 새 슬라이스로 복사
		childPath := make([]string, len(path)+1)
		copy(childPath, path)
		childPath[len(path)] = child.Label

		if strings.Contains(strings.ToLower(child.Label), query) ||
			strings.Contains(strings.ToLower(child.Summary), query) ||
			strings.Contains(strings.ToLower(child.SearchText), query) {
			*results = append(*results, SearchResult{
				ID:      child.ID,
				Label:   child.Label,
				Kind:    child.Kind,
				Summary: child.Summary,
				DocPath: child.DocPath,
				Details: child.Details,
				Path:    childPath,
			})
		}
		searchNode(child, query, childPath, results, maxResults)
	}
}

// FindNode는 root 트리에서 id와 일치하는 TreeNode를 재귀적으로 찾아 반환한다.
// 없으면 nil을 반환한다.
// @intent 인덱스 트리에서 특정 노드를 ID로 직접 찾을 수 있게 한다.
func FindNode(root *TreeNode, id string) *TreeNode {
	if root == nil {
		return nil
	}
	if root.ID == id {
		return root
	}
	for _, child := range root.Children {
		if found := FindNode(child, id); found != nil {
			return found
		}
	}
	return nil
}

// PruneTree는 root 트리를 maxDepth 깊이까지만 포함한 새 트리를 반환한다.
// maxDepth <= 0이면 전체 트리를 반환한다. 원본 트리는 변경하지 않는다.
// depth 계산: root는 depth 0, root의 직계 자식은 depth 1.
// @intent 대형 인덱스를 요약 응답에 맞게 깊이 제한된 복사본으로 축약한다.
// @ensures 원본 트리는 수정되지 않는다.
func PruneTree(root *TreeNode, maxDepth int) *TreeNode {
	if root == nil {
		return nil
	}
	return pruneNode(root, 0, maxDepth)
}

// pruneNode copies one subtree while enforcing the depth limit.
// @intent PruneTree의 재귀 복사 작업을 깊이 기준으로 수행한다.
// @return 자식이 제한된 새로운 TreeNode 복사본을 반환한다.
func pruneNode(n *TreeNode, currentDepth, maxDepth int) *TreeNode {
	copied := &TreeNode{
		ID:          n.ID,
		Label:       n.Label,
		Kind:        n.Kind,
		Summary:     n.Summary,
		DocPath:     n.DocPath,
		SearchText:  n.SearchText,
		HasChildren: len(n.Children) > 0 || n.HasChildren,
		FieldTexts:  n.FieldTexts,
		Details:     n.Details,
	}
	if maxDepth <= 0 || currentDepth < maxDepth {
		copied.Children = make([]*TreeNode, 0, len(n.Children))
		for _, child := range n.Children {
			copied.Children = append(copied.Children, pruneNode(child, currentDepth+1, maxDepth))
		}
	} else {
		copied.Children = []*TreeNode{}
	}
	return copied
}
