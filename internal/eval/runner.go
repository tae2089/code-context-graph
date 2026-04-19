package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/imtaebin/code-context-graph/internal/model"
	"github.com/imtaebin/code-context-graph/internal/parse/treesitter"
)

type RunOptions struct {
	CorpusDir string
	Suite     string
	Format    string
	Update    bool
	Walkers   map[string]*treesitter.Walker
	SearchFn  SearchFunc
	Writer    io.Writer
}

func Run(ctx context.Context, opts RunOptions) (*Report, error) {
	report := &Report{Suite: opts.Suite}

	if opts.Suite == "all" || opts.Suite == "parser" {
		if err := runParserEval(ctx, opts, report); err != nil {
			return nil, fmt.Errorf("parser eval: %w", err)
		}
	}

	if opts.Suite == "all" || opts.Suite == "search" {
		if err := runSearchEval(ctx, opts, report); err != nil {
			return nil, fmt.Errorf("search eval: %w", err)
		}
	}

	if opts.Format == "json" {
		return report, writeJSON(opts.Writer, report)
	}
	return report, writeTable(opts.Writer, report)
}

func runParserEval(ctx context.Context, opts RunOptions, report *Report) error {
	if opts.Update {
		return runParserUpdate(ctx, opts)
	}
	return runParserCompare(ctx, opts, report)
}

func runParserUpdate(ctx context.Context, opts RunOptions) error {
	entries, err := os.ReadDir(opts.CorpusDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		lang := entry.Name()
		walker, ok := opts.Walkers[lang]
		if !ok {
			continue
		}
		langDir := filepath.Join(opts.CorpusDir, lang)
		files, err := os.ReadDir(langDir)
		if err != nil {
			return err
		}
		for _, f := range files {
			if f.IsDir() || strings.HasSuffix(f.Name(), ".golden.json") {
				continue
			}
			srcFile := filepath.Join(langDir, f.Name())
			src, err := readFileContent(srcFile)
			if err != nil {
				return fmt.Errorf("read source %s: %w", srcFile, err)
			}
			nodes, edges, _, err := walker.ParseWithComments(ctx, srcFile, src)
			if err != nil {
				return fmt.Errorf("parse %s: %w", srcFile, err)
			}
			actualNodes := NormalizeNodes(nodes, langDir)
			nodeMap := buildNodeMap(nodes)
			actualEdges := NormalizeEdges(edges, nodeMap)

			golden := GoldenCorpus{
				Language: lang,
				File:     f.Name(),
				Nodes:    actualNodes,
				Edges:    actualEdges,
			}
			goldenPath := filepath.Join(langDir, f.Name()+".golden.json")
			if err := WriteGolden(goldenPath, golden); err != nil {
				return fmt.Errorf("write golden %s: %w", goldenPath, err)
			}
		}
	}
	return nil
}

func runParserCompare(ctx context.Context, opts RunOptions, report *Report) error {
	corpora, err := LoadGoldenDir(opts.CorpusDir)
	if err != nil {
		return err
	}

	byLang := make(map[string][]GoldenCorpus)
	for _, c := range corpora {
		byLang[c.Language] = append(byLang[c.Language], c)
	}

	for lang, goldens := range byLang {
		walker, ok := opts.Walkers[lang]
		if !ok {
			continue
		}

		var allExpectedNodes, allActualNodes []string
		var allExpectedEdges, allActualEdges []string
		fileCount := 0

		for _, golden := range goldens {
			langDir := filepath.Join(opts.CorpusDir, lang)
			srcFile := filepath.Join(langDir, golden.File)
			src, err := readFileContent(srcFile)
			if err != nil {
				return fmt.Errorf("read source %s: %w", srcFile, err)
			}

			nodes, edges, _, err := walker.ParseWithComments(ctx, srcFile, src)
			if err != nil {
				return fmt.Errorf("parse %s: %w", srcFile, err)
			}

			actualNodes := NormalizeNodes(nodes, langDir)
			nodeMap := buildNodeMap(nodes)
			actualEdges := NormalizeEdges(edges, nodeMap)

			allExpectedNodes = append(allExpectedNodes, NodeKeys(golden.Nodes)...)
			allActualNodes = append(allActualNodes, NodeKeys(actualNodes)...)
			allExpectedEdges = append(allExpectedEdges, EdgeKeys(golden.Edges)...)
			allActualEdges = append(allActualEdges, EdgeKeys(actualEdges)...)
			fileCount++
		}

		report.Languages = append(report.Languages, LanguageReport{
			Language:    lang,
			NodeMetrics: ComputeClassification(allExpectedNodes, allActualNodes),
			EdgeMetrics: ComputeClassification(allExpectedEdges, allActualEdges),
			Files:       fileCount,
		})
	}

	return nil
}

func runSearchEval(_ context.Context, opts RunOptions, report *Report) error {
	queryPath := fmt.Sprintf("%s/queries.json", opts.CorpusDir)
	qc, err := LoadQueryCorpus(queryPath)
	if err != nil {
		return nil
	}

	if opts.SearchFn == nil {
		return fmt.Errorf("search function not configured")
	}

	sr, err := EvaluateQueries(qc.Queries, opts.SearchFn)
	if err != nil {
		return err
	}
	report.Search = &sr
	return nil
}

func buildNodeMap(nodes []model.Node) map[uint]string {
	m := make(map[uint]string, len(nodes))
	for _, n := range nodes {
		m[n.ID] = n.QualifiedName
	}
	return m
}

func readFileContent(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func writeJSON(w io.Writer, report *Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

func writeTable(w io.Writer, report *Report) error {
	if len(report.Languages) > 0 {
		fmt.Fprintf(w, "=== Parser Evaluation ===\n\n")
		fmt.Fprintf(w, "%-14s %6s %8s %8s %8s   %8s %8s %8s\n",
			"Language", "Files", "Node P", "Node R", "Node F1", "Edge P", "Edge R", "Edge F1")
		fmt.Fprintf(w, "%s\n", strings.Repeat("─", 82))

		for _, lr := range report.Languages {
			fmt.Fprintf(w, "%-14s %6d %8.4f %8.4f %8.4f   %8.4f %8.4f %8.4f\n",
				lr.Language, lr.Files,
				lr.NodeMetrics.Precision, lr.NodeMetrics.Recall, lr.NodeMetrics.F1,
				lr.EdgeMetrics.Precision, lr.EdgeMetrics.Recall, lr.EdgeMetrics.F1)
		}
		fmt.Fprintln(w)
	}

	if report.Search != nil {
		fmt.Fprintf(w, "=== Search Evaluation ===\n\n")
		fmt.Fprintf(w, "Queries: %d\n", report.Search.QueriesTotal)
		fmt.Fprintf(w, "P@1:     %.4f\n", report.Search.AvgPAt1)
		fmt.Fprintf(w, "P@3:     %.4f\n", report.Search.AvgPAt3)
		fmt.Fprintf(w, "P@5:     %.4f\n", report.Search.AvgPAt5)
		fmt.Fprintf(w, "R@5:     %.4f\n", report.Search.AvgRecallAt5)
		fmt.Fprintf(w, "MRR:     %.4f\n", report.Search.AvgMRR)
		fmt.Fprintf(w, "nDCG@5:  %.4f\n", report.Search.AvgNDCGAt5)
	}

	return nil
}
