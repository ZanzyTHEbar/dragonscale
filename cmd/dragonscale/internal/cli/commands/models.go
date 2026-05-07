package commands

import (
	"errors"

	"github.com/ZanzyTHEbar/dragonscale/cmd/dragonscale/internal/cli"
	"github.com/ZanzyTHEbar/dragonscale/pkg/dragonscale/sdk"
	"github.com/spf13/cobra"
)

func registerModelsCommand() {
	cli.Register(func(ctx *cli.AppContext) *cobra.Command {
		return buildModelsCommand(ctx)
	})
}

func buildModelsCommand(ctx *cli.AppContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "models",
		Short: "Manage cached provider model lists",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(buildModelsRefreshCommand(ctx))
	cmd.AddCommand(buildModelsListCommand(ctx))
	return cmd
}

func buildModelsRefreshCommand(ctx *cli.AppContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "refresh",
		Short: "Refresh cached provider model lists",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if ctx == nil || ctx.Service == nil {
				return errors.New("service is not initialized")
			}
			provider, err := cmd.Flags().GetString("provider")
			if err != nil {
				return err
			}
			force, err := cmd.Flags().GetBool("force")
			if err != nil {
				return err
			}
			return ctx.Service.ModelsRefresh(cmd.Context(), cmd.OutOrStdout(), sdk.ModelsRefreshOptions{Provider: provider, Force: force})
		},
	}
	cmd.Flags().StringP("provider", "p", "", "Provider to refresh (default: configured providers plus public catalogs)")
	cmd.Flags().Bool("force", false, "Refresh even when the cache is fresh")
	return cmd
}

func buildModelsListCommand(ctx *cli.AppContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List cached provider models",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if ctx == nil || ctx.Service == nil {
				return errors.New("service is not initialized")
			}
			provider, err := cmd.Flags().GetString("provider")
			if err != nil {
				return err
			}
			return ctx.Service.ModelsList(cmd.Context(), cmd.OutOrStdout(), sdk.ModelsListOptions{Provider: provider})
		},
	}
	cmd.Flags().StringP("provider", "p", "", "Provider to list (default: all cached providers)")
	return cmd
}
