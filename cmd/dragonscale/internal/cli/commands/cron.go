package commands

import (
	"errors"
	"strconv"

	"github.com/ZanzyTHEbar/dragonscale/cmd/dragonscale/internal/cli"
	"github.com/ZanzyTHEbar/dragonscale/pkg/dragonscale/sdk"
	"github.com/spf13/cobra"
)

func registerCronCommand() {
	cli.Register(func(ctx *cli.AppContext) *cobra.Command {
		return buildCronCommand(ctx)
	})
}

func buildCronCommand(ctx *cli.AppContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cron",
		Short: "Manage cron jobs",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(buildCronListCommand(ctx))
	cmd.AddCommand(buildCronAddCommand(ctx))
	cmd.AddCommand(buildCronRemoveCommand(ctx))
	cmd.AddCommand(buildCronEnableCommand(ctx))
	cmd.AddCommand(buildCronDisableCommand(ctx))

	return cmd
}

func buildCronListCommand(ctx *cli.AppContext) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured cron jobs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if ctx == nil || ctx.Service == nil {
				return errors.New("service is not initialized")
			}

			return ctx.Service.CronList(cmd.Context(), cmd.OutOrStdout())
		},
	}
}

func buildCronAddCommand(ctx *cli.AppContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a cron job",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if ctx == nil || ctx.Service == nil {
				return errors.New("service is not initialized")
			}

			name, err := cmd.Flags().GetString("name")
			if err != nil {
				return err
			}
			message, err := cmd.Flags().GetString("message")
			if err != nil {
				return err
			}
			every, err := cmd.Flags().GetString("every")
			if err != nil {
				return err
			}
			cronExpr, err := cmd.Flags().GetString("cron")
			if err != nil {
				return err
			}
			deliver, err := cmd.Flags().GetBool("deliver")
			if err != nil {
				return err
			}
			to, err := cmd.Flags().GetString("to")
			if err != nil {
				return err
			}
			channel, err := cmd.Flags().GetString("channel")
			if err != nil {
				return err
			}

			opts, err := parseCronOptions(name, every, cronExpr)
			if err != nil {
				return err
			}

			opts.Message = message
			opts.Deliver = deliver
			opts.To = to
			opts.Channel = channel

			return ctx.Service.CronAdd(cmd.Context(), cmd.OutOrStdout(), opts)
		},
	}

	cmd.Flags().StringP("name", "n", "", "Unique job name")
	cmd.Flags().StringP("message", "m", "", "Message prompt for job execution")
	cmd.Flags().StringP("every", "e", "", "Run every N milliseconds")
	cmd.Flags().StringP("cron", "c", "", "Cron expression schedule")
	cmd.Flags().BoolP("deliver", "d", false, "Deliver result to recipient")
	cmd.Flags().String("to", "", "Delivery recipient")
	cmd.Flags().String("channel", "", "Delivery channel")
	return cmd
}

func parseCronOptions(name, every, cronExpr string) (sdk.CronAddOptions, error) {
	opts := sdk.CronAddOptions{}
	if name != "" {
		opts.Name = name
	}
	if every != "" {
		val, err := strconv.ParseInt(every, 10, 64)
		if err != nil {
			return opts, err
		}
		opts.EveryMS = &val
	}
	if cronExpr != "" {
		opts.Cron = cronExpr
	}
	return opts, nil
}

func buildCronRemoveCommand(ctx *cli.AppContext) *cobra.Command {
	return &cobra.Command{
		Use:   "remove [job-id]",
		Short: "Remove a cron job",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if ctx == nil || ctx.Service == nil {
				return errors.New("service is not initialized")
			}

			return ctx.Service.CronRemove(cmd.Context(), cmd.OutOrStdout(), args[0])
		},
	}
}

func buildCronEnableCommand(ctx *cli.AppContext) *cobra.Command {
	return &cobra.Command{
		Use:   "enable [job-id]",
		Short: "Enable a cron job",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if ctx == nil || ctx.Service == nil {
				return errors.New("service is not initialized")
			}

			return ctx.Service.CronEnable(cmd.Context(), cmd.OutOrStdout(), args[0], true)
		},
	}
}

func buildCronDisableCommand(ctx *cli.AppContext) *cobra.Command {
	return &cobra.Command{
		Use:   "disable [job-id]",
		Short: "Disable a cron job",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if ctx == nil || ctx.Service == nil {
				return errors.New("service is not initialized")
			}

			return ctx.Service.CronEnable(cmd.Context(), cmd.OutOrStdout(), args[0], false)
		},
	}
}
