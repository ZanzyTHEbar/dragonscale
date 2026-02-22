package commands

import (
	"github.com/ZanzyTHEbar/dragonscale/cmd/dragonscale/internal/cli"
	"github.com/spf13/cobra"
)

// RegisterAll wires command factories into the global palette.
func RegisterAll() {
	// Command factories are intentionally registered by their feature modules.
	registerAgentCommand()
	registerGatewayCommand()
	registerMigrateCommand()
	registerAuthCommand()
	registerCronCommand()
	registerSkillsCommand()
	registerSecretCommand()
	registerDaemonCommand()
	registerMemoryCommand()
	registerOnboardCommand()
	registerStatusCommand()
	registerVersionCommand()
}

// BuildRoot returns the composed root command with registered command factories.
func BuildRoot(ctx *cli.AppContext) *cobra.Command {
	RegisterAll()
	return cli.BuildRoot(ctx)
}
