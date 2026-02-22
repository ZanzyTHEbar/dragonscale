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
	t.Parallel()

	ctx := &app.Context{
		Root: t.TempDir(),
		ExtraEnv: map[string]string{
			"GOOS":              "freebsd",
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
	require.Contains(t, script, "GOOS=linux GOARCH=amd64 $GO build")
	require.Contains(t, script, "GOOS=darwin GOARCH=arm64 $GO build")
	require.Contains(t, script, "GOOS=windows GOARCH=amd64 $GO build")
	require.Contains(t, script, "GOOS=linux GOARCH=arm64 $GO build")

	joinedEnv := strings.Join(fake.Calls[0].Env, " ")
	require.Contains(t, joinedEnv, "GOOS=freebsd")
	require.Contains(t, joinedEnv, "GOARCH=sparc64")
	require.Contains(t, joinedEnv, "GOFLAGS=-mod=mod")
	require.Contains(t, joinedEnv, "DEVCONTAINER_EXEC=echo devcontainer exec")
}

func TestEvalRunScriptPreservesEvalConfig(t *testing.T) {
	t.Parallel()

	script := evalRunScript(&app.Context{
		ExtraEnv: map[string]string{
			"DRAGONSCALE_EVAL_DEBUG":  "1",
			"DRAGONSCALE_EVAL_CONFIG": "./configs/override.json",
		},
	})

	require.Contains(t, script, "DRAGONSCALE_EVAL_CONFIG=\"${DRAGONSCALE_EVAL_CONFIG:-./configs/default.json}\"")
	require.Contains(t, script, "npx --yes promptfoo eval --config promptfooconfig.yaml")
	require.NotContains(t, script, "DRAGONSCALE_EVAL_CONFIG=\"./configs/default.json\"")

	compareScript, err := os.ReadFile(filepath.Clean(filepath.Join("..", "..", "..", "eval", "scripts", "compare.sh")))
	require.NoError(t, err)
	content := string(compareScript)
	require.Contains(t, content, "EVAL_CONFIG=\"${DRAGONSCALE_EVAL_CONFIG:-./configs/default.json}\"")
	require.Contains(t, content, "trap cleanup_compare_config EXIT INT TERM")
	require.Contains(t, content, "DRAGONSCALE_EVAL_CONFIG: \"${EVAL_CONFIG}\"")
	require.Contains(t, content, "TEMP_CONFIG")
}

func TestEvalRunScriptUsesBaseConfigWhenSet(t *testing.T) {
	t.Parallel()

	script := evalRunScript(&app.Context{
		ExtraEnv: map[string]string{
			"DRAGONSCALE_EVAL_BASE_CONFIG": "/tmp/base.json",
		},
	})
	require.Contains(t, script, `if [ -n "${DRAGONSCALE_EVAL_BASE_CONFIG:-}" ] && [ -n "${DRAGONSCALE_EVAL_DEBUG:-}" ]; then`)
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
	require.Contains(t, script, "cd eval && DEVCONTAINER_EXEC= EVAL_NPM_CMD=$(npx --yes) ./scripts/compare.sh --repeat 3")
	joinedEnv := strings.Join(fake.Calls[0].Env, " ")
	require.Contains(t, joinedEnv, "DEVCONTAINER_EXEC=npx --yes @devcontainers/cli exec --workspace-folder \"$PWD\" --")

	compareScript, err := os.ReadFile(filepath.Clean(filepath.Join("..", "..", "..", "eval", "scripts", "compare.sh")))
	require.NoError(t, err)
	content := string(compareScript)
	require.Contains(t, content, "export DEVCONTAINER_EXEC=\"\"")
	require.Contains(t, content, "make DEVCONTAINER_EXEC= eval-build")
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

func ptrString(value string) *string {
	return &value
}
