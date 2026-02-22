package commands

import (
	"errors"

	"github.com/ZanzyTHEbar/dragonscale/cmd/dragonscale/internal/cli"
	"github.com/ZanzyTHEbar/dragonscale/pkg/dragonscale/sdk"
	"github.com/spf13/cobra"
)

func registerMigrateCommand() {
	cli.Register(func(ctx *cli.AppContext) *cobra.Command {
		return buildMigrateCommand(ctx)
	})
}

func buildMigrateCommand(ctx *cli.AppContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Migrate dragonscale configuration and workspace",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if ctx == nil || ctx.Service == nil {
				return errors.New("service is not initialized")
			}

			dryRun, err := cmd.Flags().GetBool("dry-run")
			if err != nil {
				return err
			}
			configOnly, err := cmd.Flags().GetBool("config-only")
			if err != nil {
				return err
			}
			workspaceOnly, err := cmd.Flags().GetBool("workspace-only")
			if err != nil {
				return err
			}
			force, err := cmd.Flags().GetBool("force")
			if err != nil {
				return err
			}
			refresh, err := cmd.Flags().GetBool("refresh")
			if err != nil {
				return err
			}
			openClawHome, err := cmd.Flags().GetString("openclaw-home")
			if err != nil {
				return err
			}
			dragonscaleHome, err := cmd.Flags().GetString("dragonscale-home")
			if err != nil {
				return err
			}

			opts := sdk.MigrateOptions{
				DryRun:          dryRun,
				ConfigOnly:      configOnly,
				WorkspaceOnly:   workspaceOnly,
				Force:           force,
				Refresh:         refresh,
				OpenClawHome:    openClawHome,
				DragonscaleHome: dragonscaleHome,
			}

			return ctx.Service.Migrate(cmd.Context(), opts, cmd.OutOrStdout())
		},
	}

	cmd.Flags().Bool("dry-run", false, "Simulate migration without writing")
	cmd.Flags().Bool("config-only", false, "Migrate only config files")
	cmd.Flags().Bool("workspace-only", false, "Migrate only workspace files")
	cmd.Flags().Bool("force", false, "Overwrite existing files")
	cmd.Flags().Bool("refresh", false, "Re-run migrations")
	cmd.Flags().String("openclaw-home", "", "Path to OpenClaw home directory")
	cmd.Flags().String("dragonscale-home", "", "Path to dragonscale home directory")

	return cmd
}
