package cli

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tae2089/trace"

	"github.com/imtaebin/code-context-graph/internal/analysis/incremental"
	"github.com/imtaebin/code-context-graph/internal/pathutil"
)

// newUpdateCmd creates the incremental graph sync command.
// @intent 변경 파일만 해시 기반으로 수집해 증분 그래프 동기화를 수행한다.
// @requires deps.Syncer와 deps.Walkers가 초기화되어 있어야 한다.
// @sideEffect 파일 시스템을 읽고 증분 동기화 결과를 저장소에 반영한다.
func newUpdateCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update [directory]",
		Short: "Incrementally sync changed files into the code graph",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) > 0 {
				dir = args[0]
			}

			absDir, err := filepath.Abs(dir)
			if err != nil {
				return trace.Wrap(err, "resolve path")
			}

			deps.Logger.Info("incremental update", "dir", absDir)

			files := make(map[string]incremental.FileInfo)
			err = filepath.Walk(absDir, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				if info.IsDir() {
					if pathutil.ShouldSkipDir(info.Name()) {
						return filepath.SkipDir
					}
					return nil
				}

				ext := strings.ToLower(filepath.Ext(path))
				if _, ok := deps.Walkers[ext]; !ok {
					return nil
				}

				content, err := os.ReadFile(path)
				if err != nil {
					deps.Logger.Warn("skip unreadable file", "path", path, "error", err)
					return nil
				}

				relPath, _ := filepath.Rel(absDir, path)
				hash := fmt.Sprintf("%x", sha256.Sum256(content))
				files[relPath] = incremental.FileInfo{Hash: hash, Content: content}

				return nil
			})
			if err != nil {
				return trace.Wrap(err, "walk directory")
			}

			ctx := context.Background()
			stats, err := deps.Syncer.Sync(ctx, files)
			if err != nil {
				return trace.Wrap(err, "incremental sync")
			}

			fmt.Fprintf(stdout(cmd), "Update complete: added=%d modified=%d skipped=%d deleted=%d\n",
				stats.Added, stats.Modified, stats.Skipped, stats.Deleted)

			return nil
		},
	}

	return cmd
}
