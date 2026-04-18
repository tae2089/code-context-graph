package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// VersionInfo holds build-time version metadata injected via ldflags.
// @intent 빌드 시 주입된 버전 정보를 구조체로 묶어 CLI 출력에 활용한다.
type VersionInfo struct {
	Version string
	Commit  string
	Date    string
}

// newVersionCmd creates the "version" subcommand that prints build info.
// @intent 빌드 메타데이터를 사람이 읽기 좋은 형식으로 출력한다.
func newVersionCmd(deps *Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print build version information",
		RunE: func(cmd *cobra.Command, args []string) error {
			v := deps.Version
			version := v.Version
			if version == "" {
				version = "dev"
			}
			commit := v.Commit
			if commit == "" {
				commit = "unknown"
			}
			date := v.Date
			if date == "" {
				date = "unknown"
			}
			fmt.Fprintf(stdout(cmd), "ccg %s (commit: %s, built: %s)\n", version, commit, date)
			return nil
		},
	}
}
