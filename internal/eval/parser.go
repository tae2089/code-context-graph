// @index Golden corpus loading and normalization helpers for parser evaluation.
package eval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/tae2089/code-context-graph/internal/model"
)

// @intent load every language-specific golden corpus file from the eval corpus directory tree.
func LoadGoldenDir(dir string) ([]GoldenCorpus, error) {
	var corpora []GoldenCorpus
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		langDir := filepath.Join(dir, entry.Name())
		files, err := os.ReadDir(langDir)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".golden.json") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(langDir, f.Name()))
			if err != nil {
				return nil, err
			}
			var c GoldenCorpus
			if err := json.Unmarshal(data, &c); err != nil {
				return nil, err
			}
			if c.Language == "" {
				c.Language = entry.Name()
			}
			corpora = append(corpora, c)
		}
	}
	return corpora, nil
}

// @intent persist normalized parser output as a golden snapshot for future comparisons.
func WriteGolden(path string, corpus GoldenCorpus) error {
	data, err := json.MarshalIndent(corpus, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// @intent project eval nodes into stable comparison keys for set-based metrics.
func NodeKeys(nodes []EvalNode) []string {
	keys := make([]string, len(nodes))
	for i, n := range nodes {
		keys[i] = n.Key()
	}
	return keys
}

// @intent project eval edges into stable comparison keys for set-based metrics.
func EdgeKeys(edges []EvalEdge) []string {
	keys := make([]string, len(edges))
	for i, e := range edges {
		keys[i] = e.Key()
	}
	return keys
}

// @intent summarize parser accuracy for one language corpus by comparing expected and actual nodes and edges.
func CompareCorpus(expected, actual GoldenCorpus) LanguageReport {
	nodeMetrics := ComputeClassification(NodeKeys(expected.Nodes), NodeKeys(actual.Nodes))
	edgeMetrics := ComputeClassification(EdgeKeys(expected.Edges), EdgeKeys(actual.Edges))
	return LanguageReport{
		Language:    expected.Language,
		NodeMetrics: nodeMetrics,
		EdgeMetrics: edgeMetrics,
		Files:       1,
	}
}

// @intent normalize parsed graph nodes into corpus-stable eval records independent of absolute paths.
func NormalizeNodes(nodes []model.Node, baseDir string) []EvalNode {
	out := make([]EvalNode, 0, len(nodes))
	for _, n := range nodes {
		if n.Kind == model.NodeKindFile {
			continue
		}
		file := n.FilePath
		if baseDir != "" {
			file, _ = filepath.Rel(baseDir, file)
		}
		out = append(out, EvalNode{
			ID:        n.QualifiedName,
			Kind:      string(n.Kind),
			Name:      n.Name,
			File:      file,
			StartLine: n.StartLine,
			EndLine:   n.EndLine,
		})
	}
	return out
}

// @intent normalize parsed graph edges into corpus-stable eval records keyed by qualified names.
func NormalizeEdges(edges []model.Edge, nodes []model.Node) []EvalEdge {
	out := make([]EvalEdge, 0, len(edges))
	nodesByQName := make(map[string]model.Node, len(nodes))
	for _, n := range nodes {
		nodesByQName[n.QualifiedName] = n
	}
	for _, e := range edges {
		from, to := normalizeEdgeEndpoints(e, nodesByQName)
		if from == "" || to == "" {
			continue
		}
		out = append(out, EvalEdge{
			Kind: string(e.Kind),
			From: from,
			To:   to,
		})
	}
	return out
}

// normalizeEdgeEndpoints reconstructs stable edge endpoints from parser fingerprints.
// @intent eval용 edge 비교에서 파일 경로와 fingerprint를 corpus-stable 식별자로 바꾼다.
func normalizeEdgeEndpoints(edge model.Edge, nodesByQName map[string]model.Node) (string, string) {
	parts := strings.Split(edge.Fingerprint, ":")
	if len(parts) < 2 {
		return "", ""
	}

	kind := parts[0]
	switch kind {
	case string(model.EdgeKindContains):
		if target, ok := containsTarget(edge); ok {
			return edge.FilePath, target
		}
	case string(model.EdgeKindCalls), string(model.EdgeKindFallbackCalls), string(model.EdgeKindImportsFrom):
		if len(parts) >= 4 {
			if kind == string(model.EdgeKindImportsFrom) {
				if target, ok := importsFromTarget(edge); ok {
					return edge.FilePath, target
				}
				return edge.FilePath, ""
			}
			// fallback_calls should be compared and normalized identically to calls.
			if target, ok := callsTarget(edge); ok {
				from := resolveParserStageOwner(edge, nodesByQName)
				return from, target
			}
		}
	case string(model.EdgeKindTestedBy):
		if len(parts) >= 4 {
			from := parts[len(parts)-1]
			to := strings.Join(parts[2:len(parts)-1], ":")
			if from == "" || to == "" {
				return "", ""
			}
			return from, to
		}
	case string(model.EdgeKindImplements):
		if len(parts) >= 4 {
			prefix := "implements:" + edge.FilePath + ":"
			if !strings.HasPrefix(edge.Fingerprint, prefix) {
				return "", ""
			}
			rest := strings.TrimPrefix(edge.Fingerprint, prefix)
			from, to, ok := strings.Cut(rest, ":")
			if !ok {
				return "", ""
			}
			if from == "" || to == "" {
				return "", ""
			}
			return from, to
		}
	case string(model.EdgeKindInherits):
		from, to, ok := model.ParseInheritsFingerprint(edge.FilePath, edge.Fingerprint)
		if ok && from != "" && to != "" {
			return from, to
		}
	}
	return "", ""
}

// importsFromTarget extracts the import target from an imports_from fingerprint.
// @intent import edge fingerprint를 eval 비교에 필요한 target 문자열로 복원한다.
func importsFromTarget(edge model.Edge) (string, bool) {
	prefix := "imports_from:" + edge.FilePath + ":"
	if !strings.HasPrefix(edge.Fingerprint, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(edge.Fingerprint, prefix)
	idx := strings.LastIndex(rest, ":")
	if idx < 0 {
		return "", false
	}
	target := rest[:idx]
	return target, target != ""
}

// callsTarget extracts the callee target from a calls or fallback_calls fingerprint.
// @intent call-like edge fingerprint에서 target과 trailing line 구문이 유효한지 함께 확인한다.
func callsTarget(edge model.Edge) (string, bool) {
	var prefix string
	switch edge.Kind {
	case model.EdgeKindCalls:
		prefix = "calls:" + edge.FilePath + ":"
	case model.EdgeKindFallbackCalls:
		prefix = "fallback_calls:" + edge.FilePath + ":"
	default:
		return "", false
	}
	if !strings.HasPrefix(edge.Fingerprint, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(edge.Fingerprint, prefix)
	idx := strings.LastIndex(rest, ":")
	if idx < 0 {
		return "", false
	}
	target := rest[:idx]
	if target == "" {
		return "", false
	}
	if _, err := parseTrailingLine(rest[idx+1:]); err != nil {
		return "", false
	}
	return target, true
}

// containsTarget extracts the contained symbol target from a contains fingerprint.
// @intent contains edge fingerprint를 eval 비교용 대상 심볼 이름으로 복원한다.
func containsTarget(edge model.Edge) (string, bool) {
	prefix := "contains:" + edge.FilePath + ":"
	if !strings.HasPrefix(edge.Fingerprint, prefix) {
		return "", false
	}
	return strings.TrimPrefix(edge.Fingerprint, prefix), true
}

// parseTrailingLine parses the trailing source line segment embedded in a fingerprint.
// @intent malformed fingerprint를 조기에 배제해 eval edge 비교 안정성을 높인다.
func parseTrailingLine(s string) (int, error) {
	if s == "" {
		return 0, os.ErrInvalid
	}
	line := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, os.ErrInvalid
		}
		line = line*10 + int(r-'0')
	}
	if line <= 0 {
		return 0, os.ErrInvalid
	}
	return line, nil
}

// resolveParserStageOwner picks the best owner node for a parser-stage edge.
// @intent line 정보 또는 파일 내 첫 심볼을 이용해 parser fingerprint의 from endpoint를 정한다.
func resolveParserStageOwner(edge model.Edge, nodesByQName map[string]model.Node) string {
	if edge.Line > 0 {
		if owner := resolveOwnerByLine(edge.FilePath, edge.Line, nodesByQName); owner != "" {
			return owner
		}
	}
	for _, n := range nodesByQName {
		if n.FilePath == edge.FilePath && n.Kind != model.NodeKindFile {
			return n.QualifiedName
		}
	}
	return ""
}

// resolveOwnerByLine finds the narrowest symbol spanning a given file line.
// @intent parser-stage edge가 속한 가장 구체적인 owner 심볼을 line 범위로 찾는다.
func resolveOwnerByLine(filePath string, line int, nodesByQName map[string]model.Node) string {
	var best model.Node
	bestFound := false
	for _, n := range nodesByQName {
		if n.FilePath != filePath || n.Kind == model.NodeKindFile {
			continue
		}
		if n.StartLine <= line && line <= n.EndLine {
			if !bestFound || span(n) < span(best) || (span(n) == span(best) && n.StartLine > best.StartLine) {
				best = n
				bestFound = true
			}
		}
	}
	if bestFound {
		return best.QualifiedName
	}
	return ""
}

// span returns the inclusive line span width for a node.
// @intent owner 선택 시 더 좁은 범위의 심볼을 우선하도록 비교 지표를 제공한다.
func span(n model.Node) int {
	if n.EndLine < n.StartLine {
		return 0
	}
	return n.EndLine - n.StartLine
}
