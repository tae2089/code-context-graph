package cli

import (
	"io"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gorm.io/gorm"

	"github.com/imtaebin/code-context-graph/internal/analysis/incremental"
	"github.com/imtaebin/code-context-graph/internal/parse/treesitter"
	"github.com/imtaebin/code-context-graph/internal/store"
	"github.com/imtaebin/code-context-graph/internal/store/pgstore"
	"github.com/imtaebin/code-context-graph/internal/store/search"
)

// Deps holds shared dependencies injected into all subcommands.
type Deps struct {
	Logger        *slog.Logger
	DB            *gorm.DB
	Store         store.GraphStore
	SearchBackend search.Backend
	Walkers       map[string]*treesitter.Walker
	Syncer        *incremental.Syncer
	ServeFunc     func(cfg ServeConfig) error
	InitFunc      func(dbDriver, dsn string) error
	PGStore       *pgstore.Store
}

// NewRootCmd creates the root cobra command with all subcommands attached.
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
				viper.AddConfigPath(".")
			}
			// Silently ignore missing config file; all settings have defaults.
			_ = viper.ReadInConfig()

			// 3. Initialize Database if InitFunc is provided
			if deps.InitFunc != nil {
				driver := viper.GetString("db.driver")
				dsn := viper.GetString("db.dsn")
				if err := deps.InitFunc(driver, dsn); err != nil {
					return err
				}
			}

			return nil
		},
	}

	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "Log level: debug, info, warn, error")
	rootCmd.PersistentFlags().BoolVar(&logJSON, "log-json", false, "Output logs in JSON format")
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "Config file (default: .ccg.yaml in current directory)")

	// Global database configuration flags
	rootCmd.PersistentFlags().String("db-driver", "sqlite", "Database driver (sqlite, postgres, mysql)")
	rootCmd.PersistentFlags().String("db-dsn", "ccg.db", "Database connection string")

	// Bind flags to viper
	viper.BindPFlag("db.driver", rootCmd.PersistentFlags().Lookup("db-driver"))
	viper.BindPFlag("db.dsn", rootCmd.PersistentFlags().Lookup("db-dsn"))

	// Also explicitly bind env vars just in case AutomaticEnv needs a hint
	viper.BindEnv("db.driver", "CCG_DB_DRIVER")
	viper.BindEnv("db.dsn", "CCG_DB_DSN")

	rootCmd.AddCommand(
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
	)

	return rootCmd
}

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

func stdout(cmd *cobra.Command) io.Writer {
	return cmd.OutOrStdout()
}

// resolveExcludes merges exclude patterns from the config file (viper "exclude"
// key) and the command-line flag, deduplicating nothing — order is config first,
// then flags.
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
