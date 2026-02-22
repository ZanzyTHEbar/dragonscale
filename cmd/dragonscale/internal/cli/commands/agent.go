package commands

import (
	"errors"

	"github.com/ZanzyTHEbar/dragonscale/cmd/dragonscale/internal/cli"
	"github.com/ZanzyTHEbar/dragonscale/pkg/dragonscale/sdk"
	"github.com/spf13/cobra"
)

func registerAgentCommand() {
	cli.Register(func(ctx *cli.AppContext) *cobra.Command {
		return buildAgentCommand(ctx)
	})
}

func buildAgentCommand(ctx *cli.AppContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Interact with the agent",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if ctx == nil || ctx.Service == nil {
				return errors.New("service is not initialized")
			}

			message, err := cmd.Flags().GetString("message")
			if err != nil {
				return err
			}
			session, err := cmd.Flags().GetString("session")
			if err != nil {
				return err
			}
			debug, err := cmd.Flags().GetBool("debug")
			if err != nil {
				return err
			}

			return ctx.Service.Agent(cmd.Context(), ctx.In, cmd.OutOrStdout(), sdk.AgentOptions{
				Message:    message,
				SessionKey: session,
				Debug:      debug,
			})
		},
	}

	cmd.Flags().BoolP("debug", "d", false, "Enable debug logging")
	cmd.Flags().StringP("message", "m", "", "Send a single message and exit")
	cmd.Flags().StringP("session", "s", "cli:default", "Session key for conversation context")

	return cmd
}
