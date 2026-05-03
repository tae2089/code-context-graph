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
func NormalizeEdges(edges []model.Edge, nodeMap map[uint]string) []EvalEdge {
	out := make([]EvalEdge, 0, len(edges))
	for _, e := range edges {
		from := nodeMap[e.FromNodeID]
		to := nodeMap[e.ToNodeID]
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
