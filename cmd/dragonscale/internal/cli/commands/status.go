package commands

import (
	"errors"

	"github.com/ZanzyTHEbar/dragonscale/cmd/dragonscale/internal/cli"
	"github.com/spf13/cobra"
)

func registerStatusCommand() {
	cli.Register(func(ctx *cli.AppContext) *cobra.Command {
		return buildStatusCommand(ctx)
	})
}

func buildStatusCommand(ctx *cli.AppContext) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show dragonscale status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if ctx == nil || ctx.Service == nil {
				return errors.New("service is not initialized")
			}
			return ctx.Service.Status(cmd.Context(), cmd.OutOrStdout())
		},
	}
}
