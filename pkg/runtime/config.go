package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg"
	"github.com/ZanzyTHEbar/dragonscale/pkg/auth"
	"github.com/ZanzyTHEbar/dragonscale/pkg/config"
)

const (
	EvalConfigEnvVar     = "DRAGONSCALE_EVAL_CONFIG"
	EvalBaseConfigEnvVar = "DRAGONSCALE_EVAL_BASE_CONFIG"
	EvalHostHomeEnvVar   = "DRAGONSCALE_EVAL_HOST_HOME"

	evalDefaultModel          = "glm-4.7"
	evalChatGPTModel          = "gpt-5.5"
	evalOpenCodeGoModel       = "kimi-k2.6"
	evalOpenCodeZenModel      = "gpt-5.5"
	evalOpenRouterModel       = "openai/gpt-4o-mini"
	evalOpenAIModel           = "gpt-4o-mini"
	evalOpenCodeGoAPIBaseURL  = "https://opencode.ai/zen/go/v1"
	evalOpenCodeZenAPIBaseURL = "https://opencode.ai/zen/v1"
	evalOpenRouterAPIBaseURL  = "https://openrouter.ai/api/v1"
	evalOpenAIAPIBaseURL      = "https://api.openai.com/v1"
)

type LoadConfigOptions struct {
	BaseConfigPath     string
	OverlayConfigPath  string
	MinProviderTimeout time.Duration
}

func ResolveBaseConfigPath() string {
	if explicit := os.Getenv(EvalBaseConfigEnvVar); explicit != "" {
		if _, err := os.Stat(explicit); err == nil {
			return explicit
		}
	}

	checkHostConfig := func(hostHome string) string {
		if hostHome == "" {
			return ""
		}
		// XDG standard path.
		hostXDG := filepath.Join(hostHome, ".config", pkg.NAME, "config.json")
		if _, err := os.Stat(hostXDG); err == nil {
			return hostXDG
		}

		return ""
	}

	// Prefer host-mounted config when running in containerized eval.
	if hostHome := os.Getenv(EvalHostHomeEnvVar); hostHome != "" {
		if path := checkHostConfig(hostHome); path != "" {
			return path
		}
	}

	// Prefer XDG standard path (~/.config/dragonscale/config.json) when present.
	xdgPath, err := config.DefaultConfigPath()
	if err == nil {
		if _, statErr := os.Stat(xdgPath); statErr == nil {
			return xdgPath
		}
	}

	// Neither exists; return XDG path if resolvable so defaults still load.
	if err == nil {
		return xdgPath
	}

	return ""
}

func LoadResolvedConfig(opts LoadConfigOptions) (*config.Config, error) {
	basePath := opts.BaseConfigPath
	if basePath == "" {
		basePath = ResolveBaseConfigPath()
	}

	cfg, err := config.LoadConfig(basePath)
	if err != nil {
		return nil, fmt.Errorf("load base config: %w", err)
	}

	if opts.OverlayConfigPath != "" {
		if err := config.OverlayConfigFile(cfg, opts.OverlayConfigPath); err != nil {
			return nil, fmt.Errorf("load config overlay: %w", err)
		}
		config.ApplyEnvOverrides(cfg)
	}

	EnsureMinProviderTimeout(cfg, opts.MinProviderTimeout)
	return cfg, nil
}

func LoadEvalConfig(minTimeout time.Duration) (*config.Config, error) {
	cfg, err := LoadResolvedConfig(LoadConfigOptions{
		OverlayConfigPath:  os.Getenv(EvalConfigEnvVar),
		MinProviderTimeout: minTimeout,
	})
	if err != nil {
		return nil, err
	}
	ApplyEvalProviderDefaults(cfg)
	EnsureMinProviderTimeout(cfg, minTimeout)
	return cfg, nil
}

func ApplyEvalProviderDefaults(cfg *config.Config) {
	if cfg == nil {
		return
	}

	if cfg.Providers.OpenRouter.APIKey == "" {
		cfg.Providers.OpenRouter.APIKey = lookupFirstEnv("DRAGONSCALE_PROVIDERS_OPENROUTER_API_KEY", "OPENROUTER_API_KEY")
	}
	if cfg.Providers.OpenAI.APIKey == "" {
		cfg.Providers.OpenAI.APIKey = lookupFirstEnv("DRAGONSCALE_PROVIDERS_OPENAI_API_KEY", "OPENAI_API_KEY")
	}
	if cfg.Providers.OpenCode.APIKey == "" {
		cfg.Providers.OpenCode.APIKey = lookupFirstEnv("DRAGONSCALE_PROVIDERS_OPENCODE_API_KEY", "OPENCODE_API_KEY", "OPENCODE_GO_API_KEY", "OPENCODE_ZEN_API_KEY")
	}
	if provider := lookupFirstEnv("DRAGONSCALE_AGENTS_DEFAULTS_PROVIDER"); provider != "" {
		cfg.Agents.Defaults.Provider = provider
	}
	if model := lookupFirstEnv("DRAGONSCALE_AGENTS_DEFAULTS_MODEL"); model != "" {
		cfg.Agents.Defaults.Model = model
	}
	applyExplicitEvalProviderDefaults(cfg)

	if hasUsableEvalProvider(cfg) {
		return
	}
	if strings.TrimSpace(cfg.Agents.Defaults.Provider) != "" {
		return
	}

	if cfg.Providers.OpenCode.APIKey != "" && cfg.Agents.Defaults.Provider == "" {
		cfg.Agents.Defaults.Provider = "opencode-go"
		if cfg.Providers.OpenCode.APIBase == "" {
			cfg.Providers.OpenCode.APIBase = evalOpenCodeGoAPIBaseURL
		}
		if isDefaultOrEmptyEvalModel(cfg.Agents.Defaults.Model) {
			cfg.Agents.Defaults.Model = evalOpenCodeGoModel
		}
		return
	}

	if cfg.Providers.OpenRouter.APIKey != "" {
		cfg.Agents.Defaults.Provider = "openrouter"
		if cfg.Providers.OpenRouter.APIBase == "" {
			cfg.Providers.OpenRouter.APIBase = evalOpenRouterAPIBaseURL
		}
		if isDefaultOrEmptyEvalModel(cfg.Agents.Defaults.Model) {
			cfg.Agents.Defaults.Model = evalOpenRouterModel
		}
		return
	}

	if cfg.Providers.OpenAI.APIKey != "" {
		cfg.Agents.Defaults.Provider = "openai"
		if cfg.Providers.OpenAI.APIBase == "" {
			cfg.Providers.OpenAI.APIBase = evalOpenAIAPIBaseURL
		}
		if isDefaultOrEmptyEvalModel(cfg.Agents.Defaults.Model) {
			cfg.Agents.Defaults.Model = evalOpenAIModel
		}
		return
	}

	if hasChatGPTOAuthCredential() && cfg.Agents.Defaults.Provider == "" {
		cfg.Agents.Defaults.Provider = "chatgpt"
		if isDefaultOrEmptyEvalModel(cfg.Agents.Defaults.Model) {
			cfg.Agents.Defaults.Model = evalChatGPTModel
		}
	}
}

func applyExplicitEvalProviderDefaults(cfg *config.Config) {
	provider := strings.ToLower(strings.TrimSpace(cfg.Agents.Defaults.Provider))
	switch provider {
	case "opencode-go", "opencode_go", "oc-go":
		if cfg.Providers.OpenCode.APIBase == "" {
			cfg.Providers.OpenCode.APIBase = evalOpenCodeGoAPIBaseURL
		}
		if isDefaultOrEmptyEvalModel(cfg.Agents.Defaults.Model) {
			cfg.Agents.Defaults.Model = evalOpenCodeGoModel
		}
	case "opencode-zen", "opencode_zen", "oc-zen":
		if cfg.Providers.OpenCode.APIBase == "" {
			cfg.Providers.OpenCode.APIBase = evalOpenCodeZenAPIBaseURL
		}
		if isDefaultOrEmptyEvalModel(cfg.Agents.Defaults.Model) {
			cfg.Agents.Defaults.Model = evalOpenCodeZenModel
		}
	case "chatgpt":
		if hasChatGPTOAuthCredential() && isDefaultOrEmptyEvalModel(cfg.Agents.Defaults.Model) {
			cfg.Agents.Defaults.Model = evalChatGPTModel
		}
	}
}

func lookupFirstEnv(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func isDefaultOrEmptyEvalModel(model string) bool {
	switch strings.TrimSpace(model) {
	case "", evalDefaultModel:
		return true
	default:
		return false
	}
}

func hasUsableEvalProvider(cfg *config.Config) bool {
	provider := strings.ToLower(strings.TrimSpace(cfg.Agents.Defaults.Provider))
	model := strings.ToLower(strings.TrimSpace(cfg.Agents.Defaults.Model))

	if provider != "" {
		return evalProviderConfigured(cfg, provider)
	}

	switch {
	case strings.HasPrefix(model, "opencode-go/") || strings.HasPrefix(model, "opencode_go/") || strings.HasPrefix(model, "oc-go/") || strings.HasPrefix(model, "opencode-zen/") || strings.HasPrefix(model, "opencode_zen/") || strings.HasPrefix(model, "oc-zen/"):
		return providerKeyConfigured(cfg.Providers.OpenCode.APIKey)
	case strings.HasPrefix(model, "openai/") || strings.Contains(model, "gpt"):
		return providerKeyConfigured(cfg.Providers.OpenAI.APIKey)
	case strings.HasPrefix(model, "openrouter/"), strings.HasPrefix(model, "meta-llama/"), strings.HasPrefix(model, "deepseek/"), strings.HasPrefix(model, "google/"):
		return providerKeyConfigured(cfg.Providers.OpenRouter.APIKey)
	case strings.Contains(model, "glm") || strings.Contains(model, "zhipu") || strings.Contains(model, "zai"):
		return providerKeyConfigured(cfg.Providers.Zhipu.APIKey)
	case strings.Contains(model, "claude") || strings.HasPrefix(model, "anthropic/"):
		return providerKeyConfigured(cfg.Providers.Anthropic.APIKey)
	case strings.Contains(model, "gemini") || strings.HasPrefix(model, "gemini/"):
		return providerKeyConfigured(cfg.Providers.Gemini.APIKey)
	}

	return false
}

func evalProviderConfigured(cfg *config.Config, provider string) bool {
	switch provider {
	case "openrouter":
		return providerKeyConfigured(cfg.Providers.OpenRouter.APIKey)
	case "openai", "gpt":
		return providerKeyConfigured(cfg.Providers.OpenAI.APIKey)
	case "anthropic", "claude":
		return providerKeyConfigured(cfg.Providers.Anthropic.APIKey)
	case "zhipu", "glm":
		return providerKeyConfigured(cfg.Providers.Zhipu.APIKey)
	case "gemini", "google":
		return providerKeyConfigured(cfg.Providers.Gemini.APIKey)
	case "groq":
		return providerKeyConfigured(cfg.Providers.Groq.APIKey)
	case "vllm":
		return providerBaseConfigured(cfg.Providers.VLLM.APIBase)
	case "ollama":
		return providerBaseConfigured(cfg.Providers.Ollama.APIBase)
	case "deepseek":
		return providerKeyConfigured(cfg.Providers.DeepSeek.APIKey)
	case "moonshot", "kimi":
		return providerKeyConfigured(cfg.Providers.Moonshot.APIKey)
	case "nvidia":
		return providerKeyConfigured(cfg.Providers.Nvidia.APIKey)
	case "shengsuanyun":
		return providerKeyConfigured(cfg.Providers.ShengSuanYun.APIKey)
	case "github_copilot":
		return providerKeyConfigured(cfg.Providers.GitHubCopilot.APIKey)
	case "opencode-go", "opencode_go", "oc-go", "opencode-zen", "opencode_zen", "oc-zen":
		return providerKeyConfigured(cfg.Providers.OpenCode.APIKey)
	case "chatgpt":
		return hasChatGPTOAuthCredential()
	default:
		return false
	}
}

func hasChatGPTOAuthCredential() bool {
	cred, err := auth.GetCredential("openai")
	if err != nil || cred == nil || cred.AuthMethod != "oauth" {
		return false
	}
	return cred.RefreshToken != "" || (cred.AccessToken != "" && !cred.IsExpired())
}

func providerKeyConfigured(apiKey string) bool {
	return strings.TrimSpace(apiKey) != ""
}

func providerBaseConfigured(apiBase string) bool {
	return strings.TrimSpace(apiBase) != ""
}

func EnsureMinProviderTimeout(cfg *config.Config, minTimeout time.Duration) {
	if cfg == nil || minTimeout <= 0 {
		return
	}
	minSeconds := int(minTimeout.Seconds())
	if minSeconds <= 0 {
		return
	}

	set := func(p *config.ProviderConfig) {
		if p.Timeout == 0 || p.Timeout < minSeconds {
			p.Timeout = minSeconds
		}
	}

	providers := []*config.ProviderConfig{
		&cfg.Providers.Anthropic,
		&cfg.Providers.OpenAI.ProviderConfig,
		&cfg.Providers.OpenRouter,
		&cfg.Providers.Groq,
		&cfg.Providers.Zhipu,
		&cfg.Providers.VLLM,
		&cfg.Providers.Gemini,
		&cfg.Providers.Nvidia,
		&cfg.Providers.Ollama,
		&cfg.Providers.Moonshot,
		&cfg.Providers.ShengSuanYun,
		&cfg.Providers.DeepSeek,
		&cfg.Providers.GitHubCopilot,
		&cfg.Providers.OpenCode,
		&cfg.Providers.ChatGPT,
	}
	for _, p := range providers {
		set(p)
	}
}
