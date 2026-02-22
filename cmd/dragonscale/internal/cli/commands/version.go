package commands

import (
	"errors"

	"github.com/ZanzyTHEbar/dragonscale/cmd/dragonscale/internal/cli"
	"github.com/spf13/cobra"
)

func registerVersionCommand() {
	cli.Register(func(ctx *cli.AppContext) *cobra.Command {
		return buildVersionCommand(ctx)
	})
}

func buildVersionCommand(ctx *cli.AppContext) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show dragonscale version information",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if ctx == nil || ctx.Service == nil {
				return errors.New("service is not initialized")
			}
			return ctx.Service.PrintVersion(cmd.Context(), cmd.OutOrStdout())
		},
	}
}
