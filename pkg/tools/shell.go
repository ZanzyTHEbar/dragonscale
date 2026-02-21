package tools

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/security"
)

var shellRedactor = security.NewRedactor()

// ShellMode controls how command filtering works.
type ShellMode string

const (
	ShellModeDenyList  ShellMode = "denylist"  // default: block known-dangerous patterns
	ShellModeAllowList ShellMode = "allowlist" // only permit commands matching allow patterns
	ShellModeDisabled  ShellMode = "disabled"  // shell execution entirely disabled

	// maxOutputBytes is the maximum bytes we'll read from stdout/stderr combined.
	// This prevents OOM from commands like `yes` or `cat /dev/urandom`.
	maxOutputBytes = 1024 * 1024 // 1 MB

	// maxOutputDisplay is the max characters shown to the LLM.
	maxOutputDisplay = 10000

	// maxCommandLength is the maximum allowed command string length.
	// Prevents abuse via excessively long commands that could hide payloads.
	maxCommandLength = 8192
)

type ExecTool struct {
	workingDir          string
	timeout             time.Duration
	denyPatterns        []*regexp.Regexp
	allowPatterns       []*regexp.Regexp
	restrictToWorkspace bool
	mode                ShellMode
	workspace           string // root workspace path for audit logging
}

func NewExecTool(workingDir string, restrict bool) *ExecTool {
	denyPatterns := buildDenyPatterns()

	return &ExecTool{
		workingDir:          workingDir,
		timeout:             60 * time.Second,
		denyPatterns:        denyPatterns,
		allowPatterns:       nil,
		restrictToWorkspace: restrict,
		mode:                ShellModeDenyList,
		workspace:           workingDir,
	}
}

// buildDenyPatterns returns the hardened set of deny-list patterns.
func buildDenyPatterns() []*regexp.Regexp {
	patterns := []string{
		// Destructive file operations
		`\brm\s+-[rf]{1,2}\b`,
		`\bdel\s+/[fq]\b`,
		`\brmdir\s+/s\b`,

		// Disk wiping
		`\b(format|mkfs|diskpart)\b`,
		`\bdd\s+if=`,
		`>\s*/dev/sd[a-z]\b`,

		// System control
		`\b(shutdown|reboot|poweroff|init\s+[06])\b`,

		// Fork bomb
		`:\(\)\s*\{.*\};\s*:`,

		// Scripting language exec bypass
		`\b(python[23]?|perl|ruby)\s+.*(-c\s+|.*\b(system|exec|os\.system|subprocess|popen|eval)\b)`,

		// Base64 decode piped to shell
		`base64\s+(-d|--decode).*\|\s*(sh|bash|zsh|dash)`,

		// curl/wget piped to shell
		`\b(curl|wget)\b.*\|\s*(sh|bash|zsh|dash|sudo)`,

		// Direct writes to critical system paths
		`>\s*/etc/(passwd|shadow|sudoers|hosts)`,

		// Crontab manipulation
		`\bcrontab\s+-[re]\b`,

		// Network exfiltration via common tools to non-local
		`\bnc\s+-[el]`,

		// chmod to world-writable
		`\bchmod\s+.*777\b`,

		// Attempting to modify env to bypass PATH
		`\bexport\s+path\s*=`,
		`\benv\s+path\s*=`,

		// sudo escalation
		`\bsudo\s+(su|bash|sh|zsh|chmod|chown)\b`,

		// Hex/octal escape exec bypass (e.g. $'\x72\x6d' for "rm")
		`\$'\\x[0-9a-f]`,

		// Process substitution into shell
		`<\(.*\)\s*\|\s*(sh|bash|zsh)`,

		// Environment variable overrides hiding commands
		`\bLD_PRELOAD\s*=`,
		`\bLD_LIBRARY_PATH\s*=`,

		// Docker container escape
		`\bdocker\s+run.*--privileged`,
		`\bdocker\s+run.*-v\s+/:/`,

		// Reverse shell patterns
		`\b(bash|sh|zsh)\s+.*-i\s+.*>&\s*/dev/tcp/`,
		`\bmkfifo\s+.*\bcat\b.*\b(sh|bash)\b`,

		// Command obfuscation via eval
		`\beval\s+.*\$\(`,

		// Host identity exfiltration shortcut
		`\bhostname\b`,
	}

	compiled := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		compiled = append(compiled, regexp.MustCompile(p))
	}
	return compiled
}

func (t *ExecTool) Name() string {
	return "exec"
}

func (t *ExecTool) Description() string {
	return "Execute a shell command and return its output. Use with caution."
}

func (t *ExecTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"command": map[string]interface{}{
				"type":        "string",
				"description": "The shell command to execute",
			},
			"working_dir": map[string]interface{}{
				"type":        "string",
				"description": "Optional working directory for the command",
			},
		},
		"required": []string{"command"},
	}
}

// Capabilities declares that ExecTool requires shell access with denylist
// enforcement. This satisfies the CapableTool interface so the SecureBus
// applies the correct policy without restricting existing behavior.
func (t *ExecTool) Capabilities() ToolCapabilities {
	return ToolCapabilities{
		Shell: ShellDenylist,
		Filesystem: []PathRule{
			{Pattern: "**", Mode: "rw"},
		},
	}
}

func (t *ExecTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	command, ok := args["command"].(string)
	if !ok {
		return ErrorResult("command is required")
	}

	if len(command) > maxCommandLength {
		return ErrorResult(fmt.Sprintf("command too long: %d bytes (max %d)", len(command), maxCommandLength))
	}

	if strings.TrimSpace(command) == "" {
		return ErrorResult("command cannot be empty")
	}

	cwd := t.workingDir
	if wd, ok := args["working_dir"].(string); ok && wd != "" {
		if t.restrictToWorkspace && t.workspace != "" {
			resolved, err := validatePath(wd, t.workspace, true)
			if err != nil {
				return ErrorResult(fmt.Sprintf("working_dir blocked: %v", err))
			}
			cwd = resolved
		} else {
			cwd = wd
		}
	}

	if cwd == "" {
		wd, err := os.Getwd()
		if err == nil {
			cwd = wd
		}
	}

	startTime := time.Now()

	if t.mode == ShellModeDisabled {
		t.auditLog(command, cwd, -1, 0, true, time.Since(startTime))
		return ErrorResult("Shell execution is disabled")
	}

	if guardError := t.guardCommand(command, cwd); guardError != "" {
		t.auditLog(command, cwd, -1, 0, true, time.Since(startTime))
		return ErrorResult(guardError)
	}

	// timeout == 0 means no timeout
	var cmdCtx context.Context
	var cancel context.CancelFunc
	if t.timeout > 0 {
		cmdCtx, cancel = context.WithTimeout(ctx, t.timeout)
	} else {
		cmdCtx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(cmdCtx, "powershell", "-NoProfile", "-NonInteractive", "-Command", command)
	} else {
		cmd = exec.CommandContext(cmdCtx, "sh", "-c", command)
	}
	if cwd != "" {
		cmd.Dir = cwd
	}

	// Set process group so we can kill children on timeout
	if runtime.GOOS != "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}

	// Use limited readers to prevent OOM from unbounded output
	var stdout, stderr bytes.Buffer
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to create stdout pipe: %v", err))
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to create stderr pipe: %v", err))
	}

	if err := cmd.Start(); err != nil {
		t.auditLog(command, cwd, -1, 0, false, time.Since(startTime))
		return ErrorResult(fmt.Sprintf("failed to start command: %v", err))
	}

	// Read with size limits
	io.Copy(&stdout, io.LimitReader(stdoutPipe, maxOutputBytes))
	io.Copy(&stderr, io.LimitReader(stderrPipe, maxOutputBytes))

	err = cmd.Wait()
	duration := time.Since(startTime)

	output := stdout.String()
	if stderr.Len() > 0 {
		output += "\nSTDERR:\n" + stderr.String()
	}

	exitCode := 0
	if err != nil {
		if cmdCtx.Err() == context.DeadlineExceeded {
			// Kill the entire process group on timeout
			t.killProcessGroup(cmd)

			msg := fmt.Sprintf("Command timed out after %v", t.timeout)
			t.auditLog(command, cwd, -1, len(output), false, duration)
			return &ToolResult{
				ForLLM:  msg,
				ForUser: msg,
				IsError: true,
			}
		}
		// Extract exit code if possible
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
		output += fmt.Sprintf("\nExit code: %v", err)
	}

	if output == "" {
		output = "(no output)"
	}

	// Truncate display output
	if len(output) > maxOutputDisplay {
		output = output[:maxOutputDisplay] + fmt.Sprintf("\n... (truncated, %d more chars)", len(output)-maxOutputDisplay)
	}

	t.auditLog(command, cwd, exitCode, len(output), false, duration)

	if err != nil {
		return &ToolResult{
			ForLLM:  output,
			ForUser: output,
			IsError: true,
		}
	}

	return &ToolResult{
		ForLLM:  output,
		ForUser: output,
		IsError: false,
	}
}

// killProcessGroup sends SIGKILL to the entire process group.
func (t *ExecTool) killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil || runtime.GOOS == "windows" {
		return
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		// Fallback: kill just the process
		cmd.Process.Kill()
		return
	}
	syscall.Kill(-pgid, syscall.SIGKILL)
}

func (t *ExecTool) guardCommand(command, cwd string) string {
	cmd := strings.TrimSpace(command)
	lower := strings.ToLower(cmd)

	// In allowlist mode, only permit matching commands
	if t.mode == ShellModeAllowList {
		if len(t.allowPatterns) == 0 {
			return "Command blocked: allowlist mode enabled but no patterns configured"
		}
		allowed := false
		for _, pattern := range t.allowPatterns {
			if pattern.MatchString(lower) {
				allowed = true
				break
			}
		}
		if !allowed {
			return "Command blocked by safety guard (not in allowlist)"
		}
		// Even in allowlist mode, still check path traversal
	} else {
		// Denylist mode: check deny patterns
		for _, pattern := range t.denyPatterns {
			if pattern.MatchString(lower) {
				return "Command blocked by safety guard (dangerous pattern detected)"
			}
		}

		// Legacy allowlist check (for backward compat when in denylist mode)
		if len(t.allowPatterns) > 0 {
			allowed := false
			for _, pattern := range t.allowPatterns {
				if pattern.MatchString(lower) {
					allowed = true
					break
				}
			}
			if !allowed {
				return "Command blocked by safety guard (not in allowlist)"
			}
		}
	}

	if t.restrictToWorkspace {
		if strings.Contains(cmd, "..\\") || strings.Contains(cmd, "../") {
			return "Command blocked by safety guard (path traversal detected)"
		}

		cwdPath, err := filepath.Abs(cwd)
		if err != nil {
			return ""
		}

		pathPattern := regexp.MustCompile(`[A-Za-z]:\\[^\\\"']+|/[^\s\"']+`)
		matches := pathPattern.FindAllString(cmd, -1)

		for _, raw := range matches {
			p, err := filepath.Abs(raw)
			if err != nil {
				continue
			}

			rel, err := filepath.Rel(cwdPath, p)
			if err != nil {
				continue
			}

			if strings.HasPrefix(rel, "..") {
				return "Command blocked by safety guard (path outside working dir)"
			}
		}
	}

	return ""
}

// auditLog writes a structured log entry for every shell command execution.
func (t *ExecTool) auditLog(command, cwd string, exitCode, outputLen int, blocked bool, duration time.Duration) {
	status := "ok"
	if blocked {
		status = "blocked"
	} else if exitCode != 0 {
		status = "error"
	}

	logger.InfoCF("shell", "Command execution",
		map[string]interface{}{
			"command":    shellRedactor.Redact(command),
			"cwd":        cwd,
			"exit_code":  exitCode,
			"output_len": outputLen,
			"blocked":    blocked,
			"status":     status,
			"duration":   duration.String(),
		})
}

func (t *ExecTool) SetTimeout(timeout time.Duration) {
	t.timeout = timeout
}

// SetRestrictToWorkspace is the legacy name. Use SetRestrictToSandbox for new code.
func (t *ExecTool) SetRestrictToWorkspace(restrict bool) {
	t.restrictToWorkspace = restrict
}

func (t *ExecTool) SetRestrictToSandbox(restrict bool) {
	t.restrictToWorkspace = restrict
}

func (t *ExecTool) SetMode(mode ShellMode) {
	t.mode = mode
}

func (t *ExecTool) SetAllowPatterns(patterns []string) error {
	t.allowPatterns = make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return fmt.Errorf("invalid allow pattern %q: %w", p, err)
		}
		t.allowPatterns = append(t.allowPatterns, re)
	}
	return nil
}
