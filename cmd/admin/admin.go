package admin

import (
	"github.com/spf13/cobra"
)

// NewCommand returns the admin command
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admin",
		Short: "Administrative commands for apt-cacher-go",
		Long:  `Manage apt-cacher-go configuration, migration, and client setup`,
	}

	// Add subcommands
	cmd.AddCommand(newMigrateCommand())
	cmd.AddCommand(newConfigureCommand())

	return cmd
}
