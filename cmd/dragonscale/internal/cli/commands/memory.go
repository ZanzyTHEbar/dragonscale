package commands

import (
	"errors"

	"github.com/ZanzyTHEbar/dragonscale/cmd/dragonscale/internal/cli"
	"github.com/spf13/cobra"
)

func registerMemoryCommand() {
	cli.Register(func(ctx *cli.AppContext) *cobra.Command {
		return buildMemoryCommand(ctx)
	})
}

func buildMemoryCommand(ctx *cli.AppContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "memory",
		Short: "Manage memory system",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(buildMemoryMigrateSessionsCommand(ctx))
	cmd.AddCommand(buildMemoryDBStatusCommand(ctx))

	return cmd
}

func buildMemoryMigrateSessionsCommand(ctx *cli.AppContext) *cobra.Command {
	return &cobra.Command{
		Use:   "migrate-sessions",
		Short: "Migrate sessions into memory DB",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if ctx == nil || ctx.Service == nil {
				return errors.New("service is not initialized")
			}

			return ctx.Service.MemoryMigrateSessions(cmd.Context(), cmd.OutOrStdout())
		},
	}
}

func buildMemoryDBStatusCommand(ctx *cli.AppContext) *cobra.Command {
	return &cobra.Command{
		Use:   "db-status",
		Short: "Show memory DB status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if ctx == nil || ctx.Service == nil {
				return errors.New("service is not initialized")
			}

			return ctx.Service.MemoryDBStatus(cmd.Context(), cmd.OutOrStdout())
		},
	}
}
