package tasks

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ZanzyTHEbar/dragonscale/internal/opsctl/app"
	"github.com/ZanzyTHEbar/dragonscale/internal/opsctl/runner"
	"github.com/stretchr/testify/require"
)

func TestNewRegistryContainsCoreTasks(t *testing.T) {
	t.Parallel()

	tasks := NewRegistry("/workspaces/picoclaw")
	names := make(map[string]struct{}, len(tasks))
	for _, task := range tasks {
		names[task.Name()] = struct{}{}
	}

	required := []string{
		"build",
		"build-all",
		"check",
		"clean",
		"deps",
		"devcontainer-build",
		"devcontainer-up",
		"devcontainer-generate",
		"devcontainer-verify",
		"eval",
		"eval-build",
		"eval-compare",
		"eval-fixtures",
		"eval-test",
		"eval-view",
		"eval-clean",
		"generate",
		"fmt",
		"flatc-check",
		"hooks",
		"install",
		"lint",
		"run",
		"sqlc-check",
		"sqlc-vet",
		"test",
		"test-integration",
		"uninstall",
		"uninstall-all",
		"update-deps",
		"vet",
	}

	for _, name := range required {
		_, ok := names[name]
		require.Truef(t, ok, "missing core task %q in registry", name)
	}
}

func TestRunTaskEnvUsesArgv(t *testing.T) {
	t.Parallel()

	ctx := &app.Context{
		Argv: []string{"--verbose", "foo bar", "baz"},
	}

	got := runTaskEnv(ctx)
	require.Len(t, got, 1)
	require.Equal(t, "ARGS=--verbose foo bar baz", got[0])
}

func TestQuoteArgs(t *testing.T) {
	t.Parallel()

	got := quoteArgs([]string{"foo bar", "x\ty", `a"b`})
	require.Equal(t, []string{"\"foo bar\"", "\"x\\ty\"", `"a\"b"`}, got)
}

func TestBuildAllTaskPreservesGoEnvironmentForwarding(t *testing.T) {
	origCheck := detectGlibc
	detectGlibc = func() error { return nil }
	t.Cleanup(func() { detectGlibc = origCheck })

	ctx := &app.Context{
		Root: t.TempDir(),
		ExtraEnv: map[string]string{
			"GOOS":              "linux",
			"GOARCH":            "sparc64",
			"GOFLAGS":           "-mod=mod",
			"DEVCONTAINER_EXEC": "echo devcontainer exec",
		},
	}
	fake := &runner.FakeRunner{
		Result: runner.CommandResult{ExitCode: 0},
	}

	var buildAll app.Task
	for _, task := range NewRegistry(ctx.Root) {
		if task.Name() == "build-all" {
			buildAll = task
			break
		}
	}
	require.NotNil(t, buildAll)

	_, err := buildAll.Run(context.Background(), fake, ctx)
	require.NoError(t, err)
	require.Len(t, fake.Calls, 1)

	script := strings.Join(fake.Calls[0].Args, " ")
	require.Contains(t, script, "GOOS=$GOOS")
	require.NotContains(t, script, "if [ \"$GOOS\" != \"linux\" ]; then")
	require.Contains(t, script, "CGO_ENABLED=1")
	require.NotContains(t, script, "CGO_BUILD=1")
	require.Contains(t, script, "ln -sf dragonscale-linux-sparc64 bin/dragonscale")
	require.NotContains(t, script, "ln -sf ./bin/dragonscale-linux-sparc64 bin/dragonscale")

	joinedEnv := strings.Join(fake.Calls[0].Env, " ")
	require.Contains(t, joinedEnv, "GOOS=linux")
	require.Contains(t, joinedEnv, "GOARCH=sparc64")
	require.Contains(t, joinedEnv, "GOFLAGS=-mod=mod")
	require.Contains(t, joinedEnv, "DEVCONTAINER_EXEC=echo devcontainer exec")
}

func TestBuildAllTaskDefaultsToHostTarget(t *testing.T) {
	origCheck := detectGlibc
	detectGlibc = func() error { return nil }
	t.Cleanup(func() { detectGlibc = origCheck })

	ctx := &app.Context{
		Root: t.TempDir(),
		ExtraEnv: map[string]string{
			"GOFLAGS":           "-mod=mod",
			"DEVCONTAINER_EXEC": "echo devcontainer exec",
		},
	}
	fake := &runner.FakeRunner{
		Result: runner.CommandResult{ExitCode: 0},
	}

	var buildAll app.Task
	for _, task := range NewRegistry(ctx.Root) {
		if task.Name() == "build-all" {
			buildAll = task
			break
		}
	}
	require.NotNil(t, buildAll)

	_, err := buildAll.Run(context.Background(), fake, ctx)
	require.NoError(t, err)
	require.Len(t, fake.Calls, 1)

	script := strings.Join(fake.Calls[0].Args, " ")
	require.Contains(t, script, "build-all: target=linux/")
	require.NotContains(t, script, "GOOS=$($GO env GOOS)")
	require.Contains(t, script, "CGO_ENABLED=1")
	require.NotContains(t, script, "CGO_BUILD=1")
}

func TestBuildAllTaskRejectsNonLinuxTarget(t *testing.T) {
	origCheck := detectGlibc
	detectGlibc = func() error { return nil }
	t.Cleanup(func() { detectGlibc = origCheck })

	ctx := &app.Context{
		Root: t.TempDir(),
		ExtraEnv: map[string]string{
			"GOOS": "freebsd",
		},
	}
	fake := &runner.FakeRunner{
		Result: runner.CommandResult{ExitCode: 0},
	}

	var buildAll app.Task
	for _, task := range NewRegistry(ctx.Root) {
		if task.Name() == "build-all" {
			buildAll = task
			break
		}
	}
	require.NotNil(t, buildAll)

	_, err := buildAll.Run(context.Background(), fake, ctx)
	require.Error(t, err)
	require.Len(t, fake.Calls, 0)
}

func TestDepsTaskPrefixesEachGoCommand(t *testing.T) {
	t.Parallel()

	ctx := &app.Context{
		Root: t.TempDir(),
		ExtraEnv: map[string]string{
			"SKIP_DEVCONTAINER_WRAPPER": "1",
		},
	}
	fake := &runner.FakeRunner{Result: runner.CommandResult{ExitCode: 0}}

	var deps app.Task
	for _, task := range NewRegistry(ctx.Root) {
		if task.Name() == "deps" {
			deps = task
			break
		}
	}
	require.NotNil(t, deps)

	_, err := deps.Run(context.Background(), fake, ctx)
	require.NoError(t, err)
	require.Len(t, fake.Calls, 1)

	script := strings.Join(fake.Calls[0].Args, " ")
	require.Contains(t, script, "$GO mod download && $GO mod verify")
	require.NotContains(t, script, "$GO mod download && mod verify")
}

func TestUpdateDepsTaskPrefixesEachGoCommand(t *testing.T) {
	t.Parallel()

	ctx := &app.Context{
		Root: t.TempDir(),
		ExtraEnv: map[string]string{
			"SKIP_DEVCONTAINER_WRAPPER": "1",
		},
	}
	fake := &runner.FakeRunner{Result: runner.CommandResult{ExitCode: 0}}

	var updateDeps app.Task
	for _, task := range NewRegistry(ctx.Root) {
		if task.Name() == "update-deps" {
			updateDeps = task
			break
		}
	}
	require.NotNil(t, updateDeps)

	_, err := updateDeps.Run(context.Background(), fake, ctx)
	require.NoError(t, err)
	require.Len(t, fake.Calls, 1)

	script := strings.Join(fake.Calls[0].Args, " ")
	require.Contains(t, script, "$GO get -u ./... && $GO mod tidy")
	require.NotContains(t, script, "$GO get -u ./... && mod tidy")
}

func TestEvalRunSpecsPreservesEvalConfig(t *testing.T) {
	t.Parallel()

	specs := evalRunSpecs(&app.Context{
		ExtraEnv: map[string]string{
			"DRAGONSCALE_EVAL_DEBUG":  "1",
			"DRAGONSCALE_EVAL_CONFIG": "./configs/override.json",
		},
	})

	require.Len(t, specs, 2)
	require.Equal(t, "echo", specs[0].Name)
	require.Equal(t, []string{"DRAGONSCALE_EVAL_CONFIG=./configs/override.json"}, specs[0].Args)
	require.Equal(t, "npx", specs[1].Name)
	require.Contains(t, strings.Join(specs[1].Env, " "), "DRAGONSCALE_EVAL_CONFIG=./configs/override.json")
	require.NotContains(t, strings.Join(specs[1].Env, " "), "DRAGONSCALE_EVAL_CONFIG=./configs/default.json")
	require.Contains(t, strings.Join(specs[1].Args, " "), "--yes promptfoo eval --config promptfooconfig.yaml --no-cache --no-progress-bar -j 1")

	compareScript, err := os.ReadFile(filepath.Clean(filepath.Join("..", "..", "..", "eval", "scripts", "compare.sh")))
	require.NoError(t, err)
	content := string(compareScript)
	require.Contains(t, content, "EVAL_CONFIG=\"${DRAGONSCALE_EVAL_CONFIG:-./configs/default.json}\"")
	require.Contains(t, content, "trap cleanup_compare_config EXIT INT TERM")
	require.Contains(t, content, "DRAGONSCALE_EVAL_CONFIG: \"${EVAL_CONFIG}\"")
	require.NotContains(t, content, "DRAGONSCALE_EVAL_HOST_HOME: \"/host_home\"")
	require.Contains(t, content, "PROMPTFOO_ARGS=\"${DRAGONSCALE_PROMPTFOO_ARGS:---no-cache --no-progress-bar -j 1}\"")
	require.Contains(t, content, "git -C \"$PROJECT_ROOT\" worktree add --force --detach")
	require.Contains(t, content, "TEMP_CONFIG")
}

func TestEvalRunSpecsUsesBaseConfigWhenSetAndDebugEnabled(t *testing.T) {
	t.Parallel()

	specs := evalRunSpecs(&app.Context{
		ExtraEnv: map[string]string{
			"DRAGONSCALE_EVAL_BASE_CONFIG": "/tmp/base.json",
			"DRAGONSCALE_EVAL_DEBUG":       "1",
		},
	})

	require.Len(t, specs, 3)
	require.Equal(t, "echo", specs[0].Name)
	require.Equal(t, []string{"DRAGONSCALE_EVAL_BASE_CONFIG=/tmp/base.json"}, specs[0].Args)
	require.Equal(t, "echo", specs[1].Name)
	require.Equal(t, []string{"DRAGONSCALE_EVAL_CONFIG=./configs/default.json"}, specs[1].Args)
	require.Equal(t, "npx", specs[2].Name)
	require.Equal(t, []string{"--yes", "promptfoo", "eval", "--config", "promptfooconfig.yaml", "--no-cache", "--no-progress-bar", "-j", "1"}, specs[2].Args)
}

func TestEvalRunSpecsUsesPromptfooArgsOverride(t *testing.T) {
	t.Parallel()

	specs := evalRunSpecs(&app.Context{
		Root: t.TempDir(),
		ExtraEnv: map[string]string{
			"DRAGONSCALE_PROMPTFOO_ARGS": "--no-cache --max-concurrency 1",
		},
	})

	require.NotEmpty(t, specs)
	last := specs[len(specs)-1]
	require.Equal(t, "npx", last.Name)
	require.Equal(t, []string{
		"--yes", "promptfoo", "eval", "--config", "promptfooconfig.yaml",
		"--no-cache", "--max-concurrency", "1",
	}, last.Args)
}

func TestEvalCompareTaskDisablesNestedDevcontainerExecution(t *testing.T) {
	t.Parallel()

	ctx := &app.Context{
		Root: t.TempDir(),
		ExtraEnv: map[string]string{
			"DEVCONTAINER_EXEC":      "npx --yes @devcontainers/cli exec --workspace-folder \"$PWD\" --",
			"DRAGONSCALE_EVAL_DEBUG": "1",
		},
	}
	fake := &runner.FakeRunner{
		Result: runner.CommandResult{ExitCode: 0},
	}

	var evalCompare app.Task
	for _, task := range NewRegistry(ctx.Root) {
		if task.Name() == "eval-compare" {
			evalCompare = task
			break
		}
	}
	require.NotNil(t, evalCompare)

	_, err := evalCompare.Run(context.Background(), fake, ctx)
	require.NoError(t, err)
	require.Len(t, fake.Calls, 1)
	script := strings.Join(fake.Calls[0].Args, " ")
	require.Contains(t, script, evalWorkspaceDir(ctx))
	require.Contains(t, script, "DEVCONTAINER_EXEC= EVAL_NPM_CMD=\"npx --yes\" ./scripts/compare.sh --repeat 3")
	joinedEnv := strings.Join(fake.Calls[0].Env, " ")
	require.Contains(t, joinedEnv, "DEVCONTAINER_EXEC=npx --yes @devcontainers/cli exec --workspace-folder \"$PWD\" --")

	compareScript, err := os.ReadFile(filepath.Clean(filepath.Join("..", "..", "..", "eval", "scripts", "compare.sh")))
	require.NoError(t, err)
	content := string(compareScript)
	require.Contains(t, content, "export DEVCONTAINER_EXEC=\"\"")
	require.Contains(t, content, "make DEVCONTAINER_EXEC= eval-build")
	require.Contains(t, content, "git -C \"$PROJECT_ROOT\" worktree add --force --detach")
	require.Contains(t, content, "git -C \"$PROJECT_ROOT\" worktree remove --force")
}

func TestEvalCompareTaskPassesBaseConfigEnvToRunner(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name             string
		baseConfig       *string
		expectedIncluded bool
		expectedValue    string
	}{
		{name: "unset", baseConfig: nil, expectedIncluded: false},
		{name: "empty", baseConfig: ptrString(""), expectedIncluded: false},
		{name: "explicit", baseConfig: ptrString("/tmp/explicit.json"), expectedIncluded: true, expectedValue: "/tmp/explicit.json"},
		{name: "invalid", baseConfig: ptrString("/tmp/missing-base-config.json"), expectedIncluded: true, expectedValue: "/tmp/missing-base-config.json"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := &app.Context{
				Root: t.TempDir(),
				ExtraEnv: map[string]string{
					"DRAGONSCALE_EVAL_DEBUG": "1",
				},
			}
			if tc.baseConfig != nil {
				ctx.ExtraEnv["DRAGONSCALE_EVAL_BASE_CONFIG"] = *tc.baseConfig
			}

			fake := &runner.FakeRunner{
				Result: runner.CommandResult{ExitCode: 0},
			}

			var evalCompare app.Task
			for _, task := range NewRegistry(ctx.Root) {
				if task.Name() == "eval-compare" {
					evalCompare = task
					break
				}
			}
			require.NotNil(t, evalCompare)

			_, err := evalCompare.Run(context.Background(), fake, ctx)
			require.NoError(t, err)
			require.Len(t, fake.Calls, 1)

			joinedEnv := strings.Join(fake.Calls[0].Env, " ")
			if tc.expectedIncluded {
				require.Contains(t, joinedEnv, "DRAGONSCALE_EVAL_BASE_CONFIG="+tc.expectedValue)
			} else {
				require.NotContains(t, joinedEnv, "DRAGONSCALE_EVAL_BASE_CONFIG=")
			}
		})
	}
}

func TestLintTaskUsesCommandSpecs(t *testing.T) {
	ctx := &app.Context{
		Root: t.TempDir(),
	}
	fake := &runner.FakeRunner{
		Result: runner.CommandResult{ExitCode: 0},
	}

	task := findTaskByName(t, NewRegistry(ctx.Root), "lint")
	_, err := task.Run(context.Background(), fake, ctx)
	require.NoError(t, err)
	require.Len(t, fake.Calls, 3)
	require.Equal(t, "go", fake.Calls[0].Name)
	require.Equal(t, []string{"fmt", "./..."}, fake.Calls[0].Args)
	require.Equal(t, "go", fake.Calls[1].Name)
	require.Equal(t, []string{"vet", "./..."}, fake.Calls[1].Args)
	require.Equal(t, "go", fake.Calls[2].Name)
	require.Equal(t, []string{"build", "./..."}, fake.Calls[2].Args)
	require.NotContains(t, strings.Join(fake.Calls[0].Env, " "), "set -euo pipefail")
}

func TestCheckTaskUsesCommandSpecs(t *testing.T) {
	ctx := &app.Context{
		Root: t.TempDir(),
		ExtraEnv: map[string]string{
			"GO": "go",
		},
	}
	fake := &runner.FakeRunner{
		Result: runner.CommandResult{ExitCode: 0},
	}

	task := findTaskByName(t, NewRegistry(ctx.Root), "check")
	_, err := task.Run(context.Background(), fake, ctx)
	require.NoError(t, err)
	require.Len(t, fake.Calls, 5)
	require.Equal(t, []string{"mod", "download"}, fake.Calls[0].Args)
	require.Equal(t, []string{"mod", "verify"}, fake.Calls[1].Args)
	require.Equal(t, []string{"fmt", "./..."}, fake.Calls[2].Args)
	require.Equal(t, []string{"vet", "./..."}, fake.Calls[3].Args)
	require.Equal(t, []string{"test", "./..."}, fake.Calls[4].Args)
}

func TestDevcontainerGenerateTaskUsesNpxExecCommand(t *testing.T) {
	ctx := &app.Context{
		Root: t.TempDir(),
	}
	fake := &runner.FakeRunner{
		Result: runner.CommandResult{ExitCode: 0},
	}

	task := findTaskByName(t, NewRegistry(ctx.Root), "devcontainer-generate")
	_, err := task.Run(context.Background(), fake, ctx)
	require.NoError(t, err)
	require.Len(t, fake.Calls, 1)
	require.Equal(t, "npx", fake.Calls[0].Name)
	require.Contains(t, fake.Calls[0].Args, "@devcontainers/cli")
	require.Contains(t, fake.Calls[0].Args, "exec")
	require.Contains(t, fake.Calls[0].Args, "bash")
	require.Contains(t, strings.Join(fake.Calls[0].Args, " "), "go generate ./pkg/itr ./pkg/tools")
}

func TestEvalTaskBuildsPromptfooCommandWithDefaultConfig(t *testing.T) {
	ctx := &app.Context{
		Root: t.TempDir(),
		ExtraEnv: map[string]string{
			"DRAGONSCALE_EVAL_DEBUG": "1",
		},
	}
	fake := &runner.FakeRunner{
		Result: runner.CommandResult{ExitCode: 0},
	}

	task := findTaskByName(t, NewRegistry(ctx.Root), "eval")
	_, err := task.Run(context.Background(), fake, ctx)
	require.NoError(t, err)
	require.Len(t, fake.Calls, 2)
	require.Equal(t, "echo", fake.Calls[0].Name)
	require.Equal(t, "npx", fake.Calls[1].Name)
	require.Equal(t, filepath.Join(ctx.Root, "eval"), fake.Calls[1].Dir)
	joinedEnv := strings.Join(fake.Calls[1].Env, " ")
	require.Contains(t, joinedEnv, "DRAGONSCALE_EVAL_CONFIG=./configs/default.json")
	require.Contains(t, strings.Join(fake.Calls[1].Args, " "), "--no-progress-bar")
}

func TestEvalTestTaskBypassesDevcontainerWrapperWhenDisabled(t *testing.T) {
	t.Parallel()

	ctx := &app.Context{
		Root: t.TempDir(),
		ExtraEnv: map[string]string{
			"DEVCONTAINER_EXEC":         "npx --yes @devcontainers/cli exec --workspace-folder \"$PWD\" --",
			"SKIP_DEVCONTAINER_WRAPPER": "1",
		},
	}
	fake := &runner.FakeRunner{Result: runner.CommandResult{ExitCode: 0}}

	task := findTaskByName(t, NewRegistry(ctx.Root), "eval-test")
	_, err := task.Run(context.Background(), fake, ctx)
	require.NoError(t, err)
	require.Len(t, fake.Calls, 1)
	require.Equal(t, "bash", fake.Calls[0].Name)
	joinedArgs := strings.Join(fake.Calls[0].Args, " ")
	require.Contains(t, joinedArgs, "$GO test -v ./eval/go_evals/...")
	require.NotContains(t, joinedArgs, "@devcontainers/cli")
	joinedEnv := strings.Join(fake.Calls[0].Env, " ")
	require.Contains(t, joinedEnv, "DEVCONTAINER_EXEC=npx --yes @devcontainers/cli exec --workspace-folder \"$PWD\" --")
}

func TestEvalViewTaskRunsInEvalDirectory(t *testing.T) {
	ctx := &app.Context{
		Root: t.TempDir(),
	}
	fake := &runner.FakeRunner{
		Result: runner.CommandResult{ExitCode: 0},
	}
	task := findTaskByName(t, NewRegistry(ctx.Root), "eval-view")
	_, err := task.Run(context.Background(), fake, ctx)
	require.NoError(t, err)
	require.Len(t, fake.Calls, 1)
	require.Equal(t, filepath.Join(ctx.Root, "eval"), fake.Calls[0].Dir)
	require.Equal(t, "npx", fake.Calls[0].Name)
	require.Equal(t, []string{"--yes", "promptfoo", "view"}, fake.Calls[0].Args)
}

func TestEvalRunSpecsPrependsBuildWhenRunnerMissingAndSourceTreeExists(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "eval", "cmd", "eval-runner"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "eval", "cmd", "eval-runner", "main.go"), []byte("package main\nfunc main() {}\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "eval", "fixtures", "skills"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "eval", "fixtures", "sample_data.txt"), []byte("fixture"), 0o644))

	specs := evalRunSpecs(&app.Context{Root: root})
	require.GreaterOrEqual(t, len(specs), 10)
	require.Equal(t, "go", specs[0].Name)
	require.Equal(t, []string{"generate", "./..."}, specs[0].Args)
	require.Equal(t, root, specs[0].Dir)
	require.Equal(t, "mkdir", specs[3].Name)
	require.Equal(t, "rm", specs[4].Name)
	require.Equal(t, "cp", specs[8].Name)
	require.Equal(t, "npx", specs[len(specs)-1].Name)
	require.Equal(t, filepath.Join(root, "eval"), specs[len(specs)-1].Dir)
}

func TestEvalRunSpecsPrependsBuildWhenRunnerAlreadyExists(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "eval", "cmd", "eval-runner"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "eval", "cmd", "eval-runner", "main.go"), []byte("package main\nfunc main() {}\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "eval", "bin"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "eval", "bin", "eval-runner"), []byte("stale-binary"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "eval", "fixtures", "skills"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "eval", "fixtures", "sample_data.txt"), []byte("fixture"), 0o644))

	specs := evalRunSpecs(&app.Context{Root: root})
	require.GreaterOrEqual(t, len(specs), 10)
	require.Equal(t, "go", specs[0].Name)
	require.Equal(t, []string{"generate", "./..."}, specs[0].Args)
	require.Equal(t, root, specs[0].Dir)
	require.Equal(t, "npx", specs[len(specs)-1].Name)
}

func TestEvalRunSpecsPrependsFixturesWhenSourceTreeExists(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "eval", "cmd", "eval-runner"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "eval", "cmd", "eval-runner", "main.go"), []byte("package main\nfunc main() {}\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "eval", "fixtures", "skills"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "eval", "fixtures", "sample_data.txt"), []byte("fixture"), 0o644))

	specs := evalRunSpecs(&app.Context{Root: root})
	require.GreaterOrEqual(t, len(specs), 10)
	require.Equal(t, "mkdir", specs[3].Name)
	require.Equal(t, []string{"-p", filepath.Join(homeDir(), ".local", "share", "dragonscale", "sandbox")}, specs[3].Args)
	require.Equal(t, "rm", specs[4].Name)
	require.Equal(t, "bash", specs[7].Name)
	require.Equal(t, "cp", specs[8].Name)
	require.Equal(t, "bash", specs[9].Name)
	require.Equal(t, "npx", specs[len(specs)-1].Name)
	joinedArgs := strings.Join(specs[len(specs)-1].Args, " ")
	require.Contains(t, joinedArgs, "promptfoo eval --config promptfooconfig.yaml")
}

func TestEvalTasksUseRepoRootPathsWhenCwdIsNested(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	nested := filepath.Join(root, "pkg", "agent")
	require.NoError(t, os.MkdirAll(nested, 0o755))
	ctx := &app.Context{
		Root: root,
		Cwd:  nested,
	}
	fake := &runner.FakeRunner{Result: runner.CommandResult{ExitCode: 0}}

	evalView := findTaskByName(t, NewRegistry(ctx.Root), "eval-view")
	_, err := evalView.Run(context.Background(), fake, ctx)
	require.NoError(t, err)
	require.Len(t, fake.Calls, 1)
	require.Equal(t, filepath.Join(root, "eval"), fake.Calls[0].Dir)

	fake.Calls = nil
	evalCompare := findTaskByName(t, NewRegistry(ctx.Root), "eval-compare")
	_, err = evalCompare.Run(context.Background(), fake, ctx)
	require.NoError(t, err)
	require.Len(t, fake.Calls, 1)
	compareCmd := strings.Join(fake.Calls[0].Args, " ")
	require.Contains(t, compareCmd, filepath.Join(root, "eval"))
	require.Contains(t, compareCmd, "DEVCONTAINER_EXEC= EVAL_NPM_CMD=\"npx --yes\" ./scripts/compare.sh --repeat 3")
}

func TestEvalFixturesTaskCreatesFixtureCommands(t *testing.T) {
	ctx := &app.Context{
		Root: t.TempDir(),
	}
	fake := &runner.FakeRunner{
		Result: runner.CommandResult{ExitCode: 0},
	}
	task := findTaskByName(t, NewRegistry(ctx.Root), "eval-fixtures")
	_, err := task.Run(context.Background(), fake, ctx)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(fake.Calls), 5)
	require.Equal(t, "mkdir", fake.Calls[0].Name)
	require.Equal(t, "rm", fake.Calls[1].Name)
	require.Equal(t, "bash", fake.Calls[4].Name)
	require.Equal(t, "cp", fake.Calls[5].Name)
}

func findTaskByName(t *testing.T, tasks []app.Task, name string) app.Task {
	t.Helper()
	for _, task := range tasks {
		if task.Name() == name {
			return task
		}
	}
	t.Fatalf("task %q missing from registry", name)
	return nil
}

func ptrString(value string) *string {
	return &value
}
