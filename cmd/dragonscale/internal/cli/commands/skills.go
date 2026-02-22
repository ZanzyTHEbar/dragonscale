package commands

import (
	"errors"

	"github.com/ZanzyTHEbar/dragonscale/cmd/dragonscale/internal/cli"
	"github.com/spf13/cobra"
)

func registerSkillsCommand() {
	cli.Register(func(ctx *cli.AppContext) *cobra.Command {
		return buildSkillsCommand(ctx)
	})
}

func buildSkillsCommand(ctx *cli.AppContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills",
		Short: "Manage skills",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(buildSkillsListCommand(ctx))
	cmd.AddCommand(buildSkillsInstallCommand(ctx))
	cmd.AddCommand(buildSkillsRemoveCommand(ctx))
	cmd.AddCommand(buildSkillsInstallBuiltinCommand(ctx))
	cmd.AddCommand(buildSkillsListBuiltinCommand(ctx))
	cmd.AddCommand(buildSkillsSearchCommand(ctx))
	cmd.AddCommand(buildSkillsShowCommand(ctx))

	return cmd
}

func buildSkillsListCommand(ctx *cli.AppContext) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List installed skills",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if ctx == nil || ctx.Service == nil {
				return errors.New("service is not initialized")
			}

			return ctx.Service.SkillsList(cmd.Context(), cmd.OutOrStdout())
		},
	}
}

func buildSkillsInstallCommand(ctx *cli.AppContext) *cobra.Command {
	return &cobra.Command{
		Use:   "install <repo>",
		Short: "Install a skill from GitHub",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if ctx == nil || ctx.Service == nil {
				return errors.New("service is not initialized")
			}

			return ctx.Service.SkillsInstall(cmd.Context(), cmd.OutOrStdout(), args[0])
		},
	}
}

func buildSkillsRemoveCommand(ctx *cli.AppContext) *cobra.Command {
	return &cobra.Command{
		Use:     "remove <name>",
		Aliases: []string{"uninstall"},
		Short:   "Uninstall a skill",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if ctx == nil || ctx.Service == nil {
				return errors.New("service is not initialized")
			}

			return ctx.Service.SkillsRemove(cmd.Context(), cmd.OutOrStdout(), args[0])
		},
	}
}

func buildSkillsInstallBuiltinCommand(ctx *cli.AppContext) *cobra.Command {
	return &cobra.Command{
		Use:   "install-builtin",
		Short: "Install built-in skills",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if ctx == nil || ctx.Service == nil {
				return errors.New("service is not initialized")
			}

			return ctx.Service.SkillsInstallBuiltin(cmd.Context(), cmd.OutOrStdout(), "")
		},
	}
}

func buildSkillsListBuiltinCommand(ctx *cli.AppContext) *cobra.Command {
	return &cobra.Command{
		Use:   "list-builtin",
		Short: "List built-in skills",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if ctx == nil || ctx.Service == nil {
				return errors.New("service is not initialized")
			}

			return ctx.Service.SkillsListBuiltin(cmd.Context(), cmd.OutOrStdout())
		},
	}
}

func buildSkillsSearchCommand(ctx *cli.AppContext) *cobra.Command {
	return &cobra.Command{
		Use:   "search",
		Short: "Search available skills",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if ctx == nil || ctx.Service == nil {
				return errors.New("service is not initialized")
			}

			return ctx.Service.SkillsSearch(cmd.Context(), cmd.OutOrStdout())
		},
	}
}

func buildSkillsShowCommand(ctx *cli.AppContext) *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Show skill details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if ctx == nil || ctx.Service == nil {
				return errors.New("service is not initialized")
			}

			return ctx.Service.SkillsShow(cmd.Context(), cmd.OutOrStdout(), args[0])
		},
	}
}
