// @index Search retrieval response DTOs, scoring service, and consumer-owned outbound ports.
package retrieval

import (
	"context"

	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// CandidateSearcher returns relevance-ordered full-text candidates.
// @intent let retrieval consume a bound search implementation without a database handle.
type CandidateSearcher interface {
	Query(ctx context.Context, query string, limit int) ([]graph.Node, error)
}

// Repository supplies bounded fallback candidates and structured annotations.
// @intent isolate namespace-scoped retrieval persistence from grouping, scoring, and ranking policy.
type Repository interface {
	ScanCandidates(ctx context.Context, kinds []graph.NodeKind, limit int) ([]graph.Node, error)
	Annotations(ctx context.Context, nodeIDs []uint) (map[uint]*graph.Annotation, error)
}

// Service coordinates full-text candidates, bounded persistence fallback, and content lookup.
// @intent provide one application entry point for document retrieval.
type Service struct {
	Search        CandidateSearcher
	Repository    Repository
	ContentReader ContentReader
}

// New constructs a retrieval service from consumer-owned outbound ports.
// @intent make search and persistence dependencies explicit at composition time.
func New(search CandidateSearcher, repository Repository) *Service {
	return &Service{Search: search, Repository: repository}
}

// @intent 문서 본문을 namespace/docPath 기준으로 읽고 필요시 잘라내기 여부를 반환한다.
type ContentReader func(ctx context.Context, namespace, docPath string, limit int) (string, bool, error)

// @intent 검색 결과 배열을 JSON 응답으로 직렬화하는 공용 DTO를 제공한다.
type Response struct {
	Results []Result `json:"results"`
}

// SearchResult is compact node evidence nested under a file-level retrieval result.
// @intent preserve the existing JSON evidence shape without depending on Wiki tree DTO ownership.
type SearchResult struct {
	ID      string   `json:"id"`
	Label   string   `json:"label"`
	Kind    string   `json:"kind"`
	Summary string   `json:"summary"`
	DocPath string   `json:"doc_path,omitempty"`
	Path    []string `json:"path"`
}

// RetrieveResult is one scored file-level document candidate.
// @intent own search retrieval output independently from the legacy Wiki tree index.
type RetrieveResult struct {
	ID            string         `json:"id"`
	Label         string         `json:"label"`
	Kind          string         `json:"kind"`
	Summary       string         `json:"summary"`
	DocPath       string         `json:"doc_path"`
	Path          []string       `json:"path"`
	Score         int            `json:"score"`
	MatchedTerms  []string       `json:"matched_terms"`
	MatchedFields []string       `json:"matched_fields"`
	Matches       []SearchResult `json:"matches,omitempty"`
}

// Result adds optional document content to one scored retrieval candidate.
// @intent preserve the MCP and Wiki retrieval JSON contract while app/search owns the result.
type Result struct {
	RetrieveResult
	Content          string `json:"content,omitempty"`
	ContentTruncated bool   `json:"content_truncated,omitempty"`
}

// @intent DB 후보 수 제한의 하한과 상한을 retrieval 패키지 내부에서 고정한다.
const (
	dbCandidateFloor = 50
	dbCandidateCap   = 500
	// scanRowCap bounds the fallback namespace scan so a sparse-FTS query cannot load an
	// unbounded number of nodes+annotations. Namespaces with fewer retrievable nodes than
	// this behave identically to an uncapped scan; larger ones are truncated in stable order.
	scanRowCap = 5000
)
