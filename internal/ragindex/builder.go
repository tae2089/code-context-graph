// @index ragindex 패키지는 DB의 커뮤니티/어노테이션 데이터를 읽어 doc-index.json을 빌드한다.
package ragindex

import (
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/tae2089/code-context-graph/internal/ccgref"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/trace"
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

// LoadIndex reads a persisted Index (e.g. wiki-index.json) from disk.
// @intent let the wiki index round-trip its written tree without the RAG-index builder.
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
