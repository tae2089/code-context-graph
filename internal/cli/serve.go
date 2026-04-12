package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// ServeConfig holds parsed flags for the serve subcommand.
type ServeConfig struct {
	DBDriver string
	DSN      string
}

func newServeCmd(deps *Deps) *cobra.Command {
	var cfg ServeConfig

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the MCP server over stdio",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if deps.ServeFunc != nil {
				return deps.ServeFunc(cfg)
			}
			return fmt.Errorf("ServeFunc not configured")
		},
	}

	cmd.Flags().StringVar(&cfg.DBDriver, "db", "sqlite", "Database driver: sqlite, postgres, mysql")
	cmd.Flags().StringVar(&cfg.DSN, "dsn", "ccg.db", "Database connection string")

	return cmd
}
