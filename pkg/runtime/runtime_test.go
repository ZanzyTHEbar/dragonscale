package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg"
	"github.com/ZanzyTHEbar/dragonscale/pkg/bus"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubKernelChecker struct {
	secure bool
	deps   bool
}

func (s stubKernelChecker) HasSecureBus() bool {
	return s.secure
}

func (s stubKernelChecker) HasUnifiedRuntimeDeps() bool {
	return s.deps
}

func TestResolveBaseConfigPath_PrefersXDGOverLegacy(t *testing.T) {
	home := t.TempDir()
	xdg := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)

	xdgPath := filepath.Join(xdg, pkg.NAME, "config.json")
	legacyPath := filepath.Join(home, ".dragonscale", "config.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(xdgPath), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(legacyPath), 0o755))
	require.NoError(t, os.WriteFile(xdgPath, []byte(`{}`), 0o644))
	require.NoError(t, os.WriteFile(legacyPath, []byte(`{}`), 0o644))

	got := ResolveBaseConfigPath()
	assert.Empty(t, cmp.Diff(xdgPath, got))
}

func TestResolveBaseConfigPath_FallsBackToXDGWhenLegacyOnlyExists(t *testing.T) {
	home := t.TempDir()
	xdg := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)

	xdgPath := filepath.Join(xdg, pkg.NAME, "config.json")
	legacyPath := filepath.Join(home, ".dragonscale", "config.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(xdgPath), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(legacyPath), 0o755))
	require.NoError(t, os.WriteFile(xdgPath, []byte(`{}`), 0o644))
	require.NoError(t, os.WriteFile(legacyPath, []byte(`{}`), 0o644))

	got := ResolveBaseConfigPath()
	assert.Empty(t, cmp.Diff(xdgPath, got))
	assert.NotEmpty(t, legacyPath)
}

func TestResolveBaseConfigPath_UsesEvalHostHomeWhenContainerConfigMissing(t *testing.T) {
	home := t.TempDir()
	xdg := t.TempDir()
	hostHome := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv(EvalHostHomeEnvVar, hostHome)

	hostXDGConfig := filepath.Join(hostHome, ".config", pkg.NAME, "config.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(hostXDGConfig), 0o755))
	require.NoError(t, os.WriteFile(hostXDGConfig, []byte(`{}`), 0o644))

	got := ResolveBaseConfigPath()
	assert.Empty(t, cmp.Diff(hostXDGConfig, got))
}

func TestResolveBaseConfigPath_PrefersEvalHostHomeOverContainerConfig(t *testing.T) {
	home := t.TempDir()
	xdg := t.TempDir()
	hostHome := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv(EvalHostHomeEnvVar, hostHome)

	containerXDG := filepath.Join(xdg, pkg.NAME, "config.json")
	hostXDG := filepath.Join(hostHome, ".config", pkg.NAME, "config.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(containerXDG), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(hostXDG), 0o755))
	require.NoError(t, os.WriteFile(containerXDG, []byte(`{}`), 0o644))
	require.NoError(t, os.WriteFile(hostXDG, []byte(`{}`), 0o644))

	got := ResolveBaseConfigPath()
	assert.Empty(t, cmp.Diff(hostXDG, got))
}

func TestResolveBaseConfigPath_DoesNotUseLegacyHostPathForHostHome(t *testing.T) {
	home := t.TempDir()
	xdg := t.TempDir()
	hostHome := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv(EvalHostHomeEnvVar, hostHome)

	containerXDG := filepath.Join(xdg, pkg.NAME, "config.json")
	hostLegacy := filepath.Join(hostHome, ".dragonscale", "config.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(containerXDG), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(hostLegacy), 0o755))
	require.NoError(t, os.WriteFile(containerXDG, []byte(`{}`), 0o644))
	require.NoError(t, os.WriteFile(hostLegacy, []byte(`{}`), 0o644))

	got := ResolveBaseConfigPath()
	assert.Empty(t, cmp.Diff(containerXDG, got))
}

func TestResolveBaseConfigPath_UsesEvalBaseConfigOverride(t *testing.T) {
	override := filepath.Join(t.TempDir(), "explicit-config.json")
	require.NoError(t, os.WriteFile(override, []byte(`{}`), 0o644))
	t.Setenv(EvalBaseConfigEnvVar, override)

	got := ResolveBaseConfigPath()
	assert.Empty(t, cmp.Diff(override, got))
}

func TestResolveBaseConfigPath_UsesEvalBaseConfigUnsetAsFallback(t *testing.T) {
	home := t.TempDir()
	xdg := t.TempDir()
	hostHome := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv(EvalHostHomeEnvVar, hostHome)

	hostXDG := filepath.Join(hostHome, ".config", pkg.NAME, "config.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(hostXDG), 0o755))
	require.NoError(t, os.WriteFile(hostXDG, []byte(`{}`), 0o644))

	t.Setenv(EvalBaseConfigEnvVar, "")
	got := ResolveBaseConfigPath()
	assert.Empty(t, cmp.Diff(hostXDG, got))
}

func TestResolveBaseConfigPath_InvalidEvalBaseConfigFallsBackToHostConfig(t *testing.T) {
	home := t.TempDir()
	xdg := t.TempDir()
	hostHome := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv(EvalHostHomeEnvVar, hostHome)

	hostXDG := filepath.Join(hostHome, ".config", pkg.NAME, "config.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(hostXDG), 0o755))
	require.NoError(t, os.WriteFile(hostXDG, []byte(`{}`), 0o644))

	t.Setenv(EvalBaseConfigEnvVar, filepath.Join(t.TempDir(), "no-such-file.json"))
	got := ResolveBaseConfigPath()
	assert.Empty(t, cmp.Diff(hostXDG, got))
}

func TestLoadResolvedConfig_FallsBackWhenExplicitBaseConfigInvalid(t *testing.T) {
	workDir := t.TempDir()
	hostDir := t.TempDir()
	home := t.TempDir()
	xdg := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv(EvalHostHomeEnvVar, hostDir)

	basePath := filepath.Join(hostDir, ".config", pkg.NAME, "config.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(basePath), 0o755))
	require.NoError(t, os.WriteFile(basePath, []byte(`{"agents":{"defaults":{"restrict_to_sandbox":true,"max_tool_iterations":20}}}`), 0o644))

	t.Setenv(EvalBaseConfigEnvVar, filepath.Join(workDir, "does-not-exist.json"))

	cfg, err := LoadResolvedConfig(LoadConfigOptions{MinProviderTimeout: 2 * time.Second})
	require.NoError(t, err)
	assert.Empty(t, cmp.Diff(true, cfg.Agents.Defaults.RestrictToSandbox))
}

func TestLoadResolvedConfig_AppliesOverlayAndKeepsBaseValues(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	basePath := filepath.Join(dir, "base.json")
	overlayPath := filepath.Join(dir, "overlay.json")

	base := []byte(`{
	  "providers": {"openai": {"api_key": "base-key"}},
	  "agents": {"defaults": {"restrict_to_sandbox": false}}
	}`)
	overlay := []byte(`{
	  "agents": {"defaults": {"restrict_to_sandbox": true}}
	}`)
	require.NoError(t, os.WriteFile(basePath, base, 0o644))
	require.NoError(t, os.WriteFile(overlayPath, overlay, 0o644))

	cfg, err := LoadResolvedConfig(LoadConfigOptions{
		BaseConfigPath:    basePath,
		OverlayConfigPath: overlayPath,
	})
	require.NoError(t, err)
	assert.Empty(t, cmp.Diff("base-key", cfg.Providers.OpenAI.APIKey))
	assert.True(t, cfg.Agents.Defaults.RestrictToSandbox)
}

func TestLoadResolvedConfig_ReappliesProviderEnvOverridesAfterOverlay(t *testing.T) {
	dir := t.TempDir()
	basePath := filepath.Join(dir, "base.json")
	overlayPath := filepath.Join(dir, "overlay.json")

	base := []byte(`{
	  "providers": {"openai": {"api_key": "base-key"}}
	}`)
	overlay := []byte(`{
	  "providers": {"openai": {"api_key": "overlay-key", "timeout": 15}}
	}`)
	require.NoError(t, os.WriteFile(basePath, base, 0o644))
	require.NoError(t, os.WriteFile(overlayPath, overlay, 0o644))
	t.Setenv("DRAGONSCALE_PROVIDERS_OPENAI_API_KEY", "env-key")
	t.Setenv("DRAGONSCALE_PROVIDERS_OPENAI_TIMEOUT", "60")

	cfg, err := LoadResolvedConfig(LoadConfigOptions{
		BaseConfigPath:    basePath,
		OverlayConfigPath: overlayPath,
	})
	require.NoError(t, err)
	assert.Empty(t, cmp.Diff("env-key", cfg.Providers.OpenAI.APIKey))
	assert.Empty(t, cmp.Diff(60, cfg.Providers.OpenAI.Timeout))
}

func TestLoadResolvedConfig_ReappliesProviderSpecificEnvOverridesAfterOverlay(t *testing.T) {
	dir := t.TempDir()
	basePath := filepath.Join(dir, "base.json")
	overlayPath := filepath.Join(dir, "overlay.json")

	base := []byte(`{
	  "providers": {"openai": {"web_search": true}}
	}`)
	overlay := []byte(`{
	  "providers": {"openai": {"web_search": false}}
	}`)
	require.NoError(t, os.WriteFile(basePath, base, 0o644))
	require.NoError(t, os.WriteFile(overlayPath, overlay, 0o644))
	t.Setenv("DRAGONSCALE_PROVIDERS_OPENAI_WEB_SEARCH", "true")

	cfg, err := LoadResolvedConfig(LoadConfigOptions{
		BaseConfigPath:    basePath,
		OverlayConfigPath: overlayPath,
	})
	require.NoError(t, err)
	assert.True(t, cfg.Providers.OpenAI.WebSearch)
}

func TestLoadEvalConfig_DefaultsToOpenRouterAlias(t *testing.T) {
	basePath := writeEvalBaseConfig(t, `{}`)
	t.Setenv(EvalBaseConfigEnvVar, basePath)
	t.Setenv(EvalConfigEnvVar, "")
	t.Setenv("OPENROUTER_API_KEY", "test-openrouter-key")

	cfg, err := LoadEvalConfig(0)
	require.NoError(t, err)
	assert.Empty(t, cmp.Diff("openrouter", cfg.Agents.Defaults.Provider))
	assert.Empty(t, cmp.Diff(evalOpenRouterModel, cfg.Agents.Defaults.Model))
	assert.Empty(t, cmp.Diff("test-openrouter-key", cfg.Providers.OpenRouter.APIKey))
	assert.Empty(t, cmp.Diff(evalOpenRouterAPIBaseURL, cfg.Providers.OpenRouter.APIBase))
}

func TestLoadEvalConfig_DefaultsToOpenAIAliasWhenNoOpenRouter(t *testing.T) {
	basePath := writeEvalBaseConfig(t, `{}`)
	t.Setenv(EvalBaseConfigEnvVar, basePath)
	t.Setenv(EvalConfigEnvVar, "")
	t.Setenv("OPENAI_API_KEY", "test-openai-key")

	cfg, err := LoadEvalConfig(0)
	require.NoError(t, err)
	assert.Empty(t, cmp.Diff("openai", cfg.Agents.Defaults.Provider))
	assert.Empty(t, cmp.Diff(evalOpenAIModel, cfg.Agents.Defaults.Model))
	assert.Empty(t, cmp.Diff("test-openai-key", cfg.Providers.OpenAI.APIKey))
	assert.Empty(t, cmp.Diff(evalOpenAIAPIBaseURL, cfg.Providers.OpenAI.APIBase))
}

func TestLoadEvalConfig_PrefersDragonScaleProviderEnvOverAlias(t *testing.T) {
	basePath := writeEvalBaseConfig(t, `{}`)
	t.Setenv(EvalBaseConfigEnvVar, basePath)
	t.Setenv(EvalConfigEnvVar, "")
	t.Setenv("DRAGONSCALE_PROVIDERS_OPENROUTER_API_KEY", "dragon-key")
	t.Setenv("OPENROUTER_API_KEY", "alias-key")

	cfg, err := LoadEvalConfig(0)
	require.NoError(t, err)
	assert.Empty(t, cmp.Diff("openrouter", cfg.Agents.Defaults.Provider))
	assert.Empty(t, cmp.Diff("dragon-key", cfg.Providers.OpenRouter.APIKey))
}

func TestLoadEvalConfig_DoesNotOverrideUsableBaseConfigProvider(t *testing.T) {
	basePath := writeEvalBaseConfig(t, `{
	  "providers": {"openai": {"api_key": "base-openai-key"}},
	  "agents": {"defaults": {"provider": "openai", "model": "gpt-4o"}}
	}`)
	t.Setenv(EvalBaseConfigEnvVar, basePath)
	t.Setenv(EvalConfigEnvVar, "")
	t.Setenv("OPENROUTER_API_KEY", "test-openrouter-key")

	cfg, err := LoadEvalConfig(0)
	require.NoError(t, err)
	assert.Empty(t, cmp.Diff("openai", cfg.Agents.Defaults.Provider))
	assert.Empty(t, cmp.Diff("gpt-4o", cfg.Agents.Defaults.Model))
	assert.Empty(t, cmp.Diff("base-openai-key", cfg.Providers.OpenAI.APIKey))
}

func TestLoadEvalConfig_OpenRouterPreferredOverOpenAI(t *testing.T) {
	basePath := writeEvalBaseConfig(t, `{}`)
	t.Setenv(EvalBaseConfigEnvVar, basePath)
	t.Setenv(EvalConfigEnvVar, "")
	t.Setenv("OPENROUTER_API_KEY", "test-openrouter-key")
	t.Setenv("OPENAI_API_KEY", "test-openai-key")

	cfg, err := LoadEvalConfig(0)
	require.NoError(t, err)
	assert.Empty(t, cmp.Diff("openrouter", cfg.Agents.Defaults.Provider))
	assert.Empty(t, cmp.Diff(evalOpenRouterModel, cfg.Agents.Defaults.Model))
	assert.Empty(t, cmp.Diff("test-openrouter-key", cfg.Providers.OpenRouter.APIKey))
	assert.Empty(t, cmp.Diff("test-openai-key", cfg.Providers.OpenAI.APIKey))
}

func TestLoadEvalConfig_RemoteAPIBaseOnlyDoesNotBlockAliasFallback(t *testing.T) {
	basePath := writeEvalBaseConfig(t, `{
	  "providers": {"zhipu": {"api_base": "https://zhipu.example/v1"}},
	  "agents": {"defaults": {"provider": "zhipu", "model": "glm-4.7"}}
	}`)
	t.Setenv(EvalBaseConfigEnvVar, basePath)
	t.Setenv(EvalConfigEnvVar, "")
	t.Setenv("OPENROUTER_API_KEY", "test-openrouter-key")

	cfg, err := LoadEvalConfig(0)
	require.NoError(t, err)
	assert.Empty(t, cmp.Diff("openrouter", cfg.Agents.Defaults.Provider))
	assert.Empty(t, cmp.Diff(evalOpenRouterModel, cfg.Agents.Defaults.Model))
	assert.Empty(t, cmp.Diff("test-openrouter-key", cfg.Providers.OpenRouter.APIKey))
}

func TestLoadEvalConfig_AgentEnvOverridesOverlayForEval(t *testing.T) {
	dir := t.TempDir()
	basePath := filepath.Join(dir, "base.json")
	overlayPath := filepath.Join(dir, "overlay.json")
	require.NoError(t, os.WriteFile(basePath, []byte(`{
	  "providers": {"openrouter": {"api_key": "base-openrouter-key"}}
	}`), 0o644))
	require.NoError(t, os.WriteFile(overlayPath, []byte(`{
	  "agents": {"defaults": {"provider": "openai", "model": "gpt-4o"}}
	}`), 0o644))
	t.Setenv(EvalBaseConfigEnvVar, basePath)
	t.Setenv(EvalConfigEnvVar, overlayPath)
	t.Setenv("DRAGONSCALE_AGENTS_DEFAULTS_PROVIDER", "openrouter")
	t.Setenv("DRAGONSCALE_AGENTS_DEFAULTS_MODEL", "openai/gpt-4o-mini")

	cfg, err := LoadEvalConfig(0)
	require.NoError(t, err)
	assert.Empty(t, cmp.Diff("openrouter", cfg.Agents.Defaults.Provider))
	assert.Empty(t, cmp.Diff("openai/gpt-4o-mini", cfg.Agents.Defaults.Model))
}

func writeEvalBaseConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func TestEnsureMinProviderTimeout_SetsFloor(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	basePath := filepath.Join(dir, "base.json")
	require.NoError(t, os.WriteFile(basePath, []byte(`{"providers":{"openai":{"timeout":0}}}`), 0o644))

	cfg, err := LoadResolvedConfig(LoadConfigOptions{
		BaseConfigPath:     basePath,
		MinProviderTimeout: 180 * time.Second,
	})
	require.NoError(t, err)
	assert.Empty(t, cmp.Diff(180, cfg.Providers.OpenAI.Timeout))
}

func TestStartOutbound_DropAndConsumeDoNotBlockPublishers(t *testing.T) {
	t.Parallel()
	modes := []OutboundMode{OutboundModeDrop, OutboundModeConsume}
	for _, mode := range modes {
		t.Run(string(mode), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			msgBus := bus.NewMessageBus()
			done := startOutbound(msgBus, ctx, BootstrapOptions{OutboundMode: mode})
			require.NotNil(t, done)

			publishDone := make(chan struct{})
			go func() {
				defer close(publishDone)
				for i := 0; i < 300; i++ {
					msgBus.PublishOutbound(bus.OutboundMessage{
						Channel: "test",
						ChatID:  "test",
						Content: "payload",
					})
				}
			}()

			select {
			case <-publishDone:
			case <-time.After(2 * time.Second):
				t.Fatalf("publishing outbound messages blocked under mode=%s", mode)
			}
		})
	}
}

func TestStartOutbound_CallbackReceivesMessages(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	msgBus := bus.NewMessageBus()
	received := make(chan bus.OutboundMessage, 1)
	done := startOutbound(msgBus, ctx, BootstrapOptions{
		OutboundMode: OutboundModeCallback,
		OutboundCallback: func(msg bus.OutboundMessage) {
			select {
			case received <- msg:
			default:
			}
		},
	})
	require.NotNil(t, done)

	msgBus.PublishOutbound(bus.OutboundMessage{
		Channel: "cli",
		ChatID:  "direct",
		Content: "hello",
	})

	select {
	case got := <-received:
		assert.Empty(t, cmp.Diff("hello", got.Content))
	case <-time.After(2 * time.Second):
		t.Fatal("did not receive callback outbound message")
	}
}

func TestValidateKernelInvariants(t *testing.T) {
	t.Parallel()
	t.Run("nil loop", func(t *testing.T) {
		err := validateKernelInvariants(nil)
		require.Error(t, err)
		assert.ErrorContains(t, err, "agent loop is nil")
	})

	t.Run("secure bus missing", func(t *testing.T) {
		err := validateKernelInvariants(stubKernelChecker{secure: false, deps: true})
		require.Error(t, err)
		assert.ErrorContains(t, err, "secure bus is not configured")
	})

	t.Run("unified deps missing", func(t *testing.T) {
		err := validateKernelInvariants(stubKernelChecker{secure: true, deps: false})
		require.Error(t, err)
		assert.ErrorContains(t, err, "unified runtime dependencies are missing")
	})

	t.Run("all invariants satisfied", func(t *testing.T) {
		err := validateKernelInvariants(stubKernelChecker{secure: true, deps: true})
		require.NoError(t, err)
	})
}
