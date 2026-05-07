package commands

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ZanzyTHEbar/dragonscale/cmd/dragonscale/internal/cli"
	"github.com/ZanzyTHEbar/dragonscale/pkg/dragonscale/sdk"
	"github.com/spf13/cobra"
)

func TestPaletteCommandsRejectsDuplicateCommandNames(t *testing.T) {
	p := cli.Palette{
		func(*cli.AppContext) *cobra.Command {
			return &cobra.Command{Use: "agent"}
		},
		func(*cli.AppContext) *cobra.Command {
			return &cobra.Command{Use: "agent"}
		},
		func(*cli.AppContext) *cobra.Command {
			return &cobra.Command{Use: "daemon"}
		},
	}

	ctx := cli.NewAppContext(sdk.NewService(sdk.WithVersion("test", "", "", "")), "test", "", "", "")
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected duplicate command registration to panic")
		}
	}()

	_ = p.Commands(ctx)
}

func TestPaletteCommandsAllowsUniqueCommandNames(t *testing.T) {
	p := cli.Palette{
		func(*cli.AppContext) *cobra.Command {
			return &cobra.Command{Use: "agent"}
		},
		func(*cli.AppContext) *cobra.Command {
			return &cobra.Command{Use: "daemon"}
		},
	}

	ctx := cli.NewAppContext(sdk.NewService(sdk.WithVersion("test", "", "", "")), "test", "", "", "")
	cmds := p.Commands(ctx)
	if len(cmds) != 2 {
		t.Fatalf("expected 2 unique commands, got %d", len(cmds))
	}
}

func TestBuildRoot_RegistersExpectedCommands(t *testing.T) {
	originalPalette := cli.DefaultPalette
	cli.DefaultPalette = nil
	t.Cleanup(func() {
		cli.DefaultPalette = originalPalette
	})

	ctx := cli.NewAppContext(sdk.NewService(sdk.WithVersion("test", "", "", "")), "test", "", "", "")
	out := &bytes.Buffer{}
	ctx = ctx.WithIO(bytes.NewBuffer(nil), out, out)

	root := BuildRoot(ctx)
	if got, want := root.Use, "dragonscale"; got != want {
		t.Fatalf("root command use = %q, want %q", got, want)
	}

	found := map[string]bool{}
	for _, cmd := range root.Commands() {
		found[cmd.Name()] = true
	}

	expected := []string{
		"agent",
		"gateway",
		"auth",
		"cron",
		"skills",
		"secret",
		"daemon",
		"memory",
		"models",
		"onboard",
		"status",
		"version",
	}

	for _, name := range expected {
		if !found[name] {
			t.Fatalf("missing expected command: %s", name)
		}
	}

	if len(found) != len(expected) {
		t.Fatalf("expected %d registered commands, got %d", len(expected), len(found))
	}
}

func TestVersionCommandWritesVersionInfo(t *testing.T) {
	svc := sdk.NewService(sdk.WithVersion("v9.9.9", "", "", ""))
	ctx := cli.NewAppContext(svc, "v9.9.9", "", "", "")
	out := &bytes.Buffer{}
	ctx = ctx.WithIO(bytes.NewBuffer(nil), out, out)

	cmd := buildVersionCommand(ctx)
	cmd.SetArgs([]string{})
	cmd.SetOut(out)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("version command returned error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "dragonscale v9.9.9") {
		t.Fatalf("unexpected version output: %q", got)
	}
}

func TestVersionCommandRequiresService(t *testing.T) {
	cmd := buildVersionCommand(nil)
	cmd.SetOut(&bytes.Buffer{})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when service context is nil")
	}
	if err.Error() != "service is not initialized" {
		t.Fatalf("unexpected error: %v", err)
	}
}
