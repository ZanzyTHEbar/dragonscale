package commands

import (
	"errors"

	"github.com/ZanzyTHEbar/dragonscale/cmd/dragonscale/internal/cli"
	"github.com/spf13/cobra"
)

func registerOnboardCommand() {
	cli.Register(func(ctx *cli.AppContext) *cobra.Command {
		return buildOnboardCommand(ctx)
	})
}

func buildOnboardCommand(ctx *cli.AppContext) *cobra.Command {
	return &cobra.Command{
		Use:   "onboard",
		Short: "Initialize dragonscale configuration and workspace",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if ctx == nil || ctx.Service == nil {
				return errors.New("service is not initialized")
			}

			return ctx.Service.Onboard(cmd.Context(), ctx.In, cmd.OutOrStdout())
		},
	}
}
