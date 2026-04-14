package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/tae2089/trace"
)

const defaultConfig = `db:
  driver: sqlite
  dsn: ccg.db

exclude:
  - vendor
  - "*.pb.go"
  - "*_test.go"

docs:
  out: docs
`

// newInitCmd creates the default config scaffolding command.
// @intent 프로젝트 또는 사용자 범위에 기본 .ccg 설정 파일을 생성한다.
// @domainRule --project와 --user는 동시에 사용할 수 없다.
// @sideEffect 설정 디렉터리를 만들고 .ccg.yaml 파일을 기록한다.
// @ensures 기존 설정 파일이 있으면 덮어쓰지 않는다.
func newInitCmd(_ *Deps) *cobra.Command {
	var (
		project    bool
		user       bool
		configHome string
	)

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Generate a default .ccg.yaml config file",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if project && user {
				return fmt.Errorf("cannot use both --project and --user")
			}

			dest, err := resolveInitDest(user, configHome)
			if err != nil {
				return err
			}

			if _, err := os.Stat(dest); err == nil {
				return fmt.Errorf("config file already exists: %s", dest)
			}

			if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
				return trace.Wrap(err, "create config directory")
			}

			if err := os.WriteFile(dest, []byte(defaultConfig), 0644); err != nil {
				return trace.Wrap(err, "write config file")
			}

			fmt.Fprintf(stdout(cmd), "Created %s\n", dest)
			return nil
		},
	}

	cmd.Flags().BoolVar(&project, "project", false, "Create .ccg.yaml in current directory (default)")
	cmd.Flags().BoolVar(&user, "user", false, "Create .ccg.yaml in ~/.config/ccg/")
	cmd.Flags().StringVar(&configHome, "config-home", "", "Override home directory for --user (testing)")
	cmd.Flags().MarkHidden("config-home")

	return cmd
}

// resolveInitDest resolves the output path for ccg init.
// @intent init 명령이 어느 위치에 설정 파일을 만들어야 하는지 결정한다.
// @param configHome 테스트에서 사용자 홈 경로를 대체할 때만 사용한다.
// @return 생성 대상 .ccg.yaml 절대 또는 사용자 설정 경로를 반환한다.
func resolveInitDest(user bool, configHome string) (string, error) {
	if !user {
		return filepath.Abs(".ccg.yaml")
	}

	base := configHome
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", trace.Wrap(err, "determine home directory")
		}
		base = home
	}
	return filepath.Join(base, ".config", "ccg", ".ccg.yaml"), nil
}
