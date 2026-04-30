package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ZanzyTHEbar/dragonscale/internal/opsctl/format"
	"github.com/ZanzyTHEbar/dragonscale/internal/testcmp"
	"github.com/stretchr/testify/require"
)

func TestParseOutputMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		raw     bool
		json    bool
		format  string
		want    format.OutputMode
		wantErr bool
	}{
		{name: "default", want: format.OutputText},
		{name: "raw overrides format", raw: true, format: "text", want: format.OutputRaw},
		{name: "raw overrides json", raw: true, json: true, format: "text", want: format.OutputRaw},
		{name: "json flag", json: true, format: "text", want: format.OutputJSON},
		{name: "invalid format", format: "invalid", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mode, err := parseOutputMode(tt.raw, tt.json, tt.format)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			testcmp.RequireEqual(t, tt.want, mode)
		})
	}
}

func TestParseOutputModePriority(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		raw    bool
		json   bool
		format string
		want   format.OutputMode
	}{
		{name: "raw overrides json and format", raw: true, json: true, format: "json", want: format.OutputRaw},
		{name: "json overrides format", json: true, format: "text", want: format.OutputJSON},
		{name: "raw overrides format", raw: true, format: "json", want: format.OutputRaw},
		{name: "format used without flags", format: "json", want: format.OutputJSON},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mode, err := parseOutputMode(tt.raw, tt.json, tt.format)
			require.NoError(t, err)
			testcmp.RequireEqual(t, tt.want, mode)
		})
	}
}

func TestRunInvalidAndHelpCases(t *testing.T) {
	tests := []struct {
		name        string
		args        func(t *testing.T) []string
		wantCode    int
		wantOut     []string
		wantErrOut  []string
		emptyOut    bool
		emptyErrOut bool
	}{
		{
			name:        "usage without args",
			args:        func(t *testing.T) []string { return []string{} },
			wantCode:    0,
			wantOut:     []string{"Usage:", "Tasks:"},
			emptyErrOut: true,
		},
		{
			name:       "unknown command",
			args:       func(t *testing.T) []string { return []string{"not-a-real-command"} },
			wantCode:   2,
			wantErrOut: []string{"unknown command: not-a-real-command"},
			emptyOut:   true,
		},
		{
			name:       "invalid format",
			args:       func(t *testing.T) []string { return []string{"--format=bad", "help"} },
			wantCode:   2,
			wantErrOut: []string{"invalid --format value \"bad\""},
		},
		{
			name: "invalid root",
			args: func(t *testing.T) []string {
				return []string{"--root", filepath.Join(t.TempDir(), "missing"), "help"}
			},
			wantCode:   2,
			wantErrOut: []string{"invalid --root"},
			emptyOut:   true,
		},
		{
			name: "invalid cwd",
			args: func(t *testing.T) []string {
				root := t.TempDir()
				missing := filepath.Join(root, "does-not-exist")
				return []string{"--root", root, "--cwd", missing, "build"}
			},
			wantCode:   2,
			wantErrOut: []string{"invalid --cwd"},
			emptyOut:   true,
		},
		{
			name:        "help alias",
			args:        func(t *testing.T) []string { return []string{"--help"} },
			wantCode:    0,
			wantOut:     []string{"Usage:"},
			emptyErrOut: true,
		},
		{
			name:        "help includes all alias",
			args:        func(t *testing.T) []string { return []string{"help"} },
			wantCode:    0,
			wantOut:     []string{"all", "build"},
			emptyErrOut: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := &bytes.Buffer{}
			errOut := &bytes.Buffer{}
			code := run(tt.args(t), out, errOut)
			testcmp.RequireEqual(t, tt.wantCode, code)
			for _, want := range tt.wantOut {
				require.Contains(t, out.String(), want)
			}
			for _, want := range tt.wantErrOut {
				require.Contains(t, errOut.String(), want)
			}
			if tt.emptyOut {
				testcmp.RequireEqual(t, "", out.String())
			}
			if tt.emptyErrOut {
				testcmp.RequireEqual(t, "", errOut.String())
			}
		})
	}
}

func TestRunHelpIncludesGlobalOutputFlags(t *testing.T) {
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	code := run([]string{"--help"}, out, errOut)
	testcmp.RequireEqual(t, 0, code)
	testcmp.RequireEqual(t, "", errOut.String())
	require.Contains(t, out.String(), "--format")
	require.Contains(t, out.String(), "--json")
	require.Contains(t, out.String(), "--raw")
	require.Contains(t, out.String(), "--no-color")
	require.Contains(t, out.String(), "--quiet")
	require.Contains(t, out.String(), "--root")
	require.Contains(t, out.String(), "--cwd")
	require.Contains(t, out.String(), "--timeout")
}

func TestRunHelpIncludesSkipDevcontainerWrapperEnv(t *testing.T) {
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	code := run([]string{"--help"}, out, errOut)
	testcmp.RequireEqual(t, 0, code)
	testcmp.RequireEqual(t, "", errOut.String())
	require.Contains(t, out.String(), "SKIP_DEVCONTAINER_WRAPPER")
}

func TestDefaultRunEnvKeysIncludesExpectedKeys(t *testing.T) {
	t.Parallel()

	keys := defaultRunEnvKeys()
	for _, key := range []string{
		"DRAGONSCALE_PROMPTFOO_ARGS",
		"PROMPTFOO_PASS_RATE_THRESHOLD",
		"OPENROUTER_API_KEY",
		"OPENAI_API_KEY",
		"DRAGONSCALE_PROVIDERS_OPENROUTER_API_KEY",
		"DRAGONSCALE_PROVIDERS_OPENROUTER_API_BASE",
		"DRAGONSCALE_PROVIDERS_OPENAI_API_KEY",
		"DRAGONSCALE_PROVIDERS_OPENAI_API_BASE",
		"DRAGONSCALE_AGENTS_DEFAULTS_PROVIDER",
		"DRAGONSCALE_AGENTS_DEFAULTS_MODEL",
		"SKIP_DEVCONTAINER_WRAPPER",
	} {
		t.Run(key, func(t *testing.T) {
			t.Parallel()

			require.Contains(t, keys, key)
		})
	}
}

func TestMakefilePhonyTargetsAreAvailableInOpsctl(t *testing.T) {
	makeTargets := parseMakefilePhonyTargets(t)
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	code := run([]string{"--help"}, out, errOut)
	testcmp.RequireEqual(t, 0, code)
	testcmp.RequireEqual(t, "", errOut.String())

	tasks := parseOpsctlHelpTasks(t, out.String())
	for _, target := range makeTargets {
		require.Contains(t, tasks, target, "Makefile phony target missing from opsctl help: %s", target)
	}
}

func TestMakefileAllTargetIsAliasToBuild(t *testing.T) {
	makefile, err := findProjectMakefile()
	require.NoError(t, err)
	testcmp.RequireEqual(t, []string{"build"}, parseMakeTargetPrereqs(t, makefile, "all"))
}

func TestMakeAllTargetRunsOpsctlBuild(t *testing.T) {
	output := runMakeTargetWithFakeOpsctl(t, "all", nil)
	require.Contains(t, output, "cmd:build")
}

func TestMakeEvalBuildForwardsEnvToOpsctl(t *testing.T) {
	output := runMakeTargetWithFakeOpsctl(t, "eval-build", map[string]string{
		"DRAGONSCALE_EVAL_CONFIG": "/tmp/eval/config.json",
	})
	require.Contains(t, output, "cmd:--no-color --format raw eval-build")
	require.Contains(t, output, "eval-config:/tmp/eval/config.json")
}

func TestMakeEvalTestHostModeClearsDevcontainerExec(t *testing.T) {
	output := runMakeTargetWithFakeOpsctl(t, "eval-test", map[string]string{
		"DEVCONTAINER_EXEC":         "",
		"SKIP_DEVCONTAINER_WRAPPER": "1",
	})
	require.Contains(t, output, "cmd:--no-color --format raw eval-test")
	require.NotContains(t, output, "@devcontainers/cli")
}

func TestMakeEvalProofFullRunsOuterOpsctlLocally(t *testing.T) {
	t.Parallel()

	makefile, err := findProjectMakefile()
	require.NoError(t, err)
	raw, err := os.ReadFile(makefile)
	require.NoError(t, err)
	content := string(raw)

	require.Contains(t, content, "eval-proof-full:")
	require.Contains(t, content, "SKIP_DEVCONTAINER_WRAPPER=1 DEVCONTAINER_EXEC=")
	require.Contains(t, content, "$(CGO_ENV) $(GO) run ./cmd/opsctl $(OPSCTL_EVAL_ARGS) eval-proof-full")
	require.NotContains(t, content, "$(OPSCTL_EVAL) $(OPSCTL_EVAL_ARGS) eval-proof-full")
}

func parseMakefilePhonyTargets(t *testing.T) []string {
	t.Helper()

	makefile, err := findProjectMakefile()
	require.NoError(t, err)
	raw, err := os.ReadFile(makefile)
	require.NoError(t, err)
	lines := bytes.Split(raw, []byte("\n"))

	var targets []string
	inPhony := false
	for _, b := range lines {
		line := string(b)
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, ".PHONY:") {
			inPhony = true
			line = strings.TrimPrefix(trimmed, ".PHONY:")
		} else if !inPhony {
			continue
		} else if strings.HasPrefix(trimmed, "#") {
			inPhony = false
			continue
		}

		if !inPhony {
			continue
		}

		line = strings.TrimSpace(line)
		if line == "" {
			break
		}

		if strings.HasSuffix(line, "\\") {
			line = strings.TrimSuffix(line, "\\")
		} else {
			inPhony = false
		}

		for _, target := range strings.Fields(line) {
			if target == "\\" {
				continue
			}
			targets = append(targets, target)
		}

		if !strings.HasSuffix(string(b), "\\") {
			break
		}
	}

	return targets
}

func parseMakeTargetPrereqs(t *testing.T, makefile, target string) []string {
	t.Helper()

	raw, err := os.ReadFile(makefile)
	require.NoError(t, err)
	lines := strings.Split(string(raw), "\n")
	prefix := target + ":"

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, prefix) {
			after := strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
			if idx := strings.Index(after, "#"); idx != -1 {
				after = strings.TrimSpace(after[:idx])
			}
			if after == "" {
				return nil
			}
			return strings.Fields(after)
		}
	}
	t.Fatalf("make target %q not found in %s", target, makefile)
	return nil
}

func runMakeTargetWithFakeOpsctl(t *testing.T, target string, env map[string]string) string {
	t.Helper()

	makefile, err := findProjectMakefile()
	require.NoError(t, err)
	makeRoot := filepath.Dir(makefile)

	fakeOpsctl := filepath.Join(t.TempDir(), "opsctl.sh")
	fakeScript := []byte(`#!/usr/bin/env sh
printf 'cmd:%s\n' "$*"
printf 'eval-config:%s\n' "${DRAGONSCALE_EVAL_CONFIG-}"
`)
	require.NoError(t, os.WriteFile(fakeOpsctl, fakeScript, 0o755))

	dummyOpsctl := filepath.Join(t.TempDir(), "opsctl")
	require.NoError(t, os.WriteFile(dummyOpsctl, []byte("#!/usr/bin/env sh\nexit 0\n"), 0o755))

	cmd := exec.Command("make", "-s", "-f", makefile, target)
	cmd.Dir = makeRoot
	runEnv := append(
		os.Environ(),
		"OPSCTL="+fakeOpsctl,
		"OPSCTL_BIN="+dummyOpsctl,
		"OPSCTL_EVAL="+fakeOpsctl,
		"OPSCTL_EVAL_ARGS=--no-color --format raw",
	)
	for key, value := range env {
		runEnv = append(runEnv, key+"="+value)
	}
	cmd.Env = runEnv

	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "make %s failed: %s", target, string(output))
	return string(output)
}

func findProjectMakefile() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for i := 0; i < 6; i++ {
		candidate := filepath.Join(dir, "Makefile")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", os.ErrNotExist
}

func parseOpsctlHelpTasks(t *testing.T, output string) []string {
	t.Helper()

	var tasks []string
	inTasks := false
	for _, line := range bytes.Split([]byte(output), []byte("\n")) {
		text := string(line)
		if strings.HasPrefix(text, "Tasks:") {
			inTasks = true
			continue
		}
		if !inTasks {
			continue
		}
		if strings.HasPrefix(text, "environment:") {
			break
		}
		text = strings.TrimSpace(text)
		if text == "" || strings.HasPrefix(text, "available") {
			continue
		}
		parts := strings.Fields(text)
		if len(parts) == 0 || strings.HasPrefix(parts[0], "--") {
			continue
		}
		tasks = append(tasks, parts[0])
	}
	return tasks
}
