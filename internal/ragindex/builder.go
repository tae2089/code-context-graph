// @index ragindex 패키지는 DB의 커뮤니티/어노테이션 데이터를 읽어 doc-index.json을 빌드한다.
package ragindex

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/tae2089/trace"

	"github.com/imtaebin/code-context-graph/internal/ctxns"
	"github.com/imtaebin/code-context-graph/internal/model"
)

// TreeNode는 doc-index.json의 단일 노드이다.
// @intent RAG 탐색 트리에서 커뮤니티, 파일, 심볼 노드를 동일 구조로 표현한다.
type TreeNode struct {
	ID       string      `json:"id"`
	Label    string      `json:"label"`
	Summary  string      `json:"summary"`
	DocPath  string      `json:"doc_path,omitempty"` // file 노드만 설정
	Children []*TreeNode `json:"children"`
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
}

// Build는 DB에서 커뮤니티와 멤버 노드를 읽어 doc-index.json을 생성한다.
// 반환값: (커뮤니티 수, 파일 수, 에러)
// @intent 커뮤니티 구조와 문서 요약을 트리 형태 인덱스로 합성한다.
// @sideEffect 데이터베이스를 읽고 doc-index.json 파일을 기록한다.
// @ensures 성공 시 반환된 파일 수는 인덱스에 포함된 고유 파일 수와 같다.
func (b *Builder) Build(ctx context.Context) (int, int, error) {
	slog.Debug("ragindex.Builder.Build 시작", "outDir", b.OutDir, "indexDir", b.IndexDir)

	ns := ctxns.FromContext(ctx)

	// 1. 모든 커뮤니티와 멤버 로드
	var communities []model.Community
	q := b.DB.WithContext(ctx).Preload("Members")
	if ns != "" {
		q = q.Where("id IN (?)",
			b.DB.Table("community_memberships").
				Select("DISTINCT community_id").
				Joins("JOIN nodes ON nodes.id = community_memberships.node_id").
				Where("nodes.namespace = ?", ns),
		)
	}
	if err := q.Find(&communities).Error; err != nil {
		return 0, 0, trace.Wrap(err, "load communities")
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
		nq := b.DB.WithContext(ctx).Where("id IN ?", allNodeIDs)
		if ns != "" {
			nq = nq.Where("namespace = ?", ns)
		}
		if err := nq.Find(&nodes).Error; err != nil {
			return 0, 0, trace.Wrap(err, "load all nodes")
		}
		for _, n := range nodes {
			nodeInfoMap[n.ID] = nodeInfo{
				FilePath:      n.FilePath,
				Name:          n.Name,
				QualifiedName: n.QualifiedName,
			}
		}
	}

	// 고유 파일 경로 목록 수집
	filePathSet := make(map[string]struct{})
	for _, info := range nodeInfoMap {
		filePathSet[info.FilePath] = struct{}{}
	}
	allFilePaths := make([]string, 0, len(filePathSet))
	for fp := range filePathSet {
		allFilePaths = append(allFilePaths, fp)
	}

	// 3. 배치 fileSummary 조회
	summaries, err := b.batchFileSummaries(ctx, allFilePaths)
	if err != nil {
		return 0, 0, trace.Wrap(err, "batchFileSummaries")
	}

	// 4. @intent 태그를 가진 symbol 노드 배치 조회
	symbolsByFile, err := b.batchSymbolNodes(ctx, allNodeIDs)
	if err != nil {
		return 0, 0, trace.Wrap(err, "batchSymbolNodes")
	}

	root := &TreeNode{
		ID:       "root",
		Label:    "Root",
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
			Summary:  comm.Description,
			Children: []*TreeNode{},
		}

		if len(comm.Members) > 0 {
			// 이 커뮤니티의 고유 파일 경로 수집
			commFilePathSet := make(map[string]struct{})
			for _, m := range comm.Members {
				if info, ok := nodeInfoMap[m.NodeID]; ok {
					commFilePathSet[info.FilePath] = struct{}{}
				}
			}
			slog.Debug("파일 경로 그룹 완료", "community", comm.Key, "files", len(commFilePathSet))

			// 각 파일 경로별 TreeNode 생성
			for filePath := range commFilePathSet {
				summary := summaries[filePath]
				docPath := b.docPath(filePath)
				fileNode := &TreeNode{
					ID:       fmt.Sprintf("file:%s", filePath),
					Label:    filepath.Base(filePath),
					Summary:  summary,
					DocPath:  docPath,
					Children: symbolsByFile[filePath],
				}
				uniqueFiles[filePath] = struct{}{}
				commNode.Children = append(commNode.Children, fileNode)
			}
		}

		root.Children = append(root.Children, commNode)
	}

	// 6. Index 구조체 구성
	idx := &Index{
		Version: 1,
		BuiltAt: time.Now().UTC(),
		Root:    root,
	}

	// 7. doc-index.json 파일 기록 (원자적 쓰기)
	if err := b.writeIndex(idx); err != nil {
		return 0, 0, trace.Wrap(err, "writeIndex")
	}

	slog.Debug("ragindex.Builder.Build 완료", "communities", len(communities), "files", len(uniqueFiles))
	return len(communities), len(uniqueFiles), nil
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
		Where("nodes.file_path IN ? AND doc_tags.kind = ?", filePaths, string(model.TagIndex))
	if ns != "" {
		iq = iq.Where("nodes.namespace = ?", ns)
	}
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
			Where("nodes.file_path IN ? AND doc_tags.kind = ?", missing, string(model.TagIntent))
		if ns != "" {
			fq = fq.Where("nodes.namespace = ?", ns)
		}
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

// batchSymbolNodes는 @intent 태그를 가진 노드를 filePath → []*TreeNode 맵으로 반환한다.
// 노드당 첫 번째 @intent 값만 summary로 사용한다.
// @intent 파일별 심볼 하위 노드를 인덱스 트리에 붙일 수 있게 준비한다.
// @domainRule 각 심볼은 첫 번째 @intent 태그만 summary로 사용한다.
func (b *Builder) batchSymbolNodes(ctx context.Context, nodeIDs []uint) (map[string][]*TreeNode, error) {
	result := make(map[string][]*TreeNode)
	if len(nodeIDs) == 0 {
		return result, nil
	}

	ns := ctxns.FromContext(ctx)

	// @intent 심볼 노드와 첫 번째 intent 태그 값을 함께 담는 배치 조회 결과다.
	type intentRow struct {
		NodeID        uint
		QualifiedName string
		Name          string
		FilePath      string
		Value         string
	}

	var rows []intentRow
	sq := b.DB.WithContext(ctx).Table("nodes").
		Select("nodes.id as node_id, nodes.qualified_name, nodes.name, nodes.file_path, doc_tags.value").
		Joins("JOIN annotations ON annotations.node_id = nodes.id").
		Joins("JOIN doc_tags ON doc_tags.annotation_id = annotations.id").
		Where("nodes.id IN ? AND doc_tags.kind = ?", nodeIDs, string(model.TagIntent))
	if ns != "" {
		sq = sq.Where("nodes.namespace = ?", ns)
	}
	if err := sq.Order("nodes.file_path ASC, doc_tags.ordinal ASC, doc_tags.id ASC").
		Scan(&rows).Error; err != nil {
		return nil, trace.Wrap(err, "batch symbol nodes")
	}

	// 첫 번째 @intent 태그만 사용 (node_id 기준 deduplicate)
	seen := make(map[uint]struct{})
	for _, r := range rows {
		if _, ok := seen[r.NodeID]; ok {
			continue
		}
		seen[r.NodeID] = struct{}{}

		symNode := &TreeNode{
			ID:       fmt.Sprintf("symbol:%s", r.QualifiedName),
			Label:    r.Name,
			Summary:  r.Value,
			Children: []*TreeNode{},
		}
		result[r.FilePath] = append(result[r.FilePath], symNode)
	}

	return result, nil
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
	tmp := target + ".tmp"

	f, err := os.Create(tmp)
	if err != nil {
		return trace.Wrap(err, "create temp file")
	}

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(idx); err != nil {
		f.Close()
		os.Remove(tmp)
		return trace.Wrap(err, "encode index")
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
	ID      string   `json:"id"`
	Label   string   `json:"label"`
	Summary string   `json:"summary"`
	DocPath string   `json:"doc_path,omitempty"`
	Path    []string `json:"path"` // root부터 해당 노드까지의 Label 경로
}

// Search는 root 트리를 DFS로 순회하며 query를 Label과 Summary에서
// case-insensitive 검색하여 최대 maxResults개의 결과를 반환한다.
// root 노드 자체는 결과에 포함하지 않는다.
// @intent 문서 인덱스 트리에서 제목과 요약 기반 키워드 탐색을 제공한다.
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
			strings.Contains(strings.ToLower(child.Summary), query) {
			*results = append(*results, SearchResult{
				ID:      child.ID,
				Label:   child.Label,
				Summary: child.Summary,
				DocPath: child.DocPath,
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
		ID:      n.ID,
		Label:   n.Label,
		Summary: n.Summary,
		DocPath: n.DocPath,
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
