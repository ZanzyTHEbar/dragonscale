package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg"
	"github.com/ZanzyTHEbar/dragonscale/pkg/config"
	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	"github.com/ZanzyTHEbar/dragonscale/pkg/itr"
	"github.com/ZanzyTHEbar/dragonscale/pkg/logger"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory/delegate"
	"github.com/ZanzyTHEbar/dragonscale/pkg/security"
	"github.com/ZanzyTHEbar/dragonscale/pkg/security/securebus"
	"github.com/ZanzyTHEbar/dragonscale/pkg/skills"
	"github.com/ZanzyTHEbar/dragonscale/pkg/tools"
)

type daemonAuditWriter interface {
	InsertAuditEntry(ctx context.Context, entry *memory.AuditEntry) error
}

type daemonSecureBusAuditSink struct {
	writer daemonAuditWriter
}

func newDaemonSecureBusAuditSink(writer daemonAuditWriter) securebus.AuditSink {
	if writer == nil {
		return nil
	}
	return &daemonSecureBusAuditSink{writer: writer}
}

func daemonShutdownSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}

func (s *daemonSecureBusAuditSink) Write(event securebus.AuditEvent) error {
	if s == nil || s.writer == nil {
		return nil
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	target := strings.TrimSpace(event.ToolName)
	if target == "" {
		target = strings.TrimSpace(event.CommandType)
	}
	entry := &memory.AuditEntry{
		ID:         ids.New(),
		AgentID:    pkg.NAME,
		SessionKey: strings.TrimSpace(event.SessionKey),
		Action:     daemonSecureBusAuditAction(event),
		Target:     target,
		ToolCallID: strings.TrimSpace(event.ToolCallID),
		Input:      strings.TrimSpace(event.ToolInput),
		Output:     string(payload),
		Success:    !event.IsError && strings.TrimSpace(event.PolicyViolation) == "",
		ErrorMsg:   daemonSecureBusAuditError(event),
		DurationMS: int(event.DurationMS),
		CreatedAt:  event.At,
	}
	return s.writer.InsertAuditEntry(context.Background(), entry)
}

func daemonSecureBusAuditAction(event securebus.AuditEvent) string {
	commandType := strings.TrimSpace(event.CommandType)
	base := "securebus_event"
	if commandType != "" {
		base = "securebus_" + commandType
	}
	if strings.TrimSpace(event.PolicyViolation) != "" {
		return base + "_policy_violation"
	}
	if event.LeakDetected {
		return base + "_leak"
	}
	if event.IsError {
		return base + "_error"
	}
	return base
}

func daemonSecureBusAuditError(event securebus.AuditEvent) string {
	if msg := strings.TrimSpace(event.PolicyViolation); msg != "" {
		return msg
	}
	return strings.TrimSpace(event.ExecutionError)
}

func newDaemonAuditDelegate(cfg *config.Config) (*delegate.LibSQLDelegate, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}
	return delegate.NewFromConfig(cfg.Memory, cfg.DBPath())
}

func (s *Service) SkillsList(ctx context.Context, out io.Writer) error {
	_ = ctx

	cfgDir, err := config.ConfigDir()
	if err != nil {
		return err
	}

	skillsPath, err := config.SkillsDir()
	if err != nil {
		return err
	}

	registry := skills.NewSkillsLoader(skillsPath, filepath.Join(cfgDir, "skills"), filepath.Join(cfgDir, "skills"))

	skillInfos := registry.ListSkills()
	if len(skillInfos) == 0 {
		fmt.Fprintln(out, "No skills installed.")
		return nil
	}
	sort.Slice(skillInfos, func(i, j int) bool {
		return skillInfos[i].Name < skillInfos[j].Name
	})

	for _, info := range skillInfos {
		content, ok := registry.LoadSkill(info.Name)
		if !ok {
			fmt.Fprintf(out, `%s: unable to load (not found)\n`, info.Name)
			continue
		}

		description := strings.TrimSpace(skillDescriptionFromMarkdownData([]byte(content)))
		if description == "" {
			description = "(no description)"
		}
		fmt.Fprintf(out, `%s\n  %s\n`, info.Name, description)
	}

	return nil
}

func (s *Service) SkillsListBuiltin(ctx context.Context, out io.Writer) error {
	_ = ctx

	if s.EmbeddedFS != nil {
		entries, err := fs.ReadDir(s.EmbeddedFS, "workspace/skills")
		if err == nil {
			if len(entries) == 0 {
				fmt.Fprintln(out, "No builtin skills found.")
				return nil
			}

			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}

				docPath := filepath.Join("workspace/skills", entry.Name(), "SKILL.md")
				description := skillDescriptionFromMarkdownFS(s.EmbeddedFS, docPath)
				fmt.Fprintf(out, `%s\n  %s\n`, entry.Name(), description)
			}
			return nil
		}
	}

	builtinDir := filepath.Join(s.getConfigPath(), "skills")
	if err := s.printBuiltinFromPath(out, builtinDir); err == nil {
		return nil
	}

	return fmt.Errorf(`no builtin skills source available`)
}

func (s *Service) printBuiltinFromPath(out io.Writer, dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("builtin skills path is not a directory: %s", dir)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		fmt.Fprintln(out, "No builtin skills found.")
		return nil
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		skillPath := filepath.Join(dir, entry.Name())
		docPath := filepath.Join(skillPath, "SKILL.md")
		description := skillDescriptionFromMarkdown(docPath)
		fmt.Fprintf(out, `%s\n  %s\n`, entry.Name(), description)
	}

	return nil
}

func (s *Service) SkillsInstall(ctx context.Context, out io.Writer, repo string) error {
	if strings.TrimSpace(repo) == "" {
		return fmt.Errorf(`repository path required`)
	}

	skillsPath, err := config.SkillsDir()
	if err != nil {
		return err
	}

	installer := skills.NewSkillInstaller(skillsPath)

	ctx2, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if err := installer.InstallFromGitHub(ctx2, repo); err != nil {
		return err
	}

	fmt.Fprintf(out, `Installed skill %s\n`, repo)
	return nil
}

func (s *Service) SkillsInstallBuiltin(ctx context.Context, out io.Writer, workspace string) error {
	_ = ctx

	if strings.TrimSpace(workspace) == "" {
		cfg, err := s.LoadConfig()
		if err != nil {
			return err
		}
		workspace = cfg.SandboxPath()
	}

	target := filepath.Join(workspace, "skills")
	if err := os.MkdirAll(target, 0o700); err != nil {
		return fmt.Errorf(`create workspace skills directory: %w`, err)
	}

	if s.EmbeddedFS != nil {
		if err := s.seedEmbeddedSkills(target); err != nil {
			return err
		}
		fmt.Fprintln(out, `Installed builtin skills into workspace skills directory.`)
		return nil
	}

	source := filepath.Join(filepath.Dir(s.getConfigPath()), "skills")
	if _, err := os.Stat(source); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf(`embedded builtin skills are unavailable`)
		}
		return err
	}

	if err := s.CopyDirectory(source, target); err != nil {
		return err
	}

	fmt.Fprintln(out, `Installed builtin skills into workspace skills directory.`)
	return nil
}

func (s *Service) SkillsRemove(ctx context.Context, out io.Writer, name string) error {
	_ = ctx

	if strings.TrimSpace(name) == "" {
		return fmt.Errorf(`skill name required`)
	}

	skillsDir, err := config.SkillsDir()
	if err != nil {
		return err
	}

	installer := skills.NewSkillInstaller(skillsDir)

	if err := installer.Uninstall(name); err != nil {
		return err
	}

	fmt.Fprintf(out, `Removed skill %s\n`, name)
	return nil
}

func (s *Service) SkillsSearch(ctx context.Context, out io.Writer) error {
	installer := skills.NewSkillInstaller("")

	ctx2, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	skillsInfo, err := installer.ListAvailableSkills(ctx2)
	if err != nil {
		return err
	}

	if len(skillsInfo) == 0 {
		fmt.Fprintln(out, "No skills available.")
		return nil
	}

	for _, skill := range skillsInfo {
		name := "unknown"
		if skill.Name != "" {
			name = skill.Name
		}
		description := ""
		if skill.Description != "" {
			description = skill.Description
		}
		fmt.Fprintf(out, `%s\n`, name)
		if description != "" {
			fmt.Fprintf(out, `  %s\n`, description)
		}
	}

	return nil
}

func (s *Service) SkillsShow(ctx context.Context, out io.Writer, name string) error {
	_ = ctx

	if strings.TrimSpace(name) == "" {
		return fmt.Errorf(`skill name required`)
	}

	skillDir, err := config.SkillsDir()
	if err != nil {
		return err
	}
	loader := skills.NewSkillsLoader(skillDir, "", "")

	content, ok := loader.LoadSkill(name)
	if !ok {
		return fmt.Errorf(`skill not found`)
	}

	fmt.Fprintf(out, `Name:   %s\n`, name)
	if content != "" {
		description := strings.TrimSpace(skillDescriptionFromMarkdownData([]byte(content)))
		if description != "" {
			fmt.Fprintf(out, `Desc:   %s\n`, description)
		}
	}
	fmt.Fprintf(out, `Content: %s\n`, content)

	return nil
}

func (s *Service) SecretInit(ctx context.Context, out io.Writer) error {
	_ = ctx

	key, err := security.GenerateKey()
	if err != nil {
		return err
	}
	encoded := fmt.Sprintf(`%x`, key)

	fmt.Fprintln(out, `Generated master key (keep this safe!):`)
	fmt.Fprintln(out, ``)
	fmt.Fprintln(out, `  `+encoded)
	fmt.Fprintln(out, ``)
	fmt.Fprintln(out, `Set it as an environment variable:`)
	fmt.Fprintln(out, `  export `+security.MasterKeyEnvVar+`=`+encoded)
	fmt.Fprintln(out, ``)
	fmt.Fprintln(out, `Or add to your shell profile (~/.bashrc, ~/.zshrc).`)
	return nil
}

func (s *Service) SecretAdd(ctx context.Context, in io.Reader, out io.Writer, name string) error {
	_ = ctx

	if strings.TrimSpace(name) == "" {
		return fmt.Errorf(`secret name required`)
	}

	store, err := s.secretStore()
	if err != nil {
		return fmt.Errorf(`error opening secret store: %w`, err)
	}

	fmt.Fprintf(out, `Secret value: `)
	raw, err := io.ReadAll(in)
	if err != nil {
		return err
	}

	value := strings.TrimSpace(string(raw))
	if value == "" {
		return fmt.Errorf(`secret value cannot be empty`)
	}

	if err := store.Set(name, []byte(value)); err != nil {
		return err
	}

	fmt.Fprintf(out, `Secret %s saved.\n`, name)
	return nil
}

func (s *Service) SecretList(ctx context.Context, out io.Writer) error {
	_ = ctx

	store, err := s.secretStore()
	if err != nil {
		return err
	}

	names := store.List()
	if len(names) == 0 {
		fmt.Fprintln(out, "No secrets found.")
		return nil
	}

	for _, name := range names {
		fmt.Fprintln(out, name)
	}

	return nil
}

func (s *Service) SecretDelete(ctx context.Context, out io.Writer, name string) error {
	_ = ctx

	if strings.TrimSpace(name) == "" {
		return fmt.Errorf(`secret name required`)
	}

	store, err := s.secretStore()
	if err != nil {
		return err
	}

	if err := store.Delete(name); err != nil {
		return err
	}

	fmt.Fprintf(out, `Deleted secret %s\n`, name)
	return nil
}

func (s *Service) DaemonStart(ctx context.Context, out io.Writer) error {
	cfgDir, err := config.ConfigDir()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		return err
	}

	socketPath := s.DaemonSocketPath()
	pidPath := s.DaemonPIDPath()
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		return err
	}
	if pidDir := filepath.Dir(pidPath); pidDir != filepath.Dir(socketPath) {
		if err := os.MkdirAll(pidDir, 0o700); err != nil {
			return err
		}
	}

	if pidData, err := os.ReadFile(pidPath); err == nil {
		pidText := strings.TrimSpace(string(pidData))
		if pidText != "" {
			pid, convErr := strconv.Atoi(pidText)
			if convErr != nil {
				fmt.Fprintf(out, `Warning: stale pid file cleaned up (invalid pid %q)\n`, pidText)
			} else if p, findErr := os.FindProcess(pid); findErr != nil {
				fmt.Fprintf(out, `Warning: stale pid file cleaned up (pid %d not found)\n`, pid)
			} else if sigErr := p.Signal(syscall.Signal(0)); sigErr != nil {
				fmt.Fprintf(out, `Warning: stale pid file cleaned up (pid %d not alive)\n`, pid)
			} else {
				if !daemonSocketAcceptsConnection(socketPath, time.Second) {
					fmt.Fprintf(out, `Warning: stale pid/socket files cleaned up (pid %d alive but daemon socket unavailable)\n`, pid)
				} else {
					fmt.Fprintf(out, `Daemon already running with PID %d\n`, pid)
					return nil
				}
			}
			// Stale pid; clean up.
			_ = os.Remove(pidPath)
			_ = os.Remove(socketPath)
		}
	}

	cfg, err := s.LoadConfig()
	if err != nil {
		return err
	}
	baseCtx := ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	auditDelegate, err := newDaemonAuditDelegate(cfg)
	if err != nil {
		fmt.Fprintf(out, "Warning: daemon audit persistence unavailable: %v\n", err)
		auditDelegate = nil
	} else if err := auditDelegate.Init(baseCtx); err != nil {
		fmt.Fprintf(out, "Warning: daemon audit persistence init failed: %v\n", err)
		_ = auditDelegate.Close()
		auditDelegate = nil
	} else {
		defer func() {
			_ = auditDelegate.Close()
		}()
	}

	svr, err := securebus.NewSocketTransportServer(socketPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = svr.Close()
	}()

	busStore, err := s.secretStore()
	if err != nil {
		fmt.Fprintf(out, `Warning: secret store unavailable: %v\n`, err)
		busStore = nil
	}

	toolRegistry := tools.NewToolRegistry()
	registerDefaultTools(s, toolRegistry, cfg)

	daemonCtx, cancel := signal.NotifyContext(baseCtx, daemonShutdownSignals()...)
	ctx = daemonCtx
	defer cancel()

	capabilityLookup := func(toolName string) (tools.ToolCapabilities, bool) {
		tool, ok := toolRegistry.Get(toolName)
		if !ok {
			return tools.ZeroCapabilities(), false
		}
		return tools.ExtractCapabilities(tool), true
	}
	executeTool := func(execCtx context.Context, name string, args map[string]interface{}) *tools.ToolResult {
		return toolRegistry.Execute(execCtx, name, args)
	}

	auditSink := newDaemonSecureBusAuditSink(auditDelegate)
	busCfg := securebus.DefaultBusConfig()
	if cfg.RestrictToSandbox() {
		busCfg.Policy.AllowedWorkspace = cfg.SandboxPath()
	}
	bus := securebus.New(busCfg, busStore, capabilityLookup, executeTool, auditSink)
	defer bus.Close()

	srvErr := make(chan error, 1)
	go func() {
		srvErr <- svr.Serve(func(reqCtx context.Context, req itr.ToolRequest) itr.ToolResponse {
			return bus.Execute(reqCtx, req)
		})
	}()

	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		return err
	}
	defer os.Remove(pidPath)

	logger.InfoF(`daemon started on socket`, map[string]interface{}{
		`socket_path`: socketPath,
	})
	fmt.Fprintln(out, `Daemon started.`)
	fmt.Fprintf(out, `  sandbox: %s\n`, cfg.SandboxPath())
	fmt.Fprintf(out, `  tools: %d registered\n`, len(toolRegistry.List()))

	select {
	case <-ctx.Done():
		logger.Info(`Daemon shutdown requested`)
	case err := <-srvErr:
		if err != nil {
			return fmt.Errorf(`daemon stopped with error: %w`, err)
		}
	}

	return nil
}

func daemonSocketAcceptsConnection(socketPath string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return false
		}
		dialTimeout := 200 * time.Millisecond
		if remaining < dialTimeout {
			dialTimeout = remaining
		}
		dialer := net.Dialer{Timeout: dialTimeout}
		conn, err := dialer.Dial("unix", socketPath)
		if err == nil {
			_ = conn.Close()
			return true
		}
		if time.Until(deadline) <= 50*time.Millisecond {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (s *Service) DaemonStop(ctx context.Context, out io.Writer) error {
	_ = ctx

	pidPath := s.DaemonPIDPath()
	data, err := os.ReadFile(pidPath)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintln(out, `Daemon is not running.`)
			return nil
		}
		return err
	}

	pidText := strings.TrimSpace(string(data))
	pid, err := strconv.Atoi(pidText)
	if err != nil {
		return fmt.Errorf(`invalid pid file content`)
	}

	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}

	if err := p.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf(`failed to stop daemon: %w`, err)
	}

	if err := os.Remove(pidPath); err != nil {
		fmt.Fprintf(out, `Could not remove pid file: %v\n`, err)
	}
	fmt.Fprintf(out, `Daemon stopped (pid %d)\n`, pid)
	return nil
}

func (s *Service) DaemonStatus(ctx context.Context, out io.Writer) error {
	_ = ctx

	pidPath := s.DaemonPIDPath()
	data, err := os.ReadFile(pidPath)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintln(out, `Daemon is not running.`)
			return nil
		}
		return err
	}

	pidText := strings.TrimSpace(string(data))
	if pidText == "" {
		fmt.Fprintln(out, `Daemon is not running.`)
		return nil
	}
	pid, err := strconv.Atoi(pidText)
	if err != nil {
		return fmt.Errorf(`invalid pid file content`)
	}

	p, err := os.FindProcess(pid)
	if err != nil {
		fmt.Fprintf(out, `Daemon is not running (stale pid %d).\n`, pid)
		return os.WriteFile(pidPath, nil, 0o600)
	}

	if err := p.Signal(syscall.Signal(0)); err != nil {
		fmt.Fprintf(out, `Daemon is not running (stale pid %d).\n`, pid)
		_ = os.Remove(pidPath)
		return nil
	}

	fmt.Fprintf(out, `Daemon running with PID %d\n`, pid)
	return nil
}

func (s *Service) MemoryDBStatus(ctx context.Context, out io.Writer) error {
	_ = ctx

	cfg, err := s.LoadConfig()
	if err != nil {
		return err
	}

	dbPath := cfg.Memory.DBPath
	if dbPath == "" {
		dbPath, err = config.DefaultDBPath()
		if err != nil {
			return err
		}
	}

	if stat, err := os.Stat(dbPath); err == nil {
		fmt.Fprintf(out, `SQLite DB exists at %s\n`, dbPath)
		fmt.Fprintf(out, `Size: %d bytes\n`, stat.Size())
		return nil
	} else if os.IsNotExist(err) {
		fmt.Fprintf(out, `SQLite DB missing at %s\n`, dbPath)
		return nil
	}

	return err
}

func (s *Service) secretStore() (*security.SecretStore, error) {
	secretPath := filepath.Join(filepath.Dir(s.getConfigPath()), `secrets.json`)
	var keyring security.KeyringProvider

	if secretKey := os.Getenv(security.MasterKeyEnvVar); secretKey != "" {
		keyring = security.NewEnvKeyring(security.MasterKeyEnvVar)
	} else {
		keyring = security.NewNoopKeyring(nil)
	}

	store, err := security.NewSecretStore(secretPath, keyring)
	if err != nil {
		return nil, err
	}

	return store, nil
}

func skillDescriptionFromMarkdownFS(fsys fs.FS, path string) string {
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return `No description`
	}
	return skillDescriptionFromMarkdownData(data)
}

func skillDescriptionFromMarkdown(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return `No description`
	}
	return skillDescriptionFromMarkdownData(data)
}

func skillDescriptionFromMarkdownData(data []byte) string {
	for _, line := range strings.Split(string(data), `\n`) {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, `description:`) {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, `description:`))
		}
		if strings.HasPrefix(trimmed, "#") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, "#"))
		}
	}
	return `No description`
}

func registerDefaultTools(service *Service, registry *tools.ToolRegistry, cfg *config.Config) {
	_ = service
	sandbox := cfg.SandboxPath()
	restrictToSandbox := cfg.RestrictToSandbox()
	registerTool(registry, tools.NewExecTool(sandbox, restrictToSandbox))
	registerTool(registry, tools.NewReadFileTool(sandbox, restrictToSandbox))
	registerTool(registry, tools.NewWriteFileTool(sandbox, restrictToSandbox))
	registerTool(registry, tools.NewListDirTool(sandbox, restrictToSandbox))
	registerTool(registry, tools.NewEditFileTool(sandbox, restrictToSandbox))
}

func registerTool(registry *tools.ToolRegistry, tool tools.Tool) {
	registry.Register(tool)
}
