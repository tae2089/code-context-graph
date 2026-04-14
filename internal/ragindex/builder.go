// @index ragindex 패키지는 DB의 커뮤니티/어노테이션 데이터를 읽어 doc-index.json을 빌드한다.
package ragindex

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
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
	DB       *gorm.DB
	OutDir   string // docs 디렉토리 (기본: "docs")
	IndexDir string // .ccg 디렉토리 (기본: ".ccg")
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

	root := &TreeNode{
		ID:       "root",
		Label:    "Root",
		Children: []*TreeNode{},
	}

	totalFiles := 0

	// 2. 각 커뮤니티별 처리
	for _, comm := range communities {
		slog.Debug("커뮤니티 처리 중", "key", comm.Key, "members", len(comm.Members))

		commNode := &TreeNode{
			ID:       fmt.Sprintf("community:%s", comm.Key),
			Label:    comm.Label,
			Summary:  comm.Description,
			Children: []*TreeNode{},
		}

		// 멤버 노드 ID 목록 추출
		nodeIDs := make([]uint, 0, len(comm.Members))
		for _, m := range comm.Members {
			nodeIDs = append(nodeIDs, m.NodeID)
		}

		if len(nodeIDs) > 0 {
			// 멤버 노드를 파일 경로별로 그룹핑
			filePathSet := make(map[string]struct{})
			var nodes []model.Node
			if err := b.DB.Where("id IN ?", nodeIDs).Find(&nodes).Error; err != nil {
				return 0, 0, fmt.Errorf("load nodes for community %s: %w", comm.Key, err)
			}

			for _, n := range nodes {
				filePathSet[n.FilePath] = struct{}{}
			}
			slog.Debug("파일 경로 그룹 완료", "community", comm.Key, "files", len(filePathSet))

			// 3. 각 파일 경로별 TreeNode 생성
			for filePath := range filePathSet {
				summary, err := b.fileSummary(filePath)
				if err != nil {
					return 0, 0, fmt.Errorf("fileSummary for %s: %w", filePath, err)
				}

				docPath := b.docPath(filePath)
				fileNode := &TreeNode{
					ID:       fmt.Sprintf("file:%s", filePath),
					Label:    filepath.Base(filePath),
					Summary:  summary,
					DocPath:  docPath,
					Children: []*TreeNode{},
				}
				commNode.Children = append(commNode.Children, fileNode)
				totalFiles++
			}
		}

		root.Children = append(root.Children, commNode)
	}

	// 4. Index 구조체 구성
	idx := &Index{
		Version: 1,
		BuiltAt: time.Now().UTC(),
		Root:    root,
	}

	// 5. doc-index.json 파일 기록
	if err := b.writeIndex(idx); err != nil {
		return 0, 0, fmt.Errorf("writeIndex: %w", err)
	}

	slog.Debug("ragindex.Builder.Build 완료", "communities", len(communities), "files", totalFiles)
	return len(communities), totalFiles, nil
}

// fileSummary는 주어진 파일 경로의 노드 중 @index 태그를 찾고,
// 없으면 @intent 태그를 반환한다. 둘 다 없으면 빈 문자열을 반환한다.
func (b *Builder) fileSummary(filePath string) (string, error) {
	slog.Debug("fileSummary 조회", "filePath", filePath)

	// @index 태그 조회: doc_tags → annotations → nodes WHERE nodes.file_path = ? AND doc_tags.kind = "index"
	summary, err := b.queryTagValueByFilePath(filePath, model.TagIndex)
	if err != nil {
		return "", err
	}
	if summary != "" {
		slog.Debug("fileSummary: @index 태그 발견", "filePath", filePath, "summary", summary)
		return summary, nil
	}

	// @intent 태그로 폴백
	summary, err = b.queryTagValueByFilePath(filePath, model.TagIntent)
	if err != nil {
		return "", err
	}
	slog.Debug("fileSummary: @intent 폴백", "filePath", filePath, "summary", summary)
	return summary, nil
}

// queryTagValueByFilePath는 GORM JOIN을 사용하여 특정 파일 경로와 태그 종류에 해당하는
// DocTag의 Value를 조회한다.
func (b *Builder) queryTagValueByFilePath(filePath string, kind model.TagKind) (string, error) {
	var tag model.DocTag
	result := b.DB.
		Joins("JOIN annotations ON annotations.id = doc_tags.annotation_id").
		Joins("JOIN nodes ON nodes.id = annotations.node_id").
		Where("nodes.file_path = ? AND doc_tags.kind = ?", filePath, kind).
		Order("doc_tags.ordinal ASC").
		Limit(1).
		First(&tag)

	if result.Error == gorm.ErrRecordNotFound {
		return "", nil
	}
	if result.Error != nil {
		return "", fmt.Errorf("queryTagValueByFilePath(%s, %s): %w", filePath, kind, result.Error)
	}
	return tag.Value, nil
}

// docPath는 파일 경로를 기반으로 docs 디렉토리 내의 문서 경로를 반환한다.
func (b *Builder) docPath(filePath string) string {
	outDir := b.OutDir
	if outDir == "" {
		outDir = "docs"
	}
	base := filepath.Base(filePath)
	name := base[:len(base)-len(filepath.Ext(base))]
	return filepath.Join(outDir, name+".md")
}

// writeIndex는 IndexDir/doc-index.json 파일에 Index를 JSON으로 기록한다.
func (b *Builder) writeIndex(idx *Index) error {
	indexDir := b.IndexDir
	if indexDir == "" {
		indexDir = ".ccg"
	}

	if err := os.MkdirAll(indexDir, 0o755); err != nil {
		return fmt.Errorf("MkdirAll(%s): %w", indexDir, err)
	}

	outPath := filepath.Join(indexDir, "doc-index.json")
	f, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", outPath, err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(idx); err != nil {
		return fmt.Errorf("encode JSON: %w", err)
	}

	slog.Debug("doc-index.json 기록 완료", "path", outPath)
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
