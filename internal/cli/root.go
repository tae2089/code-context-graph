package cli

import (
	"io"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"gorm.io/gorm"

	"github.com/imtaebin/code-context-graph/internal/analysis/incremental"
	"github.com/imtaebin/code-context-graph/internal/store/pgstore"
	"github.com/imtaebin/code-context-graph/internal/parse/treesitter"
	"github.com/imtaebin/code-context-graph/internal/store"
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
	PGStore       *pgstore.Store
}

// NewRootCmd creates the root cobra command with all subcommands attached.
// If deps is nil, a minimal Deps with a default logger is created.
func NewRootCmd(deps *Deps) *cobra.Command {
	if deps == nil {
		deps = &Deps{}
	}
	if deps.Logger == nil {
		deps.Logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}

	var logLevel string
	var logJSON bool

	rootCmd := &cobra.Command{
		Use:           "ccg",
		Short:         "code-context-graph — local code analysis tool",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
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
			return nil
		},
	}

	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "Log level: debug, info, warn, error")
	rootCmd.PersistentFlags().BoolVar(&logJSON, "log-json", false, "Output logs in JSON format")

	rootCmd.AddCommand(
		newBuildCmd(deps),
		newUpdateCmd(deps),
		newStatusCmd(deps),
		newSearchCmd(deps),
		newServeCmd(deps),
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
