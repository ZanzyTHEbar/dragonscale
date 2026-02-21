package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/ZanzyTHEbar/dragonscale/pkg"
	"github.com/caarlos0/env/v11"
	jsonv2 "github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"
)

// FlexibleStringSlice is a []string that also accepts JSON numbers,
// so allow_from can contain both "123" and 123.
type FlexibleStringSlice []string

func (f *FlexibleStringSlice) UnmarshalJSON(data []byte) error {
	// Try []string first
	var ss []string
	if err := jsonv2.Unmarshal(data, &ss); err == nil {
		*f = ss
		return nil
	}

	// Try []interface{} to handle mixed types
	var raw []interface{}
	if err := jsonv2.Unmarshal(data, &raw); err != nil {
		return err
	}

	result := make([]string, 0, len(raw))
	for _, v := range raw {
		switch val := v.(type) {
		case string:
			result = append(result, val)
		case float64:
			result = append(result, fmt.Sprintf("%.0f", val))
		default:
			result = append(result, fmt.Sprintf("%v", val))
		}
	}
	*f = result
	return nil
}

type Config struct {
	Agents    AgentsConfig    `json:"agents"`
	Channels  ChannelsConfig  `json:"channels"`
	Providers ProvidersConfig `json:"providers"`
	Gateway   GatewayConfig   `json:"gateway"`
	Tools     ToolsConfig     `json:"tools"`
	Memory    MemoryConfig    `json:"memory"`
	Heartbeat HeartbeatConfig `json:"heartbeat"`
	Devices   DevicesConfig   `json:"devices"`
	mu        sync.RWMutex
}

// MemoryConfig configures the 3-tier MemGPT memory system.
// Memory is always enabled; there is no opt-out. Configuration controls
// the database path, embedding dimensions, offloading threshold, and sync.
type MemoryConfig struct {
	// DBPath overrides the default database path (workspace/memory/dragonscale.db).
	// Empty string uses the default.
	DBPath string `json:"db_path" env:"DRAGONSCALE_MEMORY_DB_PATH"`

	// EmbeddingDims is the vector dimensionality for archival embeddings.
	// Default: 768 (sentence-transformers). Use 1536 for OpenAI ada-002, 384 for MiniLM.
	EmbeddingDims int `json:"embedding_dims" env:"DRAGONSCALE_MEMORY_EMBEDDING_DIMS"`

	// OffloadThresholdTokens is the token count above which tool results
	// are automatically offloaded to archival memory. Default: 4000.
	OffloadThresholdTokens int `json:"offload_threshold_tokens" env:"DRAGONSCALE_MEMORY_OFFLOAD_THRESHOLD_TOKENS"`

	// Embedding configures the embedding provider for archival vector search.
	Embedding EmbeddingConfig `json:"embedding"`

	// Sync configures Turso embedded replica sync. When SyncURL is set,
	// the local DB acts as an embedded replica that syncs with the remote primary.
	Sync MemorySyncConfig `json:"sync"`
}

// EmbeddingConfig selects which embedding provider to use for archival memory.
type EmbeddingConfig struct {
	// Provider selects the embedding backend: "ollama", "openai", or "".
	// Empty string disables embeddings (FTS5-only search).
	Provider string `json:"provider" env:"DRAGONSCALE_MEMORY_EMBEDDING_PROVIDER"`

	// Model is the embedding model name (e.g., "nomic-embed-text", "text-embedding-3-small").
	// Defaults depend on provider: "nomic-embed-text" for Ollama, "text-embedding-3-small" for OpenAI.
	Model string `json:"model" env:"DRAGONSCALE_MEMORY_EMBEDDING_MODEL"`

	// APIBase overrides the provider's API base URL.
	// For Ollama defaults to "http://localhost:11434".
	// For OpenAI defaults to "https://api.openai.com/v1".
	// Empty string uses the default for the selected provider.
	APIBase string `json:"api_base" env:"DRAGONSCALE_MEMORY_EMBEDDING_API_BASE"`

	// APIKey for the embedding provider. Required for OpenAI, optional for Ollama.
	// If empty, falls back to the matching provider's key from providers config.
	APIKey string `json:"api_key" env:"DRAGONSCALE_MEMORY_EMBEDDING_API_KEY"`
}

// MemorySyncConfig configures Turso embedded replica synchronization.
// When SyncURL is empty, the database operates in local-only mode.
type MemorySyncConfig struct {
	// SyncURL is the Turso primary database URL (e.g., "libsql://mydb.turso.io").
	// Empty string disables replication (local-only mode).
	SyncURL string `json:"sync_url" env:"DRAGONSCALE_MEMORY_SYNC_URL"`

	// AuthToken is the Turso authentication token for the remote database.
	AuthToken string `json:"auth_token" env:"DRAGONSCALE_MEMORY_SYNC_AUTH_TOKEN"`

	// SyncIntervalSeconds is how often to sync with the remote primary (in seconds).
	// Zero means manual sync only. Default: 60.
	SyncIntervalSeconds int `json:"sync_interval_seconds" env:"DRAGONSCALE_MEMORY_SYNC_INTERVAL_SECONDS"`

	// EncryptionKey enables encryption-at-rest on the local database file.
	// Empty string means no encryption.
	EncryptionKey string `json:"encryption_key" env:"DRAGONSCALE_MEMORY_SYNC_ENCRYPTION_KEY"`
}

type AgentsConfig struct {
	Defaults AgentDefaults `json:"defaults"`
}

type ContinuityRetentionConfig struct {
	// MinMessages is the minimum number of recent messages always retained
	// unsummarized for conversational continuity.
	MinMessages int `json:"min_messages" env:"DRAGONSCALE_AGENTS_DEFAULTS_CONTINUITY_RETENTION_MIN_MESSAGES"`

	// MaxMessages is the upper bound on retained recent messages, even when
	// the token budget would allow more.
	MaxMessages int `json:"max_messages" env:"DRAGONSCALE_AGENTS_DEFAULTS_CONTINUITY_RETENTION_MAX_MESSAGES"`

	// TargetContextRatio is the target fraction of model context window reserved
	// for retained recent messages.
	TargetContextRatio float64 `json:"target_context_ratio" env:"DRAGONSCALE_AGENTS_DEFAULTS_CONTINUITY_RETENTION_TARGET_CONTEXT_RATIO"`

	// FailureKeepMessages is the fallback retained-message count used when
	// summarization repeatedly fails.
	FailureKeepMessages int `json:"failure_keep_messages" env:"DRAGONSCALE_AGENTS_DEFAULTS_CONTINUITY_RETENTION_FAILURE_KEEP_MESSAGES"`
}

type AgentDefaults struct {
	// Sandbox is the directory for agent file operations (tools sandbox).
	// Defaults to $XDG_DATA_HOME/dragonscale/sandbox when empty.
	Sandbox             string                    `json:"sandbox" env:"DRAGONSCALE_AGENTS_DEFAULTS_SANDBOX"`
	RestrictToSandbox   bool                      `json:"restrict_to_sandbox" env:"DRAGONSCALE_AGENTS_DEFAULTS_RESTRICT_TO_SANDBOX"`
	Provider            string                    `json:"provider" env:"DRAGONSCALE_AGENTS_DEFAULTS_PROVIDER"`
	Model               string                    `json:"model" env:"DRAGONSCALE_AGENTS_DEFAULTS_MODEL"`
	MaxTokens           int                       `json:"max_tokens" env:"DRAGONSCALE_AGENTS_DEFAULTS_MAX_TOKENS"`
	Temperature         float64                   `json:"temperature" env:"DRAGONSCALE_AGENTS_DEFAULTS_TEMPERATURE"`
	MaxToolIterations   int                       `json:"max_tool_iterations" env:"DRAGONSCALE_AGENTS_DEFAULTS_MAX_TOOL_ITERATIONS"`
	ContinuityRetention ContinuityRetentionConfig `json:"continuity_retention"`

	// Deprecated: Use Sandbox instead. Kept for backward compatibility during migration.
	Workspace           string `json:"workspace,omitempty" env:"DRAGONSCALE_AGENTS_DEFAULTS_WORKSPACE"`
	RestrictToWorkspace bool   `json:"restrict_to_workspace,omitempty" env:"DRAGONSCALE_AGENTS_DEFAULTS_RESTRICT_TO_WORKSPACE"`
}

type ChannelsConfig struct {
	WhatsApp WhatsAppConfig `json:"whatsapp"`
	Telegram TelegramConfig `json:"telegram"`
	Feishu   FeishuConfig   `json:"feishu"`
	Discord  DiscordConfig  `json:"discord"`
	MaixCam  MaixCamConfig  `json:"maixcam"`
	QQ       QQConfig       `json:"qq"`
	DingTalk DingTalkConfig `json:"dingtalk"`
	Slack    SlackConfig    `json:"slack"`
	LINE     LINEConfig     `json:"line"`
	OneBot   OneBotConfig   `json:"onebot"`
}

type WhatsAppConfig struct {
	Enabled   bool                `json:"enabled" env:"DRAGONSCALE_CHANNELS_WHATSAPP_ENABLED"`
	BridgeURL string              `json:"bridge_url" env:"DRAGONSCALE_CHANNELS_WHATSAPP_BRIDGE_URL"`
	AllowFrom FlexibleStringSlice `json:"allow_from" env:"DRAGONSCALE_CHANNELS_WHATSAPP_ALLOW_FROM"`
}

type TelegramConfig struct {
	Enabled   bool                `json:"enabled" env:"DRAGONSCALE_CHANNELS_TELEGRAM_ENABLED"`
	Token     string              `json:"token" env:"DRAGONSCALE_CHANNELS_TELEGRAM_TOKEN"`
	Proxy     string              `json:"proxy" env:"DRAGONSCALE_CHANNELS_TELEGRAM_PROXY"`
	AllowFrom FlexibleStringSlice `json:"allow_from" env:"DRAGONSCALE_CHANNELS_TELEGRAM_ALLOW_FROM"`
}

type FeishuConfig struct {
	Enabled           bool                `json:"enabled" env:"DRAGONSCALE_CHANNELS_FEISHU_ENABLED"`
	AppID             string              `json:"app_id" env:"DRAGONSCALE_CHANNELS_FEISHU_APP_ID"`
	AppSecret         string              `json:"app_secret" env:"DRAGONSCALE_CHANNELS_FEISHU_APP_SECRET"`
	EncryptKey        string              `json:"encrypt_key" env:"DRAGONSCALE_CHANNELS_FEISHU_ENCRYPT_KEY"`
	VerificationToken string              `json:"verification_token" env:"DRAGONSCALE_CHANNELS_FEISHU_VERIFICATION_TOKEN"`
	AllowFrom         FlexibleStringSlice `json:"allow_from" env:"DRAGONSCALE_CHANNELS_FEISHU_ALLOW_FROM"`
}

type DiscordConfig struct {
	Enabled   bool                `json:"enabled" env:"DRAGONSCALE_CHANNELS_DISCORD_ENABLED"`
	Token     string              `json:"token" env:"DRAGONSCALE_CHANNELS_DISCORD_TOKEN"`
	AllowFrom FlexibleStringSlice `json:"allow_from" env:"DRAGONSCALE_CHANNELS_DISCORD_ALLOW_FROM"`
}

type MaixCamConfig struct {
	Enabled   bool                `json:"enabled" env:"DRAGONSCALE_CHANNELS_MAIXCAM_ENABLED"`
	Host      string              `json:"host" env:"DRAGONSCALE_CHANNELS_MAIXCAM_HOST"`
	Port      int                 `json:"port" env:"DRAGONSCALE_CHANNELS_MAIXCAM_PORT"`
	AllowFrom FlexibleStringSlice `json:"allow_from" env:"DRAGONSCALE_CHANNELS_MAIXCAM_ALLOW_FROM"`
}

type QQConfig struct {
	Enabled   bool                `json:"enabled" env:"DRAGONSCALE_CHANNELS_QQ_ENABLED"`
	AppID     string              `json:"app_id" env:"DRAGONSCALE_CHANNELS_QQ_APP_ID"`
	AppSecret string              `json:"app_secret" env:"DRAGONSCALE_CHANNELS_QQ_APP_SECRET"`
	AllowFrom FlexibleStringSlice `json:"allow_from" env:"DRAGONSCALE_CHANNELS_QQ_ALLOW_FROM"`
}

type DingTalkConfig struct {
	Enabled      bool                `json:"enabled" env:"DRAGONSCALE_CHANNELS_DINGTALK_ENABLED"`
	ClientID     string              `json:"client_id" env:"DRAGONSCALE_CHANNELS_DINGTALK_CLIENT_ID"`
	ClientSecret string              `json:"client_secret" env:"DRAGONSCALE_CHANNELS_DINGTALK_CLIENT_SECRET"`
	AllowFrom    FlexibleStringSlice `json:"allow_from" env:"DRAGONSCALE_CHANNELS_DINGTALK_ALLOW_FROM"`
}

type SlackConfig struct {
	Enabled   bool                `json:"enabled" env:"DRAGONSCALE_CHANNELS_SLACK_ENABLED"`
	BotToken  string              `json:"bot_token" env:"DRAGONSCALE_CHANNELS_SLACK_BOT_TOKEN"`
	AppToken  string              `json:"app_token" env:"DRAGONSCALE_CHANNELS_SLACK_APP_TOKEN"`
	AllowFrom FlexibleStringSlice `json:"allow_from" env:"DRAGONSCALE_CHANNELS_SLACK_ALLOW_FROM"`
}

type LINEConfig struct {
	Enabled            bool                `json:"enabled" env:"DRAGONSCALE_CHANNELS_LINE_ENABLED"`
	ChannelSecret      string              `json:"channel_secret" env:"DRAGONSCALE_CHANNELS_LINE_CHANNEL_SECRET"`
	ChannelAccessToken string              `json:"channel_access_token" env:"DRAGONSCALE_CHANNELS_LINE_CHANNEL_ACCESS_TOKEN"`
	WebhookHost        string              `json:"webhook_host" env:"DRAGONSCALE_CHANNELS_LINE_WEBHOOK_HOST"`
	WebhookPort        int                 `json:"webhook_port" env:"DRAGONSCALE_CHANNELS_LINE_WEBHOOK_PORT"`
	WebhookPath        string              `json:"webhook_path" env:"DRAGONSCALE_CHANNELS_LINE_WEBHOOK_PATH"`
	AllowFrom          FlexibleStringSlice `json:"allow_from" env:"DRAGONSCALE_CHANNELS_LINE_ALLOW_FROM"`
}

type OneBotConfig struct {
	Enabled            bool                `json:"enabled" env:"DRAGONSCALE_CHANNELS_ONEBOT_ENABLED"`
	WSUrl              string              `json:"ws_url" env:"DRAGONSCALE_CHANNELS_ONEBOT_WS_URL"`
	AccessToken        string              `json:"access_token" env:"DRAGONSCALE_CHANNELS_ONEBOT_ACCESS_TOKEN"`
	ReconnectInterval  int                 `json:"reconnect_interval" env:"DRAGONSCALE_CHANNELS_ONEBOT_RECONNECT_INTERVAL"`
	GroupTriggerPrefix []string            `json:"group_trigger_prefix" env:"DRAGONSCALE_CHANNELS_ONEBOT_GROUP_TRIGGER_PREFIX"`
	AllowFrom          FlexibleStringSlice `json:"allow_from" env:"DRAGONSCALE_CHANNELS_ONEBOT_ALLOW_FROM"`
}

type HeartbeatConfig struct {
	Enabled  bool `json:"enabled" env:"DRAGONSCALE_HEARTBEAT_ENABLED"`
	Interval int  `json:"interval" env:"DRAGONSCALE_HEARTBEAT_INTERVAL"` // minutes, min 5
}

type DevicesConfig struct {
	Enabled    bool `json:"enabled" env:"DRAGONSCALE_DEVICES_ENABLED"`
	MonitorUSB bool `json:"monitor_usb" env:"DRAGONSCALE_DEVICES_MONITOR_USB"`
}

type ProvidersConfig struct {
	Anthropic     ProviderConfig       `json:"anthropic"`
	OpenAI        OpenAIProviderConfig `json:"openai"`
	OpenRouter    ProviderConfig       `json:"openrouter"`
	Groq          ProviderConfig       `json:"groq"`
	Zhipu         ProviderConfig       `json:"zhipu"`
	VLLM          ProviderConfig       `json:"vllm"`
	Gemini        ProviderConfig       `json:"gemini"`
	Nvidia        ProviderConfig       `json:"nvidia"`
	Ollama        ProviderConfig       `json:"ollama"`
	Moonshot      ProviderConfig       `json:"moonshot"`
	ShengSuanYun  ProviderConfig       `json:"shengsuanyun"`
	DeepSeek      ProviderConfig       `json:"deepseek"`
	GitHubCopilot ProviderConfig       `json:"github_copilot"`
}

// ConfiguredNames returns the names of providers that have credentials set
// (either an API key or an API base URL for local inference servers).
func (p ProvidersConfig) ConfiguredNames() []string {
	entries := []struct {
		name string
		key  string
	}{
		{"anthropic", p.Anthropic.APIKey},
		{"openai", p.OpenAI.APIKey},
		{"openrouter", p.OpenRouter.APIKey},
		{"gemini", p.Gemini.APIKey},
		{"groq", p.Groq.APIKey},
		{"zhipu", p.Zhipu.APIKey},
		{"deepseek", p.DeepSeek.APIKey},
		{"moonshot", p.Moonshot.APIKey},
		{"nvidia", p.Nvidia.APIKey},
		{"shengsuanyun", p.ShengSuanYun.APIKey},
		{"ollama", p.Ollama.APIBase},
		{"vllm", p.VLLM.APIBase},
		{"github_copilot", p.GitHubCopilot.APIKey},
	}
	var names []string
	for _, e := range entries {
		if e.key != "" {
			names = append(names, e.name)
		}
	}
	return names
}

type ProviderConfig struct {
	APIKey      string `json:"api_key" env:"DRAGONSCALE_PROVIDERS_{{.Name}}_API_KEY"`
	APIBase     string `json:"api_base" env:"DRAGONSCALE_PROVIDERS_{{.Name}}_API_BASE"`
	Proxy       string `json:"proxy,omitzero" env:"DRAGONSCALE_PROVIDERS_{{.Name}}_PROXY"`
	AuthMethod  string `json:"auth_method,omitzero" env:"DRAGONSCALE_PROVIDERS_{{.Name}}_AUTH_METHOD"`
	Timeout     int    `json:"timeout,omitzero" env:"DRAGONSCALE_PROVIDERS_{{.Name}}_TIMEOUT"`           // seconds, 0 = default (120s)
	ConnectMode string `json:"connect_mode,omitzero" env:"DRAGONSCALE_PROVIDERS_{{.Name}}_CONNECT_MODE"` // only for Github Copilot, `stdio` or `grpc`
}

type OpenAIProviderConfig struct {
	ProviderConfig
	WebSearch bool `json:"web_search" env:"DRAGONSCALE_PROVIDERS_OPENAI_WEB_SEARCH"`
}

type GatewayConfig struct {
	Host string `json:"host" env:"DRAGONSCALE_GATEWAY_HOST"`
	Port int    `json:"port" env:"DRAGONSCALE_GATEWAY_PORT"`
}

type BraveConfig struct {
	Enabled    bool   `json:"enabled" env:"DRAGONSCALE_TOOLS_WEB_BRAVE_ENABLED"`
	APIKey     string `json:"api_key" env:"DRAGONSCALE_TOOLS_WEB_BRAVE_API_KEY"`
	MaxResults int    `json:"max_results" env:"DRAGONSCALE_TOOLS_WEB_BRAVE_MAX_RESULTS"`
}

type DuckDuckGoConfig struct {
	Enabled    bool `json:"enabled" env:"DRAGONSCALE_TOOLS_WEB_DUCKDUCKGO_ENABLED"`
	MaxResults int  `json:"max_results" env:"DRAGONSCALE_TOOLS_WEB_DUCKDUCKGO_MAX_RESULTS"`
}

type PerplexityConfig struct {
	Enabled    bool   `json:"enabled" env:"DRAGONSCALE_TOOLS_WEB_PERPLEXITY_ENABLED"`
	APIKey     string `json:"api_key" env:"DRAGONSCALE_TOOLS_WEB_PERPLEXITY_API_KEY"`
	MaxResults int    `json:"max_results" env:"DRAGONSCALE_TOOLS_WEB_PERPLEXITY_MAX_RESULTS"`
}

type WebToolsConfig struct {
	Brave      BraveConfig      `json:"brave"`
	DuckDuckGo DuckDuckGoConfig `json:"duckduckgo"`
	Perplexity PerplexityConfig `json:"perplexity"`
}

type CronToolsConfig struct {
	ExecTimeoutMinutes int `json:"exec_timeout_minutes" env:"DRAGONSCALE_TOOLS_CRON_EXEC_TIMEOUT_MINUTES"` // 0 means no timeout
}

type ToolsConfig struct {
	Web  WebToolsConfig  `json:"web"`
	Cron CronToolsConfig `json:"cron"`
}

func DefaultConfig() *Config {
	return &Config{
		Agents: AgentsConfig{
			Defaults: AgentDefaults{
				Sandbox:           "", // empty → resolved via SandboxDir() at runtime
				RestrictToSandbox: true,
				Provider:          "",
				Model:             "glm-4.7",
				MaxTokens:         8192,
				Temperature:       0.7,
				MaxToolIterations: 20,
				ContinuityRetention: ContinuityRetentionConfig{
					MinMessages:         4,
					MaxMessages:         24,
					TargetContextRatio:  0.10,
					FailureKeepMessages: 10,
				},
			},
		},
		Channels: ChannelsConfig{
			WhatsApp: WhatsAppConfig{
				Enabled:   false,
				BridgeURL: "ws://localhost:3001",
				AllowFrom: FlexibleStringSlice{},
			},
			Telegram: TelegramConfig{
				Enabled:   false,
				Token:     "",
				AllowFrom: FlexibleStringSlice{},
			},
			Feishu: FeishuConfig{
				Enabled:           false,
				AppID:             "",
				AppSecret:         "",
				EncryptKey:        "",
				VerificationToken: "",
				AllowFrom:         FlexibleStringSlice{},
			},
			Discord: DiscordConfig{
				Enabled:   false,
				Token:     "",
				AllowFrom: FlexibleStringSlice{},
			},
			MaixCam: MaixCamConfig{
				Enabled:   false,
				Host:      "0.0.0.0",
				Port:      18790,
				AllowFrom: FlexibleStringSlice{},
			},
			QQ: QQConfig{
				Enabled:   false,
				AppID:     "",
				AppSecret: "",
				AllowFrom: FlexibleStringSlice{},
			},
			DingTalk: DingTalkConfig{
				Enabled:      false,
				ClientID:     "",
				ClientSecret: "",
				AllowFrom:    FlexibleStringSlice{},
			},
			Slack: SlackConfig{
				Enabled:   false,
				BotToken:  "",
				AppToken:  "",
				AllowFrom: FlexibleStringSlice{},
			},
			LINE: LINEConfig{
				Enabled:            false,
				ChannelSecret:      "",
				ChannelAccessToken: "",
				WebhookHost:        "0.0.0.0",
				WebhookPort:        18791,
				WebhookPath:        "/webhook/line",
				AllowFrom:          FlexibleStringSlice{},
			},
			OneBot: OneBotConfig{
				Enabled:            false,
				WSUrl:              "ws://127.0.0.1:3001",
				AccessToken:        "",
				ReconnectInterval:  5,
				GroupTriggerPrefix: []string{},
				AllowFrom:          FlexibleStringSlice{},
			},
		},
		Providers: ProvidersConfig{
			Anthropic:     ProviderConfig{},
			OpenAI:        OpenAIProviderConfig{WebSearch: true},
			OpenRouter:    ProviderConfig{},
			Groq:          ProviderConfig{},
			Zhipu:         ProviderConfig{},
			VLLM:          ProviderConfig{},
			Gemini:        ProviderConfig{},
			Nvidia:        ProviderConfig{},
			Ollama:        ProviderConfig{},
			Moonshot:      ProviderConfig{},
			ShengSuanYun:  ProviderConfig{},
			DeepSeek:      ProviderConfig{},
			GitHubCopilot: ProviderConfig{},
		},
		Gateway: GatewayConfig{
			Host: "0.0.0.0",
			Port: 18790,
		},
		Tools: ToolsConfig{
			Web: WebToolsConfig{
				Brave: BraveConfig{
					Enabled:    false,
					APIKey:     "",
					MaxResults: 5,
				},
				DuckDuckGo: DuckDuckGoConfig{
					Enabled:    true,
					MaxResults: 5,
				},
				Perplexity: PerplexityConfig{
					Enabled:    false,
					APIKey:     "",
					MaxResults: 5,
				},
			},
			Cron: CronToolsConfig{
				ExecTimeoutMinutes: 5, // default 5 minutes for LLM operations
			},
		},
		Memory: MemoryConfig{
			EmbeddingDims:          768,
			OffloadThresholdTokens: 4000,
			Sync: MemorySyncConfig{
				SyncIntervalSeconds: 60,
			},
		},
		Heartbeat: HeartbeatConfig{
			Enabled:  true,
			Interval: 30, // default 30 minutes
		},
		Devices: DevicesConfig{
			Enabled:    false,
			MonitorUSB: true,
		},
	}
}

func LoadConfig(path string) (*Config, error) {
	cfg := DefaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}

	if err := jsonv2.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	if err := env.Parse(cfg); err != nil {
		return nil, err
	}

	if warnings := cfg.Validate(); len(warnings) > 0 {
		for _, w := range warnings {
			fmt.Fprintf(os.Stderr, "config warning: %s\n", w)
		}
	}

	return cfg, nil
}

// Validate checks configuration for common issues. Returns a list of
// warning strings. These are warnings rather than hard errors to maintain
// backward compatibility, but they indicate values that will likely cause
// problems at runtime.
func (c *Config) Validate() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var warnings []string

	if c.Agents.Defaults.Model == "" {
		warnings = append(warnings, "agents.defaults.model is empty: no default LLM model configured")
	}

	if c.Agents.Defaults.MaxTokens <= 0 {
		warnings = append(warnings, fmt.Sprintf("agents.defaults.max_tokens=%d: should be > 0", c.Agents.Defaults.MaxTokens))
	}

	if c.Agents.Defaults.MaxToolIterations <= 0 {
		warnings = append(warnings, fmt.Sprintf("agents.defaults.max_tool_iterations=%d: should be > 0", c.Agents.Defaults.MaxToolIterations))
	}

	continuity := c.Agents.Defaults.ContinuityRetention
	if continuity.MinMessages <= 0 {
		warnings = append(warnings, fmt.Sprintf("agents.defaults.continuity_retention.min_messages=%d: should be > 0", continuity.MinMessages))
	}
	if continuity.MaxMessages <= 0 {
		warnings = append(warnings, fmt.Sprintf("agents.defaults.continuity_retention.max_messages=%d: should be > 0", continuity.MaxMessages))
	}
	if continuity.MaxMessages > 0 && continuity.MinMessages > continuity.MaxMessages {
		warnings = append(warnings, fmt.Sprintf("agents.defaults.continuity_retention.min_messages=%d exceeds max_messages=%d", continuity.MinMessages, continuity.MaxMessages))
	}
	if continuity.TargetContextRatio <= 0 || continuity.TargetContextRatio > 0.5 {
		warnings = append(warnings, fmt.Sprintf("agents.defaults.continuity_retention.target_context_ratio=%.4f: expected (0, 0.5]", continuity.TargetContextRatio))
	}
	if continuity.FailureKeepMessages <= 0 {
		warnings = append(warnings, fmt.Sprintf("agents.defaults.continuity_retention.failure_keep_messages=%d: should be > 0", continuity.FailureKeepMessages))
	}

	if c.Gateway.Port < 0 || c.Gateway.Port > 65535 {
		warnings = append(warnings, fmt.Sprintf("gateway.port=%d: must be in range 1-65535", c.Gateway.Port))
	}

	if c.Heartbeat.Interval < 0 {
		warnings = append(warnings, fmt.Sprintf("heartbeat.interval=%d: must be >= 0", c.Heartbeat.Interval))
	}

	// Memory config validation (memory is always enabled)
	if c.Memory.Sync.SyncURL != "" && c.Memory.Sync.AuthToken == "" {
		warnings = append(warnings, "memory.sync.sync_url is set but auth_token is empty: Turso replication will likely fail")
	}
	dims := c.Memory.EmbeddingDims
	if dims != 0 && (dims < 64 || dims > 4096) {
		warnings = append(warnings, fmt.Sprintf("memory.embedding_dims=%d: expected 64-4096 (common: 384, 768, 1536)", dims))
	}
	embProvider := c.Memory.Embedding.Provider
	if embProvider != "" && embProvider != "ollama" && embProvider != "openai" {
		warnings = append(warnings, fmt.Sprintf("memory.embedding.provider=%q: unknown (supported: ollama, openai)", embProvider))
	}
	if embProvider == "openai" && c.Memory.Embedding.APIKey == "" && c.Providers.OpenAI.APIKey == "" {
		warnings = append(warnings, "memory.embedding.provider=openai but no API key found in memory.embedding.api_key or providers.openai.api_key")
	}

	return warnings
}

// OverlayConfigFile reads a JSON file and merges its fields into an existing
// Config. Fields present in the overlay file overwrite the corresponding fields
// in cfg; fields absent from the overlay file are left unchanged. This allows
// partial override files (e.g. eval configs that only set tools or agent
// settings) without losing base config values such as provider API keys.
//
// A non-existent overlay file is silently ignored.
func OverlayConfigFile(cfg *Config, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read overlay config %q: %w", path, err)
	}
	if err := jsonv2.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("parse overlay config %q: %w", path, err)
	}
	return nil
}

func SaveConfig(path string, cfg *Config) error {
	cfg.mu.RLock()
	defer cfg.mu.RUnlock()

	data, err := jsonv2.Marshal(cfg, jsontext.WithIndent("  "))
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	return os.WriteFile(path, data, 0600)
}

// SandboxPath returns the resolved sandbox directory for agent file operations.
// Priority: explicit Sandbox config > XDG SandboxDir().
func (c *Config) SandboxPath() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.Agents.Defaults.Sandbox != "" {
		return expandHome(c.Agents.Defaults.Sandbox)
	}
	if dir, err := SandboxDir(); err == nil {
		return dir
	}
	return expandHome("~/.local/share/dragonscale/sandbox")
}

// RestrictToSandbox returns whether tool file operations should be restricted
// to the sandbox directory. Also checks the deprecated RestrictToWorkspace field.
func (c *Config) RestrictToSandbox() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Agents.Defaults.RestrictToSandbox || c.Agents.Defaults.RestrictToWorkspace
}

// WorkspacePath returns the legacy workspace path for backward compatibility.
// Deprecated: callers should migrate to SandboxPath().
func (c *Config) WorkspacePath() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.Agents.Defaults.Workspace != "" {
		return expandHome(c.Agents.Defaults.Workspace)
	}
	return c.SandboxPath()
}

// DBPath returns the resolved database path.
// Priority: explicit Memory.DBPath config > XDG DefaultDBPath().
func (c *Config) DBPath() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.Memory.DBPath != "" {
		return expandHome(c.Memory.DBPath)
	}
	if p, err := DefaultDBPath(); err == nil {
		return p
	}
	return expandHome("~/.local/share/dragonscale/dragonscale.db")
}

func (c *Config) GetAPIKey() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.Providers.OpenRouter.APIKey != "" {
		return c.Providers.OpenRouter.APIKey
	}
	if c.Providers.Anthropic.APIKey != "" {
		return c.Providers.Anthropic.APIKey
	}
	if c.Providers.OpenAI.APIKey != "" {
		return c.Providers.OpenAI.APIKey
	}
	if c.Providers.Gemini.APIKey != "" {
		return c.Providers.Gemini.APIKey
	}
	if c.Providers.Zhipu.APIKey != "" {
		return c.Providers.Zhipu.APIKey
	}
	if c.Providers.Groq.APIKey != "" {
		return c.Providers.Groq.APIKey
	}
	if c.Providers.VLLM.APIKey != "" {
		return c.Providers.VLLM.APIKey
	}
	if c.Providers.ShengSuanYun.APIKey != "" {
		return c.Providers.ShengSuanYun.APIKey
	}
	return ""
}

func (c *Config) GetAPIBase() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.Providers.OpenRouter.APIKey != "" {
		if c.Providers.OpenRouter.APIBase != "" {
			return c.Providers.OpenRouter.APIBase
		}
		return "https://openrouter.ai/api/v1"
	}
	if c.Providers.Zhipu.APIKey != "" {
		return c.Providers.Zhipu.APIBase
	}
	if c.Providers.VLLM.APIKey != "" && c.Providers.VLLM.APIBase != "" {
		return c.Providers.VLLM.APIBase
	}
	return ""
}

func expandHome(path string) string {
	if path == "" {
		return path
	}
	if path[0] == '~' {
		home, _ := os.UserHomeDir()
		if len(path) > 1 && path[1] == '/' {
			return home + path[1:]
		}
		return home
	}
	return path
}

// ─── XDG / platform path helpers ─────────────────────────────────────────────

// ConfigDir returns the platform-appropriate user configuration directory for
// dragonscale, following XDG Base Directory spec on Linux
// (~/.config/dragonscale), Library/Application Support on macOS, and
// %AppData%\dragonscale on Windows. The directory is created if it does not exist.
func ConfigDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config dir: %w", err)
	}
	dir := filepath.Join(base, pkg.NAME)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create config dir %q: %w", dir, err)
	}
	return dir, nil
}

// DataDir returns the platform-appropriate user data directory for dragonscale.
// On Linux this respects XDG_DATA_HOME (default ~/.local/share/dragonscale).
// On macOS it uses ~/Library/Application Support/dragonscale; on Windows
// %LOCALAPPDATA%\dragonscale. The directory is created if it does not exist.
func DataDir() (string, error) {
	var base string
	switch runtime.GOOS {
	case "linux", "freebsd", "openbsd", "netbsd":
		if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
			base = xdg
		} else {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("resolve home dir: %w", err)
			}
			base = filepath.Join(home, ".local", "share")
		}
	default:
		// macOS, Windows: data and config share a base directory.
		cfgBase, err := os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("resolve user config dir: %w", err)
		}
		base = cfgBase
	}
	dir := filepath.Join(base, pkg.NAME)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create data dir %q: %w", dir, err)
	}
	return dir, nil
}

// IdentityDir returns the directory containing human-editable identity files
// (AGENT.md, IDENTITY.md, SOUL.md, USER.md) inside ConfigDir. These files
// are the user-facing surface for agent configuration; a sync layer mirrors
// their content into the database at runtime.
func IdentityDir() (string, error) {
	cfgDir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cfgDir, "identity")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create identity dir %q: %w", dir, err)
	}
	return dir, nil
}

// SkillsDir returns the directory for installed skills inside DataDir.
// Skills are filesystem-native artifacts (SKILL.md + scripts + references)
// and remain on disk rather than being synced to the database.
func SkillsDir() (string, error) {
	dataDir, err := DataDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(dataDir, "skills")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create skills dir %q: %w", dir, err)
	}
	return dir, nil
}

// SandboxDir returns the directory used as the agent's filesystem operations
// workspace inside DataDir. Tool file I/O (read, write, edit, shell) is
// restricted to this directory when restrict_to_sandbox is enabled.
func SandboxDir() (string, error) {
	dataDir, err := DataDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(dataDir, "sandbox")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create sandbox dir %q: %w", dir, err)
	}
	return dir, nil
}

// CacheDir returns the platform-appropriate user cache directory for dragonscale
// (XDG_CACHE_HOME on Linux → ~/.cache/dragonscale). The directory is created if
// it does not exist.
func CacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve user cache dir: %w", err)
	}
	dir := filepath.Join(base, pkg.NAME)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create cache dir %q: %w", dir, err)
	}
	return dir, nil
}

// DefaultDBPath returns the canonical SQLite database path inside DataDir.
// Callers that want to override this should check for a CLI flag or the
// DRAGONSCALE_DB_PATH environment variable before falling back to this value.
func DefaultDBPath() (string, error) {
	dataDir, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dataDir, pkg.NAME+".db"), nil
}

// DefaultConfigPath returns the path to the primary JSON config file inside
// ConfigDir (dragonscale/config.json).
func DefaultConfigPath() (string, error) {
	cfgDir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfgDir, "config.json"), nil
}
