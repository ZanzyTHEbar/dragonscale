package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ZanzyTHEbar/dragonscale/internal/opsctl/format"
	"github.com/stretchr/testify/require"
)

func TestParseOutputMode(t *testing.T) {
	mode, err := parseOutputMode(false, false, "")
	require.NoError(t, err)
	require.Equal(t, "text", string(mode))

	mode, err = parseOutputMode(true, false, "text")
	require.NoError(t, err)
	require.Equal(t, "raw", string(mode))

	mode, err = parseOutputMode(true, true, "text")
	require.NoError(t, err)
	require.Equal(t, "raw", string(mode))

	mode, err = parseOutputMode(false, true, "text")
	require.NoError(t, err)
	require.Equal(t, "json", string(mode))

	_, err = parseOutputMode(false, false, "invalid")
	require.Error(t, err)
}

func TestParseOutputModePriority(t *testing.T) {
	t.Parallel()

	mode, err := parseOutputMode(true, true, "json")
	require.NoError(t, err)
	require.Equal(t, format.OutputRaw, mode)

	mode, err = parseOutputMode(false, true, "text")
	require.NoError(t, err)
	require.Equal(t, format.OutputJSON, mode)

	mode, err = parseOutputMode(true, false, "json")
	require.NoError(t, err)
	require.Equal(t, format.OutputRaw, mode)

	mode, err = parseOutputMode(false, false, "json")
	require.NoError(t, err)
	require.Equal(t, format.OutputJSON, mode)
}

func TestRunPrintsUsageWithoutArgs(t *testing.T) {
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	code := run([]string{}, out, errOut)
	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "Usage:")
	require.Contains(t, out.String(), "Tasks:")
	require.Equal(t, "", errOut.String())
}

func TestRunUnknownCommand(t *testing.T) {
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	code := run([]string{"not-a-real-command"}, out, errOut)
	require.Equal(t, 2, code)
	require.Contains(t, errOut.String(), "unknown command: not-a-real-command")
	require.Equal(t, "", out.String())
}

func TestRunInvalidFormat(t *testing.T) {
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	code := run([]string{"--format=bad", "help"}, out, errOut)
	require.Equal(t, 2, code)
	require.Contains(t, errOut.String(), "invalid --format value \"bad\"")
}

func TestRunInvalidRoot(t *testing.T) {
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	root := filepath.Join(t.TempDir(), "missing")
	code := run([]string{"--root", root, "help"}, out, errOut)
	require.Equal(t, 2, code)
	require.Contains(t, errOut.String(), "invalid --root")
	require.Equal(t, "", out.String())
}

func TestRunInvalidCwd(t *testing.T) {
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	root := t.TempDir()
	missing := filepath.Join(root, "does-not-exist")
	code := run([]string{"--root", root, "--cwd", missing, "build"}, out, errOut)
	require.Equal(t, 2, code)
	require.Contains(t, errOut.String(), "invalid --cwd")
	require.Equal(t, "", out.String())
}

func TestRunHelpAlias(t *testing.T) {
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	code := run([]string{"--help"}, out, errOut)
	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "Usage:")
	require.Equal(t, "", errOut.String())
}

func TestRunHelpIncludesAllAlias(t *testing.T) {
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	code := run([]string{"help"}, out, errOut)
	require.Equal(t, 0, code)
	require.Equal(t, "", errOut.String())
	require.Contains(t, out.String(), "all")
	require.Contains(t, out.String(), "build")
}

func TestRunHelpIncludesGlobalOutputFlags(t *testing.T) {
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	code := run([]string{"--help"}, out, errOut)
	require.Equal(t, 0, code)
	require.Equal(t, "", errOut.String())
	require.Contains(t, out.String(), "--format")
	require.Contains(t, out.String(), "--json")
	require.Contains(t, out.String(), "--raw")
	require.Contains(t, out.String(), "--no-color")
	require.Contains(t, out.String(), "--quiet")
	require.Contains(t, out.String(), "--root")
	require.Contains(t, out.String(), "--cwd")
	require.Contains(t, out.String(), "--timeout")
}

func TestDefaultRunEnvKeysIncludesPromptfooArgs(t *testing.T) {
	t.Parallel()

	keys := defaultRunEnvKeys()
	require.Contains(t, keys, "DRAGONSCALE_PROMPTFOO_ARGS")
}

func TestMakefilePhonyTargetsAreAvailableInOpsctl(t *testing.T) {
	makeTargets := parseMakefilePhonyTargets(t)
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	code := run([]string{"--help"}, out, errOut)
	require.Equal(t, 0, code)
	require.Equal(t, "", errOut.String())

	tasks := parseOpsctlHelpTasks(t, out.String())
	for _, target := range makeTargets {
		require.Contains(t, tasks, target, "Makefile phony target missing from opsctl help: %s", target)
	}
}

func TestMakefileAllTargetIsAliasToBuild(t *testing.T) {
	makefile, err := findProjectMakefile()
	require.NoError(t, err)
	require.Equal(t, []string{"build"}, parseMakeTargetPrereqs(t, makefile, "all"))
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
