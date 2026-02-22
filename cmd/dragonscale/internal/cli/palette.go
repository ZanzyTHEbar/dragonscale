package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// CommandFactory builds a cobra command using the provided AppContext.
type CommandFactory func(*AppContext) *cobra.Command

// Palette is an ordered collection of CommandFactory instances that compose the command surface.
type Palette []CommandFactory

// Register appends one or more command factories to the palette.
func (p *Palette) Register(factories ...CommandFactory) {
	*p = append(*p, factories...)
}

// Commands materializes command factories into concrete Cobra commands.
func (p Palette) Commands(ctx *AppContext) []*cobra.Command {
	cmds := make([]*cobra.Command, 0, len(p))
	seen := map[string]struct{}{}
	for _, f := range p {
		if f == nil {
			continue
		}
		cmd := f(ctx)
		if cmd == nil {
			continue
		}
		name := cmd.Name()
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			panic(fmt.Sprintf("duplicate command registration: %q", name))
		}
		seen[name] = struct{}{}
		cmds = append(cmds, cmd)
	}
	return cmds
}

// FromCommand lifts an already-built command into a factory.
func FromCommand(c *cobra.Command) CommandFactory {
	return func(_ *AppContext) *cobra.Command {
		return c
	}
}
