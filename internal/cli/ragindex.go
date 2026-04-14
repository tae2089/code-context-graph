package cli

import (
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/tae2089/trace"

	"github.com/imtaebin/code-context-graph/internal/ragindex"
)

// newRagIndexCmd creates the doc-index build command.
// @intent 문서 트리와 커뮤니티 구조를 묶은 vectorless RAG 인덱스를 생성한다.
// @requires deps.DB가 초기화되어 있어야 한다.
// @sideEffect ragindex.Builder를 통해 doc-index.json을 기록하고 문서 상태 경고를 출력한다.
func newRagIndexCmd(deps *Deps) *cobra.Command {
	var outDir string
	var indexDir string
	var projectDesc string

	cmd := &cobra.Command{
		Use:   "rag-index",
		Short: "Build Vectorless RAG index from docs and community structure",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if deps.DB == nil {
				return errDBNotInitialized
			}

			// --desc 플래그가 없으면 viper config에서 읽음
			if projectDesc == "" {
				projectDesc = viper.GetString("rag.description")
			}

			b := &ragindex.Builder{
				DB:          deps.DB,
				OutDir:      resolveOutDir(outDir),
				IndexDir:    indexDir,
				ProjectDesc: projectDesc,
			}

			communities, files, err := b.Build(cmd.Context())
			if err != nil {
				return trace.Wrap(err, "build rag index")
			}

			fmt.Fprintf(stdout(cmd), "Built doc-index: %d communities, %d files\n", communities, files)

			// Warn if docs directory has fewer .md files than indexed
			if files > 0 {
				effectiveOut := resolveOutDir(outDir)
				mdCount := countMDFiles(effectiveOut)
				if mdCount == 0 {
					fmt.Fprintf(stdout(cmd), "Warning: no .md files found in %q. Run 'ccg docs' to generate documentation.\n", effectiveOut)
				} else if mdCount < files {
					fmt.Fprintf(stdout(cmd), "Warning: %d files indexed but only %d .md files found in %q. Run 'ccg docs' to update documentation.\n", files, mdCount, effectiveOut)
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&outDir, "out", "docs", "Documentation directory")
	cmd.Flags().StringVar(&indexDir, "index-dir", ".ccg", "Directory for doc-index.json output")
	cmd.Flags().StringVar(&projectDesc, "desc", "", "Project description for root node summary")

	return cmd
}

// countMDFiles counts generated per-file markdown docs under dir.
// @intent 문서 인덱싱 결과와 실제 Markdown 산출물 수를 비교하기 위한 기준값을 만든다.
// @sideEffect 디렉터리를 순회하고 읽기 실패 항목을 로그로 남길 수 있다.
func countMDFiles(dir string) int {
	count := 0
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			slog.Warn("failed to walk directory entry", "path", path, trace.SlogError(err))
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".md") && filepath.Base(path) != "index.md" {
			count++
		}
		return nil
	})
	return count
}
