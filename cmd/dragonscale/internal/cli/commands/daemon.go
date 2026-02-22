package commands

import (
	"errors"

	"github.com/ZanzyTHEbar/dragonscale/cmd/dragonscale/internal/cli"
	"github.com/spf13/cobra"
)

func registerDaemonCommand() {
	cli.Register(func(ctx *cli.AppContext) *cobra.Command {
		return buildDaemonCommand(ctx)
	})
}

func buildDaemonCommand(ctx *cli.AppContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Manage dragonscale daemon",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(buildDaemonStartCommand(ctx))
	cmd.AddCommand(buildDaemonStopCommand(ctx))
	cmd.AddCommand(buildDaemonStatusCommand(ctx))

	return cmd
}

func buildDaemonStartCommand(ctx *cli.AppContext) *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start daemon process",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if ctx == nil || ctx.Service == nil {
				return errors.New("service is not initialized")
			}

			return ctx.Service.DaemonStart(cmd.Context(), cmd.OutOrStdout())
		},
	}
}

func buildDaemonStopCommand(ctx *cli.AppContext) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop daemon process",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if ctx == nil || ctx.Service == nil {
				return errors.New("service is not initialized")
			}

			return ctx.Service.DaemonStop(cmd.Context(), cmd.OutOrStdout())
		},
	}
}

func buildDaemonStatusCommand(ctx *cli.AppContext) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show daemon status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if ctx == nil || ctx.Service == nil {
				return errors.New("service is not initialized")
			}

			return ctx.Service.DaemonStatus(cmd.Context(), cmd.OutOrStdout())
		},
	}
}
