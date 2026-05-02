package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/tae2089/trace"
)

// MigrateConfig contains the database and external migration source settings.
type MigrateConfig struct {
	DBDriver      string
	DBDSN         string
	MigrationsDir string
}

// newMigrateCmd creates the explicit database migration command.
// @intent separate schema changes from normal runtime startup.
// @sideEffect may create or alter database tables and search indexes.
func newMigrateCmd(deps *Deps) *cobra.Command {
	migrationsDir := os.Getenv("CCG_MIGRATIONS_DIR")
	if migrationsDir == "" {
		migrationsDir = "migrations"
	}

	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Run database schema migrations",
		Args:  cobra.NoArgs,
		Annotations: map[string]string{
			skipDBInitAnnotation: "true",
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if deps.MigrateFunc == nil {
				return trace.New("MigrateFunc not configured")
			}
			driver := viper.GetString("db.driver")
			dsn := viper.GetString("db.dsn")
			cfg := MigrateConfig{
				DBDriver:      driver,
				DBDSN:         dsn,
				MigrationsDir: resolveMigrationsDir(cmd, migrationsDir),
			}
			if err := deps.MigrateFunc(cfg); err != nil {
				return trace.Wrap(err, "migrate database")
			}
			fmt.Fprintln(stdout(cmd), "Migration complete")
			return nil
		},
	}
	cmd.Flags().StringVar(&migrationsDir, "migrations-dir", migrationsDir, "Migration directory containing driver subdirectories")
	_ = viper.BindEnv("migrations.dir", "CCG_MIGRATIONS_DIR")
	return cmd
}

func resolveMigrationsDir(cmd *cobra.Command, flagValue string) string {
	if cmd.Flags().Changed("migrations-dir") {
		return flagValue
	}
	if cfgDir := strings.TrimSpace(viper.GetString("migrations.dir")); cfgDir != "" {
		return cfgDir
	}
	return flagValue
}
