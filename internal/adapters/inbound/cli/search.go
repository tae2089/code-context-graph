package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/app/search/evidence"
	searchrank "github.com/tae2089/code-context-graph/internal/app/search/rank"
	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/pathspec"
)

// newSearchCmd creates the full-text search command.
// @intent 그래프 검색 결과를 빠르게 조회하고 필요 시 경로 접두사로 후처리 필터링한다.
// @requires deps.SearchBackend와 deps.DB가 초기화되어 있어야 한다.
// @sideEffect 검색 결과를 표준 출력으로 기록한다.
func newSearchCmd(deps *Deps) *cobra.Command {
	var limit int
	var offset int
	var pathPrefix string
	var includeWeak bool

	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Full-text search for code nodes",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := args[0]
			if limit <= 0 {
				return fmt.Errorf("limit must be > 0, got %d", limit)
			}
			ctx := cmd.Context()
			ns := resolveNamespace(cmd)
			ctx = requestctx.WithNamespace(ctx, ns)

			// Over-fetch a wider candidate pool so structural reranking can
			// promote good matches FTS ranked below the limit, and so path
			// filtering still leaves up to 'limit' results.
			nodes, err := deps.SearchReader.Query(ctx, query, searchrank.FetchLimit(limit))
			if err != nil {
				return trace.Wrap(err, "search")
			}

			out := stdout(cmd)

			if pathPrefix != "" {
				filtered := nodes[:0]
				for _, n := range nodes {
					if pathspec.HasPathPrefix(n.FilePath, pathPrefix) {
						filtered = append(filtered, n)
					}
				}
				nodes = filtered
			}

			// Rerank the whole pool, not just 'limit' of it: the evidence cut
			// below decides membership, so bounding here would spend slots on
			// candidates that are about to be dropped anyway.
			ranked := searchrank.Rerank(query, nodes, 0)
			list := evidence.Build(query, ranked, evidence.Options{Limit: limit, Offset: offset, IncludeWeak: includeWeak})

			printEvidenceList(out, list, offset)
			return nil
		},
	}

	cmd.Flags().IntVar(&limit, "limit", 10, "Maximum number of files to show; every hit inside a shown file is printed")
	cmd.Flags().IntVar(&offset, "offset", 0, "Skip this many files, to read on from a previous run")
	cmd.Flags().StringVar(&pathPrefix, "path", "", "Filter results to file paths starting with this prefix (e.g. internal/auth)")
	cmd.Flags().BoolVar(&includeWeak, "include-weak", false, "Also show candidates whose name, path, and @intent say nothing about the query")

	return cmd
}

// printEvidenceList writes one result per unindented line and everything else
// indented, so the plain result lines stay as machine-readable as they were.
//
// Hits arrive grouped by file, so a file's lines are contiguous. The grouping
// is not drawn: a header line would break the rule that an unindented line is a
// result, and the file path is already on every line.
//
// @domainRule an unindented line is a result; an indented line is commentary about the line above it.
// @sideEffect writes the whole search answer to out.
// @intent let a reader see why each result is in the list without opening the file.
func printEvidenceList(out io.Writer, list evidence.List, offset int) {
	if len(list.Files) == 0 {
		fmt.Fprintln(out, "No results")
		fmt.Fprintf(out, "    %s\n", list.Note)
		return
	}

	for _, f := range list.Files {
		for _, r := range f.Hits {
			n := r.Node
			fmt.Fprintf(out, "%s\t%s\t%s:%d\n", n.QualifiedName, n.Kind, n.FilePath, n.StartLine)
			// A node with no @intent gets no second line: its name and path are
			// already on the first one, so a label row would repeat what is visible.
			if r.Intent != "" {
				fmt.Fprintf(out, "    %s%s\n", r.Intent, matchedLabels(r.Matched))
			}
		}
	}

	if list.OverflowFiles > 0 {
		noun := "files"
		if list.OverflowFiles == 1 {
			noun = "file"
		}
		fmt.Fprintf(out, "    %d more %s answered this query; add --offset %d to read on.\n",
			list.OverflowFiles, noun, offset+len(list.Files))
	}

	if list.WeakFiltered > 0 {
		noun := "candidates"
		if list.WeakFiltered == 1 {
			noun = "candidate"
		}
		fmt.Fprintf(out, "    %d %s hidden with nothing in the name, path, or @intent to justify them; add --include-weak to see them.\n",
			list.WeakFiltered, noun)
	}
}

// matchedLabels renders the signals a result matched, or nothing at all.
// @intent name the parts of a result the query touched, in one glanceable token.
func matchedLabels(matched []evidence.Match) string {
	if len(matched) == 0 {
		return ""
	}
	names := make([]string, len(matched))
	for i, m := range matched {
		names[i] = string(m)
	}
	return " [" + strings.Join(names, " ") + "]"
}
