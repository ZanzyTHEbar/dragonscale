// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package fantasy

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/openaicompat"
	"github.com/openai/openai-go/v2/option"
	"github.com/sipeed/picoclaw/pkg/config"
)

// CreateProvider builds a Fantasy provider from PicoClaw config.
// It mirrors the provider selection logic from the legacy providers.CreateProvider.
func CreateProvider(cfg *config.Config) (fantasy.Provider, error) {
	model := cfg.Agents.Defaults.Model
	providerName := strings.ToLower(cfg.Agents.Defaults.Provider)

	lowerModel := strings.ToLower(model)

	// Resolve provider from explicit config
	if providerName != "" {
		switch providerName {
		case "claude-cli", "claudecode", "claude-code":
			workspace := cfg.Agents.Defaults.Workspace
			if workspace == "" {
				workspace = "."
			}
			return newClaudeCliProvider(workspace), nil
		}
	}

	// Build openaicompat options from config
	apiKey, apiBase, proxy := resolveProvider(cfg, providerName, model, lowerModel)

	if apiKey == "" && apiBase == "" {
		return nil, fmt.Errorf("no API key or base configured for provider (model: %s)", model)
	}
	if apiBase == "" {
		return nil, fmt.Errorf("no API base configured for provider (model: %s)", model)
	}

	timeout := resolveProviderTimeout(cfg, providerName)

	opts := []openaicompat.Option{
		openaicompat.WithBaseURL(apiBase),
		openaicompat.WithAPIKey(apiKey),
		openaicompat.WithName(providerNameOrDefault(providerName)),
	}

	// Build HTTP client with proxy and timeout support
	httpClient := buildHTTPClient(proxy, timeout)
	if httpClient != nil {
		opts = append(opts, openaicompat.WithHTTPClient(httpClient))
	}

	return openaicompat.New(opts...)
}

// ModelID returns the effective model ID to pass to Fantasy's LanguageModel.
// It strips provider prefixes that the old system used for routing.
func ModelID(cfg *config.Config) string {
	model := cfg.Agents.Defaults.Model

	// Strip provider prefix from model name (e.g., moonshot/kimi-k2.5 -> kimi-k2.5)
	if idx := strings.Index(model, "/"); idx != -1 {
		prefix := model[:idx]
		if prefix == "moonshot" || prefix == "nvidia" {
			return model[idx+1:]
		}
	}

	return model
}

// resolveProvider determines the API key, base URL, and proxy for a given config.
func resolveProvider(cfg *config.Config, providerName, model, lowerModel string) (apiKey, apiBase, proxy string) {
	// First, try explicitly configured provider
	if providerName != "" {
		switch providerName {
		case "groq":
			if cfg.Providers.Groq.APIKey != "" {
				return cfg.Providers.Groq.APIKey, defaultIfEmpty(cfg.Providers.Groq.APIBase, "https://api.groq.com/openai/v1"), ""
			}
		case "openai", "gpt":
			if cfg.Providers.OpenAI.APIKey != "" {
				return cfg.Providers.OpenAI.APIKey, defaultIfEmpty(cfg.Providers.OpenAI.APIBase, "https://api.openai.com/v1"), ""
			}
		case "anthropic", "claude":
			if cfg.Providers.Anthropic.APIKey != "" {
				return cfg.Providers.Anthropic.APIKey, defaultIfEmpty(cfg.Providers.Anthropic.APIBase, "https://api.anthropic.com/v1"), ""
			}
		case "openrouter":
			if cfg.Providers.OpenRouter.APIKey != "" {
				return cfg.Providers.OpenRouter.APIKey, defaultIfEmpty(cfg.Providers.OpenRouter.APIBase, "https://openrouter.ai/api/v1"), ""
			}
		case "zhipu", "glm":
			if cfg.Providers.Zhipu.APIKey != "" {
				return cfg.Providers.Zhipu.APIKey, defaultIfEmpty(cfg.Providers.Zhipu.APIBase, "https://open.bigmodel.cn/api/paas/v4"), ""
			}
		case "gemini", "google":
			if cfg.Providers.Gemini.APIKey != "" {
				return cfg.Providers.Gemini.APIKey, defaultIfEmpty(cfg.Providers.Gemini.APIBase, "https://generativelanguage.googleapis.com/v1beta"), ""
			}
		case "vllm":
			if cfg.Providers.VLLM.APIBase != "" {
				return cfg.Providers.VLLM.APIKey, cfg.Providers.VLLM.APIBase, ""
			}
		case "shengsuanyun":
			if cfg.Providers.ShengSuanYun.APIKey != "" {
				return cfg.Providers.ShengSuanYun.APIKey, defaultIfEmpty(cfg.Providers.ShengSuanYun.APIBase, "https://router.shengsuanyun.com/api/v1"), ""
			}
		case "deepseek":
			if cfg.Providers.DeepSeek.APIKey != "" {
				return cfg.Providers.DeepSeek.APIKey, defaultIfEmpty(cfg.Providers.DeepSeek.APIBase, "https://api.deepseek.com/v1"), ""
			}
		}
	}

	// Fallback: detect provider from model name
	switch {
	case (strings.Contains(lowerModel, "kimi") || strings.Contains(lowerModel, "moonshot") || strings.HasPrefix(model, "moonshot/")) && cfg.Providers.Moonshot.APIKey != "":
		return cfg.Providers.Moonshot.APIKey, defaultIfEmpty(cfg.Providers.Moonshot.APIBase, "https://api.moonshot.cn/v1"), cfg.Providers.Moonshot.Proxy

	case strings.HasPrefix(model, "openrouter/") || strings.HasPrefix(model, "anthropic/") || strings.HasPrefix(model, "openai/") || strings.HasPrefix(model, "meta-llama/") || strings.HasPrefix(model, "deepseek/") || strings.HasPrefix(model, "google/"):
		base := defaultIfEmpty(cfg.Providers.OpenRouter.APIBase, "https://openrouter.ai/api/v1")
		return cfg.Providers.OpenRouter.APIKey, base, cfg.Providers.OpenRouter.Proxy

	case (strings.Contains(lowerModel, "claude") || strings.HasPrefix(model, "anthropic/")) && cfg.Providers.Anthropic.APIKey != "":
		return cfg.Providers.Anthropic.APIKey, defaultIfEmpty(cfg.Providers.Anthropic.APIBase, "https://api.anthropic.com/v1"), cfg.Providers.Anthropic.Proxy

	case (strings.Contains(lowerModel, "gpt") || strings.HasPrefix(model, "openai/")) && cfg.Providers.OpenAI.APIKey != "":
		return cfg.Providers.OpenAI.APIKey, defaultIfEmpty(cfg.Providers.OpenAI.APIBase, "https://api.openai.com/v1"), cfg.Providers.OpenAI.Proxy

	case (strings.Contains(lowerModel, "gemini") || strings.HasPrefix(model, "google/")) && cfg.Providers.Gemini.APIKey != "":
		return cfg.Providers.Gemini.APIKey, defaultIfEmpty(cfg.Providers.Gemini.APIBase, "https://generativelanguage.googleapis.com/v1beta"), cfg.Providers.Gemini.Proxy

	case (strings.Contains(lowerModel, "glm") || strings.Contains(lowerModel, "zhipu") || strings.Contains(lowerModel, "zai")) && cfg.Providers.Zhipu.APIKey != "":
		return cfg.Providers.Zhipu.APIKey, defaultIfEmpty(cfg.Providers.Zhipu.APIBase, "https://open.bigmodel.cn/api/paas/v4"), cfg.Providers.Zhipu.Proxy

	case (strings.Contains(lowerModel, "groq") || strings.HasPrefix(model, "groq/")) && cfg.Providers.Groq.APIKey != "":
		return cfg.Providers.Groq.APIKey, defaultIfEmpty(cfg.Providers.Groq.APIBase, "https://api.groq.com/openai/v1"), cfg.Providers.Groq.Proxy

	case (strings.Contains(lowerModel, "nvidia") || strings.HasPrefix(model, "nvidia/")) && cfg.Providers.Nvidia.APIKey != "":
		return cfg.Providers.Nvidia.APIKey, defaultIfEmpty(cfg.Providers.Nvidia.APIBase, "https://integrate.api.nvidia.com/v1"), cfg.Providers.Nvidia.Proxy

	case cfg.Providers.VLLM.APIBase != "":
		return cfg.Providers.VLLM.APIKey, cfg.Providers.VLLM.APIBase, cfg.Providers.VLLM.Proxy

	default:
		if cfg.Providers.OpenRouter.APIKey != "" {
			base := defaultIfEmpty(cfg.Providers.OpenRouter.APIBase, "https://openrouter.ai/api/v1")
			return cfg.Providers.OpenRouter.APIKey, base, cfg.Providers.OpenRouter.Proxy
		}
	}

	return "", "", ""
}

// resolveProviderTimeout extracts the timeout from the matched provider config.
func resolveProviderTimeout(cfg *config.Config, providerName string) time.Duration {
	var timeoutSec int

	switch providerName {
	case "groq":
		timeoutSec = cfg.Providers.Groq.Timeout
	case "openai", "gpt":
		timeoutSec = cfg.Providers.OpenAI.Timeout
	case "anthropic", "claude":
		timeoutSec = cfg.Providers.Anthropic.Timeout
	case "openrouter":
		timeoutSec = cfg.Providers.OpenRouter.Timeout
	case "zhipu", "glm":
		timeoutSec = cfg.Providers.Zhipu.Timeout
	case "gemini", "google":
		timeoutSec = cfg.Providers.Gemini.Timeout
	case "vllm":
		timeoutSec = cfg.Providers.VLLM.Timeout
	case "shengsuanyun":
		timeoutSec = cfg.Providers.ShengSuanYun.Timeout
	case "deepseek":
		timeoutSec = cfg.Providers.DeepSeek.Timeout
	case "nvidia":
		timeoutSec = cfg.Providers.Nvidia.Timeout
	case "moonshot":
		timeoutSec = cfg.Providers.Moonshot.Timeout
	}

	if timeoutSec > 0 {
		return time.Duration(timeoutSec) * time.Second
	}
	return 120 * time.Second
}

// buildHTTPClient creates an HTTP client with optional proxy and timeout.
func buildHTTPClient(proxy string, timeout time.Duration) option.HTTPClient {
	transport := http.DefaultTransport.(*http.Transport).Clone()

	if proxy != "" {
		proxyURL, err := url.Parse(proxy)
		if err == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	}

	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
}

func defaultIfEmpty(val, fallback string) string {
	if val == "" {
		return fallback
	}
	return val
}

func providerNameOrDefault(name string) string {
	if name == "" {
		return "picoclaw"
	}
	return name
}
