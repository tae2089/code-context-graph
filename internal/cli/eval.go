package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/eval"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/parse/treesitter"
)

func newEvalCmd(deps *Deps) *cobra.Command {
	var corpusDir string
	var suite string
	var format string
	var update bool

	cmd := &cobra.Command{
		Use:   "eval",
		Short: "Evaluate parser accuracy and search quality against golden corpus",
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := eval.RunOptions{
				CorpusDir: corpusDir,
				Suite:     suite,
				Format:    format,
				Update:    update,
				Walkers:   extToLangWalkers(deps.Walkers),
				Writer:    stdout(cmd),
			}

			if (suite == "all" || suite == "search") && deps.DB != nil && deps.SearchBackend != nil {
				opts.SearchFn = makeSearchFn(deps)
			}

			ctx := context.Background()
			_, err := eval.Run(ctx, opts)
			if err != nil {
				return trace.Wrap(err, "eval")
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&corpusDir, "corpus", "testdata/eval", "Path to golden corpus directory")
	cmd.Flags().StringVar(&suite, "suite", "all", "Evaluation suite: all, parser, search")
	cmd.Flags().StringVar(&format, "format", "table", "Output format: table, json")
	cmd.Flags().BoolVar(&update, "update", false, "Update golden files from current parser output (parser suite only)")

	return cmd
}

func makeSearchFn(deps *Deps) eval.SearchFunc {
	return func(query string, limit int) ([]string, error) {
		if deps.DB == nil || deps.SearchBackend == nil {
			return nil, fmt.Errorf("database not initialized for search eval")
		}
		nodes, err := deps.SearchBackend.Query(context.Background(), deps.DB, query, limit)
		if err != nil {
			return nil, err
		}
		return nodeToKeys(nodes), nil
	}
}

// extToLangWalkers converts extension-keyed walkers (e.g. ".go") to language-keyed (e.g. "go").
func extToLangWalkers(walkers map[string]*treesitter.Walker) map[string]*treesitter.Walker {
	if walkers == nil {
		return nil
	}
	extToLang := map[string]string{
		".go": "go", ".py": "python", ".ts": "typescript", ".tsx": "typescript",
		".java": "java", ".rb": "ruby", ".js": "javascript", ".jsx": "javascript",
		".c": "c", ".cpp": "cpp", ".rs": "rust", ".kt": "kotlin", ".php": "php",
		".lua": "lua", ".luau": "lua",
	}
	result := make(map[string]*treesitter.Walker)
	for ext, w := range walkers {
		if lang, ok := extToLang[ext]; ok {
			result[lang] = w
		}
	}
	return result
}

func nodeToKeys(nodes []model.Node) []string {
	keys := make([]string, len(nodes))
	for i, n := range nodes {
		keys[i] = string(n.Kind) + ":" + n.Name + "@" + n.FilePath
	}
	return keys
}
