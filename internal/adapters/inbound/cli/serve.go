package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/tae2089/trace"
)

// ServeConfig holds parsed flags for the local stdio MCP serve subcommand.
// @intent keep the ccg binary focused on local stdio MCP use while ccg-server owns HTTP/webhook hosting.
type ServeConfig struct {
	CacheTTL            time.Duration
	NoCache             bool
	Transport           string // deprecated compatibility flag; only "stdio" is accepted by ccg
	OTELEndpoint        string
	NamespaceRoot       string
	MaxFileBytes        int64
	MaxTotalParsedBytes int64
}

// validateServeConfig checks local serve options before stdio MCP starts.
// @intent reject self-hosted HTTP transport on the local CLI and point callers to ccg-server.
func validateServeConfig(cfg ServeConfig) error {
	if cfg.Transport == "" || cfg.Transport == "stdio" {
		return nil
	}
	if cfg.Transport == "streamable-http" {
		return fmt.Errorf("streamable-http transport moved to ccg-server; use ccg-server instead")
	}
	return fmt.Errorf("--transport must be stdio")
}

// newServeCmd creates the local stdio MCP command.
// @intent let local agents start MCP over stdio without self-hosted HTTP/webhook settings.
// @requires deps.ServeFunc must be configured by the binary entry point.
// @sideEffect starts a long-running stdio MCP process.
func newServeCmd(deps *Deps) *cobra.Command {
	cfg := ServeConfig{
		CacheTTL:      5 * time.Minute,
		Transport:     "stdio",
		OTELEndpoint:  envString("CCG_OTEL_ENDPOINT"),
		NamespaceRoot: "namespaces",
	}

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the local MCP server over stdio",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateServeConfig(cfg); err != nil {
				return err
			}
			if deps.ServeFunc != nil {
				return deps.ServeFunc(cfg)
			}
			return trace.New("ServeFunc not configured")
		},
	}

	cmd.Flags().DurationVar(&cfg.CacheTTL, "cache-ttl", cfg.CacheTTL, "TTL for MCP serve session cache (0 or --no-cache to disable)")
	cmd.Flags().BoolVar(&cfg.NoCache, "no-cache", false, "Disable in-memory cache for MCP serve session")
	cmd.Flags().StringVar(&cfg.Transport, "transport", cfg.Transport, "Deprecated compatibility flag; ccg supports stdio only")
	cmd.Flags().StringVar(&cfg.OTELEndpoint, "otel-endpoint", cfg.OTELEndpoint, "OTLP HTTP trace endpoint (optional; enables span export when set)")
	cmd.Flags().StringVar(&cfg.NamespaceRoot, "namespace-root", cfg.NamespaceRoot, "Root directory for file namespaces")
	cmd.Flags().Int64Var(&cfg.MaxFileBytes, "max-file-bytes", 0, "Maximum bytes allowed per parsed source file (0 disables limit; config: parse.max_file_bytes)")
	cmd.Flags().Int64Var(&cfg.MaxTotalParsedBytes, "max-total-parsed-bytes", 0, "Maximum total bytes allowed across parsed source files (0 disables limit; config: parse.max_total_parsed_bytes)")

	cmd.PreRun = func(cmd *cobra.Command, args []string) {
		cfg.MaxFileBytes = resolveMaxFileBytes(cfg.MaxFileBytes)
		cfg.MaxTotalParsedBytes = resolveMaxTotalParsedBytes(cfg.MaxTotalParsedBytes)
	}

	return cmd
}

// envString reads an environment variable for local serve defaults.
// @intent keep optional stdio MCP environment defaults small and explicit.
func envString(name string) string {
	return os.Getenv(name)
}
