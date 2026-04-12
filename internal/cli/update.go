package cli

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/imtaebin/code-context-graph/internal/analysis/incremental"
)

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
				return fmt.Errorf("resolve path: %w", err)
			}

			deps.Logger.Info("incremental update", "dir", absDir)

			files := make(map[string]incremental.FileInfo)
			err = filepath.Walk(absDir, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				if info.IsDir() {
					if skipDirs[info.Name()] {
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
				return fmt.Errorf("walk directory: %w", err)
			}

			ctx := context.Background()
			stats, err := deps.Syncer.Sync(ctx, files)
			if err != nil {
				return fmt.Errorf("incremental sync: %w", err)
			}

			fmt.Fprintf(stdout(cmd), "Update complete: added=%d modified=%d skipped=%d deleted=%d\n",
				stats.Added, stats.Modified, stats.Skipped, stats.Deleted)

			return nil
		},
	}

	return cmd
}
