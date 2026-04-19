package cli

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/tae2089/trace"
	"gorm.io/gorm"

	"github.com/imtaebin/code-context-graph/internal/analysis/incremental"
	"github.com/imtaebin/code-context-graph/internal/parse/treesitter"
	"github.com/imtaebin/code-context-graph/internal/store"
	"github.com/imtaebin/code-context-graph/internal/store/search"
)

// errDBNotInitialized is returned when a subcommand requires a database
// connection but Deps.DB is nil.
var errDBNotInitialized = trace.New("database not initialized")

// Deps holds shared dependencies injected into all subcommands.
// @intent 중앙 CLI 초기화 단계에서 만든 런타임 의존성을 하위 명령에 전달한다.
type Deps struct {
	Logger        *slog.Logger
	DB            *gorm.DB
	Store         store.GraphStore
	SearchBackend search.Backend
	Walkers       map[string]*treesitter.Walker
	Syncer        *incremental.Syncer
	ServeFunc     func(cfg ServeConfig) error
	InitFunc      func(dbDriver, dsn string) error
	CleanupFunc   func()
	Version       VersionInfo
}

// NewRootCmd creates the root cobra command with all subcommands attached.
// @intent 공통 초기화와 서브커맨드 구성을 한곳에 모아 ccg CLI 진입점을 만든다.
// @sideEffect 환경 변수와 설정 파일을 읽고 InitFunc가 있으면 DB 초기화를 트리거한다.
// @mutates deps.Logger, 전역 slog 기본 로거
func NewRootCmd(deps *Deps) *cobra.Command {
	if deps == nil {
		deps = &Deps{}
	}
	if deps.Logger == nil {
		deps.Logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}

	var logLevel string
	var logJSON bool
	var cfgFile string

	rootCmd := &cobra.Command{
		Use:           "ccg",
		Short:         "code-context-graph — local code analysis tool",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// 1. Logger Setup
			level := parseLogLevel(logLevel)
			opts := &slog.HandlerOptions{Level: level}

			w := cmd.ErrOrStderr()
			var handler slog.Handler
			if logJSON {
				handler = slog.NewJSONHandler(w, opts)
			} else {
				handler = slog.NewTextHandler(w, opts)
			}

			deps.Logger = slog.New(handler)
			slog.SetDefault(deps.Logger)

			// 2. Viper Setup — config file, then env vars, then flags
			viper.AutomaticEnv()
			viper.SetEnvPrefix("CCG") // E.g., CCG_DB_DRIVER
			if cfgFile != "" {
				viper.SetConfigFile(cfgFile)
			} else {
				viper.SetConfigName(".ccg")
				viper.SetConfigType("yaml")
				viper.AddConfigPath(".") // project-local (highest priority)
				if home, err := os.UserHomeDir(); err == nil {
					viper.AddConfigPath(filepath.Join(home, ".config", "ccg")) // global fallback
				}
			}
			// Silently ignore missing config file; all settings have defaults.
			_ = viper.ReadInConfig()

			// 3. Initialize Database if InitFunc is provided
			if deps.InitFunc != nil {
				driver := viper.GetString("db.driver")
				dsn := viper.GetString("db.dsn")
				if err := deps.InitFunc(driver, dsn); err != nil {
					return trace.Wrap(err, "initialize database")
				}
			}

			return nil
		},
	}

	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "Log level: debug, info, warn, error")
	rootCmd.PersistentFlags().BoolVar(&logJSON, "log-json", false, "Output logs in JSON format")
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "Config file (default: .ccg.yaml in ./ then ~/.config/ccg/)")

	// Global database configuration flags
	rootCmd.PersistentFlags().String("db-driver", "sqlite", "Database driver (sqlite, postgres)")
	rootCmd.PersistentFlags().String("db-dsn", "ccg.db", "Database connection string")
	rootCmd.PersistentFlags().String("namespace", "", "Namespace for data isolation (e.g. --namespace backend)")

	// Bind flags to viper
	_ = viper.BindPFlag("db.driver", rootCmd.PersistentFlags().Lookup("db-driver"))
	_ = viper.BindPFlag("db.dsn", rootCmd.PersistentFlags().Lookup("db-dsn"))

	// Also explicitly bind env vars just in case AutomaticEnv needs a hint
	_ = viper.BindEnv("db.driver", "CCG_DB_DRIVER")
	_ = viper.BindEnv("db.dsn", "CCG_DB_DSN")

	rootCmd.AddCommand(
		newInitCmd(deps),
		newBuildCmd(deps),
		newUpdateCmd(deps),
		newStatusCmd(deps),
		newSearchCmd(deps),
		newServeCmd(deps),
		newDocsCmd(deps),
		newLanguagesCmd(deps),
		newExampleCmd(deps),
		newTagsCmd(deps),
		newHooksCmd(deps),
		newIndexCmd(deps),
		newLintCmd(deps),
		newRagIndexCmd(deps),
		newVersionCmd(deps),
	)

	return rootCmd
}

// parseLogLevel converts a CLI log level string into slog severity.
// @intent 사용자 입력 로그 레벨을 일관된 slog 레벨로 정규화한다.
func parseLogLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// stdout returns the writer used for normal command output.
// @intent 커맨드 출력 대상 선택을 한곳으로 모아 테스트와 리다이렉션을 쉽게 한다.
func stdout(cmd *cobra.Command) io.Writer {
	return cmd.OutOrStdout()
}

// resolveOutDir returns the effective output directory: if the CLI flag was left
// at its default ("docs"), check viper for a config-level override (docs.out).
// @intent 명시적 플래그를 우선하되 기본값일 때만 설정 파일의 docs.out을 반영한다.
func resolveOutDir(flagValue string) string {
	if flagValue != "docs" {
		return flagValue
	}
	if cfgOut := viper.GetString("docs.out"); cfgOut != "" {
		return cfgOut
	}
	return flagValue
}

// resolveExcludes merges exclude patterns from the config file (viper "exclude"
// key) and the command-line flag, deduplicating nothing — order is config first,
// then flags.
// @intent 전역 설정과 일회성 CLI 제외 패턴을 함께 적용할 수 있게 병합한다.
// @ensures 반환 순서는 항상 config 패턴 다음 flag 패턴이다.
func resolveExcludes(flagPatterns []string) []string {
	cfgPatterns := viper.GetStringSlice("exclude")
	if len(cfgPatterns) == 0 {
		return flagPatterns
	}
	if len(flagPatterns) == 0 {
		return cfgPatterns
	}
	combined := make([]string, 0, len(cfgPatterns)+len(flagPatterns))
	combined = append(combined, cfgPatterns...)
	combined = append(combined, flagPatterns...)
	return combined
}

func resolveIncludePaths(flagPaths []string) []string {
	cfgPaths := viper.GetStringSlice("include_paths")
	if len(cfgPaths) == 0 {
		return flagPaths
	}
	if len(flagPaths) == 0 {
		return cfgPaths
	}
	combined := make([]string, 0, len(cfgPaths)+len(flagPaths))
	combined = append(combined, cfgPaths...)
	combined = append(combined, flagPaths...)
	return combined
}
