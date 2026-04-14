package cli

import (
	"time"

	"github.com/spf13/cobra"
	"github.com/tae2089/trace"
)

// ServeConfig holds parsed flags for the serve subcommand.
type ServeConfig struct {
	CacheTTL      time.Duration
	NoCache       bool
	Transport     string // "stdio" (default) | "streamable-http"
	HTTPAddr      string // listen address for HTTP transport (default ":8080")
	Stateless     bool   // stateless session management for multi-instance deployments
	WorkspaceRoot string // root directory for file workspaces (default "workspaces")
}

func newServeCmd(deps *Deps) *cobra.Command {
	var cfg ServeConfig

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the MCP server over stdio or HTTP",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if deps.ServeFunc != nil {
				return deps.ServeFunc(cfg)
			}
			return trace.New("ServeFunc not configured")
		},
	}

	cmd.Flags().DurationVar(&cfg.CacheTTL, "cache-ttl", 5*time.Minute, "TTL for MCP serve session cache (0 or --no-cache to disable)")
	cmd.Flags().BoolVar(&cfg.NoCache, "no-cache", false, "Disable in-memory cache for MCP serve session")
	cmd.Flags().StringVar(&cfg.Transport, "transport", "stdio", "Transport mode: stdio or streamable-http")
	cmd.Flags().StringVar(&cfg.HTTPAddr, "http-addr", ":8080", "Listen address for HTTP transport")
	cmd.Flags().BoolVar(&cfg.Stateless, "stateless", false, "Stateless session management (for multi-instance deployments)")
	cmd.Flags().StringVar(&cfg.WorkspaceRoot, "workspace-root", "workspaces", "Root directory for file workspaces")

	return cmd
}
