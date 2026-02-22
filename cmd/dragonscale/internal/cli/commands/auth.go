package commands

import (
	"errors"

	"github.com/ZanzyTHEbar/dragonscale/cmd/dragonscale/internal/cli"
	"github.com/spf13/cobra"
)

func registerAuthCommand() {
	cli.Register(func(ctx *cli.AppContext) *cobra.Command {
		return buildAuthCommand(ctx)
	})
}

func buildAuthCommand(ctx *cli.AppContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage authentication",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(buildAuthLoginCommand(ctx))
	cmd.AddCommand(buildAuthLogoutCommand(ctx))
	cmd.AddCommand(buildAuthStatusCommand(ctx))

	return cmd
}

func buildAuthLoginCommand(ctx *cli.AppContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate with a provider",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if ctx == nil || ctx.Service == nil {
				return errors.New("service is not initialized")
			}

			provider, err := cmd.Flags().GetString("provider")
			if err != nil {
				return err
			}
			if provider == "" {
				return errors.New("provider is required")
			}
			useDeviceCode, err := cmd.Flags().GetBool("device-code")
			if err != nil {
				return err
			}

			return ctx.Service.AuthLogin(cmd.Context(), ctx.In, cmd.OutOrStdout(), provider, useDeviceCode)
		},
	}

	cmd.Flags().StringP("provider", "p", "", "Provider to authenticate")
	cmd.Flags().Bool("device-code", false, "Use device code flow")
	return cmd
}

func buildAuthLogoutCommand(ctx *cli.AppContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Clear authentication credentials",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if ctx == nil || ctx.Service == nil {
				return errors.New("service is not initialized")
			}

			provider, err := cmd.Flags().GetString("provider")
			if err != nil {
				return err
			}

			return ctx.Service.AuthLogout(cmd.Context(), cmd.OutOrStdout(), provider)
		},
	}

	cmd.Flags().StringP("provider", "p", "", "Provider to logout (optional)")
	return cmd
}

func buildAuthStatusCommand(ctx *cli.AppContext) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show authentication status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if ctx == nil || ctx.Service == nil {
				return errors.New("service is not initialized")
			}

			return ctx.Service.AuthStatus(cmd.Context(), cmd.OutOrStdout())
		},
	}
}
