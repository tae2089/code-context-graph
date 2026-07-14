package cli

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/app/analyze"
	"github.com/tae2089/code-context-graph/internal/app/docs"
	"github.com/tae2089/code-context-graph/internal/app/ingest"
	"github.com/tae2089/code-context-graph/internal/app/search/retrieval"
	"github.com/tae2089/code-context-graph/internal/app/wiki"
	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
)

// errDBNotInitialized is returned when a subcommand requires a database
// connection but Deps.DB is nil.
var errDBNotInitialized = trace.New("database not initialized")

const skipDBInitAnnotation = "skipDBInit"

// shouldSkipDBInit checks if the command requires skipping database initialization.
// @intent 특정 커맨드나 플래그 설정에 따라 DB 초기화 단계를 건너뛸지 결정한다.
func shouldSkipDBInit(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		if c.Annotations[skipDBInitAnnotation] == "true" {
			return true
		}
	}
	if flag := cmd.Flags().Lookup("migrate-auto-rules"); flag != nil && flag.Changed {
		return true
	}
	return false
}

// Deps holds shared dependencies injected into all subcommands.
// @intent 중앙 CLI 초기화 단계에서 만든 런타임 의존성을 하위 명령에 전달한다.
type Deps struct {
	Logger       *slog.Logger
	Store        ingest.GraphStore
	UnitOfWork   ingest.UnitOfWork
	Search       ingest.SearchWriter
	SearchReader retrieval.CandidateSearcher
	Statistics   analyze.StatisticsReader
	Docs         docs.Repository
	Wiki         wiki.Repository
	Walkers      map[string]ingest.Parser
	Syncer       ingest.IncrementalSyncer
	ServeFunc    func(cfg ServeConfig) error
	InitFunc     func(dbDriver, dsn string) error
	MigrateFunc  func(cfg MigrateConfig) error
	CleanupFunc  func()
	Version      VersionInfo
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
			if err := viper.ReadInConfig(); err != nil {
				var notFound viper.ConfigFileNotFoundError
				if !errors.As(err, &notFound) && !errors.Is(err, os.ErrNotExist) {
					return trace.Wrap(err, "read config")
				}
			}

			// 3. Initialize Database if InitFunc is provided
			if deps.InitFunc != nil && !shouldSkipDBInit(cmd) {
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
	rootCmd.PersistentFlags().String("db-dsn", "ccg.db", "Database connection string (default local SQLite ccg.db auto-migrates only when schema is missing)")
	rootCmd.PersistentFlags().String("namespace", requestctx.DefaultNamespace, "Namespace for data isolation (e.g. --namespace backend)")

	// Bind flags to viper
	_ = viper.BindPFlag("db.driver", rootCmd.PersistentFlags().Lookup("db-driver"))
	_ = viper.BindPFlag("db.dsn", rootCmd.PersistentFlags().Lookup("db-dsn"))
	_ = viper.BindPFlag("namespace", rootCmd.PersistentFlags().Lookup("namespace"))

	// Also explicitly bind env vars just in case AutomaticEnv needs a hint
	_ = viper.BindEnv("db.driver", "CCG_DB_DRIVER")
	_ = viper.BindEnv("db.dsn", "CCG_DB_DSN")
	_ = viper.BindEnv("migrations.dir", "CCG_MIGRATIONS_DIR")

	rootCmd.AddCommand(
		newInitCmd(deps),
		newMigrateCmd(deps),
		newBuildCmd(deps),
		newUpdateCmd(deps),
		newStatusCmd(deps),
		newSearchCmd(deps),
		newServeCmd(deps),
		newDocsCmd(deps),
		newHooksCmd(deps),
		newLintCmd(deps),
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

// resolveNamespace returns the effective namespace for a command, reading through
// viper instead of cmd.Flags() so a namespace set only in the config file is not
// masked by the --namespace flag's default value.
// @intent config의 namespace 설정이 --namespace 플래그 기본값에 가려지지 않도록 우선순위대로 해석한다.
// @ensures 우선순위는 명시적 --namespace 플래그 > CCG_NAMESPACE 환경변수 > config namespace > 기본값(default) 순이다.
func resolveNamespace(cmd *cobra.Command) string {
	if flag := cmd.Flags().Lookup("namespace"); flag != nil && flag.Changed {
		return flag.Value.String()
	}
	if ns := viper.GetString("namespace"); ns != "" {
		return ns
	}
	return requestctx.DefaultNamespace
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

// resolveIncludePaths merges include paths from the config file and the command-line flag.
// @intent 전역 설정과 CLI로 입력받은 포함 경로들을 하나로 병합하여 분석 대상을 결정한다.
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

// resolveMaxFileBytes returns the maximum file size in bytes to be parsed.
// @intent 분석 대상 단일 파일의 최대 크기 제한을 설정 파일 혹은 플래그로부터 결정한다.
func resolveMaxFileBytes(flagValue int64) int64 {
	if flagValue != 0 {
		return flagValue
	}
	return viper.GetInt64("parse.max_file_bytes")
}

// resolveMaxTotalParsedBytes returns the maximum total bytes to be parsed.
// @intent 전체 분석 과정에서 파싱할 소스 코드의 총량(Byte) 제한을 결정한다.
func resolveMaxTotalParsedBytes(flagValue int64) int64 {
	if flagValue != 0 {
		return flagValue
	}
	return viper.GetInt64("parse.max_total_parsed_bytes")
}
