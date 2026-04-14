// @index ragindex 패키지는 DB의 커뮤니티/어노테이션 데이터를 읽어 doc-index.json을 빌드한다.
package ragindex

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/imtaebin/code-context-graph/internal/model"
)

// TreeNode는 doc-index.json의 단일 노드이다.
type TreeNode struct {
	ID       string      `json:"id"`
	Label    string      `json:"label"`
	Summary  string      `json:"summary"`
	DocPath  string      `json:"doc_path,omitempty"` // file 노드만 설정
	Children []*TreeNode `json:"children"`
}

// Index는 .ccg/doc-index.json 전체 포맷이다.
type Index struct {
	Version int       `json:"version"`
	BuiltAt time.Time `json:"built_at"`
	Root    *TreeNode `json:"root"`
}

// Builder는 DB에서 인덱스를 빌드하는 구조체이다.
type Builder struct {
	DB          *gorm.DB
	OutDir      string // docs 디렉토리 (기본: "docs")
	IndexDir    string // .ccg 디렉토리 (기본: ".ccg")
	ProjectDesc string // root 노드 summary (기본: "")
}

// indexDir는 IndexDir 필드의 기본값을 반환한다.
func (b *Builder) indexDir() string {
	if b.IndexDir == "" {
		return ".ccg"
	}
	return b.IndexDir
}

// Build는 DB에서 커뮤니티와 멤버 노드를 읽어 doc-index.json을 생성한다.
// 반환값: (커뮤니티 수, 파일 수, 에러)
func (b *Builder) Build() (int, int, error) {
	slog.Debug("ragindex.Builder.Build 시작", "outDir", b.OutDir, "indexDir", b.IndexDir)

	// 1. 모든 커뮤니티와 멤버 로드
	var communities []model.Community
	if err := b.DB.Preload("Members").Find(&communities).Error; err != nil {
		return 0, 0, fmt.Errorf("load communities: %w", err)
	}
	slog.Debug("커뮤니티 로드 완료", "count", len(communities))

	// 2. 1-pass: 모든 커뮤니티의 고유 파일 경로 수집
	allNodeIDs := make([]uint, 0)
	for _, comm := range communities {
		for _, m := range comm.Members {
			allNodeIDs = append(allNodeIDs, m.NodeID)
		}
	}

	// 노드 ID → 파일 경로 매핑
	nodeFileMap := make(map[uint]string)
	if len(allNodeIDs) > 0 {
		var nodes []model.Node
		if err := b.DB.Where("id IN ?", allNodeIDs).Find(&nodes).Error; err != nil {
			return 0, 0, fmt.Errorf("load all nodes: %w", err)
		}
		for _, n := range nodes {
			nodeFileMap[n.ID] = n.FilePath
		}
	}

	// 고유 파일 경로 목록 수집
	filePathSet := make(map[string]struct{})
	for _, fp := range nodeFileMap {
		filePathSet[fp] = struct{}{}
	}
	allFilePaths := make([]string, 0, len(filePathSet))
	for fp := range filePathSet {
		allFilePaths = append(allFilePaths, fp)
	}

	// 3. 배치 fileSummary 조회
	summaries, err := b.batchFileSummaries(allFilePaths)
	if err != nil {
		return 0, 0, fmt.Errorf("batchFileSummaries: %w", err)
	}

	root := &TreeNode{
		ID:       "root",
		Label:    "Root",
		Summary:  b.ProjectDesc,
		Children: []*TreeNode{},
	}

	uniqueFiles := make(map[string]struct{})

	// 4. 2-pass: 커뮤니티별 TreeNode 구성
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
				if fp, ok := nodeFileMap[m.NodeID]; ok {
					commFilePathSet[fp] = struct{}{}
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
					Children: []*TreeNode{},
				}
				uniqueFiles[filePath] = struct{}{}
				commNode.Children = append(commNode.Children, fileNode)
			}
		}

		root.Children = append(root.Children, commNode)
	}

	// 5. Index 구조체 구성
	idx := &Index{
		Version: 1,
		BuiltAt: time.Now().UTC(),
		Root:    root,
	}

	// 6. doc-index.json 파일 기록 (원자적 쓰기)
	if err := b.writeIndex(idx); err != nil {
		return 0, 0, fmt.Errorf("writeIndex: %w", err)
	}

	slog.Debug("ragindex.Builder.Build 완료", "communities", len(communities), "files", len(uniqueFiles))
	return len(communities), len(uniqueFiles), nil
}

// batchFileSummaries는 주어진 파일 경로 목록에 대해 filePath → summary 맵을 한 번에 반환한다.
// @index 태그를 우선하고, 없으면 @intent 태그로 폴백한다. 둘 다 없으면 빈 문자열이다.
func (b *Builder) batchFileSummaries(filePaths []string) (map[string]string, error) {
	result := make(map[string]string, len(filePaths))
	if len(filePaths) == 0 {
		return result, nil
	}

	type row struct {
		FilePath string
		Value    string
	}

	// @index 태그를 파일 경로별로 일괄 조회
	var indexRows []row
	if err := b.DB.Table("doc_tags").
		Select("nodes.file_path, doc_tags.value").
		Joins("JOIN annotations ON annotations.id = doc_tags.annotation_id").
		Joins("JOIN nodes ON nodes.id = annotations.node_id").
		Where("nodes.file_path IN ? AND doc_tags.kind = ?", filePaths, string(model.TagIndex)).
		Order("doc_tags.ordinal ASC, doc_tags.id ASC").
		Scan(&indexRows).Error; err != nil {
		return nil, fmt.Errorf("batch index tags: %w", err)
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
		if err := b.DB.Table("doc_tags").
			Select("nodes.file_path, doc_tags.value").
			Joins("JOIN annotations ON annotations.id = doc_tags.annotation_id").
			Joins("JOIN nodes ON nodes.id = annotations.node_id").
			Where("nodes.file_path IN ? AND doc_tags.kind = ?", missing, string(model.TagIntent)).
			Order("doc_tags.ordinal ASC, doc_tags.id ASC").
			Scan(&intentRows).Error; err != nil {
			return nil, fmt.Errorf("batch intent tags: %w", err)
		}
		for _, r := range intentRows {
			if result[r.FilePath] == "" {
				result[r.FilePath] = r.Value
			}
		}
	}

	return result, nil
}


// docPath는 파일 경로를 기반으로 docs 디렉토리 내의 문서 경로를 반환한다.
// 전체 상대 경로 구조를 유지하여 basename 충돌을 방지한다.
// 예: "internal/mcp/handlers.go" → "docs/internal/mcp/handlers.go.md"
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
func (b *Builder) writeIndex(idx *Index) error {
	dir := b.indexDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create index dir: %w", err)
	}

	target := filepath.Join(dir, "doc-index.json")
	tmp := target + ".tmp"

	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(idx); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("encode index: %w", err)
	}

	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := os.Rename(tmp, target); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename to doc-index.json: %w", err)
	}

	slog.Debug("doc-index.json 기록 완료", "path", target)
	return nil
}

// LoadIndex는 주어진 경로에서 doc-index.json을 읽어 Index를 반환한다.
func LoadIndex(path string) (*Index, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("LoadIndex open %s: %w", path, err)
	}
	defer f.Close()

	var idx Index
	if err := json.NewDecoder(f).Decode(&idx); err != nil {
		return nil, fmt.Errorf("LoadIndex decode: %w", err)
	}
	return &idx, nil
}

// FindNode는 root 트리에서 id와 일치하는 TreeNode를 재귀적으로 찾아 반환한다.
// 없으면 nil을 반환한다.
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
