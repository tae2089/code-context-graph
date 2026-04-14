package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

// ServeConfig holds parsed flags for the serve subcommand.
type ServeConfig struct {
	CacheTTL time.Duration
	NoCache  bool
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

	cmd.Flags().DurationVar(&cfg.CacheTTL, "cache-ttl", 5*time.Minute, "TTL for MCP serve session cache (0 or --no-cache to disable)")
	cmd.Flags().BoolVar(&cfg.NoCache, "no-cache", false, "Disable in-memory cache for MCP serve session")

	return cmd
}
