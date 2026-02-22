package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseOutputMode(t *testing.T) {
	mode, err := parseOutputMode(false, false, "")
	require.NoError(t, err)
	require.Equal(t, "text", string(mode))

	mode, err = parseOutputMode(true, false, "text")
	require.NoError(t, err)
	require.Equal(t, "raw", string(mode))

	mode, err = parseOutputMode(false, true, "text")
	require.NoError(t, err)
	require.Equal(t, "json", string(mode))

	_, err = parseOutputMode(false, false, "invalid")
	require.Error(t, err)
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
