package benchmark

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/tae2089/code-context-graph/internal/model"
)

// SearchBackend는 FTS 검색 백엔드 추상화다.
type SearchBackend interface {
	Query(ctx context.Context, db *gorm.DB, query string, limit int) ([]model.Node, error)
}

// NodeExpander는 노드의 1-hop 이웃과 어노테이션을 가져오는 추상화다.
// nil을 전달하면 확장 없이 기본 검색 결과만 사용한다.
type NodeExpander interface {
	GetEdgesFrom(ctx context.Context, nodeID uint) ([]model.Edge, error)
	GetNodesByIDs(ctx context.Context, ids []uint) ([]model.Node, error)
	GetAnnotation(ctx context.Context, nodeID uint) (*model.Annotation, error)
}

// EstimateTokens는 텍스트의 대략적인 토큰 수를 반환한다 (4자 = 1토큰).
func EstimateTokens(text string) int {
	return len(text) / 4
}

// NaiveTokens는 repoRoot 아래 exts 확장자를 가진 모든 파일의 토큰 수 합계를 반환한다.
func NaiveTokens(repoRoot string, exts []string) (int, error) {
	extSet := make(map[string]struct{}, len(exts))
	for _, e := range exts {
		extSet[e] = struct{}{}
	}
	var total int
	err := filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if _, ok := extSet[filepath.Ext(path)]; !ok {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		total += EstimateTokens(string(data))
		return nil
	})
	return total, err
}

// TokenBenchResult는 단일 쿼리에 대한 토큰 벤치마크 결과다.
type TokenBenchResult struct {
	QueryID         string  `json:"query_id"`
	NaiveTokens     int     `json:"naive_tokens"`
	GraphTokens     int     `json:"graph_tokens"`
	Ratio           float64 `json:"ratio"`
	SearchElapsedMs int64   `json:"search_elapsed_ms"`
	ResultCount     int     `json:"result_count"`
	// Recall: 정답 파일/심볼이 결과에 포함되었는지 측정
	FilesHit     int     `json:"files_hit"`
	FilesTotal   int     `json:"files_total"`
	SymbolsHit   int     `json:"symbols_hit"`
	SymbolsTotal int     `json:"symbols_total"`
	Recall       float64 `json:"recall"`
}

// extractASCIITerms는 텍스트에서 ASCII 영숫자 단어만 추출한다.
// 한국어 등 비ASCII 문자를 포함한 설명에서 코드 심볼에 해당하는 영어 단어만 반환한다.
func extractASCIITerms(text string) []string {
	var terms []string
	for _, word := range strings.Fields(text) {
		var clean strings.Builder
		for _, r := range word {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
				clean.WriteRune(r)
			}
		}
		if clean.Len() > 1 {
			terms = append(terms, clean.String())
		}
	}
	return terms
}

// readLines는 파일에서 startLine~endLine 범위의 텍스트를 반환한다 (1-based).
func readLines(path string, startLine, endLine int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	lo := max(startLine-1, 0)
	hi := min(endLine, len(lines))
	if lo >= hi {
		return ""
	}
	return strings.Join(lines[lo:hi], "\n")
}

// searchAndCollect는 FTS 검색 후 1-hop 확장까지 수행해 노드 목록과 텍스트를 반환한다.
// seen은 호출 간 중복 제거에 사용되며 nil이면 새로 생성한다.
func searchAndCollect(
	ctx context.Context, db *gorm.DB, backend SearchBackend, expander NodeExpander,
	query, repoRoot string, limit int, seen map[uint]struct{},
) (nodes []model.Node, text string, elapsedMs int64, err error) {
	if seen == nil {
		seen = make(map[uint]struct{})
	}

	terms := extractASCIITerms(query)
	if len(terms) == 0 {
		return nil, "", 0, nil
	}

	var sb strings.Builder
	writeNode := func(n model.Node) {
		nodes = append(nodes, n)
		fmt.Fprintf(&sb, "%s %s %s\n", n.QualifiedName, n.Kind, n.FilePath)
		if repoRoot != "" && n.FilePath != "" && n.StartLine > 0 {
			code := readLines(filepath.Join(repoRoot, n.FilePath), n.StartLine, n.EndLine)
			sb.WriteString(code)
			sb.WriteByte('\n')
		}
	}

	// 각 단어를 개별 검색해 FTS5 AND 제약을 피하고 결과를 누적한다.
	for _, term := range terms {
		start := time.Now()
		found, qerr := backend.Query(ctx, db, term, limit)
		elapsedMs += time.Since(start).Milliseconds()
		if qerr != nil {
			return nil, "", elapsedMs, qerr
		}
		for _, n := range found {
			if n.ID > 0 {
				if _, dup := seen[n.ID]; dup {
					continue
				}
				seen[n.ID] = struct{}{}
			}
			writeNode(n)
			if expander == nil {
				continue
			}
			edges, eerr := expander.GetEdgesFrom(ctx, n.ID)
			if eerr != nil {
				continue
			}
			neighborIDs := make([]uint, 0, len(edges))
			for _, e := range edges {
				if _, dup := seen[e.ToNodeID]; !dup {
					neighborIDs = append(neighborIDs, e.ToNodeID)
				}
			}
			if len(neighborIDs) == 0 {
				continue
			}
			neighbors, nerr := expander.GetNodesByIDs(ctx, neighborIDs)
			if nerr != nil {
				continue
			}
			for _, nb := range neighbors {
				seen[nb.ID] = struct{}{}
				writeNode(nb)
			}
			if ann, aerr := expander.GetAnnotation(ctx, n.ID); aerr == nil && ann != nil {
				fmt.Fprintf(&sb, "annotation: %s\n", ann.RawText)
			}
		}
	}
	return nodes, sb.String(), elapsedMs, nil
}

// GraphTokens는 단일 쿼리로 검색해 토큰 수, 경과 시간(ms), 결과 수를 반환한다.
func GraphTokens(ctx context.Context, db *gorm.DB, backend SearchBackend, expander NodeExpander, query, repoRoot string, limit int) (tokens int, elapsedMs int64, count int, err error) {
	nodes, text, elapsedMs, err := searchAndCollect(ctx, db, backend, expander, query, repoRoot, limit, nil)
	if err != nil {
		return 0, elapsedMs, 0, err
	}
	return EstimateTokens(text), elapsedMs, len(nodes), nil
}

// countFilesHit는 nodes 중 expectedFiles에 해당하는 FilePath를 가진 노드 수를 반환한다.
func countFilesHit(nodes []model.Node, expectedFiles []string) int {
	if len(expectedFiles) == 0 {
		return 0
	}
	found := make(map[string]struct{}, len(nodes))
	for _, n := range nodes {
		found[n.FilePath] = struct{}{}
	}
	hit := 0
	for _, f := range expectedFiles {
		if _, ok := found[f]; ok {
			hit++
		}
	}
	return hit
}

// countSymbolsHit는 nodes 중 QualifiedName에 expectedSymbols 심볼명이 포함된 노드 수를 반환한다.
func countSymbolsHit(nodes []model.Node, expectedSymbols []string) int {
	if len(expectedSymbols) == 0 {
		return 0
	}
	hit := 0
	for _, sym := range expectedSymbols {
		for _, n := range nodes {
			if strings.Contains(n.QualifiedName, sym) {
				hit++
				break
			}
		}
	}
	return hit
}

// computeRecall은 파일/심볼 히트율을 0~1로 반환한다.
func computeRecall(filesHit, filesTotal, symbolsHit, symbolsTotal int) float64 {
	total := filesTotal + symbolsTotal
	if total == 0 {
		return 0
	}
	return float64(filesHit+symbolsHit) / float64(total)
}

// RunTokenBench는 corpus의 각 쿼리에 대해 naive/graph 토큰과 recall을 비교한다.
// 검색은 항상 Description을 사용하며, expected_symbols/files는 정답 매칭에만 사용한다.
func RunTokenBench(ctx context.Context, db *gorm.DB, backend SearchBackend, expander NodeExpander, corpus *Corpus, repoRoot string, exts []string) ([]TokenBenchResult, error) {
	naive, err := NaiveTokens(repoRoot, exts)
	if err != nil {
		return nil, err
	}

	results := make([]TokenBenchResult, 0, len(corpus.Queries))
	for _, q := range corpus.Queries {
		nodes, text, elapsed, qerr := searchAndCollect(ctx, db, backend, expander, q.Description, repoRoot, 10, nil)
		if qerr != nil {
			return nil, qerr
		}
		tokens := EstimateTokens(text)

		filesHit := countFilesHit(nodes, q.ExpectedFiles)
		symbolsHit := countSymbolsHit(nodes, q.ExpectedSymbols)
		filesTotal := len(q.ExpectedFiles)
		symbolsTotal := len(q.ExpectedSymbols)

		var ratio float64
		if tokens > 0 {
			ratio = float64(naive) / float64(tokens)
		}
		results = append(results, TokenBenchResult{
			QueryID:         q.ID,
			NaiveTokens:     naive,
			GraphTokens:     tokens,
			Ratio:           ratio,
			SearchElapsedMs: elapsed,
			ResultCount:     len(nodes),
			FilesHit:        filesHit,
			FilesTotal:      filesTotal,
			SymbolsHit:      symbolsHit,
			SymbolsTotal:    symbolsTotal,
			Recall:          computeRecall(filesHit, filesTotal, symbolsHit, symbolsTotal),
		})
	}
	return results, nil
}
