package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/tae2089/trace"
)

// newMigrateCmd creates the explicit database migration command.
// @intent separate schema changes from normal runtime startup.
// @sideEffect may create or alter database tables and search indexes.
func newMigrateCmd(deps *Deps) *cobra.Command {
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
			if err := deps.MigrateFunc(driver, dsn); err != nil {
				return trace.Wrap(err, "migrate database")
			}
			fmt.Fprintln(stdout(cmd), "Migration complete")
			return nil
		},
	}
	return cmd
}
