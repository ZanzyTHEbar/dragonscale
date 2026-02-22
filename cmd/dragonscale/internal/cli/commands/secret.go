package commands

import (
	"errors"

	"github.com/ZanzyTHEbar/dragonscale/cmd/dragonscale/internal/cli"
	"github.com/spf13/cobra"
)

func registerSecretCommand() {
	cli.Register(func(ctx *cli.AppContext) *cobra.Command {
		return buildSecretCommand(ctx)
	})
}

func buildSecretCommand(ctx *cli.AppContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secret",
		Short: "Manage encrypted secrets",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(buildSecretInitCommand(ctx))
	cmd.AddCommand(buildSecretAddCommand(ctx))
	cmd.AddCommand(buildSecretListCommand(ctx))
	cmd.AddCommand(buildSecretDeleteCommand(ctx))

	return cmd
}

func buildSecretInitCommand(ctx *cli.AppContext) *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize secret store",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if ctx == nil || ctx.Service == nil {
				return errors.New("service is not initialized")
			}

			return ctx.Service.SecretInit(cmd.Context(), cmd.OutOrStdout())
		},
	}
}

func buildSecretAddCommand(ctx *cli.AppContext) *cobra.Command {
	return &cobra.Command{
		Use:   "add <name>",
		Short: "Add a secret",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if ctx == nil || ctx.Service == nil {
				return errors.New("service is not initialized")
			}

			return ctx.Service.SecretAdd(cmd.Context(), ctx.In, cmd.OutOrStdout(), args[0])
		},
	}
}

func buildSecretListCommand(ctx *cli.AppContext) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List secrets",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if ctx == nil || ctx.Service == nil {
				return errors.New("service is not initialized")
			}

			return ctx.Service.SecretList(cmd.Context(), cmd.OutOrStdout())
		},
	}
}

func buildSecretDeleteCommand(ctx *cli.AppContext) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a secret",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if ctx == nil || ctx.Service == nil {
				return errors.New("service is not initialized")
			}

			return ctx.Service.SecretDelete(cmd.Context(), cmd.OutOrStdout(), args[0])
		},
	}
}
