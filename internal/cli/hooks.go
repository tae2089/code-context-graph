package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

const hookGuardBegin = "# --- ccg hook begin ---"
const hookGuardEnd = "# --- ccg hook end ---"

func buildHookBody(strict bool) string {
	lint := "ccg lint"
	if strict {
		lint = "ccg lint --strict"
	}
	return "\n" + hookGuardBegin + "\nccg build . && ccg docs && " + lint + "\n" + hookGuardEnd + "\n"
}

func newHooksCmd(_ *Deps) *cobra.Command {
	hooksCmd := &cobra.Command{
		Use:   "hooks",
		Short: "Manage git hooks for automatic code graph updates",
	}
	hooksCmd.AddCommand(newHooksInstallCmd())
	return hooksCmd
}

func newHooksInstallCmd() *cobra.Command {
	var gitDir string
	var lintStrict bool

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install ccg pre-commit git hook",
		Long: `Install a pre-commit hook that runs "ccg build && ccg docs && ccg lint".

Use --strict to block commits when lint finds issues.
If a pre-commit hook already exists, the ccg block is appended (idempotent).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if gitDir == "" {
				gitDir = "."
			}

			hooksDir := filepath.Join(gitDir, ".git", "hooks")
			if _, err := os.Stat(hooksDir); os.IsNotExist(err) {
				return fmt.Errorf(".git/hooks directory not found in %q; is this a git repository?", gitDir)
			}

			hookPath := filepath.Join(hooksDir, "pre-commit")

			existing, err := os.ReadFile(hookPath)
			if err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("read pre-commit hook: %w", err)
			}

			content := string(existing)

			// Idempotency: skip if guard is already present
			if strings.Contains(content, hookGuardBegin) {
				fmt.Fprintf(stdout(cmd), "ccg hook already installed in %s\n", hookPath)
				return nil
			}

			if content == "" {
				content = "#!/bin/sh\n"
			}

			content += buildHookBody(lintStrict)

			if err := os.WriteFile(hookPath, []byte(content), 0o755); err != nil {
				return fmt.Errorf("write pre-commit hook: %w", err)
			}

			fmt.Fprintf(stdout(cmd), "Installed ccg pre-commit hook: %s\n", hookPath)
			return nil
		},
	}

	cmd.Flags().StringVar(&gitDir, "git-dir", "", "Path to the git repository root (default: current directory)")
	cmd.Flags().BoolVar(&lintStrict, "lint-strict", false, "Include --strict on ccg lint (blocks commit on issues)")
	return cmd
}
