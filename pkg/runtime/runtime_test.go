package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveBaseConfigPath_PrefersXDGOverLegacy(t *testing.T) {
	home := t.TempDir()
	xdg := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)

	xdgPath := filepath.Join(xdg, "picoclaw", "config.json")
	legacyPath := filepath.Join(home, ".picoclaw", "config.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(xdgPath), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(legacyPath), 0o755))
	require.NoError(t, os.WriteFile(xdgPath, []byte(`{}`), 0o644))
	require.NoError(t, os.WriteFile(legacyPath, []byte(`{}`), 0o644))

	got := ResolveBaseConfigPath()
	assert.Equal(t, xdgPath, got)
}

func TestResolveBaseConfigPath_FallsBackToLegacyWhenXDGMissing(t *testing.T) {
	home := t.TempDir()
	xdg := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)

	legacyPath := filepath.Join(home, ".picoclaw", "config.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(legacyPath), 0o755))
	require.NoError(t, os.WriteFile(legacyPath, []byte(`{}`), 0o644))

	got := ResolveBaseConfigPath()
	assert.Equal(t, legacyPath, got)
}

func TestLoadResolvedConfig_AppliesOverlayAndKeepsBaseValues(t *testing.T) {
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
	assert.Equal(t, "base-key", cfg.Providers.OpenAI.APIKey)
	assert.True(t, cfg.Agents.Defaults.RestrictToSandbox)
}

func TestEnsureMinProviderTimeout_SetsFloor(t *testing.T) {
	dir := t.TempDir()
	basePath := filepath.Join(dir, "base.json")
	require.NoError(t, os.WriteFile(basePath, []byte(`{"providers":{"openai":{"timeout":0}}}`), 0o644))

	cfg, err := LoadResolvedConfig(LoadConfigOptions{
		BaseConfigPath:     basePath,
		MinProviderTimeout: 180 * time.Second,
	})
	require.NoError(t, err)
	assert.Equal(t, 180, cfg.Providers.OpenAI.Timeout)
}

func TestStartOutbound_DropAndConsumeDoNotBlockPublishers(t *testing.T) {
	modes := []OutboundMode{OutboundModeDrop, OutboundModeConsume}
	for _, mode := range modes {
		t.Run(string(mode), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
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
	ctx, cancel := context.WithCancel(context.Background())
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
		assert.Equal(t, "hello", got.Content)
	case <-time.After(2 * time.Second):
		t.Fatal("did not receive callback outbound message")
	}
}
