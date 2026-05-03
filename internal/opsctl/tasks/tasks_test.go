package tasks

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/ZanzyTHEbar/dragonscale/internal/opsctl/app"
	"github.com/ZanzyTHEbar/dragonscale/internal/opsctl/runner"
	"github.com/ZanzyTHEbar/dragonscale/internal/testcmp"
	"github.com/stretchr/testify/require"
)

func readRepoFile(t *testing.T, path string) string {
	t.Helper()

	content, err := os.ReadFile(filepath.Clean(filepath.Join("..", "..", "..", filepath.FromSlash(path))))
	require.NoError(t, err)
	return string(content)
}

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
		"mockgen-check",
		"run",
		"sqlc-check",
		"sqlc-vet",
		"test",
		"test-containers",
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

func TestFlatcPinMatchesGoRuntime(t *testing.T) {
	t.Parallel()

	installScript := readRepoFile(t, "scripts/install-flatc.sh")
	versionRE := regexp.MustCompile(`(?m)^FLATC_VERSION="\$\{FLATC_VERSION:-([0-9.]+)\}"$`)
	match := versionRE.FindStringSubmatch(installScript)
	require.Len(t, match, 2)

	goMod := readRepoFile(t, "go.mod")
	require.Contains(t, goMod, "github.com/google/flatbuffers v"+match[1]+"+incompatible")
	require.Contains(t, installScript, `FLATC_SHA256="${FLATC_SHA256:-9f87066dc5dfa7fe02090b55bab5f3e55df03e32c9b0cdf229004ade7d091039}"`)
}

func TestFlatcInstallSurfacesUsePinnedInstaller(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		".github/workflows/pr.yml",
		".github/workflows/eval.yml",
		".devcontainer/Dockerfile",
	} {
		content := readRepoFile(t, path)
		require.Contains(t, content, "scripts/install-flatc.sh", path)
		require.NotContains(t, content, "flatbuffers-compiler", path)
	}
}

func TestDeploymentFilesUseSupportedLinuxGlibcContract(t *testing.T) {
	t.Parallel()

	dockerfile := readRepoFile(t, "Dockerfile")
	dockerfileLower := strings.ToLower(dockerfile)
	require.Contains(t, dockerfile, "FROM golang:1.26.2-bookworm AS builder")
	require.Contains(t, dockerfile, "FROM debian:bookworm-slim")
	require.Contains(t, dockerfile, "CGO_ENABLED=1 GOOS=linux make build")
	require.Contains(t, dockerfile, "curl -fsS http://localhost:18790/health")
	require.NotContains(t, dockerfileLower, "alpine")
	require.NotContains(t, dockerfile, "dragonscale onboard")

	dockerfileGoreleaser := readRepoFile(t, "Dockerfile.goreleaser")
	require.Contains(t, dockerfileGoreleaser, "FROM debian:bookworm-slim")
	require.Contains(t, dockerfileGoreleaser, "USER dragonscale")
	require.Contains(t, dockerfileGoreleaser, "XDG_CONFIG_HOME=/home/dragonscale/.config")
	require.NotContains(t, strings.ToLower(dockerfileGoreleaser), "alpine")

	compose := readRepoFile(t, "docker-compose.yml")
	require.Contains(t, compose, "/home/dragonscale/.config/dragonscale/config.json")
	require.Contains(t, compose, "/home/dragonscale/.local/share/dragonscale")
	require.Contains(t, compose, "path: .env")
	require.Contains(t, compose, "required: false")
	require.Contains(t, compose, "DRAGONSCALE_GATEWAY_HOST: 0.0.0.0")
	require.Contains(t, compose, `"18790:18790"`)
	require.NotContains(t, compose, "/home/dragonscale/.dragonscale")
	require.NotContains(t, compose, ":-}")

	goreleaser := readRepoFile(t, ".goreleaser.yaml")
	require.Contains(t, goreleaser, "CGO_ENABLED=1")
	require.Contains(t, goreleaser, "- linux")
	require.Contains(t, goreleaser, "- amd64")
	for _, unsupported := range []string{"CGO_ENABLED=0", "- windows", "- darwin", "- freebsd", "- riscv64", "- s390x", "- mips64", "- arm64"} {
		require.NotContains(t, goreleaser, unsupported)
	}
}

func TestExampleConfigUsesCurrentSchemaAndBlankSecrets(t *testing.T) {
	t.Parallel()

	content := readRepoFile(t, "config/config.example.json")
	var raw map[string]any
	require.NoError(t, json.Unmarshal([]byte(content), &raw))

	agents, ok := raw["agents"].(map[string]any)
	require.True(t, ok)
	defaults, ok := agents["defaults"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, defaults, "sandbox")
	require.Contains(t, defaults, "restrict_to_sandbox")
	require.NotContains(t, defaults, "workspace")
	require.NotContains(t, defaults, "restrict_to_workspace")

	for _, stale := range []string{"~/.dragonscale", "YOUR_", "sk-or-v1-", "gsk_", "nvapi-", "sk-xxx", "pplx-xxx"} {
		require.NotContains(t, content, stale)
	}
}

func TestEnvExampleUsesRuntimeEnvNames(t *testing.T) {
	t.Parallel()

	content := readRepoFile(t, ".env.example")
	for _, name := range []string{
		"DRAGONSCALE_AGENTS_DEFAULTS_PROVIDER",
		"DRAGONSCALE_AGENTS_DEFAULTS_MODEL",
		"DRAGONSCALE_PROVIDERS_OPENROUTER_API_KEY",
		"DRAGONSCALE_PROVIDERS_OPENCODE_API_KEY",
		"DRAGONSCALE_PROVIDERS_OPENCODE_API_BASE",
		"OPENCODE_API_KEY",
		"OPENCODE_GO_API_KEY",
		"DRAGONSCALE_TOOLS_WEB_BRAVE_API_KEY",
	} {
		require.Contains(t, content, name)
	}
	for _, legacy := range []string{"# OPENROUTER_API_KEY=", "# OPENAI_API_KEY=", "# BRAVE_SEARCH_API_KEY=", "# TELEGRAM_BOT_TOKEN=", "# DISCORD_BOT_TOKEN="} {
		require.NotContains(t, content, legacy)
	}
}

func TestFlatcCheckUsesSchemaLocalFlatcCommands(t *testing.T) {
	t.Parallel()

	script := flatcCheckScript(nil)
	require.Contains(t, script, "(cd pkg/itr && flatc --go -o . ./commands.fbs)")
	require.Contains(t, script, "(cd pkg/tools && flatc --go -o . ./map_payloads.fbs)")
	require.NotContains(t, script, "$GO generate ./pkg/itr ./pkg/tools")
	require.Contains(t, script, "pkg/itr/itrfb/ pkg/tools/mapopsfb/")
}

func TestMockgenCheckUsesMockgenOnlyGeneration(t *testing.T) {
	t.Parallel()

	script := mockgenCheckScript(nil)
	require.Contains(t, script, "$GO generate -run mockgen ./...")
	require.Contains(t, script, "(cd internal/fantasy && $GO generate -run mockgen ./...)")
	require.Contains(t, script, ":(glob)**/mock_*_test.go")
	require.Contains(t, script, "trap 'rm -f")
	require.NotContains(t, script, "$GO generate ./...")
	require.NotContains(t, script, "flatc")
	require.NotContains(t, script, "sqlc generate")
}

func TestDevcontainerVerifyTaskIncludesGeneratedCodeChecks(t *testing.T) {
	t.Parallel()

	specs := devcontainerVerifySpecs(&app.Context{Root: "/repo"})
	require.Len(t, specs, 1)
	joined := strings.Join(specs[0].Args, " ")

	require.Contains(t, joined, "make flatc-check sqlc-check mockgen-check")
}

func TestFullEvalWorkflowUsesPromptfooThresholdGate(t *testing.T) {
	t.Parallel()

	workflow := readRepoFile(t, ".github/workflows/eval.yml")
	prWorkflow := readRepoFile(t, ".github/workflows/pr.yml")
	resultArtifact := regexp.MustCompile(`(?s)- name: Upload full eval result artifact\s+if: always\(\)\s+uses: actions/upload-artifact@v4\s+with:\s+name: full-eval-result-.*?\s+path: eval/results/latest\.json\s+if-no-files-found: warn`)

	require.Contains(t, workflow, `PROMPTFOO_PASS_RATE_THRESHOLD: "100"`)
	require.Contains(t, workflow, "SKIP_DEVCONTAINER_WRAPPER=1 make DEVCONTAINER_EXEC= eval")
	require.Regexp(t, resultArtifact, workflow)
	require.Contains(t, prWorkflow, "SKIP_DEVCONTAINER_WRAPPER=1 make DEVCONTAINER_EXEC= eval-test")
	require.NotContains(t, prWorkflow, "make DEVCONTAINER_EXEC= eval 2>&1")
}

func TestRunTaskEnvUsesArgv(t *testing.T) {
	t.Parallel()

	ctx := &app.Context{
		Argv: []string{"--verbose", "foo bar", "baz"},
	}

	got := runTaskEnv(ctx)
	testcmp.RequireEqual(t, []string{"ARGS=--verbose foo bar baz"}, got)
}

func TestScriptHeaderDefaultsGoEnvironment(t *testing.T) {
	script := scriptHeader()

	require.Contains(t, script, `export GO="${GO:-go}"`)
	require.Contains(t, script, `export GOFLAGS="${GOFLAGS:--v -trimpath -tags=stdjson}"`)
	require.Contains(t, script, `export CGO_ENABLED="${CGO_ENABLED:-1}"`)
	require.Contains(t, script, `export GOOS="${GOOS:-linux}"`)
	require.Contains(t, script, `export GOARCH="${GOARCH:-`)
}

func TestQuoteArgs(t *testing.T) {
	t.Parallel()

	got := quoteArgs([]string{"foo bar", "x\ty", `a"b`})
	testcmp.RequireEqual(t, []string{"\"foo bar\"", "\"x\\ty\"", `"a\"b"`}, got)
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
	testcmp.RequireEqual(t, []runner.CommandSpec{
		{Name: "echo", Args: []string{"DRAGONSCALE_EVAL_CONFIG=./configs/override.json"}},
		{Name: "npx", Args: []string{"--yes", "promptfoo", "eval", "--config", "promptfooconfig.yaml", "--no-cache", "--no-progress-bar", "-j", "1"}},
	}, projectCommandSpecs(specs))
	require.Contains(t, strings.Join(specs[1].Env, " "), "DRAGONSCALE_EVAL_CONFIG=./configs/override.json")
	require.NotContains(t, strings.Join(specs[1].Env, " "), "DRAGONSCALE_EVAL_CONFIG=./configs/default.json")

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

	promptfooConfig := readRepoFile(t, "eval/promptfooconfig.yaml")
	require.NotContains(t, promptfooConfig, "DRAGONSCALE_EVAL_CONFIG:")
	reverifyConfig := readRepoFile(t, "eval/reverify-two-cases.yaml")
	require.NotContains(t, reverifyConfig, "DRAGONSCALE_EVAL_CONFIG:")
}

func TestEvalRunSpecsUsesBaseConfigWhenSetAndDebugEnabled(t *testing.T) {
	t.Parallel()

	specs := evalRunSpecs(&app.Context{
		ExtraEnv: map[string]string{
			"DRAGONSCALE_EVAL_BASE_CONFIG": "/tmp/base.json",
			"DRAGONSCALE_EVAL_DEBUG":       "1",
		},
	})

	testcmp.RequireEqual(t, []runner.CommandSpec{
		{Name: "echo", Args: []string{"DRAGONSCALE_EVAL_BASE_CONFIG=/tmp/base.json"}},
		{Name: "echo", Args: []string{"DRAGONSCALE_EVAL_CONFIG=./configs/default.json"}},
		{Name: "npx", Args: []string{"--yes", "promptfoo", "eval", "--config", "promptfooconfig.yaml", "--no-cache", "--no-progress-bar", "-j", "1"}},
	}, projectCommandSpecs(specs))
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
	testcmp.RequireEqual(t, runner.CommandSpec{
		Name: "npx",
		Args: []string{
			"--yes", "promptfoo", "eval", "--config", "promptfooconfig.yaml",
			"--no-cache", "--max-concurrency", "1",
		},
	}, runner.CommandSpec{Name: last.Name, Args: last.Args})
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
	require.Contains(t, content, "Local Git ref or fetchable <remote>/<branch>")
	require.Contains(t, content, "export DEVCONTAINER_EXEC=\"\"")
	require.Contains(t, content, "make DEVCONTAINER_EXEC= eval-build")
	require.Contains(t, content, "resolve_base_ref")
	require.Contains(t, content, "fetch --prune")
	require.Contains(t, content, "rev-parse --verify")
	require.NotContains(t, content, "fetch origin \"${BASE_REF#origin/}\" >/dev/null 2>&1 || true")
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

func TestDefaultEnvForwardsOllamaContainerOptions(t *testing.T) {
	ctx := &app.Context{ExtraEnv: map[string]string{
		"DRAGONSCALE_OLLAMA_CONTAINER_IMAGE": "ollama/ollama:0.12.6",
		"DRAGONSCALE_OLLAMA_CONTAINER_MODEL": "nomic-embed-text",
	}}

	env := strings.Join(defaultEnv(ctx), " ")
	require.Contains(t, env, "DRAGONSCALE_OLLAMA_CONTAINER_IMAGE=ollama/ollama:0.12.6")
	require.Contains(t, env, "DRAGONSCALE_OLLAMA_CONTAINER_MODEL=nomic-embed-text")
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
	testcmp.RequireEqual(t, []runner.CommandSpec{
		{Name: "go", Args: []string{"fmt", "./..."}},
		{Name: "go", Args: []string{"vet", "./..."}},
		{Name: "go", Args: []string{"build", "./..."}},
	}, projectCommandSpecs(fake.Calls))
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
	testcmp.RequireEqual(t, []runner.CommandSpec{
		{Name: "go", Args: []string{"mod", "download"}},
		{Name: "go", Args: []string{"mod", "verify"}},
		{Name: "go", Args: []string{"fmt", "./..."}},
		{Name: "go", Args: []string{"vet", "./..."}},
		{Name: "go", Args: []string{"test", "./..."}},
	}, projectCommandSpecs(fake.Calls))
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
	testcmp.RequireEqual(t, "npx", fake.Calls[0].Name)
	require.Contains(t, fake.Calls[0].Args, "@devcontainers/cli")
	require.Contains(t, fake.Calls[0].Args, "exec")
	require.Contains(t, fake.Calls[0].Args, "bash")
	require.Contains(t, strings.Join(fake.Calls[0].Args, " "), "go generate ./pkg/itr ./pkg/tools")
	require.Contains(t, strings.Join(fake.Calls[0].Args, " "), "go generate -run mockgen ./...")
	require.Contains(t, strings.Join(fake.Calls[0].Args, " "), "cd internal/fantasy")
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
	testcmp.RequireEqual(t, []runner.CommandSpec{
		{Name: "echo", Dir: ctx.Root},
		{Name: "npx", Dir: filepath.Join(ctx.Root, "eval")},
	}, projectCommandNamesAndDirs(fake.Calls))
	joinedEnv := strings.Join(fake.Calls[1].Env, " ")
	require.Contains(t, joinedEnv, "DRAGONSCALE_EVAL_CONFIG=./configs/default.json")
	require.Contains(t, strings.Join(fake.Calls[1].Args, " "), "--no-progress-bar")
}

func TestEvalTaskForwardsPromptfooPassRateThreshold(t *testing.T) {
	t.Parallel()

	ctx := &app.Context{
		Root: t.TempDir(),
		ExtraEnv: map[string]string{
			"PROMPTFOO_PASS_RATE_THRESHOLD": "100",
		},
	}
	fake := &runner.FakeRunner{Result: runner.CommandResult{ExitCode: 0}}

	task := findTaskByName(t, NewRegistry(ctx.Root), "eval")
	_, err := task.Run(context.Background(), fake, ctx)
	require.NoError(t, err)
	require.NotEmpty(t, fake.Calls)

	joinedEnv := strings.Join(fake.Calls[len(fake.Calls)-1].Env, " ")
	require.Contains(t, joinedEnv, "PROMPTFOO_PASS_RATE_THRESHOLD=100")
}

func TestEvalTaskForwardsProviderAuthEnv(t *testing.T) {
	t.Parallel()

	ctx := &app.Context{
		Root: t.TempDir(),
		ExtraEnv: map[string]string{
			"OPENROUTER_API_KEY":                        "test-openrouter-key",
			"OPENAI_API_KEY":                            "test-openai-key",
			"DRAGONSCALE_PROVIDERS_OPENROUTER_API_KEY":  "dragon-openrouter-key",
			"DRAGONSCALE_PROVIDERS_OPENROUTER_API_BASE": "https://openrouter.example/v1",
			"DRAGONSCALE_PROVIDERS_OPENAI_API_KEY":      "dragon-openai-key",
			"DRAGONSCALE_PROVIDERS_OPENAI_API_BASE":     "https://openai.example/v1",
			"DRAGONSCALE_PROVIDERS_OPENCODE_API_KEY":    "dragon-opencode-key",
			"DRAGONSCALE_PROVIDERS_OPENCODE_API_BASE":   "https://opencode.example/v1",
			"DRAGONSCALE_AGENTS_DEFAULTS_PROVIDER":      "openrouter",
			"DRAGONSCALE_AGENTS_DEFAULTS_MODEL":         "openai/gpt-4o-mini",
		},
	}
	fake := &runner.FakeRunner{Result: runner.CommandResult{ExitCode: 0}}

	task := findTaskByName(t, NewRegistry(ctx.Root), "eval")
	_, err := task.Run(context.Background(), fake, ctx)
	require.NoError(t, err)
	require.NotEmpty(t, fake.Calls)

	joinedEnv := strings.Join(fake.Calls[len(fake.Calls)-1].Env, " ")
	for _, expected := range []string{
		"OPENROUTER_API_KEY=test-openrouter-key",
		"OPENAI_API_KEY=test-openai-key",
		"DRAGONSCALE_PROVIDERS_OPENROUTER_API_KEY=dragon-openrouter-key",
		"DRAGONSCALE_PROVIDERS_OPENROUTER_API_BASE=https://openrouter.example/v1",
		"DRAGONSCALE_PROVIDERS_OPENAI_API_KEY=dragon-openai-key",
		"DRAGONSCALE_PROVIDERS_OPENAI_API_BASE=https://openai.example/v1",
		"DRAGONSCALE_PROVIDERS_OPENCODE_API_KEY=dragon-opencode-key",
		"DRAGONSCALE_PROVIDERS_OPENCODE_API_BASE=https://opencode.example/v1",
		"DRAGONSCALE_AGENTS_DEFAULTS_PROVIDER=openrouter",
		"DRAGONSCALE_AGENTS_DEFAULTS_MODEL=openai/gpt-4o-mini",
	} {
		require.Contains(t, joinedEnv, expected)
	}
}

func TestEvalProofFullTaskRunsLocalThresholdProof(t *testing.T) {
	t.Parallel()

	ctx := &app.Context{Root: t.TempDir()}
	fake := &runner.FakeRunner{Result: runner.CommandResult{ExitCode: 0}}

	task := findTaskByName(t, NewRegistry(ctx.Root), "eval-proof-full")
	_, err := task.Run(context.Background(), fake, ctx)
	require.NoError(t, err)
	require.Len(t, fake.Calls, 1)

	script := strings.Join(fake.Calls[0].Args, " ")
	require.Contains(t, script, `PROMPTFOO_PASS_RATE_THRESHOLD="${PROMPTFOO_PASS_RATE_THRESHOLD:-100}"`)
	require.Contains(t, script, "SKIP_DEVCONTAINER_WRAPPER=1 DEVCONTAINER_EXEC= make eval")
	require.Contains(t, script, "eval/results/make-eval.log")
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
	testcmp.RequireEqual(t, "bash", fake.Calls[0].Name)
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
	testcmp.RequireEqual(t, []runner.CommandSpec{
		{Name: "npx", Args: []string{"--yes", "promptfoo", "view"}, Dir: filepath.Join(ctx.Root, "eval")},
	}, projectCommandSpecsWithDirs(fake.Calls))
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
	testcmp.RequireEqual(t, runner.CommandSpec{Name: "go", Args: []string{"generate", "./..."}, Dir: root}, projectCommandSpec(specs[0]))
	testcmp.RequireEqual(t, []string{"mkdir", "rm", "cp"}, []string{specs[3].Name, specs[4].Name, specs[8].Name})
	testcmp.RequireEqual(t, runner.CommandSpec{Name: "npx", Dir: filepath.Join(root, "eval")}, projectCommandNameAndDir(specs[len(specs)-1]))
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
	testcmp.RequireEqual(t, runner.CommandSpec{Name: "go", Args: []string{"generate", "./..."}, Dir: root}, projectCommandSpec(specs[0]))
	testcmp.RequireEqual(t, "npx", specs[len(specs)-1].Name)
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
	testcmp.RequireEqual(t, runner.CommandSpec{
		Name: "mkdir",
		Args: []string{"-p", filepath.Join(homeDir(), ".local", "share", "dragonscale", "sandbox")},
		Dir:  root,
	}, projectCommandSpec(specs[3]))
	testcmp.RequireEqual(t, []string{"rm", "bash", "cp", "bash"}, []string{specs[4].Name, specs[7].Name, specs[8].Name, specs[9].Name})
	testcmp.RequireEqual(t, "npx", specs[len(specs)-1].Name)
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
	testcmp.RequireEqual(t, runner.CommandSpec{Name: "npx", Dir: filepath.Join(root, "eval")}, projectCommandNameAndDir(fake.Calls[0]))

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
	require.GreaterOrEqual(t, len(fake.Calls), 6)
	testcmp.RequireEqual(t, []string{"mkdir", "rm", "bash", "cp"}, []string{fake.Calls[0].Name, fake.Calls[1].Name, fake.Calls[4].Name, fake.Calls[5].Name})
}

func TestGenerateTaskIncludesNestedFantasyMocks(t *testing.T) {
	script := generateScript(&app.Context{})

	require.Contains(t, script, "$GO generate ./...")
	require.Contains(t, script, "(cd internal/fantasy && $GO generate -run mockgen ./...)")
}

func TestTestContainersTaskOptsIntoContainerTests(t *testing.T) {
	ctx := &app.Context{
		Root: t.TempDir(),
		ExtraEnv: map[string]string{
			"DEVCONTAINER_EXEC":                  "echo devcontainer exec",
			"DRAGONSCALE_OLLAMA_CONTAINER_IMAGE": "ollama/ollama:0.12.6",
			"DRAGONSCALE_OLLAMA_CONTAINER_MODEL": "nomic-embed-text",
		},
	}
	fake := &runner.FakeRunner{Result: runner.CommandResult{ExitCode: 0}}
	task := findTaskByName(t, NewRegistry(ctx.Root), "test-containers")

	_, err := task.Run(context.Background(), fake, ctx)
	require.NoError(t, err)
	require.Len(t, fake.Calls, 1)
	script := strings.Join(fake.Calls[0].Args, " ")
	require.Contains(t, script, "export DRAGONSCALE_OLLAMA_CONTAINER_IMAGE='ollama/ollama:0.12.6'")
	require.Contains(t, script, "export DRAGONSCALE_OLLAMA_CONTAINER_MODEL='nomic-embed-text'")
	require.Contains(t, script, "DRAGONSCALE_RUN_CONTAINER_TESTS=1 $GO test -tags 'integration containers'")
	require.Contains(t, script, "-timeout 20m")
	require.Contains(t, script, "./pkg/memory/...")
	require.NotContains(t, script, "devcontainer exec")
}

func TestContainerTestScriptQuotesOllamaOptions(t *testing.T) {
	script := containerTestScript(&app.Context{ExtraEnv: map[string]string{
		"DRAGONSCALE_OLLAMA_CONTAINER_IMAGE": "registry.example/ollama:tag'quoted",
		"DRAGONSCALE_OLLAMA_CONTAINER_MODEL": "nomic'embed",
	}})

	require.Contains(t, script, `export DRAGONSCALE_OLLAMA_CONTAINER_IMAGE='registry.example/ollama:tag'"'"'quoted'`)
	require.Contains(t, script, `export DRAGONSCALE_OLLAMA_CONTAINER_MODEL='nomic'"'"'embed'`)
}

func projectCommandSpecs(specs []runner.CommandSpec) []runner.CommandSpec {
	projected := make([]runner.CommandSpec, len(specs))
	for i, spec := range specs {
		projected[i] = runner.CommandSpec{
			Name: spec.Name,
			Args: spec.Args,
		}
	}
	return projected
}

func projectCommandSpecsWithDirs(specs []runner.CommandSpec) []runner.CommandSpec {
	projected := make([]runner.CommandSpec, len(specs))
	for i, spec := range specs {
		projected[i] = projectCommandSpec(spec)
	}
	return projected
}

func projectCommandSpec(spec runner.CommandSpec) runner.CommandSpec {
	return runner.CommandSpec{
		Name: spec.Name,
		Args: spec.Args,
		Dir:  spec.Dir,
	}
}

func projectCommandNamesAndDirs(specs []runner.CommandSpec) []runner.CommandSpec {
	projected := make([]runner.CommandSpec, len(specs))
	for i, spec := range specs {
		projected[i] = projectCommandNameAndDir(spec)
	}
	return projected
}

func projectCommandNameAndDir(spec runner.CommandSpec) runner.CommandSpec {
	return runner.CommandSpec{
		Name: spec.Name,
		Dir:  spec.Dir,
	}
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
