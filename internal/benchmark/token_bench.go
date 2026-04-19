package benchmark

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

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
}

// sanitizeFTSQuery는 FTS5 쿼리에서 특수문자를 공백으로 대체한다.
func sanitizeFTSQuery(query string) string {
	var sb strings.Builder
	for _, r := range query {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' {
			sb.WriteRune(r)
		} else {
			sb.WriteRune(' ')
		}
	}
	return strings.TrimSpace(sb.String())
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

// GraphTokens는 backend.Query 결과와 실제 코드 내용을 합산해 토큰 수, 경과 시간(ms), 결과 수를 반환한다.
// expander가 nil이 아니면 1-hop 이웃 노드와 어노테이션도 포함한다.
// repoRoot가 비어있으면 코드 내용은 포함하지 않는다.
func GraphTokens(ctx context.Context, db *gorm.DB, backend SearchBackend, expander NodeExpander, query, repoRoot string, limit int) (tokens int, elapsedMs int64, count int, err error) {
	start := time.Now()
	nodes, err := backend.Query(ctx, db, sanitizeFTSQuery(query), limit)
	elapsedMs = time.Since(start).Milliseconds()
	if err != nil {
		return 0, elapsedMs, 0, err
	}
	var sb strings.Builder
	writeNode := func(n model.Node) {
		fmt.Fprintf(&sb, "%s %s %s\n", n.QualifiedName, n.Kind, n.FilePath)
		if repoRoot != "" && n.FilePath != "" && n.StartLine > 0 {
			code := readLines(filepath.Join(repoRoot, n.FilePath), n.StartLine, n.EndLine)
			sb.WriteString(code)
			sb.WriteByte('\n')
		}
	}
	seen := make(map[uint]struct{})
	for _, n := range nodes {
		seen[n.ID] = struct{}{}
		writeNode(n)
		if expander == nil {
			continue
		}
		// 1-hop 이웃 확장
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
		// 어노테이션 포함
		if ann, aerr := expander.GetAnnotation(ctx, n.ID); aerr == nil && ann != nil {
			fmt.Fprintf(&sb, "annotation: %s\n", ann.RawText)
		}
	}
	return EstimateTokens(sb.String()), elapsedMs, len(nodes), nil
}

// graphTokensMulti는 여러 심볼을 개별 검색해 중복 없이 토큰을 합산한다.
func graphTokensMulti(ctx context.Context, db *gorm.DB, backend SearchBackend, expander NodeExpander, symbols []string, repoRoot string, limitEach int) (tokens int, elapsedMs int64, count int, err error) {
	seen := make(map[uint]struct{})
	var sb strings.Builder
	var totalElapsed int64
	for _, sym := range symbols {
		start := time.Now()
		nodes, qerr := backend.Query(ctx, db, sanitizeFTSQuery(sym), limitEach)
		totalElapsed += time.Since(start).Milliseconds()
		if qerr != nil {
			return 0, totalElapsed, 0, qerr
		}
		for _, n := range nodes {
			if _, dup := seen[n.ID]; dup {
				continue
			}
			seen[n.ID] = struct{}{}
			count++
			fmt.Fprintf(&sb, "%s %s %s\n", n.QualifiedName, n.Kind, n.FilePath)
			if repoRoot != "" && n.FilePath != "" && n.StartLine > 0 {
				code := readLines(filepath.Join(repoRoot, n.FilePath), n.StartLine, n.EndLine)
				sb.WriteString(code)
				sb.WriteByte('\n')
			}
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
				fmt.Fprintf(&sb, "%s %s %s\n", nb.QualifiedName, nb.Kind, nb.FilePath)
				if repoRoot != "" && nb.FilePath != "" && nb.StartLine > 0 {
					code := readLines(filepath.Join(repoRoot, nb.FilePath), nb.StartLine, nb.EndLine)
					sb.WriteString(code)
					sb.WriteByte('\n')
				}
			}
			if ann, aerr := expander.GetAnnotation(ctx, n.ID); aerr == nil && ann != nil {
				fmt.Fprintf(&sb, "annotation: %s\n", ann.RawText)
			}
		}
	}
	return EstimateTokens(sb.String()), totalElapsed, count, nil
}

// RunTokenBench는 corpus의 각 쿼리에 대해 naive/graph 토큰을 비교한다.
func RunTokenBench(ctx context.Context, db *gorm.DB, backend SearchBackend, expander NodeExpander, corpus *Corpus, repoRoot string, exts []string) ([]TokenBenchResult, error) {
	naive, err := NaiveTokens(repoRoot, exts)
	if err != nil {
		return nil, err
	}

	results := make([]TokenBenchResult, 0, len(corpus.Queries))
	for _, q := range corpus.Queries {
		var tokens int
		var elapsed int64
		var count int
		var qerr error
		if len(q.ExpectedSymbols) > 0 {
			// 심볼별로 개별 검색 후 중복 제거 합산
			tokens, elapsed, count, qerr = graphTokensMulti(ctx, db, backend, expander, q.ExpectedSymbols, repoRoot, 5)
		} else {
			tokens, elapsed, count, qerr = GraphTokens(ctx, db, backend, expander, q.Description, repoRoot, 10)
		}
		if qerr != nil {
			return nil, qerr
		}
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
			ResultCount:     count,
		})
	}
	return results, nil
}
