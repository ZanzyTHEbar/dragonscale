package tasks

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	name                 string
	description          string
	script               func(*app.Context) string
	env                  func(*app.Context) []string
	prepare              func(*app.Context) error
	skipDevcontainerWrap bool
}

func (t shellTask) Name() string {
	return t.name
}

func (t shellTask) Description() string {
	return t.description
}

func (t shellTask) Run(ctx context.Context, rnr runner.Runner, c *app.Context) (app.Result, error) {
	if t.prepare != nil {
		if err := t.prepare(c); err != nil {
			return app.Result{
				Task:     t.name,
				Command:  t.script(c),
				ExitCode: 1,
				Err:      err.Error(),
			}, err
		}
	}

	script := t.script(c)
	fullScript := script
	if !t.skipDevcontainerWrap {
		fullScript = applyDevcontainerWrapper(script, c)
	}
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
		Command: fullScript,
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

type commandTask struct {
	name        string
	description string
	specs       func(*app.Context) []runner.CommandSpec
	env         func(*app.Context) []string
	prepare     func(*app.Context) error
}

func (t commandTask) Name() string {
	return t.name
}

func (t commandTask) Description() string {
	return t.description
}

func (t commandTask) Run(ctx context.Context, rnr runner.Runner, c *app.Context) (app.Result, error) {
	if t.prepare != nil {
		if err := t.prepare(c); err != nil {
			return app.Result{
				Task:     t.name,
				ExitCode: 1,
				Err:      err.Error(),
			}, err
		}
	}

	specs := t.specs(c)
	workDir := c.Root
	if c.Cwd != "" {
		workDir = c.Cwd
	}
	baseEnv := mergeEnv(c, t.env)

	result := app.Result{
		Task: t.name,
	}
	commandParts := make([]string, 0, len(specs))
	for i, spec := range specs {
		spec.InheritOutput = c.Format == format.OutputRaw
		if spec.Dir == "" {
			spec.Dir = workDir
		}
		spec.Env = mergeEnvPairs(baseEnv, spec.Env)

		commandParts = append(commandParts, describeCommandSpec(spec))
		commandResult, err := rnr.Run(ctx, spec)
		result.ExitCode = commandResult.ExitCode
		result.Stdout += commandResult.Stdout
		result.Stderr += commandResult.Stderr
		if i == 0 {
			result.Command = commandParts[i]
		} else {
			result.Command += " && " + commandParts[i]
		}

		if err != nil {
			result.Err = err.Error()
			return result, err
		}
	}

	return result, nil
}

func describeCommandSpec(spec runner.CommandSpec) string {
	if len(spec.Args) == 0 {
		return spec.Name
	}
	quotedArgs := make([]string, 0, len(spec.Args))
	for _, arg := range spec.Args {
		quotedArgs = append(quotedArgs, strconv.Quote(arg))
	}
	return spec.Name + " " + strings.Join(quotedArgs, " ")
}

func mergeEnvPairs(base []string, extra []string) []string {
	seen := make(map[string]int)
	values := make(map[string]string)
	keys := make([]string, 0, len(base)+len(extra))

	merge := func(items []string) {
		for _, item := range items {
			eq := strings.Index(item, "=")
			if eq <= 0 {
				continue
			}
			key := item[:eq]
			value := item[eq+1:]
			if _, ok := seen[key]; !ok {
				keys = append(keys, key)
				seen[key] = len(keys) - 1
			}
			values[key] = value
		}
	}

	merge(base)
	merge(extra)

	env := make([]string, 0, len(keys))
	for _, key := range keys {
		value := values[key]
		if strings.TrimSpace(value) == "" {
			continue
		}
		env = append(env, fmt.Sprintf("%s=%s", key, value))
	}
	return env
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

func NewHostShellTask(name, description string, script func(*app.Context) string, env func(*app.Context) []string) shellTask {
	return shellTask{name: name, description: description, script: script, env: env, skipDevcontainerWrap: true}
}

func NewCommandTask(name, description string, specs func(*app.Context) []runner.CommandSpec, env func(*app.Context) []string, prepare func(*app.Context) error) commandTask {
	return commandTask{name: name, description: description, specs: specs, env: env, prepare: prepare}
}

func NewShellTaskWithPrepare(name, description string, script func(*app.Context) string, env func(*app.Context) []string, prepare func(*app.Context) error) shellTask {
	return shellTask{name: name, description: description, script: script, env: env, prepare: prepare}
}

func NewRegistry(_ string) []app.Task {
	tasks := []app.Task{
		NewShellTask("all", "Build the dragonscale binary for current platform", buildScript, nil),
		NewShellTask("generate", "Run generate", generateScript, nil),
		NewShellTaskWithPrepare("build", "Build the dragonscale binary for current platform", buildScript, nil, validateBuildTaskEnv),
		NewShellTaskWithPrepare("build-all", "Build dragonscale for linux target with CGO", buildAllScript, nil, validateBuildTaskEnv),
		NewShellTask("install", "Install dragonscale to system and copy builtin skills", installScript, nil),
		NewShellTask("uninstall", "Uninstall dragonscale from system", uninstallScript, nil),
		NewShellTask("uninstall-all", "Uninstall dragonscale and workspace data", uninstallAllScript, nil),
		NewShellTask("clean", "Remove build artifacts", cleanScript, nil),
		NewShellTask("vet", "Run go vet for static analysis", staticGoScript("vet ./..."), nil),
		NewShellTask("test", "Run tests", staticGoScript("test ./..."), nil),
		NewShellTask("fmt", "Format Go code", staticGoScript("fmt ./..."), nil),
		NewCommandTask("lint", "Run all linting checks", lintSpecs, nil, nil),
		NewShellTask("hooks", "Install git hooks", hooksScript, nil),
		NewShellTask("deps", "Download dependencies", staticGoScript("mod download && $GO mod verify"), nil),
		NewShellTask("update-deps", "Update dependencies", staticGoScript("get -u ./... && $GO mod tidy"), nil),
		NewShellTask("sqlc-check", "Verify sqlc generation is idempotent", sqlcCheckScript, nil),
		NewShellTask("flatc-check", "Verify flatc generation is idempotent", flatcCheckScript, nil),
		NewShellTask("mockgen-check", "Verify mockgen generation is idempotent", mockgenCheckScript, nil),
		NewShellTask("sqlc-vet", "Run sqlc vet rules", sqlcVetScript, nil),
		NewShellTask("fantasy-check", "Compare vendored Fantasy SDK against upstream", simpleScript("./scripts/sync-fantasy.sh --check"), nil),
		NewShellTask("fantasy-diff", "Show diff between vendored and upstream", fantasyDiffScript, nil),
		NewShellTask("fantasy-sync", "Full sync of vendored Fantasy SDK", fantasySyncScript, nil),
		NewShellTask("fantasy-patch", "Save local modifications as a patch", fantasyPatchScript, nil),
		NewShellTask("test-integration", "Run integration tests", staticGoScript("test -tags integration -count=1 -timeout 120s -v ./pkg/memory/..."), nil),
		NewHostShellTask("test-containers", "Run memory integration tests plus Docker-backed smoke tests", containerTestScript, nil),
		NewCommandTask("check", "Run deps, formatting, linting and tests", checkSpecs, nil, nil),
		NewShellTask("run", "Build and run dragonscale", runScript, runTaskEnv),
		NewCommandTask("devcontainer-build", "Build the local devcontainer image via npx", devcontainerBuildSpecs, nil, nil),
		NewCommandTask("devcontainer-up", "Start/update the local devcontainer", devcontainerUpSpecs, nil, nil),
		NewCommandTask("devcontainer-generate", "Run generation in devcontainer", devcontainerGenerateSpecs, nil, nil),
		NewCommandTask("devcontainer-verify", "Verify generated code in devcontainer", devcontainerVerifySpecs, nil, nil),
		NewCommandTask("eval-build", "Build the eval runner from current branch", evalBuildSpecs, nil, nil),
		NewCommandTask("eval", "Run the eval suite", evalRunSpecs, nil, nil),
		NewCommandTask("eval-fixtures", "Prepare eval fixture workspace", evalFixturesSpecs, nil, nil),
		NewCommandTask("eval-view", "Open the promptfoo results viewer", evalViewSpecs, nil, nil),
		NewShellTask("eval-proof-full", "Run local full eval proof with threshold and artifacts", evalProofFullScript, nil),
		NewShellTask("eval-clean", "Cleanup eval artifacts", evalCleanScript, nil),
		NewShellTask("eval-compare", "Run A/B comparison of current branch vs main", evalCompareScript, nil),
		NewShellTask("eval-test", "Run Go-native component evals", evalTestScript, nil),
	}
	return tasks
}

func defaultEnv(c *app.Context) []string {
	pairs := map[string]string{
		"GO":            cEnv(c, "GO", "go"),
		"GOFLAGS":       cEnv(c, "GOFLAGS", "-v -trimpath -tags=stdjson"),
		"CGO_ENABLED":   cEnv(c, "CGO_ENABLED", "1"),
		"BINARY_NAME":   cEnv(c, "BINARY_NAME", defaultBinaryName),
		"BUILD_DIR":     cEnv(c, "BUILD_DIR", defaultBuildDir),
		"CMD_DIR":       cEnv(c, "CMD_DIR", defaultCmdDir),
		"WORKSPACE_DIR": cEnv(c, "WORKSPACE_DIR", filepath.Join(homeDir(), ".dragonscale", "workspace")),
		"GOOS":          cEnv(c, "GOOS", "linux"),
		"GOARCH":        cEnv(c, "GOARCH", runtime.GOARCH),
	}
	appendIfSet := func(key, value string) {
		if strings.TrimSpace(value) != "" {
			pairs[key] = value
		}
	}
	appendIfSet("PLATFORM", cEnv(c, "PLATFORM", ""))
	appendIfSet("ARCH", cEnv(c, "ARCH", ""))
	appendIfSet("DEVCONTAINER_EXEC", cEnv(c, "DEVCONTAINER_EXEC", ""))
	appendIfSet("DRAGONSCALE_EVAL_HOST_HOME", cEnv(c, "DRAGONSCALE_EVAL_HOST_HOME", ""))
	appendIfSet("DRAGONSCALE_EVAL_BASE_CONFIG", cEnv(c, "DRAGONSCALE_EVAL_BASE_CONFIG", ""))
	appendIfSet("DRAGONSCALE_EVAL_CONFIG", cEnv(c, "DRAGONSCALE_EVAL_CONFIG", ""))
	appendIfSet("DRAGONSCALE_EVAL_DEBUG", cEnv(c, "DRAGONSCALE_EVAL_DEBUG", ""))
	appendIfSet("DRAGONSCALE_PROMPTFOO_ARGS", cEnv(c, "DRAGONSCALE_PROMPTFOO_ARGS", ""))
	appendIfSet("PROMPTFOO_PASS_RATE_THRESHOLD", cEnv(c, "PROMPTFOO_PASS_RATE_THRESHOLD", ""))
	appendIfSet("OPENROUTER_API_KEY", cEnv(c, "OPENROUTER_API_KEY", ""))
	appendIfSet("OPENAI_API_KEY", cEnv(c, "OPENAI_API_KEY", ""))
	appendIfSet("DRAGONSCALE_PROVIDERS_OPENROUTER_API_KEY", cEnv(c, "DRAGONSCALE_PROVIDERS_OPENROUTER_API_KEY", ""))
	appendIfSet("DRAGONSCALE_PROVIDERS_OPENROUTER_API_BASE", cEnv(c, "DRAGONSCALE_PROVIDERS_OPENROUTER_API_BASE", ""))
	appendIfSet("DRAGONSCALE_PROVIDERS_OPENAI_API_KEY", cEnv(c, "DRAGONSCALE_PROVIDERS_OPENAI_API_KEY", ""))
	appendIfSet("DRAGONSCALE_PROVIDERS_OPENAI_API_BASE", cEnv(c, "DRAGONSCALE_PROVIDERS_OPENAI_API_BASE", ""))
	appendIfSet("DRAGONSCALE_AGENTS_DEFAULTS_PROVIDER", cEnv(c, "DRAGONSCALE_AGENTS_DEFAULTS_PROVIDER", ""))
	appendIfSet("DRAGONSCALE_AGENTS_DEFAULTS_MODEL", cEnv(c, "DRAGONSCALE_AGENTS_DEFAULTS_MODEL", ""))
	appendIfSet("DRAGONSCALE_OLLAMA_CONTAINER_IMAGE", cEnv(c, "DRAGONSCALE_OLLAMA_CONTAINER_IMAGE", ""))
	appendIfSet("DRAGONSCALE_OLLAMA_CONTAINER_MODEL", cEnv(c, "DRAGONSCALE_OLLAMA_CONTAINER_MODEL", ""))
	appendIfSet("VERSION", cEnv(c, "VERSION", ""))
	appendIfSet("FANTASY_VERSION", cEnv(c, "FANTASY_VERSION", ""))
	appendIfSet("NAME", cEnv(c, "NAME", ""))
	appendIfSet("ARGS", cEnv(c, "ARGS", ""))
	if c.Root != "" {
		appendIfSet("DRAGONSCALE_HOME", filepath.Join(homeDir(), ".dragonscale"))
	}
	env := make([]string, 0, len(pairs))
	for key, value := range pairs {
		if key == "" || value == "" {
			continue
		}
		env = append(env, fmt.Sprintf("%s=%s", key, value))
	}
	return env
}

func cEnv(c *app.Context, key string, fallback string) string {
	if c != nil && c.ExtraEnv != nil {
		if v, ok := c.ExtraEnv[key]; ok {
			if strings.TrimSpace(v) != "" {
				return v
			}
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

	return fmt.Sprintf("%s -- bash -lc %s", execCmd, shellSingleQuote(script))
}

func shellSingleQuote(input string) string {
	return "'" + strings.ReplaceAll(input, "'", `'"'"'`) + "'"
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
	return fmt.Sprintf("set -euo pipefail\nexport GO=\"${GO:-go}\"\nexport GOFLAGS=\"${GOFLAGS:--v -trimpath -tags=stdjson}\"\nexport CGO_ENABLED=\"${CGO_ENABLED:-1}\"\nexport GOOS=\"${GOOS:-linux}\"\nexport GOARCH=\"${GOARCH:-%s}\"\ngit config --global --add safe.directory \"$PWD\" >/dev/null 2>&1 || true\n", runtime.GOARCH)
}

func staticGoScript(rest string) func(*app.Context) string {
	return func(*app.Context) string {
		return scriptHeader() + "$GO " + rest + "\n"
	}
}

func containerTestScript(c *app.Context) string {
	var script strings.Builder
	script.WriteString(scriptHeader())
	if image := cEnv(c, "DRAGONSCALE_OLLAMA_CONTAINER_IMAGE", ""); image != "" {
		fmt.Fprintf(&script, "export DRAGONSCALE_OLLAMA_CONTAINER_IMAGE=%s\n", shellSingleQuote(image))
	}
	if model := cEnv(c, "DRAGONSCALE_OLLAMA_CONTAINER_MODEL", ""); model != "" {
		fmt.Fprintf(&script, "export DRAGONSCALE_OLLAMA_CONTAINER_MODEL=%s\n", shellSingleQuote(model))
	}
	script.WriteString("DRAGONSCALE_RUN_CONTAINER_TESTS=1 $GO test -tags 'integration containers' -count=1 -timeout 20m -v ./pkg/memory/...\n")
	return script.String()
}

func generateScript(c *app.Context) string {
	cmdDir := cEnv(c, "CMD_DIR", defaultCmdDir)
	return scriptHeader() +
		fmt.Sprintf("rm -rf ./%s/workspace 2>/dev/null || true\n", cmdDir) +
		"$GO generate ./...\n" +
		"(cd internal/fantasy && $GO generate -run mockgen ./...)\n"
}

func buildScript(c *app.Context) string {
	version := "$(git describe --tags --always --dirty 2>/dev/null || echo \"dev\")"
	gitCommit := "$(git rev-parse --short=8 HEAD 2>/dev/null || echo \"dev\")"
	buildTime := "$(date +%FT%T%z)"
	goVersion := "$($GO version | awk '{print $3}')"
	buildDir := cEnv(c, "BUILD_DIR", defaultBuildDir)
	binaryName := cEnv(c, "BINARY_NAME", defaultBinaryName)
	cmdDir := cEnv(c, "CMD_DIR", defaultCmdDir)
	targetGOOS := cEnv(c, "GOOS", "linux")
	targetGOARCH := cEnv(c, "GOARCH", runtime.GOARCH)
	ldFlags := fmt.Sprintf("-X main.version=%s -X main.gitCommit=%s -X main.buildTime=%s -X main.goVersion=%s -s -w", version, gitCommit, buildTime, goVersion)
	return scriptHeader() +
		fmt.Sprintf("mkdir -p %s\n", buildDir) +
		fmt.Sprintf("echo \"build: target=%s/%s/%s\"\n", targetGOOS, targetGOARCH, binaryName) +
		fmt.Sprintf("GOOS=$GOOS GOARCH=$GOARCH CGO_ENABLED=$CGO_ENABLED $GO build $GOFLAGS -ldflags \"%s\" -o %s/%s-${GOOS}-${GOARCH} ./%s\n", ldFlags, buildDir, binaryName, cmdDir) +
		fmt.Sprintf("ln -sf %s-${GOOS}-${GOARCH} %s/%s\n", binaryName, buildDir, binaryName)
}

func buildAllScript(c *app.Context) string {
	ldFlags := "-ldflags '-s -w'"
	buildDir := cEnv(c, "BUILD_DIR", defaultBuildDir)
	binaryName := cEnv(c, "BINARY_NAME", defaultBinaryName)
	cmdDir := cEnv(c, "CMD_DIR", defaultCmdDir)
	targetGOOS := cEnv(c, "GOOS", "linux")
	targetGOARCH := cEnv(c, "GOARCH", runtime.GOARCH)
	var script strings.Builder
	script.WriteString(scriptHeader())
	fmt.Fprintf(&script, "mkdir -p %s\n", buildDir)
	fmt.Fprintf(&script, "echo \"build-all: target=%s/%s output=%s/%s-%s-%s\"\n", targetGOOS, targetGOARCH, buildDir, binaryName, targetGOOS, targetGOARCH)
	fmt.Fprintf(&script, "OUTPUT_NAME=%s/%s-%s-%s\n", buildDir, binaryName, targetGOOS, targetGOARCH)
	script.WriteString("export CGO_ENABLED=1\n")
	fmt.Fprintf(&script, "GOOS=$GOOS GOARCH=$GOARCH CGO_ENABLED=$CGO_ENABLED $GO build $GOFLAGS %s -o ${OUTPUT_NAME} ./%s\n", ldFlags, cmdDir)
	fmt.Fprintf(&script, "ln -sf %s-%s-%s %s/%s\n", binaryName, targetGOOS, targetGOARCH, buildDir, binaryName)
	return script.String()
}

func validateBuildTaskEnv(c *app.Context) error {
	targetGOOS := cEnv(c, "GOOS", "linux")
	if targetGOOS != "linux" {
		return fmt.Errorf("unsupported GOOS=%s: this project only supports linux/gnu builds", targetGOOS)
	}

	if cgoEnabled := strings.TrimSpace(cEnv(c, "CGO_ENABLED", "1")); cgoEnabled != "" && cgoEnabled != "1" {
		return fmt.Errorf("unsupported CGO_ENABLED=%s: builds require CGO_ENABLED=1", cgoEnabled)
	}

	if err := detectGlibc(); err != nil {
		return err
	}

	return nil
}

var detectGlibc = detectGlibcAvailable

func detectGlibcAvailable() error {
	output, err := exec.Command("ldd", "--version").Output()
	if err != nil {
		return fmt.Errorf("unable to verify libc: %w", err)
	}
	header := strings.ToLower(strings.SplitN(string(output), "\n", 2)[0])
	if strings.Contains(header, "glibc") || strings.Contains(header, "gnu c library") {
		return nil
	}
	return fmt.Errorf("unsupported libc: this project requires glibc")
}

func installScript(*app.Context) string {
	prefix := filepath.Join(homeDir(), ".local")
	buildDir := cEnv(nil, "BUILD_DIR", defaultBuildDir)
	binaryName := cEnv(nil, "BINARY_NAME", defaultBinaryName)
	return scriptHeader() +
		"mkdir -p ${INSTALL_PREFIX:-" + prefix + "}/bin\n" +
		fmt.Sprintf("cp %s/%s ${INSTALL_PREFIX:-%s}/bin/%s\n", buildDir, binaryName, prefix, binaryName) +
		fmt.Sprintf("chmod +x ${INSTALL_PREFIX:-%s}/bin/%s\n", prefix, binaryName)
}

func uninstallScript(*app.Context) string {
	binaryName := cEnv(nil, "BINARY_NAME", defaultBinaryName)
	prefix := filepath.Join(homeDir(), ".local")
	return "rm -f ${INSTALL_PREFIX:-" + prefix + "}/bin/" + binaryName + "\n"
}

func uninstallAllScript(c *app.Context) string {
	dHome := filepath.Join(homeDir(), ".dragonscale")
	if v := cEnv(c, "DRAGONSCALE_HOME", ""); v != "" {
		dHome = v
	}
	return "rm -rf " + dHome + "\n"
}

func cleanScript(*app.Context) string {
	buildDir := cEnv(nil, "BUILD_DIR", defaultBuildDir)
	return "rm -rf " + buildDir + "\n"
}

func lintSpecs(c *app.Context) []runner.CommandSpec {
	goBinary := cEnv(c, "GO", "go")
	return []runner.CommandSpec{
		{Name: goBinary, Args: []string{"fmt", "./..."}},
		{Name: goBinary, Args: []string{"vet", "./..."}},
		{Name: goBinary, Args: []string{"build", "./..."}},
	}
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
		"(cd pkg/itr && flatc --go -o . ./commands.fbs)\n" +
		"(cd pkg/tools && flatc --go -o . ./map_payloads.fbs)\n" +
		"snapshot > \"$after\"\n" +
		"if ! cmp -s \"$before\" \"$after\"; then echo \"::error::FlatBuffers generated code is stale. Run 'go generate ./pkg/itr ./pkg/tools' with the pinned flatc from scripts/install-flatc.sh and commit.\"; rm -f \"$before\" \"$after\"; exit 1; fi\nrm -f \"$before\" \"$after\"\n"
}

func mockgenCheckScript(*app.Context) string {
	return scriptHeader() +
		"repo=\"$PWD\"\n" +
		"before=\"$(mktemp)\"\nafter=\"$(mktemp)\"\n" +
		"trap 'rm -f \"$before\" \"$after\"' EXIT\n" +
		"snapshot() { git -c safe.directory=\"$repo\" diff --binary -- ':(glob)**/mock_*_test.go'; git -c safe.directory=\"$repo\" ls-files --others --exclude-standard -- ':(glob)**/mock_*_test.go' | LC_ALL=C sort | while IFS= read -r f; do [ -f \"$f\" ] && sha256sum \"$f\"; done; };\n" +
		"snapshot > \"$before\"\n" +
		"$GO generate -run mockgen ./...\n" +
		"(cd internal/fantasy && $GO generate -run mockgen ./...)\n" +
		"snapshot > \"$after\"\n" +
		"if ! cmp -s \"$before\" \"$after\"; then echo \"::error::mockgen generated mocks are stale. Run '$GO generate -run mockgen ./...' and '(cd internal/fantasy && $GO generate -run mockgen ./...)', then commit.\"; exit 1; fi\n"
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

func checkSpecs(c *app.Context) []runner.CommandSpec {
	goBinary := cEnv(c, "GO", "go")
	return []runner.CommandSpec{
		{Name: goBinary, Args: []string{"mod", "download"}},
		{Name: goBinary, Args: []string{"mod", "verify"}},
		{Name: goBinary, Args: []string{"fmt", "./..."}},
		{Name: goBinary, Args: []string{"vet", "./..."}},
		{Name: goBinary, Args: []string{"test", "./..."}},
	}
}

func runTaskEnv(c *app.Context) []string {
	if len(c.Argv) == 0 {
		return nil
	}
	return []string{"ARGS=" + strings.Join(c.Argv, " ")}
}

func runScript(c *app.Context) string {
	buildDir := cEnv(c, "BUILD_DIR", defaultBuildDir)
	binaryName := cEnv(c, "BINARY_NAME", defaultBinaryName)
	return scriptHeader() + buildDir + "/" + binaryName + " " + strings.Join(quoteArgs(c.Argv), " ") + "\n"
}

func quoteArgs(args []string) []string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, strconv.Quote(arg))
	}
	return quoted
}

func devcontainerRoot(c *app.Context) string {
	if c != nil && c.Root != "" {
		return c.Root
	}
	workspace := cValue(c, "DEVCONTAINER_WORKSPACE", ".")
	if workspace == "" {
		return "."
	}
	return workspace
}

func devcontainerBuildSpecs(c *app.Context) []runner.CommandSpec {
	return []runner.CommandSpec{
		{
			Name: "npx",
			Args: []string{"--yes", "@devcontainers/cli", "build", "--workspace-folder", devcontainerRoot(c)},
		},
	}
}

func devcontainerUpSpecs(c *app.Context) []runner.CommandSpec {
	return []runner.CommandSpec{
		{
			Name: "npx",
			Args: []string{"--yes", "@devcontainers/cli", "up", "--workspace-folder", devcontainerRoot(c)},
		},
	}
}

func devcontainerGenerateSpecs(c *app.Context) []runner.CommandSpec {
	root := devcontainerRoot(c)
	goBinary := cEnv(c, "GO", "go")
	return []runner.CommandSpec{
		{
			Name: "npx",
			Args: []string{
				"--yes", "@devcontainers/cli", "exec",
				"--workspace-folder", root, "--",
				"bash", "-lc",
				fmt.Sprintf("%s generate ./pkg/itr ./pkg/tools && sqlc generate -f pkg/memory/sqlc/sqlc.yaml && %s generate -run mockgen ./... && (cd internal/fantasy && %s generate -run mockgen ./...)", goBinary, goBinary, goBinary),
			},
		},
	}
}

func devcontainerVerifySpecs(c *app.Context) []runner.CommandSpec {
	root := devcontainerRoot(c)
	return []runner.CommandSpec{
		{
			Name: "npx",
			Args: []string{
				"--yes", "@devcontainers/cli", "exec",
				"--workspace-folder", root, "--",
				"bash", "-lc", "make flatc-check sqlc-check mockgen-check",
			},
		},
	}
}

func evalBuildSpecs(c *app.Context) []runner.CommandSpec {
	goBinary := cEnv(c, "GO", "go")
	goFlags := strings.Fields(cEnv(c, "GOFLAGS", "-v -trimpath -tags=stdjson"))
	goVersion := "unknown"
	goVersionParts := strings.Fields(outputOrDefault(goBinary + " version"))
	if len(goVersionParts) >= 3 {
		goVersion = goVersionParts[2]
	}
	version := outputOrDefault("git describe --tags --always --dirty 2>/dev/null || echo \"dev\"")
	commit := outputOrDefault("git rev-parse --short=8 HEAD 2>/dev/null || echo \"dev\"")
	buildTime := outputOrDefault("date +%FT%T%z")

	ldFlags := fmt.Sprintf(
		"-X main.version=%s -X main.gitCommit=%s -X main.buildTime=%s -X main.goVersion=%s -s -w",
		version, commit, buildTime, goVersion,
	)
	return []runner.CommandSpec{
		{Name: goBinary, Args: []string{"generate", "./..."}, Dir: evalTaskRoot(c)},
		{Name: "mkdir", Args: []string{"-p", "eval/bin"}, Dir: evalTaskRoot(c)},
		{
			Name: goBinary,
			Args: append(
				append(append([]string{"build"}, goFlags...), "-ldflags", ldFlags),
				"-o", filepath.Join("eval", "bin", "eval-runner"), "./eval/cmd/eval-runner",
			),
			Dir: evalTaskRoot(c),
		},
	}
}

func evalRunSpecs(c *app.Context) []runner.CommandSpec {
	cfgPath := cEnv(c, "DRAGONSCALE_EVAL_CONFIG", "./configs/default.json")
	baseCfg := cEnv(c, "DRAGONSCALE_EVAL_BASE_CONFIG", "")
	debug := cEnv(c, "DRAGONSCALE_EVAL_DEBUG", "") != ""
	promptfooArgs := strings.Fields(cEnv(c, "DRAGONSCALE_PROMPTFOO_ARGS", "--no-cache --no-progress-bar -j 1"))
	if len(promptfooArgs) == 0 {
		promptfooArgs = []string{"--no-cache", "--no-progress-bar", "-j", "1"}
	}

	specs := append([]runner.CommandSpec{}, maybeEvalBuildSpecs(c)...)
	if hasEvalSourceTree(c) {
		specs = append(specs, evalFixturesSpecs(c)...)
	}
	if debug && strings.TrimSpace(baseCfg) != "" {
		specs = append(specs, runner.CommandSpec{
			Name: "echo",
			Args: []string{fmt.Sprintf("DRAGONSCALE_EVAL_BASE_CONFIG=%s", baseCfg)},
			Dir:  evalTaskRoot(c),
		})
	}
	if debug {
		specs = append(specs, runner.CommandSpec{
			Name: "echo",
			Args: []string{fmt.Sprintf("DRAGONSCALE_EVAL_CONFIG=%s", cfgPath)},
			Dir:  evalTaskRoot(c),
		})
	}
	args := append([]string{"--yes", "promptfoo", "eval", "--config", "promptfooconfig.yaml"}, promptfooArgs...)
	specs = append(specs, runner.CommandSpec{
		Name: "npx",
		Args: args,
		Dir:  evalWorkspaceDir(c),
		Env:  []string{fmt.Sprintf("DRAGONSCALE_EVAL_CONFIG=%s", cfgPath)},
	})
	return specs
}

func evalProofFullScript(_ *app.Context) string {
	return strings.Join([]string{
		"set -euo pipefail",
		"mkdir -p eval/results",
		": > eval/results/make-eval.log",
		"export PROMPTFOO_PASS_RATE_THRESHOLD=\"${PROMPTFOO_PASS_RATE_THRESHOLD:-100}\"",
		"echo \"[eval-proof-full] starting local make eval\" | tee -a eval/results/make-eval.log",
		"echo \"[eval-proof-full] promptfoo pass-rate threshold: ${PROMPTFOO_PASS_RATE_THRESHOLD}%\" | tee -a eval/results/make-eval.log",
		"echo \"[eval-proof-full] provider auth defaults: OpenRouter via OPENROUTER_API_KEY, then OpenAI via OPENAI_API_KEY\" | tee -a eval/results/make-eval.log",
		"SKIP_DEVCONTAINER_WRAPPER=1 DEVCONTAINER_EXEC= make eval 2>&1 | tee -a eval/results/make-eval.log",
	}, "\n")
}

func evalFixturesSpecs(c *app.Context) []runner.CommandSpec {
	sandbox := filepath.Join(homeDir(), ".local", "share", "dragonscale", "sandbox")
	project := filepath.Join(sandbox, "project")
	skills := filepath.Join(homeDir(), ".local", "share", "dragonscale", "skills")
	shared := filepath.Join(sandbox, "sample_data.txt")
	fixture := filepath.Join(sandbox, "eval_fixture.txt")
	files := []string{
		filepath.Join(sandbox, "eval_test_output.txt"),
		filepath.Join(sandbox, "test_steps.txt"),
		filepath.Join(sandbox, "eval_checkpoint.txt"),
		filepath.Join(sandbox, "chain_test.txt"),
		filepath.Join(sandbox, "current_year.txt"),
		filepath.Join(sandbox, "result.txt"),
		filepath.Join(sandbox, "progressive_test.txt"),
		filepath.Join(sandbox, "os_name.txt"),
	}
	sourceFixture := filepath.Join("eval", "fixtures", "sample_data.txt")

	specs := []runner.CommandSpec{
		{Name: "mkdir", Args: []string{"-p", sandbox}, Dir: evalTaskRoot(c)},
		{Name: "rm", Args: append([]string{"-f"}, files...), Dir: evalTaskRoot(c)},
		{Name: "rm", Args: []string{"-rf", project}, Dir: evalTaskRoot(c)},
		{Name: "mkdir", Args: []string{"-p", skills}, Dir: evalTaskRoot(c)},
		{Name: "bash", Args: []string{"-lc", fmt.Sprintf("printf '%%s\\n%%s\\n' \"dragonscale eval fixture — hello from the eval harness\" \"This is line two of the fixture file.\" > %q", fixture)}, Dir: evalTaskRoot(c)},
		{Name: "cp", Args: []string{"-f", sourceFixture, shared}, Dir: evalTaskRoot(c)},
	}
	specs = append(specs, runner.CommandSpec{
		Name: "bash",
		Args: []string{"-lc", "if [ -d eval/fixtures/skills ]; then cp -rf eval/fixtures/skills/. " + strconv.Quote(skills) + "; fi"},
		Dir:  evalTaskRoot(c),
	})
	return specs
}

func evalViewSpecs(c *app.Context) []runner.CommandSpec {
	return []runner.CommandSpec{
		{
			Name: "npx",
			Args: []string{"--yes", "promptfoo", "view"},
			Dir:  evalWorkspaceDir(c),
		},
	}
}

func outputOrDefault(command string) string {
	output, err := exec.Command("bash", "-lc", command).Output()
	if err != nil {
		return "dev"
	}
	return strings.TrimSpace(string(output))
}

func evalTaskRoot(c *app.Context) string {
	if c != nil && strings.TrimSpace(c.Root) != "" {
		return c.Root
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

func evalWorkspaceDir(c *app.Context) string {
	return filepath.Join(evalTaskRoot(c), "eval")
}

func evalRunnerBinaryPath(c *app.Context) string {
	return filepath.Join(evalWorkspaceDir(c), "bin", "eval-runner")
}

func hasEvalSourceTree(c *app.Context) bool {
	_, err := os.Stat(filepath.Join(evalWorkspaceDir(c), "cmd", "eval-runner", "main.go"))
	return err == nil
}

func maybeEvalBuildSpecs(c *app.Context) []runner.CommandSpec {
	if !hasEvalSourceTree(c) {
		return nil
	}
	return evalBuildSpecs(c)
}

func evalCompareScript(c *app.Context) string {
	return "cd " + shellSingleQuote(evalWorkspaceDir(c)) + " && DEVCONTAINER_EXEC= EVAL_NPM_CMD=\"npx --yes\" ./scripts/compare.sh --repeat 3\n"
}

func evalTestScript(c *app.Context) string {
	return "cd " + shellSingleQuote(evalTaskRoot(c)) + " && $GO test -v ./eval/go_evals/...\n"
}

func evalCleanScript(c *app.Context) string {
	return "rm -rf " + shellSingleQuote(filepath.Join(evalWorkspaceDir(c), "results")) + " " + shellSingleQuote(filepath.Join(evalWorkspaceDir(c), "bin")) + "\n"
}
