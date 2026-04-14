package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tae2089/trace"
)

const hookGuardBegin = "# --- ccg hook begin ---"
const hookGuardEnd = "# --- ccg hook end ---"

// buildHookBody builds the guarded ccg pre-commit hook block.
// @intent 설치된 훅 안에 ccg 전용 블록을 일관된 형태로 삽입한다.
// @domainRule strict 모드에서는 lint 단계가 반드시 --strict로 실행된다.
func buildHookBody(strict bool) string {
	lint := "ccg lint"
	if strict {
		lint = "ccg lint --strict"
	}
	return "\n" + hookGuardBegin + "\nccg build . && ccg docs && " + lint + "\n" + hookGuardEnd + "\n"
}

// newHooksCmd creates the top-level hooks command group.
// @intent git hook 관리 하위 명령을 하나의 네임스페이스 아래로 묶는다.
func newHooksCmd(_ *Deps) *cobra.Command {
	hooksCmd := &cobra.Command{
		Use:   "hooks",
		Short: "Manage git hooks for automatic code graph updates",
	}
	hooksCmd.AddCommand(newHooksInstallCmd())
	return hooksCmd
}

// newHooksInstallCmd creates the pre-commit hook installer command.
// @intent 커밋 전에 그래프 빌드·문서 생성·lint를 자동 실행하는 훅을 설치한다.
// @sideEffect .git/hooks/pre-commit 파일을 읽고 필요하면 생성 또는 갱신한다.
// @ensures 동일한 ccg 훅 블록이 중복 삽입되지 않는다.
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
				return trace.New(fmt.Sprintf(".git/hooks directory not found in %q; is this a git repository?", gitDir))
			}

			hookPath := filepath.Join(hooksDir, "pre-commit")

			existing, err := os.ReadFile(hookPath)
			if err != nil && !os.IsNotExist(err) {
				return trace.Wrap(err, "read pre-commit hook")
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
				return trace.Wrap(err, "write pre-commit hook")
			}

			fmt.Fprintf(stdout(cmd), "Installed ccg pre-commit hook: %s\n", hookPath)
			return nil
		},
	}

	cmd.Flags().StringVar(&gitDir, "git-dir", "", "Path to the git repository root (default: current directory)")
	cmd.Flags().BoolVar(&lintStrict, "lint-strict", false, "Include --strict on ccg lint (blocks commit on issues)")
	return cmd
}
