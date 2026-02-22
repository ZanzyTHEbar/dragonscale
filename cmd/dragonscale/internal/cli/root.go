package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// NewRoot creates a root command and composes commands from the palette.
func NewRoot(ctx *AppContext, p Palette) *cobra.Command {
	if ctx == nil {
		ctx = NewAppContext(nil, "", "", "", "")
	}
	if p == nil {
		p = Palette{}
	}

	root := &cobra.Command{
		Use:   "dragonscale",
		Short: "Personal AI Assistant",
		Long:  "dragonscale is a CLI for interacting with your local DragonScale agent and services.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	root.Version = ctx.Version
	root.SetContext(context.Background())
	root.AddCommand(p.Commands(ctx)...)
	root.SetIn(ctx.In)
	root.SetOut(ctx.stdout())
	root.SetErr(ctx.stderr())

	return root
}

// BuildRoot builds using the default palette.
func BuildRoot(ctx *AppContext) *cobra.Command {
	return NewRoot(ctx, DefaultPalette)
}

// Execute builds and executes the root command.
func Execute(ctx *AppContext) error {
	root := BuildRoot(ctx)
	if err := root.Execute(); err != nil {
		return err
	}
	return nil
}

// ExecuteOrExit executes the root command and exits with status 1 on error.
func ExecuteOrExit(ctx *AppContext) {
	if err := Execute(ctx); err != nil {
		out := ctx.stderr()
		_, _ = fmt.Fprintln(out, err)
		os.Exit(1)
	}
}
