package cli

import (
	"time"

	"github.com/spf13/cobra"
	"github.com/tae2089/trace"
)

// ServeConfig holds parsed flags for the serve subcommand.
// @intent MCP 서버 실행에 필요한 전송 방식과 세션 관련 옵션을 전달한다.
type ServeConfig struct {
	CacheTTL      time.Duration
	NoCache       bool
	Transport     string // "stdio" (default) | "streamable-http"
	HTTPAddr      string // listen address for HTTP transport (default ":8080")
	Stateless     bool   // stateless session management for multi-instance deployments
	WorkspaceRoot string // root directory for file workspaces (default "workspaces")
}

// newServeCmd creates the MCP server command.
// @intent CLI에서 stdio 또는 HTTP 기반 MCP 서버를 시작할 수 있게 한다.
// @requires deps.ServeFunc가 설정되어 있어야 한다.
// @sideEffect 실행 시 장시간 서버 프로세스를 시작한다.
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
