package tasks

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ZanzyTHEbar/dragonscale/internal/opsctl/app"
	"github.com/ZanzyTHEbar/dragonscale/internal/opsctl/format"
	"github.com/ZanzyTHEbar/dragonscale/internal/opsctl/runner"
)

const (
	defaultBinaryName = "dragonscale"
	defaultBuildDir   = "bin"
	defaultCmdDir     = "cmd/dragonscale"
)

type shellTask struct {
	name        string
	description string
	script      func(*app.Context) string
	env         func(*app.Context) []string
}

func (t shellTask) Name() string {
	return t.name
}

func (t shellTask) Description() string {
	return t.description
}

func (t shellTask) Run(ctx context.Context, rnr runner.Runner, c *app.Context) (app.Result, error) {
	script := t.script(c)
	fullScript := applyDevcontainerWrapper(script, c)
	workDir := c.Root
	if c.Cwd != "" {
		workDir = c.Cwd
	}

	spec := runner.CommandSpec{
		Name:          "bash",
		Args:          []string{"-lc", fullScript},
		Dir:           workDir,
		Env:           mergeEnv(c, t.env),
		InheritOutput: c.Format == format.OutputRaw,
	}

	result, err := rnr.Run(ctx, spec)
	res := app.Result{
		Task:    t.name,
		Command: script,
		Stdout:  result.Stdout,
		Stderr:  result.Stderr,
		Err: func() string {
			if err != nil {
				return err.Error()
			}
			return ""
		}(),
		ExitCode: result.ExitCode,
	}
	if err == nil {
		return res, nil
	}
	return res, err
}

type helpTask struct {
	app *app.App
}

func (t helpTask) Name() string {
	return "help"
}

func (t helpTask) Description() string {
	return "Show task help"
}

func (t helpTask) Run(_ context.Context, _ runner.Runner, c *app.Context) (app.Result, error) {
	if c == nil || t.app == nil {
		return app.Result{
			Task:     "help",
			Command:  "help",
			ExitCode: 0,
		}, nil
	}

	lines := []string{
		"Usage: opsctl [global flags] <command> [args]",
		"",
		"Global flags:",
		"  --format string   output mode: text, json, raw (default text)",
		"  --json            shortcut for --format json",
		"  --raw             shortcut for --format raw",
		"  --no-color        disable color output",
		"  --quiet           suppress non-error output",
		"  --root string     repository root path (default current directory)",
		"  --cwd string      working directory for command execution",
		"  --timeout value   command timeout (ex: 2m, 30s)",
		"",
		"Tasks:",
	}
	lines = append(lines, t.app.TaskHelp()...)
	output := strings.Join(lines, "\n")

	return app.Result{
		Task:     "help",
		Command:  "help",
		Stdout:   output,
		ExitCode: 0,
	}, nil
}

func NewHelpTask(app *app.App) app.Task {
	return helpTask{app: app}
}

func mergeEnv(c *app.Context, custom func(*app.Context) []string) []string {
	base := defaultEnv(c)
	if custom == nil {
		return base
	}
	return append(base, custom(c)...)
}

func NewShellTask(name, description string, script func(*app.Context) string, env func(*app.Context) []string) shellTask {
	return shellTask{name: name, description: description, script: script, env: env}
}

func NewRegistry(_ string) []app.Task {
	tasks := []app.Task{
		NewShellTask("all", "Build the dragonscale binary for current platform", buildScript, nil),
		NewShellTask("generate", "Run generate", generateScript, nil),
		NewShellTask("build", "Build the dragonscale binary for current platform", buildScript, nil),
		NewShellTask("build-all", "Build dragonscale for all platforms", buildAllScript, nil),
		NewShellTask("install", "Install dragonscale to system and copy builtin skills", installScript, nil),
		NewShellTask("uninstall", "Uninstall dragonscale from system", uninstallScript, nil),
		NewShellTask("uninstall-all", "Uninstall dragonscale and workspace data", uninstallAllScript, nil),
		NewShellTask("clean", "Remove build artifacts", cleanScript, nil),
		NewShellTask("vet", "Run go vet for static analysis", staticGoScript("vet ./..."), nil),
		NewShellTask("test", "Run tests", staticGoScript("test ./..."), nil),
		NewShellTask("fmt", "Format Go code", staticGoScript("fmt ./..."), nil),
		NewShellTask("lint", "Run all linting checks", lintScript, nil),
		NewShellTask("hooks", "Install git hooks", hooksScript, nil),
		NewShellTask("deps", "Download dependencies", staticGoScript("mod download && mod verify"), nil),
		NewShellTask("update-deps", "Update dependencies", staticGoScript("get -u ./... && mod tidy"), nil),
		NewShellTask("sqlc-check", "Verify sqlc generation is idempotent", sqlcCheckScript, nil),
		NewShellTask("flatc-check", "Verify flatc generation is idempotent", flatcCheckScript, nil),
		NewShellTask("sqlc-vet", "Run sqlc vet rules", sqlcVetScript, nil),
		NewShellTask("fantasy-check", "Compare vendored Fantasy SDK against upstream", simpleScript("./scripts/sync-fantasy.sh --check"), nil),
		NewShellTask("fantasy-diff", "Show diff between vendored and upstream", fantasyDiffScript, nil),
		NewShellTask("fantasy-sync", "Full sync of vendored Fantasy SDK", fantasySyncScript, nil),
		NewShellTask("fantasy-patch", "Save local modifications as a patch", fantasyPatchScript, nil),
		NewShellTask("test-integration", "Run integration tests", staticGoScript("-tags integration -count=1 -timeout 120s -v ./pkg/memory/..."), nil),
		NewShellTask("check", "Run deps, formatting, linting and tests", checkScript, nil),
		NewShellTask("run", "Build and run dragonscale", runScript, runTaskEnv),
		NewShellTask("devcontainer-build", "Build the local devcontainer image via npx", devcontainerBuildScript, nil),
		NewShellTask("devcontainer-up", "Start/update the local devcontainer", devcontainerUpScript, nil),
		NewShellTask("devcontainer-generate", "Run generation in devcontainer", devcontainerGenerateScript, nil),
		NewShellTask("devcontainer-verify", "Verify generated code in devcontainer", devcontainerVerifyScript, nil),
		NewShellTask("eval-build", "Build the eval runner from current branch", evalBuildScript, nil),
		NewShellTask("eval", "Run the eval suite", evalRunScript, nil),
		NewShellTask("eval-fixtures", "Prepare eval fixture workspace", evalFixturesScript, nil),
		NewShellTask("eval-view", "Open the promptfoo results viewer", evalViewScript, nil),
		NewShellTask("eval-clean", "Cleanup eval artifacts", simpleScript("rm -rf eval/results eval/bin"), nil),
		NewShellTask("eval-compare", "Run A/B comparison of current branch vs main", simpleScript("cd eval && DEVCONTAINER_EXEC= EVAL_NPM_CMD=$(npx --yes) ./scripts/compare.sh --repeat 3"), nil),
		NewShellTask("eval-test", "Run Go-native component evals", staticGoScript("-v ./eval/go_evals/..."), nil),
	}
	return tasks
}

func defaultEnv(c *app.Context) []string {
	env := []string{
		fmt.Sprintf("GO=%s", cEnv(c, "GO", "go")),
		fmt.Sprintf("GOFLAGS=%s", cEnv(c, "GOFLAGS", "-v -trimpath -tags stdjson")),
		fmt.Sprintf("CGO_ENABLED=%s", cEnv(c, "CGO_ENABLED", "1")),
		fmt.Sprintf("BINARY_NAME=%s", cEnv(c, "BINARY_NAME", defaultBinaryName)),
		fmt.Sprintf("BUILD_DIR=%s", cEnv(c, "BUILD_DIR", defaultBuildDir)),
		fmt.Sprintf("CMD_DIR=%s", cEnv(c, "CMD_DIR", defaultCmdDir)),
		fmt.Sprintf("WORKSPACE_DIR=%s", cEnv(c, "WORKSPACE_DIR", filepath.Join(homeDir(), ".dragonscale", "workspace"))),
	}
	if value := cEnv(c, "GOOS", ""); value != "" {
		env = append(env, fmt.Sprintf("GOOS=%s", value))
	}
	if value := cEnv(c, "GOARCH", ""); value != "" {
		env = append(env, fmt.Sprintf("GOARCH=%s", value))
	}
	if value := cEnv(c, "PLATFORM", ""); value != "" {
		env = append(env, fmt.Sprintf("PLATFORM=%s", value))
	}
	if value := cEnv(c, "ARCH", ""); value != "" {
		env = append(env, fmt.Sprintf("ARCH=%s", value))
	}
	if v := cEnv(c, "DEVCONTAINER_EXEC", ""); v != "" {
		env = append(env, "DEVCONTAINER_EXEC="+v)
	}
	if value := cEnv(c, "DRAGONSCALE_EVAL_HOST_HOME", ""); value != "" {
		env = append(env, fmt.Sprintf("DRAGONSCALE_EVAL_HOST_HOME=%s", value))
	}
	if value := cEnv(c, "DRAGONSCALE_EVAL_BASE_CONFIG", ""); value != "" {
		env = append(env, fmt.Sprintf("DRAGONSCALE_EVAL_BASE_CONFIG=%s", value))
	}
	if value := cEnv(c, "DRAGONSCALE_EVAL_CONFIG", ""); value != "" {
		env = append(env, fmt.Sprintf("DRAGONSCALE_EVAL_CONFIG=%s", value))
	}
	if value := cEnv(c, "DRAGONSCALE_EVAL_DEBUG", ""); value != "" {
		env = append(env, fmt.Sprintf("DRAGONSCALE_EVAL_DEBUG=%s", value))
	}
	if value := cEnv(c, "VERSION", ""); value != "" {
		env = append(env, fmt.Sprintf("VERSION=%s", value))
	}
	if value := cEnv(c, "FANTASY_VERSION", ""); value != "" {
		env = append(env, fmt.Sprintf("FANTASY_VERSION=%s", value))
	}
	if value := cEnv(c, "NAME", ""); value != "" {
		env = append(env, fmt.Sprintf("NAME=%s", value))
	}
	if value := cEnv(c, "ARGS", ""); value != "" {
		env = append(env, fmt.Sprintf("ARGS=%s", value))
	}
	if c.Root != "" {
		env = append(env, fmt.Sprintf("DRAGONSCALE_HOME=%s", filepath.Join(homeDir(), ".dragonscale")))
	}
	return env
}

func cEnv(c *app.Context, key string, fallback string) string {
	if v, ok := c.ExtraEnv[key]; ok {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return fallback
}

func cValue(c *app.Context, key string, fallback string) string {
	if c == nil || c.ExtraEnv == nil {
		return fallback
	}
	if v, ok := c.ExtraEnv[key]; ok {
		return v
	}
	return fallback
}

func applyDevcontainerWrapper(script string, c *app.Context) string {
	if value := cValue(c, "SKIP_DEVCONTAINER_WRAPPER", ""); value != "" {
		if value != "0" {
			return script
		}
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return script
	}
	execCmd := cEnv(c, "DEVCONTAINER_EXEC", "")
	if execCmd == "" {
		execCmd = detectDevcontainerExec(c.Root)
	}
	if execCmd == "" {
		return script
	}
	if strings.Contains(script, "@devcontainers/cli") || strings.Contains(script, "devcontainer exec ") {
		return script
	}

	workDir := c.Root
	if c.Cwd != "" {
		workDir = c.Cwd
	}
	if strings.TrimSpace(workDir) != "" {
		script = fmt.Sprintf("cd %s\n%s", strconv.Quote(workDir), script)
	}

	return fmt.Sprintf("%s -- bash -lc %s", execCmd, strconv.Quote(script))
}

func detectDevcontainerExec(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		root = os.Getenv("DEVCONTAINER_WORKSPACE")
	}
	if root == "" {
		if wd, err := os.Getwd(); err == nil {
			root = wd
		}
	}
	if root == "" {
		return ""
	}

	devcontainerJSON := filepath.Join(root, ".devcontainer", "devcontainer.json")
	if _, err := os.Stat(devcontainerJSON); err != nil {
		legacyDevcontainer := filepath.Join(root, ".devcontainer", "devcontainer.yaml")
		if _, legacyErr := os.Stat(legacyDevcontainer); legacyErr != nil {
			return ""
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".devcontainer")); err != nil {
		return ""
	}
	if _, err := exec.LookPath("npx"); err != nil {
		return ""
	}
	workspace := os.Getenv("DEVCONTAINER_WORKSPACE")
	if strings.TrimSpace(workspace) == "" {
		workspace = root
	}
	cli := "npx --yes @devcontainers/cli"
	return fmt.Sprintf("%s up --workspace-folder %q && %s exec --workspace-folder %q", cli, workspace, cli, workspace)
}

func homeDir() string {
	home, _ := os.UserHomeDir()
	return home
}

func scriptHeader() string {
	return "set -euo pipefail\ngit config --global --add safe.directory \"$PWD\" >/dev/null 2>&1 || true\n"
}

func staticGoScript(rest string) func(*app.Context) string {
	return func(*app.Context) string {
		return scriptHeader() + "$GO " + rest + "\n"
	}
}

func generateScript(*app.Context) string {
	return scriptHeader() +
		"rm -rf ./$(CMD_DIR)/workspace 2>/dev/null || true\n" +
		"$GO generate ./...\n"
}

func buildScript(*app.Context) string {
	version := "$(git describe --tags --always --dirty 2>/dev/null || echo \"dev\")"
	gitCommit := "$(git rev-parse --short=8 HEAD 2>/dev/null || echo \"dev\")"
	buildTime := "$(date +%FT%T%z)"
	goVersion := "$($GO version | awk '{print \"$3\"}')"
	ldFlags := fmt.Sprintf("-X main.version=%s -X main.gitCommit=%s -X main.buildTime=%s -X main.goVersion=%s -s -w", version, gitCommit, buildTime, goVersion)
	return scriptHeader() +
		"mkdir -p $(BUILD_DIR)\n" +
		"GOOS=$($GO env GOOS)\n" +
		"GOARCH=$($GO env GOARCH)\n" +
		fmt.Sprintf("$GO build $GOFLAGS -ldflags \"%s\" -o $(BUILD_DIR)/$(BINARY_NAME)-${GOOS}-${GOARCH} ./$(CMD_DIR)\n", ldFlags) +
		"ln -sf $(BINARY_NAME)-$(GOOS)-$(GOARCH) $(BUILD_DIR)/$(BINARY_NAME)\n"
}

func buildAllScript(*app.Context) string {
	ldFlags := "-ldflags '-s -w'"
	return scriptHeader() +
		"mkdir -p $(BUILD_DIR)\n" +
		fmt.Sprintf("GOOS=linux GOARCH=amd64 $GO build $GOFLAGS %s -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 ./$(CMD_DIR)\n", ldFlags) +
		fmt.Sprintf("GOOS=linux GOARCH=arm64 $GO build $GOFLAGS %s -o $(BUILD_DIR)/$(BINARY_NAME)-linux-arm64 ./$(CMD_DIR)\n", ldFlags) +
		fmt.Sprintf("GOOS=linux GOARCH=loong64 $GO build $GOFLAGS %s -o $(BUILD_DIR)/$(BINARY_NAME)-linux-loong64 ./$(CMD_DIR)\n", ldFlags) +
		fmt.Sprintf("GOOS=linux GOARCH=riscv64 $GO build $GOFLAGS %s -o $(BUILD_DIR)/$(BINARY_NAME)-linux-riscv64 ./$(CMD_DIR)\n", ldFlags) +
		fmt.Sprintf("GOOS=darwin GOARCH=arm64 $GO build $GOFLAGS %s -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64 ./$(CMD_DIR)\n", ldFlags) +
		fmt.Sprintf("GOOS=windows GOARCH=amd64 $GO build $GOFLAGS %s -o $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe ./$(CMD_DIR)\n", ldFlags)
}

func installScript(*app.Context) string {
	prefix := filepath.Join(homeDir(), ".local")
	return scriptHeader() +
		"mkdir -p ${INSTALL_PREFIX:-" + prefix + "}/bin\n" +
		"cp $(BUILD_DIR)/$(BINARY_NAME) ${INSTALL_PREFIX:-" + prefix + "}/bin/$(BINARY_NAME)\n" +
		"chmod +x ${INSTALL_PREFIX:-" + prefix + "}/bin/$(BINARY_NAME)\n"
}

func uninstallScript(*app.Context) string {
	return "rm -f ${INSTALL_PREFIX:-" + filepath.Join(homeDir(), ".local") + "}/bin/$(BINARY_NAME)\n"
}

func uninstallAllScript(*app.Context) string {
	return "rm -rf $(DRAGONSCALE_HOME)\n"
}

func cleanScript(*app.Context) string {
	return "rm -rf $(BUILD_DIR)\n"
}

func lintScript(*app.Context) string {
	return scriptHeader() +
		"$GO fmt ./...\n$GO vet ./...\n$GO build ./...\n"
}

func hooksScript(*app.Context) string {
	return "ln -sf ../../scripts/hooks/pre-commit .git/hooks/pre-commit\nln -sf ../../scripts/hooks/commit-msg .git/hooks/commit-msg\nchmod +x .git/hooks/pre-commit .git/hooks/commit-msg\n"
}

func sqlcCheckScript(*app.Context) string {
	return scriptHeader() +
		"repo=\"$PWD\"\n" +
		"before=\"$(mktemp)\"\nafter=\"$(mktemp)\"\n" +
		"snapshot() { git -c safe.directory=\"$repo\" diff --binary -- pkg/memory/sqlc/; git -c safe.directory=\"$repo\" ls-files --others --exclude-standard -- pkg/memory/sqlc/ | LC_ALL=C sort | while IFS= read -r f; do [ -f \"$f\" ] && sha256sum \"$f\"; done; };\n" +
		"snapshot > \"$before\"\nsqlc generate -f pkg/memory/sqlc/sqlc.yaml\nsnapshot > \"$after\"\n" +
		"if ! cmp -s \"$before\" \"$after\"; then echo \"::error::sqlc generated code is stale. Run 'sqlc generate -f pkg/memory/sqlc/sqlc.yaml' and commit.\"; rm -f \"$before\" \"$after\"; exit 1; fi\nrm -f \"$before\" \"$after\"\n"
}

func flatcCheckScript(*app.Context) string {
	return scriptHeader() +
		"repo=\"$PWD\"\n" +
		"before=\"$(mktemp)\"\nafter=\"$(mktemp)\"\n" +
		"snapshot() { git -c safe.directory=\"$repo\" diff --binary -- pkg/itr/itrfb/ pkg/tools/mapopsfb/; git -c safe.directory=\"$repo\" ls-files --others --exclude-standard -- pkg/itr/itrfb/ pkg/tools/mapopsfb/ | LC_ALL=C sort | while IFS= read -r f; do [ -f \"$f\" ] && sha256sum \"$f\"; done; };\n" +
		"snapshot > \"$before\"\n" +
		"$GO generate ./pkg/itr ./pkg/tools\nsnapshot > \"$after\"\n" +
		"if ! cmp -s \"$before\" \"$after\"; then echo \"::error::FlatBuffers generated code is stale. Run 'go generate ./pkg/itr ./pkg/tools' and commit.\"; rm -f \"$before\" \"$after\"; exit 1; fi\nrm -f \"$before\" \"$after\"\n"
}

func sqlcVetScript(*app.Context) string {
	return "sqlc vet -f pkg/memory/sqlc/sqlc.yaml\n"
}

func simpleScript(cmd string) func(*app.Context) string {
	return func(*app.Context) string { return cmd + "\n" }
}

func fantasyDiffScript(c *app.Context) string {
	return simpleScript("./scripts/sync-fantasy.sh --diff " + cEnv(c, "FANTASY_VERSION", ""))(c)
}

func fantasySyncScript(c *app.Context) string {
	return simpleScript("./scripts/sync-fantasy.sh --sync " + cEnv(c, "FANTASY_VERSION", ""))(c)
}

func fantasyPatchScript(c *app.Context) string {
	return simpleScript("./scripts/sync-fantasy.sh --save-patch " + cEnv(c, "NAME", ""))(c)
}

func checkScript(*app.Context) string {
	return staticGoScript("mod download && mod verify")(&app.Context{}) +
		staticGoScript("fmt ./...")(&app.Context{}) +
		staticGoScript("vet ./...")(&app.Context{}) +
		staticGoScript("test ./...")(&app.Context{})
}

func runTaskEnv(c *app.Context) []string {
	if len(c.Argv) == 0 {
		return nil
	}
	return []string{"ARGS=" + strings.Join(c.Argv, " ")}
}

func runScript(c *app.Context) string {
	return scriptHeader() + "$(BUILD_DIR)/$(BINARY_NAME) " + strings.Join(quoteArgs(c.Argv), " ") + "\n"
}

func quoteArgs(args []string) []string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, strconv.Quote(arg))
	}
	return quoted
}

func devcontainerBuildScript(*app.Context) string {
	return "npx --yes @devcontainers/cli build --workspace-folder \"$(pwd)\"\n"
}

func devcontainerUpScript(*app.Context) string {
	return "npx --yes @devcontainers/cli up --workspace-folder \"$(pwd)\"\n"
}

func devcontainerGenerateScript(*app.Context) string {
	return "npx --yes @devcontainers/cli exec --workspace-folder \"$(pwd)\" -- bash -lc \"go generate ./pkg/itr ./pkg/tools && sqlc generate -f pkg/memory/sqlc/sqlc.yaml\"\n"
}

func devcontainerVerifyScript(*app.Context) string {
	return "npx --yes @devcontainers/cli exec --workspace-folder \"$(pwd)\" -- bash -lc \"make flatc-check sqlc-check\"\n"
}

func evalBuildScript(*app.Context) string {
	ldFlags := "-X main.version=$(git describe --tags --always --dirty 2>/dev/null || echo \"dev\") -X main.gitCommit=$(git rev-parse --short=8 HEAD 2>/dev/null || echo \"dev\") -X main.buildTime=$(date +%FT%T%z) -X main.goVersion=\"$($GO version | awk '{print \"$3\"}')\" -s -w"
	return scriptHeader() +
		"$GO generate ./...\n" +
		"mkdir -p eval/bin\n" +
		fmt.Sprintf("$GO build $GOFLAGS -ldflags \"%s\" -o eval/bin/eval-runner ./eval/cmd/eval-runner\n", ldFlags)
}

func evalRunScript(*app.Context) string {
	return `if [ -n "${DRAGONSCALE_EVAL_BASE_CONFIG:-}" ] && [ -n "${DRAGONSCALE_EVAL_DEBUG:-}" ]; then
  echo "DRAGONSCALE_EVAL_BASE_CONFIG=${DRAGONSCALE_EVAL_BASE_CONFIG}"
fi
if [ -n "${DRAGONSCALE_EVAL_DEBUG:-}" ]; then
  echo "DRAGONSCALE_EVAL_CONFIG=${DRAGONSCALE_EVAL_CONFIG:-./configs/default.json}"
fi
DRAGONSCALE_EVAL_HOST_HOME=/host_home DRAGONSCALE_EVAL_CONFIG="${DRAGONSCALE_EVAL_CONFIG:-./configs/default.json}" npx --yes promptfoo eval --config promptfooconfig.yaml --no-cache --no-progress-bar
`
}

func evalFixturesScript(*app.Context) string {
	return scriptHeader() +
		"mkdir -p \"$HOME/.local/share/dragonscale/sandbox\"\n" +
		"rm -f \"$HOME/.local/share/dragonscale/sandbox/eval_test_output.txt\" \"$HOME/.local/share/dragonscale/sandbox/test_steps.txt\" \"$HOME/.local/share/dragonscale/sandbox/eval_checkpoint.txt\" \"$HOME/.local/share/dragonscale/sandbox/chain_test.txt\" \"$HOME/.local/share/dragonscale/sandbox/current_year.txt\" \"$HOME/.local/share/dragonscale/sandbox/result.txt\" \"$HOME/.local/share/dragonscale/sandbox/progressive_test.txt\" \"$HOME/.local/share/dragonscale/sandbox/os_name.txt\"\n" +
		"rm -rf \"$HOME/.local/share/dragonscale/sandbox/project\"\n" +
		"printf 'dragonscale eval fixture — hello from the eval harness\\nThis is line two of the fixture file.\\n' > \"$HOME/.local/share/dragonscale/sandbox/eval_fixture.txt\"\n" +
		"cp -f eval/fixtures/sample_data.txt \"$HOME/.local/share/dragonscale/sandbox/sample_data.txt\"\n" +
		"mkdir -p \"$HOME/.local/share/dragonscale/skills\"\n" +
		"cp -rf eval/fixtures/skills/* \"$HOME/.local/share/dragonscale/skills/\" 2>/dev/null || true\n"
}

func evalViewScript(*app.Context) string {
	return "cd eval && npx --yes promptfoo view\n"
}
