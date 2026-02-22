package commands

import (
	"errors"

	"github.com/ZanzyTHEbar/dragonscale/cmd/dragonscale/internal/cli"
	"github.com/ZanzyTHEbar/dragonscale/pkg/dragonscale/sdk"
	"github.com/spf13/cobra"
)

func registerGatewayCommand() {
	cli.Register(func(ctx *cli.AppContext) *cobra.Command {
		return buildGatewayCommand(ctx)
	})
}

func buildGatewayCommand(ctx *cli.AppContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gateway",
		Short: "Start dragonscale gateway",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if ctx == nil || ctx.Service == nil {
				return errors.New("service is not initialized")
			}

			debug, err := cmd.Flags().GetBool("debug")
			if err != nil {
				return err
			}

			return ctx.Service.Gateway(cmd.Context(), cmd.OutOrStdout(), sdk.GatewayOptions{
				Debug: debug,
			})
		},
	}

	cmd.Flags().BoolP("debug", "d", false, "Enable debug logging")
	return cmd
}
