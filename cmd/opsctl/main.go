package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/internal/opsctl/app"
	"github.com/ZanzyTHEbar/dragonscale/internal/opsctl/format"
	"github.com/ZanzyTHEbar/dragonscale/internal/opsctl/runner"
	"github.com/ZanzyTHEbar/dragonscale/internal/opsctl/tasks"
	"github.com/spf13/cobra"
)

type exitCodeError struct {
	code int
	err  error
}

func (e *exitCodeError) Error() string {
	return e.err.Error()
}

func collectEnvFromHost(keys []string) map[string]string {
	extra := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := os.LookupEnv(key); ok {
			extra[key] = value
		}
	}
	return extra
}

func printUsage(w io.Writer, a *app.App, appName string) {
	_, _ = fmt.Fprintf(w, "Usage: %s [global flags] <command> [args]\n\n", appName)
	_, _ = fmt.Fprintf(w, "Global flags:\n")
	_, _ = fmt.Fprintln(w, "  --format string   output mode: text, json, raw (default text)")
	_, _ = fmt.Fprintln(w, "  --json            shortcut for --format json")
	_, _ = fmt.Fprintln(w, "  --raw             shortcut for --format raw")
	_, _ = fmt.Fprintln(w, "  --no-color        disable color output")
	_, _ = fmt.Fprintln(w, "  --quiet           suppress non-error output")
	_, _ = fmt.Fprintln(w, "  --root string     repository root path (default current directory)")
	_, _ = fmt.Fprintln(w, "  --cwd string      working directory for command execution")
	_, _ = fmt.Fprintln(w, "  --timeout value   command timeout (ex: 2m, 30s)")
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Tasks:")
	for _, line := range a.TaskHelp() {
		_, _ = fmt.Fprintln(w, line)
	}
}

func parseOutputMode(raw, json bool, mode string) (format.OutputMode, error) {
	if raw {
		return format.OutputRaw, nil
	}
	if json {
		return format.OutputJSON, nil
	}
	if mode == "" {
		mode = string(format.OutputText)
	}
	switch format.OutputMode(mode) {
	case format.OutputText, format.OutputJSON, format.OutputRaw:
		return format.OutputMode(mode), nil
	default:
		return "", fmt.Errorf("invalid --format value %q", mode)
	}
}

func resolveRootPath(root string) (string, error) {
	resolved := app.EnsureRoot(root)
	if strings.TrimSpace(resolved) == "" {
		return "", fmt.Errorf("invalid --root: resolved empty repository path")
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("invalid --root %q: %w", resolved, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("invalid --root %q: not a directory", resolved)
	}
	return resolved, nil
}

func resolveWorkingDirectory(root, cwd string) (string, error) {
	clean := strings.TrimSpace(cwd)
	if clean == "" {
		return "", nil
	}
	if !filepath.IsAbs(clean) {
		clean = filepath.Join(root, clean)
	}
	info, err := os.Stat(clean)
	if err != nil {
		return "", fmt.Errorf("invalid --cwd %q: %w", clean, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("invalid --cwd %q: not a directory", clean)
	}
	return clean, nil
}

func newOpsApp(root string) *app.App {
	ops := app.New(runner.OSRunner{}, root)
	for _, t := range tasks.NewRegistry(root) {
		ops.Register(t)
	}
	ops.Register(tasks.NewHelpTask(ops))
	return ops
}

func defaultRunEnvKeys() []string {
	return []string{
		"GO",
		"GOFLAGS",
		"CGO_ENABLED",
		"GOOS",
		"GOARCH",
		"DRAGONSCALE_EVAL_HOST_HOME",
		"DRAGONSCALE_EVAL_BASE_CONFIG",
		"DRAGONSCALE_EVAL_CONFIG",
		"DRAGONSCALE_EVAL_DEBUG",
		"DEVCONTAINER_EXEC",
		"PLATFORM",
		"ARCH",
		"BINARY_NAME",
		"BUILD_DIR",
		"VERSION",
		"FANTASY_VERSION",
		"NAME",
		"ARGS",
		"WORKSPACE_DIR",
	}
}

func buildOpsctlCommand(stdout, stderr io.Writer, args []string) *cobra.Command {
	var (
		formatValue string
		jsonMode    bool
		rawMode     bool
		noColor     bool
		quiet       bool
		cwd         string
		rootFlag    string
		timeout     time.Duration
	)

	root := &cobra.Command{
		Use:   "opsctl [global flags] <command> [args]",
		Short: "opsctl is a thin CLI wrapper over repository operations",
		Long:  "opsctl is a thin CLI wrapper over repository operations.",
		RunE: func(cobraCmd *cobra.Command, cmdArgs []string) error {
			mode, err := parseOutputMode(rawMode, jsonMode, formatValue)
			if err != nil {
				return &exitCodeError{code: 2, err: err}
			}

			resolvedRoot, err := resolveRootPath(rootFlag)
			if err != nil {
				return &exitCodeError{code: 2, err: err}
			}
			ops := newOpsApp(resolvedRoot)

			if len(cmdArgs) == 0 || cmdArgs[0] == "help" || cmdArgs[0] == "--help" || cmdArgs[0] == "-h" {
				printUsage(cobraCmd.OutOrStdout(), ops, cobraCmd.Root().Name())
				return nil
			}

			taskName := strings.TrimSpace(cmdArgs[0])
			if taskName == "" {
				printUsage(cobraCmd.OutOrStdout(), ops, cobraCmd.Root().Name())
				return nil
			}
			taskArgs := cmdArgs[1:]

			if !ops.HasTask(taskName) {
				return &exitCodeError{code: 2, err: fmt.Errorf("unknown command: %s", taskName)}
			}

			resolvedCwd, err := resolveWorkingDirectory(resolvedRoot, cwd)
			if err != nil {
				return &exitCodeError{code: 2, err: err}
			}

			ctx := &app.Context{
				Root:     resolvedRoot,
				Cwd:      resolvedCwd,
				Format:   mode,
				Quiet:    quiet,
				NoColor:  noColor,
				Timeout:  timeout,
				ExtraEnv: collectEnvFromHost(defaultRunEnvKeys()),
				Stdout:   stdout,
				Stderr:   stderr,
			}

			result, runErr := ops.Run(context.Background(), taskName, taskArgs, ctx)
			if err := format.Render(cobraCmd.OutOrStdout(), mode, result, ctx.Quiet); err != nil {
				return &exitCodeError{code: 1, err: fmt.Errorf("failed to render output: %v", err)}
			}
			if runErr != nil {
				return &exitCodeError{code: 1, err: runErr}
			}
			return nil
		},
		SilenceErrors: true,
		SilenceUsage:  true,
	}

	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetArgs(args)

	root.PersistentFlags().StringVar(&formatValue, "format", string(format.OutputText), "output mode: text, json, raw")
	root.PersistentFlags().BoolVar(&jsonMode, "json", false, "shortcut for --format json")
	root.PersistentFlags().BoolVar(&rawMode, "raw", false, "shortcut for --format raw")
	root.PersistentFlags().BoolVar(&noColor, "no-color", false, "disable color output")
	root.PersistentFlags().BoolVar(&quiet, "quiet", false, "suppress non-error output")
	root.PersistentFlags().StringVar(&rootFlag, "root", "", "repository root")
	root.PersistentFlags().StringVar(&cwd, "cwd", "", "working directory for command execution")
	root.PersistentFlags().DurationVar(&timeout, "timeout", 0, "command timeout")

	root.SetHelpFunc(func(cobraCmd *cobra.Command, _ []string) {
		if _, err := parseOutputMode(rawMode, jsonMode, formatValue); err != nil {
			_, _ = fmt.Fprintln(cobraCmd.ErrOrStderr(), err)
			return
		}
		rootPath, err := resolveRootPath(rootFlag)
		if err != nil {
			_, _ = fmt.Fprintln(cobraCmd.ErrOrStderr(), err)
			return
		}
		printUsage(cobraCmd.OutOrStdout(), newOpsApp(rootPath), cobraCmd.Root().Name())
	})

	return root
}

func run(args []string, stdout, stderr io.Writer) int {
	command := buildOpsctlCommand(stdout, stderr, args)
	if err := command.Execute(); err != nil {
		if codeErr, ok := err.(*exitCodeError); ok {
			_, _ = fmt.Fprintln(stderr, codeErr.err)
			return codeErr.code
		}
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func main() {
	if code := run(os.Args[1:], os.Stdout, os.Stderr); code != 0 {
		os.Exit(code)
	}
}
