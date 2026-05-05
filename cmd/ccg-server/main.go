// @index Self-hosted HTTP server bootstrap for code-context-graph.
package main

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	ccgconfig "github.com/tae2089/code-context-graph/internal/config"
	"github.com/tae2089/code-context-graph/internal/core"
	ccgserver "github.com/tae2089/code-context-graph/internal/server"
	"github.com/tae2089/trace"
)

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

// @intent run the self-hosted HTTP MCP/webhook server as a dedicated binary.
func main() {
	logger := slog.Default()
	rt := core.NewRuntime(logger)
	cmd := newRootCmd(rt, version)
	if err := cmd.Execute(); err != nil {
		slog.Error("server command failed", trace.SlogError(err))
		rt.Close()
		os.Exit(1)
	}
	rt.Close()
}

// newRootCmd builds the ccg-server command with HTTP and webhook options.
// @intent keep self-hosted server flags separate from the local ccg CLI.
// @sideEffect reads config/env, opens the DB, and starts a long-running HTTP server.
func newRootCmd(rt *core.Runtime, serviceVersion string) *cobra.Command {
	cfg := ccgserver.DefaultConfig()
	cfg.Transport = "streamable-http"

	var logLevel string
	var logJSON bool
	var cfgFile string
	var dbDriver string
	var dbDSN string

	cmd := &cobra.Command{
		Use:           "ccg-server",
		Short:         "self-hosted code-context-graph server",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			level := parseLogLevel(logLevel)
			opts := &slog.HandlerOptions{Level: level}
			if logJSON {
				rt.Logger = slog.New(slog.NewJSONHandler(cmd.ErrOrStderr(), opts))
			} else {
				rt.Logger = slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), opts))
			}
			slog.SetDefault(rt.Logger)

			viper.AutomaticEnv()
			viper.SetEnvPrefix("CCG")
			if cfgFile != "" {
				viper.SetConfigFile(cfgFile)
			} else {
				viper.SetConfigName(".ccg")
				viper.SetConfigType("yaml")
				viper.AddConfigPath(".")
				if home, err := os.UserHomeDir(); err == nil {
					viper.AddConfigPath(filepath.Join(home, ".config", "ccg"))
				}
			}
			if err := viper.ReadInConfig(); err != nil {
				var notFound viper.ConfigFileNotFoundError
				if !errors.As(err, &notFound) && !errors.Is(err, os.ErrNotExist) {
					return trace.Wrap(err, "read config")
				}
			}

			dbDriver = viper.GetString("db.driver")
			dbDSN = viper.GetString("db.dsn")

			if cfg.MaxFileBytes == 0 {
				cfg.MaxFileBytes = viper.GetInt64("parse.max_file_bytes")
			}
			if cfg.MaxTotalParsedBytes == 0 {
				cfg.MaxTotalParsedBytes = viper.GetInt64("parse.max_total_parsed_bytes")
			}
			if len(cfg.AllowRepo) > 0 &&
				!cmd.Flags().Changed("webhook-workers") &&
				!ccgserver.EnvIsSet("CCG_WEBHOOK_WORKERS") &&
				dbDriver == "sqlite" {
				cfg.WebhookWorkers = 1
			}
			if err := ccgserver.ValidateConfig(cfg); err != nil {
				return err
			}
			if err := rt.Init(dbDriver, dbDSN); err != nil {
				return trace.Wrap(err, "initialize database")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return ccgserver.Run(rt, cfg, serviceVersion, ccgconfig.RagIndexDir(), ccgconfig.RagDescription())
		},
	}

	cmd.Flags().StringVar(&logLevel, "log-level", "info", "Log level: debug, info, warn, error")
	cmd.Flags().BoolVar(&logJSON, "log-json", false, "Output logs in JSON format")
	cmd.Flags().StringVar(&cfgFile, "config", "", "Config file (default: .ccg.yaml in ./ then ~/.config/ccg/)")
	cmd.Flags().StringVar(&dbDriver, "db-driver", "sqlite", "Database driver (sqlite, postgres)")
	cmd.Flags().StringVar(&dbDSN, "db-dsn", "ccg.db", "Database connection string")

	_ = viper.BindPFlag("db.driver", cmd.Flags().Lookup("db-driver"))
	_ = viper.BindPFlag("db.dsn", cmd.Flags().Lookup("db-dsn"))
	_ = viper.BindEnv("db.driver", "CCG_DB_DRIVER")
	_ = viper.BindEnv("db.dsn", "CCG_DB_DSN")

	cmd.Flags().DurationVar(&cfg.CacheTTL, "cache-ttl", cfg.CacheTTL, "TTL for MCP serve session cache (0 or --no-cache to disable)")
	cmd.Flags().BoolVar(&cfg.NoCache, "no-cache", false, "Disable in-memory cache for MCP serve session")
	cmd.Flags().StringVar(&cfg.HTTPAddr, "http-addr", cfg.HTTPAddr, "Listen address for HTTP transport")
	cmd.Flags().StringVar(&cfg.HTTPBearerToken, "http-bearer-token", cfg.HTTPBearerToken, "Bearer token required for MCP HTTP requests when set")
	cmd.Flags().StringVar(&cfg.OTELEndpoint, "otel-endpoint", cfg.OTELEndpoint, "OTLP HTTP trace endpoint (optional; enables span export when set)")
	cmd.Flags().BoolVar(&cfg.InsecureHTTP, "insecure-http", false, "Allow externally bound HTTP transport without bearer token (unsafe; testing only)")
	cmd.Flags().BoolVar(&cfg.Stateless, "stateless", false, "Stateless session management (for multi-instance deployments)")
	cmd.Flags().StringVar(&cfg.WikiDir, "wiki-dir", cfg.WikiDir, "Directory containing built Wiki UI assets; enables /wiki when set")
	cmd.Flags().StringVar(&cfg.NamespaceRoot, "namespace-root", cfg.NamespaceRoot, "Root directory for file namespaces")

	cmd.Flags().IntVar(&cfg.WebhookWorkers, "webhook-workers", cfg.WebhookWorkers, "Number of webhook sync workers")
	cmd.Flags().IntVar(&cfg.WebhookMaxTrackedRepos, "webhook-max-tracked-repos", cfg.WebhookMaxTrackedRepos, "Maximum repositories tracked by the webhook sync queue")
	cmd.Flags().DurationVar(&cfg.WebhookAttemptTimeout, "webhook-attempt-timeout", cfg.WebhookAttemptTimeout, "Timeout for one webhook sync attempt")
	cmd.Flags().DurationVar(&cfg.WebhookShutdownTimeout, "webhook-shutdown-timeout", cfg.WebhookShutdownTimeout, "Timeout for graceful webhook queue shutdown and HTTP drain")
	cmd.Flags().IntVar(&cfg.WebhookRetryAttempts, "webhook-retry-attempts", cfg.WebhookRetryAttempts, "Maximum webhook sync attempts per queued item")
	cmd.Flags().DurationVar(&cfg.WebhookRetryBaseDelay, "webhook-retry-base-delay", cfg.WebhookRetryBaseDelay, "Initial webhook sync retry delay")
	cmd.Flags().DurationVar(&cfg.WebhookRetryMaxDelay, "webhook-retry-max-delay", cfg.WebhookRetryMaxDelay, "Maximum webhook sync retry delay")
	cmd.Flags().StringSliceVar(&cfg.AllowRepo, "allow-repo", nil, "Allowed repo patterns for webhook sync (repeatable, e.g. org/*, !org/private)")
	cmd.Flags().StringVar(&cfg.WebhookSecret, "webhook-secret", "", "HMAC secret for GitHub webhook signature verification")
	cmd.Flags().BoolVar(&cfg.InsecureWebhook, "insecure-webhook", false, "Allow unsigned webhook requests (unsafe; testing only)")
	cmd.Flags().StringArrayVar(&cfg.RepoCloneBaseURLs, "repo-clone-base-url", nil, "Canonical base URL used to reconstruct clone targets for allowed repos (repeatable)")
	cmd.Flags().StringVar(&cfg.RepoRoot, "repo-root", cfg.RepoRoot, "Root directory for cloned repositories")
	cmd.Flags().Int64Var(&cfg.MaxFileBytes, "max-file-bytes", 0, "Maximum bytes allowed per parsed source file (0 disables limit; config: parse.max_file_bytes)")
	cmd.Flags().Int64Var(&cfg.MaxTotalParsedBytes, "max-total-parsed-bytes", 0, "Maximum total bytes allowed across parsed source files (0 disables limit; config: parse.max_total_parsed_bytes)")
	cmd.Flags().BoolVar(&cfg.WebhookFailOnUnreadable, "webhook-fail-on-unreadable", false, "Fail webhook sync attempts when source files cannot be read instead of skipping them")

	return cmd
}

// parseLogLevel converts a CLI log level string into slog severity.
// @intent normalize server log-level input consistently with ccg.
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
