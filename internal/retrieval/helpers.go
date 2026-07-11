// @index retrieval 패키지의 순수 DB 후보 처리 헬퍼 함수 모음.
package retrieval

import (
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/ragindex"
)

var retrievableNodeKinds = []model.NodeKind{
	model.NodeKindFile,
	model.NodeKindFunction,
	model.NodeKindClass,
	model.NodeKindType,
	model.NodeKindTest,
}

// @intent identify graph node kinds that can resolve to a generated file-level documentation result.
func IsRetrievableNodeKind(kind model.NodeKind) bool {
	return slices.Contains(retrievableNodeKinds, kind)
}

// DBFileGroup은 파일 경로별로 묶인 DB 후보 노드 그룹이다.
// @intent doc retrieval DB-backed 결과가 파일 단위 세분성을 유지하도록 후보를 그룹화한다.
type DBFileGroup struct {
	FilePath string
	Nodes    []model.Node
}

// DBCandidateLimit은 그룹화 후 파일 단위 결과 수가 충분하도록 FTS 후보 수를 결정한다.
// @intent doc retrieval는 파일당 하나의 결과를 반환하므로 후보 수는 최종 파일 한도를 초과해야 하되 무한정 커지면 안 된다.
// @domainRule 후보 수는 최소 dbCandidateFloor, 최대 dbCandidateCap으로 제한한다.
func DBCandidateLimit(limit int) int {
	return min(max(limit*10, dbCandidateFloor), dbCandidateCap)
}

// MatchedTerms는 쿼리 문자열을 소문자 토큰으로 분리하고 중복을 제거한다.
// @intent DB-backed 응답 증거를 위해 검색 쿼리를 결정론적 소문자 용어로 토큰화하고 코드 식별자 alias를 보강한다.
func MatchedTerms(query string) []string {
	seen := map[string]struct{}{}
	terms := make([]string, 0)
	addTerm := func(term string) {
		term = strings.TrimSpace(term)
		if term == "" {
			return
		}
		if _, ok := seen[term]; ok {
			return
		}
		seen[term] = struct{}{}
		terms = append(terms, term)
	}
	for term := range strings.FieldsSeq(strings.ToLower(query)) {
		term = strings.Trim(term, `"'()[]{}.,:;!?`)
		addTerm(term)
	}
	baseLen := len(terms)
	for i := 0; i+1 < baseLen; i++ {
		left, right := terms[i], terms[i+1]
		if left == "" || right == "" || strings.Contains(left, "_") || strings.Contains(right, "_") {
			continue
		}
		for _, alias := range []string{left + "_" + right, left + right} {
			addTerm(alias)
		}
	}
	for _, term := range terms[:baseLen] {
		for _, alias := range retrievalTermAliases(term) {
			addTerm(alias)
		}
	}
	return terms
}

// @intent expand common natural-language retrieval terms into code identifier variants without calling an LLM reranker.
func retrievalTermAliases(term string) []string {
	switch term {
	case "reference":
		return []string{"ref", "refs"}
	case "references":
		return []string{"reference", "ref", "refs"}
	case "ref":
		return []string{"reference", "refs"}
	default:
		return nil
	}
}

// TextContainsAnyTerm은 텍스트가 주어진 용어 중 하나라도 포함하는지 대소문자 무시로 확인한다.
// @intent DB-backed 매칭 필드 진단을 위한 단순 대소문자 무시 용어 포함 검사를 수행한다.
func TextContainsAnyTerm(text string, terms []string) bool {
	if text == "" || len(terms) == 0 {
		return false
	}
	lower := strings.ToLower(text)
	for _, term := range terms {
		if containsRetrievalTerm(lower, term) {
			return true
		}
	}
	return false
}

// @intent match natural-language terms against code identifiers and hyphenated prose using the same collapsed form.
func containsRetrievalTerm(lowerText, term string) bool {
	if term == "" {
		return false
	}
	if strings.Contains(lowerText, term) {
		return true
	}
	if !strings.ContainsAny(lowerText, "_-/.:#") {
		return false
	}
	collapsedTerm := collapseRetrievalText(term)
	if collapsedTerm == term || len(collapsedTerm) < 3 {
		return false
	}
	return strings.Contains(collapseRetrievalText(lowerText), collapsedTerm)
}

// @intent collapse separators so cross_namespace, cross-namespace, and cross namespace can match the same evidence.
func collapseRetrievalText(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	for _, r := range text {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// NodeMatchesTerms는 노드 또는 어노테이션 텍스트가 검색 용어 중 하나라도 일치하는지 확인한다.
// @intent 저장된 노드 또는 어노테이션 텍스트가 검색 용어와 일치하는지 감지한다.
func NodeMatchesTerms(node model.Node, terms []string) bool {
	if TextContainsAnyTerm(node.Name, terms) || TextContainsAnyTerm(node.QualifiedName, terms) || TextContainsAnyTerm(node.FilePath, terms) {
		return true
	}
	ann := node.Annotation
	if ann == nil {
		return false
	}
	if TextContainsAnyTerm(ann.Summary, terms) || TextContainsAnyTerm(ann.Context, terms) || TextContainsAnyTerm(ann.RawText, terms) {
		return true
	}
	for _, tag := range ann.Tags {
		if TextContainsAnyTerm(tag.Name, terms) || TextContainsAnyTerm(tag.Value, terms) {
			return true
		}
	}
	return false
}

// GroupCandidatesByFile은 관련성 순서의 심볼/파일 후보를 안정적인 파일 그룹으로 축소한다.
// @intent 관련성 순서의 심볼/파일 후보를 안정적인 파일 그룹으로 축소하면서 각 파일의 노드 증거를 유지한다.
func GroupCandidatesByFile(nodes []model.Node, limit int) ([]DBFileGroup, []uint) {
	groups := make([]DBFileGroup, 0, min(limit, len(nodes)))
	groupByPath := make(map[string]int)
	nodeIDs := make([]uint, 0, len(nodes))
	seenNodeIDs := make(map[uint]struct{}, len(nodes))
	for _, node := range nodes {
		if !IsRetrievableNodeKind(node.Kind) {
			continue
		}
		filePath := strings.TrimSpace(node.FilePath)
		if filePath == "" {
			continue
		}
		idx, ok := groupByPath[filePath]
		if !ok {
			if len(groups) >= limit {
				continue
			}
			idx = len(groups)
			groupByPath[filePath] = idx
			groups = append(groups, DBFileGroup{FilePath: filePath})
		}
		groups[idx].Nodes = append(groups[idx].Nodes, node)
		if _, seen := seenNodeIDs[node.ID]; !seen {
			seenNodeIDs[node.ID] = struct{}{}
			nodeIDs = append(nodeIDs, node.ID)
		}
	}
	return groups, nodeIDs
}

// BuildDBResult는 그룹화된 DB 검색 히트와 구조화된 어노테이션으로부터 파일 수준 문서 후보를 도출한다.
// @intent score DB-backed doc candidates from structured annotation buckets while preserving backend rank in response order.
func BuildDBResult(group DBFileGroup, annotations map[uint]*model.Annotation, terms []string, index int) ragindex.RetrieveResult {
	score, fieldScores, matchedTerms := ScoreDBFields(group.Nodes, annotations, terms)
	fields := sortedFieldNames(fieldScores)
	if len(fields) == 0 {
		fields = []string{"search"}
	}
	if len(matchedTerms) == 0 {
		matchedTerms = terms
	}
	summary := DBSummary(group.Nodes, annotations)
	return ragindex.RetrieveResult{
		ID:            "file:" + group.FilePath,
		Label:         filepath.Base(group.FilePath),
		Kind:          "file",
		Summary:       summary,
		DocPath:       filepath.ToSlash(filepath.Join("docs", filepath.FromSlash(group.FilePath)+".md")),
		Path:          DBPath(group.FilePath),
		Score:         score,
		MatchedTerms:  matchedTerms,
		MatchedFields: fields,
	}
}

// ScoreDBFields scores DB-backed retrieval evidence using PageIndex-style structured bucket weights.
// @intent rank high-signal annotation buckets above generic text fallback for DB-backed doc retrieval.
func ScoreDBFields(nodes []model.Node, annotations map[uint]*model.Annotation, terms []string) (int, map[string]int, []string) {
	return scoreDBFields(nodes, annotations, terms)
}

// @intent accumulate DB retrieval bucket scores and matched query terms across one file group.
func scoreDBFields(nodes []model.Node, annotations map[uint]*model.Annotation, terms []string) (int, map[string]int, []string) {
	seen := map[string]struct{}{}
	matchedTerms := map[string]struct{}{}
	fieldScores := map[string]int{}
	add := func(field string, points int, term string) {
		if field == "" || points <= 0 {
			return
		}
		fieldScores[field] += points
		if term != "" {
			matchedTerms[term] = struct{}{}
		}
	}
	addOnce := func(field string, points int, term string) {
		key := field + "\x00" + term
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		add(field, points, term)
	}
	for _, node := range nodes {
		lowerName := strings.ToLower(node.Name)
		lowerQN := strings.ToLower(node.QualifiedName)
		lowerPath := strings.ToLower(node.FilePath)
		ann := annotations[node.ID]
		if ann == nil {
			ann = node.Annotation
		}
		for _, term := range terms {
			if term == "" {
				continue
			}
			if lowerName == term {
				addOnce("label", 12, term)
			} else if containsRetrievalTerm(lowerName, term) {
				addOnce("label_contains", 7, term)
			}
			if containsRetrievalTerm(lowerQN, term) {
				addOnce("qualified_name", 4, term)
			}
			if containsRetrievalTerm(lowerPath, term) {
				addOnce("path", 2, term)
			}
			if ann == nil {
				continue
			}
			if containsRetrievalTerm(strings.ToLower(ann.Summary), term) || containsRetrievalTerm(strings.ToLower(ann.Context), term) {
				addOnce("annotation_text", 2, term)
			}
			if containsRetrievalTerm(strings.ToLower(ann.RawText), term) {
				addOnce("generic", 1, term)
			}
			for _, tag := range ann.Tags {
				if !containsRetrievalTerm(strings.ToLower(tag.Value), term) && !containsRetrievalTerm(strings.ToLower(tag.Name), term) {
					continue
				}
				addOnce(dbFieldName(tag.Kind), dbFieldWeight(tag.Kind), term)
			}
		}
	}
	score := 0
	for _, points := range fieldScores {
		score += points
	}
	score += len(matchedTerms) * 10
	return score, fieldScores, sortedTerms(matchedTerms)
}

// @intent map persisted annotation tag kinds to doc retrieval matched_fields names.
func dbFieldName(kind model.TagKind) string {
	if kind == model.TagIndex {
		return "index_summary"
	}
	return string(kind)
}

// @intent assign DB retrieval weights so high-signal annotation buckets outrank generic annotation text.
func dbFieldWeight(kind model.TagKind) int {
	switch kind {
	case model.TagIntent:
		return 7
	case model.TagIndex:
		return 6
	case model.TagDomainRule, model.TagRequires, model.TagEnsures:
		return 5
	case model.TagSideEffect, model.TagMutates:
		return 4
	case model.TagSee:
		return 3
	default:
		return 2
	}
}

// @intent produce stable matched_fields ordering from DB retrieval score buckets.
func sortedFieldNames(fieldScores map[string]int) []string {
	fields := make([]string, 0, len(fieldScores))
	for field := range fieldScores {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	return fields
}

// @intent produce stable matched_terms ordering for DB-backed doc retrieval responses.
func sortedTerms(terms map[string]struct{}) []string {
	out := make([]string, 0, len(terms))
	for term := range terms {
		out = append(out, term)
	}
	sort.Strings(out)
	return out
}

// DBSummary는 그룹화된 DB-backed 노드에서 첫 번째 사용 가능한 어노테이션 요약/태그 값을 선택한다.
// @intent 첫 번째 사용 가능한 어노테이션 요약 또는 태그 값을 간결한 파일 수준 DB-backed 요약으로 선택한다.
func DBSummary(nodes []model.Node, annotations map[uint]*model.Annotation) string {
	for _, node := range nodes {
		ann := annotations[node.ID]
		if ann == nil {
			continue
		}
		if summary := strings.TrimSpace(ann.Summary); summary != "" {
			return summary
		}
		for _, tag := range ann.Tags {
			if value := strings.TrimSpace(tag.Value); value != "" {
				return value
			}
		}
	}
	return ""
}

// DBPath는 정규화된 파일 경로 세그먼트로부터 DB-backed 결과의 비어있지 않은 경로 조각을 생성한다.
// @intent 정규화된 파일 경로 세그먼트로부터 DB-backed 결과의 비어있지 않은 경로 조각을 생성한다.
func DBPath(filePath string) []string {
	parts := strings.Split(filepath.ToSlash(filePath), "/")
	path := make([]string, 0, len(parts)+1)
	path = append(path, "docs")
	for _, part := range parts {
		if part != "" {
			path = append(path, part)
		}
	}
	return path
}

// DBMatches는 그룹화된 DB-backed 노드에 대한 간결한 검색 스타일 증거 항목을 생성한다.
// @intent 그룹화된 DB-backed 노드에 대한 간결한 검색 스타일 증거 항목을 생성한다.
func DBMatches(nodes []model.Node, annotations map[uint]*model.Annotation) []ragindex.SearchResult {
	if len(nodes) == 0 {
		return nil
	}
	matches := make([]ragindex.SearchResult, 0, len(nodes))
	seen := make(map[uint]struct{}, len(nodes))
	for _, node := range nodes {
		if _, ok := seen[node.ID]; ok {
			continue
		}
		seen[node.ID] = struct{}{}
		summary := strings.TrimSpace(node.Name)
		ann := annotations[node.ID]
		if ann == nil {
			ann = node.Annotation
		}
		if ann != nil {
			if s := strings.TrimSpace(ann.Summary); s != "" {
				summary = s
			}
		}
		matches = append(matches, ragindex.SearchResult{
			ID:      node.QualifiedName,
			Label:   node.Name,
			Kind:    string(node.Kind),
			Summary: summary,
			DocPath: filepath.ToSlash(filepath.Join("docs", filepath.FromSlash(node.FilePath)+".md")),
			Path:    DBPath(node.FilePath),
		})
	}
	return matches
}
