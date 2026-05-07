package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestDefaultConfig_HeartbeatEnabled verifies heartbeat is enabled by default
func TestDefaultConfig_HeartbeatEnabled(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()

	if !cfg.Heartbeat.Enabled {
		t.Error("Heartbeat should be enabled by default")
	}
}

// TestDefaultConfig_SandboxPath verifies sandbox path is resolvable
func TestDefaultConfig_SandboxPath(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	path := cfg.SandboxPath()
	if path == "" {
		t.Error("SandboxPath should not be empty")
	}
}

// TestDefaultConfig_Model verifies model is set
func TestDefaultConfig_Model(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()

	if cfg.Agents.Defaults.Model == "" {
		t.Error("Model should not be empty")
	}
}

// TestDefaultConfig_MaxTokens verifies max tokens has default value
func TestDefaultConfig_MaxTokens(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()

	if cfg.Agents.Defaults.MaxTokens == 0 {
		t.Error("MaxTokens should not be zero")
	}
}

// TestDefaultConfig_MaxToolIterations verifies max tool iterations has default value
func TestDefaultConfig_MaxToolIterations(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()

	if cfg.Agents.Defaults.MaxToolIterations == 0 {
		t.Error("MaxToolIterations should not be zero")
	}
}

func TestDefaultConfig_ContinuityRetention(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()

	if cfg.Agents.Defaults.ContinuityRetention.MinMessages <= 0 {
		t.Error("ContinuityRetention.MinMessages should be > 0")
	}
	if cfg.Agents.Defaults.ContinuityRetention.MaxMessages < cfg.Agents.Defaults.ContinuityRetention.MinMessages {
		t.Error("ContinuityRetention.MaxMessages should be >= MinMessages")
	}
	if cfg.Agents.Defaults.ContinuityRetention.TargetContextRatio <= 0 {
		t.Error("ContinuityRetention.TargetContextRatio should be > 0")
	}
	if cfg.Agents.Defaults.ContinuityRetention.FailureKeepMessages < cfg.Agents.Defaults.ContinuityRetention.MinMessages {
		t.Error("ContinuityRetention.FailureKeepMessages should be >= MinMessages")
	}
}

// TestDefaultConfig_Temperature verifies temperature has default value
func TestDefaultConfig_Temperature(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()

	if cfg.Agents.Defaults.Temperature == 0 {
		t.Error("Temperature should not be zero")
	}
}

// TestDefaultConfig_Gateway verifies gateway defaults
func TestDefaultConfig_Gateway(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()

	if cfg.Gateway.Host != "127.0.0.1" {
		t.Error("Gateway host should have default value")
	}
	if cfg.Gateway.Port == 0 {
		t.Error("Gateway port should have default value")
	}
}

// TestDefaultConfig_Providers verifies provider structure
func TestDefaultConfig_Providers(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()

	// Verify all providers are empty by default
	if cfg.Providers.Anthropic.APIKey != "" {
		t.Error("Anthropic API key should be empty by default")
	}
	if cfg.Providers.OpenAI.APIKey != "" {
		t.Error("OpenAI API key should be empty by default")
	}
	if cfg.Providers.OpenRouter.APIKey != "" {
		t.Error("OpenRouter API key should be empty by default")
	}
	if cfg.Providers.Groq.APIKey != "" {
		t.Error("Groq API key should be empty by default")
	}
	if cfg.Providers.Zhipu.APIKey != "" {
		t.Error("Zhipu API key should be empty by default")
	}
	if cfg.Providers.VLLM.APIKey != "" {
		t.Error("VLLM API key should be empty by default")
	}
	if cfg.Providers.Gemini.APIKey != "" {
		t.Error("Gemini API key should be empty by default")
	}
}

// TestDefaultConfig_Channels verifies channels are disabled by default
func TestDefaultConfig_Channels(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()

	// Verify all channels are disabled by default
	if cfg.Channels.WhatsApp.Enabled {
		t.Error("WhatsApp should be disabled by default")
	}
	if cfg.Channels.Telegram.Enabled {
		t.Error("Telegram should be disabled by default")
	}
	if cfg.Channels.Feishu.Enabled {
		t.Error("Feishu should be disabled by default")
	}
	if cfg.Channels.Discord.Enabled {
		t.Error("Discord should be disabled by default")
	}
	if cfg.Channels.MaixCam.Enabled {
		t.Error("MaixCam should be disabled by default")
	}
	if cfg.Channels.QQ.Enabled {
		t.Error("QQ should be disabled by default")
	}
	if cfg.Channels.DingTalk.Enabled {
		t.Error("DingTalk should be disabled by default")
	}
	if cfg.Channels.Slack.Enabled {
		t.Error("Slack should be disabled by default")
	}
}

// TestDefaultConfig_WebTools verifies web tools config
func TestDefaultConfig_WebTools(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()

	// Verify web tools defaults
	if cfg.Tools.Web.Brave.MaxResults != 5 {
		t.Error("Expected Brave MaxResults 5, got ", cfg.Tools.Web.Brave.MaxResults)
	}
	if cfg.Tools.Web.Brave.APIKey != "" {
		t.Error("Brave API key should be empty by default")
	}
	if cfg.Tools.Web.DuckDuckGo.MaxResults != 5 {
		t.Error("Expected DuckDuckGo MaxResults 5, got ", cfg.Tools.Web.DuckDuckGo.MaxResults)
	}
}

func TestSaveConfig_FilePermissions(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("file permission bits are not enforced on Windows")
	}

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.json")

	cfg := DefaultConfig()
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}

	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("config file has permission %04o, want 0600", perm)
	}
}

// TestConfig_Complete verifies all config fields are set
func TestConfig_Complete(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()

	// Verify complete config structure
	if cfg.SandboxPath() == "" {
		t.Error("SandboxPath should not be empty")
	}
	if cfg.Agents.Defaults.Model == "" {
		t.Error("Model should not be empty")
	}
	if cfg.Agents.Defaults.Temperature == 0 {
		t.Error("Temperature should have default value")
	}
	if cfg.Agents.Defaults.MaxTokens == 0 {
		t.Error("MaxTokens should not be zero")
	}
	if cfg.Agents.Defaults.MaxToolIterations == 0 {
		t.Error("MaxToolIterations should not be zero")
	}
	if cfg.Gateway.Host != "127.0.0.1" {
		t.Error("Gateway host should have default value")
	}
	if cfg.Gateway.Port == 0 {
		t.Error("Gateway port should have default value")
	}
	if !cfg.Heartbeat.Enabled {
		t.Error("Heartbeat should be enabled by default")
	}
}

func TestDefaultConfig_OpenAIWebSearchEnabled(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	if !cfg.Providers.OpenAI.WebSearch {
		t.Fatal("DefaultConfig().Providers.OpenAI.WebSearch should be true")
	}
}

func TestLoadConfig_OpenAIWebSearchDefaultsTrueWhenUnset(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"providers":{"openai":{"api_base":""}}}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	if !cfg.Providers.OpenAI.WebSearch {
		t.Fatal("OpenAI codex web search should remain true when unset in config file")
	}
}

func TestValidate_MemoryConfig(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		mutate   func(*Config)
		wantWarn string
	}{
		{
			name: "sync_url without auth_token",
			mutate: func(c *Config) {
				c.Memory.Sync.SyncURL = "libsql://test.turso.io"
			},
			wantWarn: "auth_token is empty",
		},
		{
			name: "invalid embedding dims",
			mutate: func(c *Config) {
				c.Memory.EmbeddingDims = 10
			},
			wantWarn: "expected 64-4096",
		},
		{
			name: "unknown embedding provider",
			mutate: func(c *Config) {
				c.Memory.Embedding.Provider = "nonexistent"
			},
			wantWarn: "unknown",
		},
		{
			name: "openai without key",
			mutate: func(c *Config) {
				c.Memory.Embedding.Provider = "openai"
			},
			wantWarn: "no API key found",
		},
		{
			name: "valid openai with fallback key",
			mutate: func(c *Config) {
				c.Memory.Embedding.Provider = "openai"
				c.Providers.OpenAI.APIKey = "sk-test"
			},
			wantWarn: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			tt.mutate(cfg)
			warnings := cfg.Validate()

			if tt.wantWarn == "" {
				for _, w := range warnings {
					if containsMemoryWarning(w) {
						t.Errorf("expected no memory warnings, got: %s", w)
					}
				}
				return
			}

			found := false
			for _, w := range warnings {
				if strings.Contains(w, tt.wantWarn) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected warning containing %q, got: %v", tt.wantWarn, warnings)
			}
		})
	}
}

func TestValidate_ContinuityRetentionConfig(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	cfg.Agents.Defaults.ContinuityRetention.MinMessages = 8
	cfg.Agents.Defaults.ContinuityRetention.MaxMessages = 4
	cfg.Agents.Defaults.ContinuityRetention.TargetContextRatio = 0
	cfg.Agents.Defaults.ContinuityRetention.FailureKeepMessages = 0

	warnings := cfg.Validate()
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "continuity_retention.min_messages") {
		t.Fatalf("expected continuity retention min/max warning, got: %v", warnings)
	}
	if !strings.Contains(joined, "continuity_retention.target_context_ratio") {
		t.Fatalf("expected continuity retention ratio warning, got: %v", warnings)
	}
	if !strings.Contains(joined, "continuity_retention.failure_keep_messages") {
		t.Fatalf("expected continuity retention failure keep warning, got: %v", warnings)
	}
}

func containsMemoryWarning(s string) bool {
	return strings.Contains(s, "memory.")
}

func TestLoadConfig_OpenAIWebSearchCanBeDisabled(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"providers":{"openai":{"web_search":false}}}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	if cfg.Providers.OpenAI.WebSearch {
		t.Fatal("OpenAI codex web search should be false when disabled in config file")
	}
}

func TestLoadConfig_GatewayHostEnvOverride(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"gateway":{"host":"0.0.0.0"}}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	t.Setenv("DRAGONSCALE_GATEWAY_HOST", "0.0.0.0")

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	if cfg.Gateway.Host != "0.0.0.0" {
		t.Fatalf("expected env override host 0.0.0.0, got %q", cfg.Gateway.Host)
	}
}

func TestLoadConfig_GatewayHostEnvOverrideWithoutConfigFile(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "missing.json")
	t.Setenv("DRAGONSCALE_GATEWAY_HOST", "0.0.0.0")

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	if cfg.Gateway.Host != "0.0.0.0" {
		t.Fatalf("expected env-only override host 0.0.0.0, got %q", cfg.Gateway.Host)
	}
}

func TestLoadConfig_ProviderEnvOverride(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"providers":{"openai":{"api_key":"file-key","timeout":15}}}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	t.Setenv("DRAGONSCALE_PROVIDERS_OPENAI_API_KEY", "env-key")
	t.Setenv("DRAGONSCALE_PROVIDERS_OPENAI_API_BASE", "https://example.invalid/v1")
	t.Setenv("DRAGONSCALE_PROVIDERS_OPENAI_TIMEOUT", "45")

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	if cfg.Providers.OpenAI.APIKey != "env-key" {
		t.Fatalf("expected env override api key, got %q", cfg.Providers.OpenAI.APIKey)
	}
	if cfg.Providers.OpenAI.APIBase != "https://example.invalid/v1" {
		t.Fatalf("expected env override api base, got %q", cfg.Providers.OpenAI.APIBase)
	}
	if cfg.Providers.OpenAI.Timeout != 45 {
		t.Fatalf("expected env override timeout 45, got %d", cfg.Providers.OpenAI.Timeout)
	}
}

func TestLoadConfig_OpenAIWebSearchEnvOverride(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"providers":{"openai":{"web_search":false}}}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	t.Setenv("DRAGONSCALE_PROVIDERS_OPENAI_WEB_SEARCH", "true")

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	if !cfg.Providers.OpenAI.WebSearch {
		t.Fatal("expected env override to re-enable OpenAI web_search")
	}
}

func TestLoadConfig_OpenAIResponsesWebSocketEnvOverride(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"providers":{"openai":{"responses_websocket":false}}}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	t.Setenv("DRAGONSCALE_PROVIDERS_OPENAI_RESPONSES_WEBSOCKET", "true")

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	if !cfg.Providers.OpenAI.ResponsesWebSocket {
		t.Fatal("expected env override to re-enable OpenAI responses_websocket")
	}
}

func TestConfiguredProviderInfosIncludesSource(t *testing.T) {
	t.Setenv("DRAGONSCALE_PROVIDERS_OPENAI_API_KEY", "env-key")
	cfg := DefaultConfig()
	cfg.Providers.OpenAI.APIKey = "env-key"
	cfg.Providers.OpenCode.APIKey = "config-key"

	infos := cfg.Providers.ConfiguredProviderInfos()
	sources := map[string]string{}
	for _, info := range infos {
		sources[info.Name] = info.Source
	}
	if sources["openai"] != "env" {
		t.Fatalf("openai source = %q", sources["openai"])
	}
	if sources["opencode-go"] != "config" || sources["opencode-zen"] != "config" {
		t.Fatalf("opencode sources = go:%q zen:%q", sources["opencode-go"], sources["opencode-zen"])
	}
}

func TestConfiguredProviderInfosUsesGitHubCopilotEnvPrefix(t *testing.T) {
	t.Setenv("DRAGONSCALE_PROVIDERS_GITHUB_COPILOT_API_KEY", "copilot-key")
	cfg := DefaultConfig()
	cfg.Providers.GitHubCopilot.APIKey = "copilot-key"

	infos := cfg.Providers.ConfiguredProviderInfos()
	for _, info := range infos {
		if info.Name == "github_copilot" {
			if info.Source != "env" {
				t.Fatalf("github_copilot source = %q", info.Source)
			}
			return
		}
	}
	t.Fatal("github_copilot provider not found")
}

func TestConfiguredProviderInfosKeyProviderSourceIgnoresAPIBaseEnv(t *testing.T) {
	t.Setenv("DRAGONSCALE_PROVIDERS_OPENAI_API_BASE", "https://proxy.example/v1")
	cfg := DefaultConfig()
	cfg.Providers.OpenAI.APIKey = "config-key"
	cfg.Providers.OpenAI.APIBase = "https://proxy.example/v1"

	infos := cfg.Providers.ConfiguredProviderInfos()
	for _, info := range infos {
		if info.Name == "openai" {
			if info.Source != "config" {
				t.Fatalf("openai source = %q", info.Source)
			}
			return
		}
	}
	t.Fatal("openai provider not found")
}

func TestValidate_GatewayPortZeroWarns(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	cfg.Gateway.Port = 0

	warnings := strings.Join(cfg.Validate(), "\n")
	if !strings.Contains(warnings, "gateway.port=0") {
		t.Fatalf("expected gateway port warning for zero, got: %s", warnings)
	}
}

func TestValidate_HeartbeatIntervalBelowMinimumWarns(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	cfg.Heartbeat.Interval = 1

	warnings := strings.Join(cfg.Validate(), "\n")
	if !strings.Contains(warnings, "clamped to 5 minutes") {
		t.Fatalf("expected heartbeat clamp warning, got: %s", warnings)
	}
}
