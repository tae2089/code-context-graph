// @index retrieval 패키지는 RAG retrieval 응답 DTO와 서비스 의존성을 정의한다.
package retrieval

import (
	"context"

	"gorm.io/gorm"

	"github.com/tae2089/code-context-graph/internal/ragindex"
	storesearch "github.com/tae2089/code-context-graph/internal/store/search"
)

// @intent DB와 검색 백엔드를 묶어 retrieval 로직의 공용 진입점을 제공한다.
type Service struct {
	DB            *gorm.DB
	SearchBackend storesearch.Backend
	ContentReader ContentReader
}

// @intent 문서 본문을 namespace/docPath 기준으로 읽고 필요시 잘라내기 여부를 반환한다.
type ContentReader func(ctx context.Context, namespace, docPath string, limit int) (string, bool, error)

// @intent 검색 결과 배열을 JSON 응답으로 직렬화하는 공용 DTO를 제공한다.
type Response struct {
	Results []Result `json:"results"`
}

// @intent RAG 후보 결과에 본문과 잘라내기 상태를 덧붙인 최종 응답 항목을 제공한다.
type Result struct {
	ragindex.RetrieveResult
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
