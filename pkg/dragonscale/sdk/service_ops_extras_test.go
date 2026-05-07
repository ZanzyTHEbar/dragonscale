package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/internal/testcmp"
	"github.com/ZanzyTHEbar/dragonscale/pkg"
	"github.com/ZanzyTHEbar/dragonscale/pkg/config"
	dragonfantasy "github.com/ZanzyTHEbar/dragonscale/pkg/fantasy"
	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	"github.com/ZanzyTHEbar/dragonscale/pkg/itr"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory/delegate"
	memsqlc "github.com/ZanzyTHEbar/dragonscale/pkg/memory/sqlc"
	"github.com/ZanzyTHEbar/dragonscale/pkg/security/securebus"
	"github.com/ZanzyTHEbar/dragonscale/pkg/tools"
	"github.com/stretchr/testify/require"
)

type daemonStaticTool struct{}

func (t *daemonStaticTool) Name() string        { return "echo" }
func (t *daemonStaticTool) Description() string { return "echo" }
func (t *daemonStaticTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (t *daemonStaticTool) Execute(_ context.Context, _ map[string]any) *tools.ToolResult {
	return &tools.ToolResult{ForLLM: "daemon ok"}
}

type daemonCountingTool struct {
	text string
	err  bool
}

func TestDaemonShutdownSignalsIncludeStopSignal(t *testing.T) {
	t.Parallel()

	signals := daemonShutdownSignals()
	require.Contains(t, signals, os.Interrupt)
	require.Contains(t, signals, syscall.SIGTERM)
}

func (t *daemonCountingTool) Name() string        { return "echo" }
func (t *daemonCountingTool) Description() string { return "echo" }
func (t *daemonCountingTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (t *daemonCountingTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	text, _ := args["text"].(string)
	if t.text != "" {
		text = t.text
	}
	return &tools.ToolResult{ForLLM: text, IsError: t.err}
}

func TestModelsListShowsProviderFreshness(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	cachePath, err := dragonfantasy.ModelCatalogPath()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(cachePath), 0o700))

	now := time.Now().UTC()
	catalog := dragonfantasy.ModelCatalog{
		FetchedAt:  now,
		TTLSeconds: int64(time.Hour.Seconds()),
		Providers: map[string]dragonfantasy.ModelCatalogProvider{
			"fresh-provider": {
				Name:       "fresh-provider",
				FetchedAt:  now,
				TTLSeconds: int64(time.Hour.Seconds()),
				Models:     []dragonfantasy.ModelCatalogModel{{ID: "fresh-model"}},
			},
			"stale-provider": {
				Name:       "stale-provider",
				FetchedAt:  now.Add(-2 * time.Hour),
				TTLSeconds: int64(time.Hour.Seconds()),
				Models:     []dragonfantasy.ModelCatalogModel{{ID: "stale-model"}},
			},
		},
	}
	data, err := json.Marshal(catalog)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cachePath, data, 0o600))

	var out bytes.Buffer
	require.NoError(t, NewService().ModelsList(t.Context(), &out, ModelsListOptions{}))
	got := out.String()
	require.Contains(t, got, "fresh-provider (1 models, fresh, fetched")
	require.Contains(t, got, "stale-provider (1 models, stale, fetched")
	require.Contains(t, got, "fresh-model")
	require.Contains(t, got, "stale-model")
}

func newDaemonAuditBus(t *testing.T, tool tools.Tool) (*delegate.LibSQLDelegate, *securebus.Bus) {
	t.Helper()

	d, err := delegate.NewLibSQLInMemory()
	require.NoError(t, err)
	require.NoError(t, d.Init(t.Context()))
	t.Cleanup(func() { _ = d.Close() })

	capLookup := func(name string) (tools.ToolCapabilities, bool) {
		if name != tool.Name() {
			return tools.ZeroCapabilities(), false
		}
		return tools.ExtractCapabilities(tool), true
	}
	executor := func(ctx context.Context, name string, args map[string]any) *tools.ToolResult {
		if name != tool.Name() {
			return &tools.ToolResult{ForLLM: "tool not found", IsError: true}
		}
		return tool.Execute(ctx, args)
	}

	bus := securebus.New(securebus.DefaultBusConfig(), nil, capLookup, executor, newDaemonSecureBusAuditSink(d))
	t.Cleanup(bus.Close)
	return d, bus
}

func waitForDaemonAuditRow(t *testing.T, d *delegate.LibSQLDelegate, action, toolCallID string) memsqlc.AgentAuditLog {
	t.Helper()

	var matched memsqlc.AgentAuditLog
	require.Eventually(t, func() bool {
		rows, err := d.Queries().ListAuditEntriesBySession(t.Context(), memsqlc.ListAuditEntriesBySessionParams{
			AgentID:    pkg.NAME,
			SessionKey: "daemon-session",
			Lim:        8,
		})
		if err != nil {
			return false
		}
		for _, row := range rows {
			if row.Action == action && row.ToolCallID == toolCallID && row.Output != nil {
				matched = row
				return true
			}
		}
		return false
	}, 2*time.Second, 50*time.Millisecond)
	return matched
}

func waitForDaemonPIDFile(t *testing.T, pidPath string) string {
	t.Helper()

	wantPID := fmt.Sprintf("%d", os.Getpid())
	var pidText string
	require.Eventually(t, func() bool {
		pidData, err := os.ReadFile(pidPath)
		if err != nil {
			return false
		}
		pidText = strings.TrimSpace(string(pidData))
		return pidText == wantPID
	}, 5*time.Second, 50*time.Millisecond)
	return pidText
}

func TestDaemonSecureBusAuditSink_PersistsBusNativeAuditRow(t *testing.T) {
	t.Parallel()

	d, bus := newDaemonAuditBus(t, &daemonStaticTool{})

	resp := bus.Execute(t.Context(), itr.NewToolExecRequest("req-daemon-1", "daemon-session", "call-1", "echo", `{}`))
	require.False(t, resp.IsError)

	row := waitForDaemonAuditRow(t, d, "securebus_tool_exec", "call-1")
	var payload securebus.AuditEvent
	require.NoError(t, json.Unmarshal([]byte(*row.Output), &payload))
	require.True(t, row.Success)
	require.NotNil(t, row.DurationMs)
	require.GreaterOrEqual(t, *row.DurationMs, int64(0))
	require.NotNil(t, row.Input)
	testcmp.RequireEqual(t, `{}`, *row.Input)
	testcmp.RequireEqual(t, string(itr.CmdToolExec), payload.CommandType)
	testcmp.RequireEqual(t, "echo", payload.ToolName)
	testcmp.RequireEqual(t, "call-1", payload.ToolCallID)
	testcmp.RequireEqual(t, "daemon-session", payload.SessionKey)
	testcmp.RequireEqual(t, "req-daemon-1", payload.RequestID)
	testcmp.RequireEqual(t, `{}`, payload.ToolInput)
}

func TestDaemonSecureBusAuditSink_PersistsPolicyViolationAuditRow(t *testing.T) {
	t.Parallel()

	d, bus := newDaemonAuditBus(t, &daemonStaticTool{})
	req := itr.NewToolExecRequest("req-daemon-depth", "daemon-session", "call-depth", "echo", `{}`)
	req.Depth = 255

	resp := bus.Execute(t.Context(), req)
	require.True(t, resp.IsError)

	row := waitForDaemonAuditRow(t, d, "securebus_tool_exec_policy_violation", "call-depth")
	require.False(t, row.Success)
	require.True(t, strings.Contains(strings.ToLower(row.ErrorMsg), "recursion depth"))
	var payload securebus.AuditEvent
	require.NoError(t, json.Unmarshal([]byte(*row.Output), &payload))
	require.True(t, payload.IsError)
	testcmp.RequireEqual(t, "echo", payload.ToolName)
	require.NotNil(t, row.Input)
	testcmp.RequireEqual(t, `{}`, *row.Input)
	testcmp.RequireEqual(t, `{}`, payload.ToolInput)
	require.NotEmpty(t, payload.PolicyViolation)
}

func TestDaemonSecureBusAuditSink_PersistsToolErrorAuditRow(t *testing.T) {
	t.Parallel()

	d, bus := newDaemonAuditBus(t, &daemonCountingTool{err: true})
	resp := bus.Execute(t.Context(), itr.NewToolExecRequest("req-daemon-error", "daemon-session", "call-error", "echo", `{"text":"boom"}`))
	require.True(t, resp.IsError)

	row := waitForDaemonAuditRow(t, d, "securebus_tool_exec_error", "call-error")
	require.False(t, row.Success)
	require.NotNil(t, row.Input)
	testcmp.RequireEqual(t, `{"text":"boom"}`, *row.Input)
	testcmp.RequireEqual(t, "boom", row.ErrorMsg)
	var payload securebus.AuditEvent
	require.NoError(t, json.Unmarshal([]byte(*row.Output), &payload))
	require.True(t, payload.IsError)
	testcmp.RequireEqual(t, `{"text":"boom"}`, payload.ToolInput)
	testcmp.RequireEqual(t, "", payload.PolicyViolation)
	testcmp.RequireEqual(t, "boom", payload.ExecutionError)
	require.False(t, payload.LeakDetected)
}

func TestDaemonSecureBusAuditSink_PersistsLeakAuditRow(t *testing.T) {
	t.Parallel()

	d, bus := newDaemonAuditBus(t, &daemonCountingTool{text: "result: AKIAIOSFODNN7EXAMPLE"})
	resp := bus.Execute(t.Context(), itr.NewToolExecRequest("req-daemon-leak", "daemon-session", "call-leak", "echo", `{}`))
	require.False(t, resp.IsError)
	require.True(t, resp.LeakDetected)

	row := waitForDaemonAuditRow(t, d, "securebus_tool_exec_leak", "call-leak")
	require.True(t, row.Success)
	require.NotNil(t, row.Input)
	testcmp.RequireEqual(t, `{}`, *row.Input)
	var payload securebus.AuditEvent
	require.NoError(t, json.Unmarshal([]byte(*row.Output), &payload))
	testcmp.RequireEqual(t, `{}`, payload.ToolInput)
	require.True(t, payload.LeakDetected)
	require.False(t, payload.IsError)
}

func TestDaemonSecureBusAuditRows_AppearInRecentAuditEntries(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "daemon-audit.db")
	d, err := delegate.NewLibSQLDelegate(dbPath)
	require.NoError(t, err)
	require.NoError(t, d.Init(t.Context()))
	t.Cleanup(func() { _ = d.Close() })

	tool := &daemonCountingTool{err: true}
	capLookup := func(name string) (tools.ToolCapabilities, bool) {
		if name != tool.Name() {
			return tools.ZeroCapabilities(), false
		}
		return tools.ExtractCapabilities(tool), true
	}
	executor := func(ctx context.Context, name string, args map[string]any) *tools.ToolResult {
		if name != tool.Name() {
			return &tools.ToolResult{ForLLM: "tool not found", IsError: true}
		}
		return tool.Execute(ctx, args)
	}
	bus := securebus.New(securebus.DefaultBusConfig(), nil, capLookup, executor, newDaemonSecureBusAuditSink(d))
	t.Cleanup(bus.Close)

	resp := bus.Execute(t.Context(), itr.NewToolExecRequest("req-daemon-reader", "daemon-session", "call-reader", "echo", `{"text":"reader"}`))
	require.True(t, resp.IsError)

	require.Eventually(t, func() bool {
		entries, err := d.GetRecentAuditEntries(t.Context(), time.Now().UTC().Add(-time.Minute))
		if err != nil {
			return false
		}
		for _, entry := range entries {
			if entry.SessionID != "daemon-session" || entry.ToolCallID != "call-reader" {
				continue
			}
			return entry.ToolName == "echo" &&
				entry.ToolInput == `{"text":"reader"}` &&
				!entry.Success &&
				entry.ErrorMsg == "reader"
		}
		return false
	}, 2*time.Second, 50*time.Millisecond)
}

func TestNewDaemonAuditDelegate_UsesConfiguredDBPath(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "configured-daemon.db")
	cfg := config.DefaultConfig()
	cfg.Memory.DBPath = dbPath

	d, err := newDaemonAuditDelegate(cfg)
	require.NoError(t, err)
	require.NoError(t, d.Init(t.Context()))
	t.Cleanup(func() { _ = d.Close() })

	entry := &memory.AuditEntry{
		ID:         ids.New(),
		AgentID:    pkg.NAME,
		SessionKey: "daemon-session",
		Action:     "securebus_tool_exec",
		Target:     "echo",
		ToolCallID: "call-configured",
		Input:      `{}`,
		Output:     fmt.Sprintf(`{"request_id":"%s"}`, "req-configured"),
		Success:    true,
		CreatedAt:  time.Now().UTC(),
	}
	require.NoError(t, d.InsertAuditEntry(t.Context(), entry))
	rows, err := d.Queries().ListAuditEntriesBySession(t.Context(), memsqlc.ListAuditEntriesBySessionParams{
		AgentID:    pkg.NAME,
		SessionKey: "daemon-session",
		Lim:        8,
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	testcmp.RequireEqual(t, "call-configured", rows[0].ToolCallID)
}

func TestServiceDaemonStart_PersistsSocketAuditRow(t *testing.T) {
	tmpDir := t.TempDir()
	homeDir := filepath.Join(tmpDir, "home")
	xdgConfig := filepath.Join(tmpDir, "xdg-config")
	xdgData := filepath.Join(tmpDir, "xdg-data")
	require.NoError(t, os.MkdirAll(homeDir, 0o700))
	require.NoError(t, os.MkdirAll(xdgConfig, 0o700))
	require.NoError(t, os.MkdirAll(xdgData, 0o700))
	t.Setenv("HOME", homeDir)
	t.Setenv("XDG_CONFIG_HOME", xdgConfig)
	t.Setenv("XDG_DATA_HOME", xdgData)

	cfgPath, err := config.DefaultConfigPath()
	require.NoError(t, err)
	cfg := config.DefaultConfig()
	cfg.Memory.DBPath = filepath.Join(tmpDir, "daemon-socket-audit.db")
	require.NoError(t, config.SaveConfig(cfgPath, cfg))

	svc := NewService()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	started := make(chan struct{})
	errCh := make(chan error, 1)
	go func() {
		close(started)
		errCh <- svc.DaemonStart(ctx, io.Discard)
	}()
	<-started

	socketPath := svc.DaemonSocketPath()
	require.Eventually(t, func() bool {
		select {
		case err := <-errCh:
			require.NoError(t, err)
			return false
		default:
		}
		_, err := os.Stat(socketPath)
		return err == nil
	}, 5*time.Second, 50*time.Millisecond)

	client, err := securebus.NewSocketTransportClient(socketPath)
	require.NoError(t, err)
	defer client.Close()

	resp, err := client.Send(t.Context(), itr.NewToolExecRequest("req-daemon-socket", "daemon-session", "call-socket", "exec", `{"command":"echo daemon-socket"}`))
	require.NoError(t, err)
	require.False(t, resp.IsError)
	require.Contains(t, resp.Result, "daemon-socket")

	d, err := delegate.NewLibSQLDelegate(cfg.Memory.DBPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	require.Eventually(t, func() bool {
		select {
		case err := <-errCh:
			require.NoError(t, err)
			return false
		default:
		}
		rows, err := d.Queries().ListAuditEntriesBySession(t.Context(), memsqlc.ListAuditEntriesBySessionParams{
			AgentID:    pkg.NAME,
			SessionKey: "daemon-session",
			Lim:        16,
		})
		if err != nil {
			return false
		}
		for _, row := range rows {
			if row.Action != "securebus_tool_exec" || row.Target != "exec" || row.ToolCallID != "call-socket" || row.Output == nil || row.Input == nil {
				continue
			}
			var payload securebus.AuditEvent
			if err := json.Unmarshal([]byte(*row.Output), &payload); err != nil {
				return false
			}
			return row.Success &&
				*row.Input == `{"command":"echo daemon-socket"}` &&
				payload.ToolInput == `{"command":"echo daemon-socket"}` &&
				payload.ToolName == "exec" &&
				payload.ToolCallID == "call-socket" &&
				payload.RequestID == "req-daemon-socket"
		}
		return false
	}, 5*time.Second, 50*time.Millisecond)

	cancel()
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not stop after context cancellation")
	}
}

func TestServiceDaemonStart_ShutsDownAndCleansRuntimeFiles(t *testing.T) {
	tmpDir := t.TempDir()
	homeDir := filepath.Join(tmpDir, "home")
	xdgConfig := filepath.Join(tmpDir, "xdg-config")
	xdgData := filepath.Join(tmpDir, "xdg-data")
	require.NoError(t, os.MkdirAll(homeDir, 0o700))
	require.NoError(t, os.MkdirAll(xdgConfig, 0o700))
	require.NoError(t, os.MkdirAll(xdgData, 0o700))
	t.Setenv("HOME", homeDir)
	t.Setenv("XDG_CONFIG_HOME", xdgConfig)
	t.Setenv("XDG_DATA_HOME", xdgData)

	cfgPath, err := config.DefaultConfigPath()
	require.NoError(t, err)
	cfg := config.DefaultConfig()
	cfg.Memory.DBPath = filepath.Join(tmpDir, "daemon-lifecycle.db")
	require.NoError(t, config.SaveConfig(cfgPath, cfg))

	svc := NewService()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	started := make(chan struct{})
	errCh := make(chan error, 1)
	go func() {
		close(started)
		errCh <- svc.DaemonStart(ctx, io.Discard)
	}()
	<-started

	socketPath := svc.DaemonSocketPath()
	pidPath := svc.DaemonPIDPath()
	require.Eventually(t, func() bool {
		select {
		case err := <-errCh:
			require.NoError(t, err)
			return false
		default:
		}

		if _, err := os.Stat(socketPath); err != nil {
			return false
		}
		if _, err := os.Stat(pidPath); err != nil {
			return false
		}
		return true
	}, 5*time.Second, 50*time.Millisecond)

	pidText := waitForDaemonPIDFile(t, pidPath)

	var statusOut bytes.Buffer
	require.NoError(t, svc.DaemonStatus(t.Context(), &statusOut))
	require.Contains(t, statusOut.String(), "Daemon running with PID "+pidText)

	cancel()
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not stop after context cancellation")
	}

	require.NoFileExists(t, pidPath)
	require.NoFileExists(t, socketPath)
}

func TestServiceDaemonStart_CleansStalePidFile(t *testing.T) {

	tmpDir := t.TempDir()
	homeDir := filepath.Join(tmpDir, "home")
	xdgConfig := filepath.Join(tmpDir, "xdg-config")
	xdgData := filepath.Join(tmpDir, "xdg-data")
	require.NoError(t, os.MkdirAll(homeDir, 0o700))
	require.NoError(t, os.MkdirAll(xdgConfig, 0o700))
	require.NoError(t, os.MkdirAll(xdgData, 0o700))
	t.Setenv("HOME", homeDir)
	t.Setenv("XDG_CONFIG_HOME", xdgConfig)
	t.Setenv("XDG_DATA_HOME", xdgData)

	cfgPath, err := config.DefaultConfigPath()
	require.NoError(t, err)
	cfg := config.DefaultConfig()
	cfg.Memory.DBPath = filepath.Join(tmpDir, "daemon-pid-cleanup.db")
	require.NoError(t, config.SaveConfig(cfgPath, cfg))

	svc := NewService()

	// Pre-create a stale pid file with a nonexistent pid.
	pidPath := svc.DaemonPIDPath()
	require.NoError(t, os.MkdirAll(filepath.Dir(pidPath), 0o700))
	require.NoError(t, os.WriteFile(pidPath, []byte("99999\n"), 0o600))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	started := make(chan struct{})
	errCh := make(chan error, 1)
	go func() {
		close(started)
		errCh <- svc.DaemonStart(ctx, io.Discard)
	}()
	<-started

	// Daemon should start despite the stale pid file.
	socketPath := svc.DaemonSocketPath()
	require.Eventually(t, func() bool {
		select {
		case err := <-errCh:
			require.NoError(t, err)
			return false
		default:
		}
		_, socketErr := os.Stat(socketPath)
		return socketErr == nil
	}, 5*time.Second, 50*time.Millisecond)

	// Stale pid should have been replaced with the current process pid.
	waitForDaemonPIDFile(t, pidPath)

	cancel()
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not stop after context cancellation")
	}
}

func TestServiceDaemonStart_StartsWhenLivePidHasNoSocket(t *testing.T) {
	tmpDir := t.TempDir()
	homeDir := filepath.Join(tmpDir, "home")
	xdgConfig := filepath.Join(tmpDir, "xdg-config")
	xdgData := filepath.Join(tmpDir, "xdg-data")
	require.NoError(t, os.MkdirAll(homeDir, 0o700))
	require.NoError(t, os.MkdirAll(xdgConfig, 0o700))
	require.NoError(t, os.MkdirAll(xdgData, 0o700))
	t.Setenv("HOME", homeDir)
	t.Setenv("XDG_CONFIG_HOME", xdgConfig)
	t.Setenv("XDG_DATA_HOME", xdgData)

	cfgPath, err := config.DefaultConfigPath()
	require.NoError(t, err)
	cfg := config.DefaultConfig()
	cfg.Memory.DBPath = filepath.Join(tmpDir, "daemon-live-pid-no-socket.db")
	require.NoError(t, config.SaveConfig(cfgPath, cfg))

	svc := NewService()

	pidPath := svc.DaemonPIDPath()
	socketPath := svc.DaemonSocketPath()
	require.NoError(t, os.MkdirAll(filepath.Dir(pidPath), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Dir(socketPath), 0o700))
	require.NoError(t, os.WriteFile(pidPath, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o600))
	require.NoFileExists(t, socketPath)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var out bytes.Buffer
	errCh := make(chan error, 1)
	go func() {
		errCh <- svc.DaemonStart(ctx, &out)
	}()

	require.Eventually(t, func() bool {
		select {
		case err := <-errCh:
			require.NoError(t, err)
			return false
		default:
		}
		_, socketErr := os.Stat(socketPath)
		return socketErr == nil
	}, 5*time.Second, 50*time.Millisecond)

	waitForDaemonPIDFile(t, pidPath)

	cancel()
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not stop after context cancellation")
	}
	require.Contains(t, out.String(), "stale pid/socket files cleaned up")
}
