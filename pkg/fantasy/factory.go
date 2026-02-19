// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package fantasy

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"fmt"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/openaicompat"
	"github.com/openai/openai-go/v2/option"
	"github.com/sipeed/picoclaw/pkg/config"
)

// providerEntry describes a single LLM provider: how to identify it, how to
// extract credentials from config, and its default base URL.
type providerEntry struct {
	// Canonical name and aliases used in agents.defaults.provider.
	names []string
	// Model name prefixes (e.g. "openai/") that auto-route to this provider.
	modelPrefixes []string
	// Model substrings (lower-case) that auto-route to this provider.
	modelContains []string
	// stripPrefixOnModelID: when true, ModelID strips the first segment before "/".
	stripPrefixOnModelID bool
	// Credential extractors.
	apiKey      func(*config.Config) string
	apiBase     func(*config.Config) string
	defaultBase string
	proxy       func(*config.Config) string
	timeout     func(*config.Config) int
}

// providerRegistry is the single source of truth for provider routing.
// Entries are checked in order for both explicit provider name matching and
// model-name-based auto-detection.
var providerRegistry = []providerEntry{
	{
		names:         []string{"groq"},
		modelPrefixes: []string{"groq/"},
		modelContains: []string{"groq"},
		apiKey:        func(c *config.Config) string { return c.Providers.Groq.APIKey },
		apiBase:       func(c *config.Config) string { return c.Providers.Groq.APIBase },
		defaultBase:   "https://api.groq.com/openai/v1",
		proxy:         func(c *config.Config) string { return c.Providers.Groq.Proxy },
		timeout:       func(c *config.Config) int { return c.Providers.Groq.Timeout },
	},
	{
		names:         []string{"openai", "gpt"},
		modelPrefixes: []string{"openai/"},
		modelContains: []string{"gpt"},
		apiKey:        func(c *config.Config) string { return c.Providers.OpenAI.APIKey },
		apiBase:       func(c *config.Config) string { return c.Providers.OpenAI.APIBase },
		defaultBase:   "https://api.openai.com/v1",
		proxy:         func(c *config.Config) string { return c.Providers.OpenAI.Proxy },
		timeout:       func(c *config.Config) int { return c.Providers.OpenAI.Timeout },
	},
	{
		names:         []string{"anthropic", "claude"},
		modelPrefixes: []string{"anthropic/"},
		modelContains: []string{"claude"},
		apiKey:        func(c *config.Config) string { return c.Providers.Anthropic.APIKey },
		apiBase:       func(c *config.Config) string { return c.Providers.Anthropic.APIBase },
		defaultBase:   "https://api.anthropic.com/v1",
		proxy:         func(c *config.Config) string { return c.Providers.Anthropic.Proxy },
		timeout:       func(c *config.Config) int { return c.Providers.Anthropic.Timeout },
	},
	{
		names:         []string{"openrouter"},
		modelPrefixes: []string{"openrouter/", "meta-llama/", "deepseek/", "google/"},
		apiKey:        func(c *config.Config) string { return c.Providers.OpenRouter.APIKey },
		apiBase:       func(c *config.Config) string { return c.Providers.OpenRouter.APIBase },
		defaultBase:   "https://openrouter.ai/api/v1",
		proxy:         func(c *config.Config) string { return c.Providers.OpenRouter.Proxy },
		timeout:       func(c *config.Config) int { return c.Providers.OpenRouter.Timeout },
	},
	{
		names:         []string{"zhipu", "glm"},
		modelContains: []string{"glm", "zhipu", "zai"},
		apiKey:        func(c *config.Config) string { return c.Providers.Zhipu.APIKey },
		apiBase:       func(c *config.Config) string { return c.Providers.Zhipu.APIBase },
		defaultBase:   "https://open.bigmodel.cn/api/paas/v4",
		proxy:         func(c *config.Config) string { return c.Providers.Zhipu.Proxy },
		timeout:       func(c *config.Config) int { return c.Providers.Zhipu.Timeout },
	},
	{
		names:         []string{"gemini", "google"},
		modelPrefixes: []string{"gemini/"},
		modelContains: []string{"gemini"},
		apiKey:        func(c *config.Config) string { return c.Providers.Gemini.APIKey },
		apiBase:       func(c *config.Config) string { return c.Providers.Gemini.APIBase },
		defaultBase:   "https://generativelanguage.googleapis.com/v1beta",
		proxy:         func(c *config.Config) string { return c.Providers.Gemini.Proxy },
		timeout:       func(c *config.Config) int { return c.Providers.Gemini.Timeout },
	},
	{
		names:   []string{"vllm"},
		apiKey:  func(c *config.Config) string { return c.Providers.VLLM.APIKey },
		apiBase: func(c *config.Config) string { return c.Providers.VLLM.APIBase },
		proxy:   func(c *config.Config) string { return c.Providers.VLLM.Proxy },
		timeout: func(c *config.Config) int { return c.Providers.VLLM.Timeout },
	},
	{
		names:       []string{"shengsuanyun"},
		apiKey:      func(c *config.Config) string { return c.Providers.ShengSuanYun.APIKey },
		apiBase:     func(c *config.Config) string { return c.Providers.ShengSuanYun.APIBase },
		defaultBase: "https://router.shengsuanyun.com/api/v1",
		proxy:       func(c *config.Config) string { return c.Providers.ShengSuanYun.Proxy },
		timeout:     func(c *config.Config) int { return c.Providers.ShengSuanYun.Timeout },
	},
	{
		names:       []string{"deepseek"},
		apiKey:      func(c *config.Config) string { return c.Providers.DeepSeek.APIKey },
		apiBase:     func(c *config.Config) string { return c.Providers.DeepSeek.APIBase },
		defaultBase: "https://api.deepseek.com/v1",
		proxy:       func(c *config.Config) string { return c.Providers.DeepSeek.Proxy },
		timeout:     func(c *config.Config) int { return c.Providers.DeepSeek.Timeout },
	},
	{
		names:                []string{"nvidia"},
		modelPrefixes:        []string{"nvidia/"},
		modelContains:        []string{"nvidia"},
		stripPrefixOnModelID: true,
		apiKey:               func(c *config.Config) string { return c.Providers.Nvidia.APIKey },
		apiBase:              func(c *config.Config) string { return c.Providers.Nvidia.APIBase },
		defaultBase:          "https://integrate.api.nvidia.com/v1",
		proxy:                func(c *config.Config) string { return c.Providers.Nvidia.Proxy },
		timeout:              func(c *config.Config) int { return c.Providers.Nvidia.Timeout },
	},
	{
		names:                []string{"moonshot", "kimi"},
		modelPrefixes:        []string{"moonshot/"},
		modelContains:        []string{"kimi", "moonshot"},
		stripPrefixOnModelID: true,
		apiKey:               func(c *config.Config) string { return c.Providers.Moonshot.APIKey },
		apiBase:              func(c *config.Config) string { return c.Providers.Moonshot.APIBase },
		defaultBase:          "https://api.moonshot.cn/v1",
		proxy:                func(c *config.Config) string { return c.Providers.Moonshot.Proxy },
		timeout:              func(c *config.Config) int { return c.Providers.Moonshot.Timeout },
	},
}

// findProviderByName returns the registry entry that matches the given explicit
// provider name (case-insensitive), or nil if not found.
func findProviderByName(name string) *providerEntry {
	lower := strings.ToLower(name)
	for i := range providerRegistry {
		for _, n := range providerRegistry[i].names {
			if n == lower {
				return &providerRegistry[i]
			}
		}
	}
	return nil
}

// findProviderByModel returns the first registry entry whose model prefixes or
// model substrings match the given model string.
func findProviderByModel(model string) *providerEntry {
	lower := strings.ToLower(model)
	for i := range providerRegistry {
		e := &providerRegistry[i]
		for _, prefix := range e.modelPrefixes {
			if strings.HasPrefix(model, prefix) {
				return e
			}
		}
		for _, sub := range e.modelContains {
			if strings.Contains(lower, sub) {
				return e
			}
		}
	}
	return nil
}

// resolveBase returns the effective API base URL for the entry and config.
func (e *providerEntry) resolveBase(cfg *config.Config) string {
	if e.apiBase != nil {
		if b := e.apiBase(cfg); b != "" {
			return b
		}
	}
	return e.defaultBase
}

// CreateProvider builds a Fantasy provider from PicoClaw config.
func CreateProvider(cfg *config.Config) (fantasy.Provider, error) {
	providerName := strings.ToLower(cfg.Agents.Defaults.Provider)
	model := cfg.Agents.Defaults.Model

	// Special non-openaicompat providers are handled first.
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

	apiKey, apiBase, proxy := resolveProvider(cfg, providerName, model)
	if apiKey == "" && apiBase == "" {
		return nil, fmt.Errorf("no API key or base configured for provider (model: %s)", model)
	}
	if apiBase == "" {
		return nil, fmt.Errorf("no API base configured for provider (model: %s)", model)
	}

	timeout := resolveProviderTimeout(cfg, providerName, model)

	opts := []openaicompat.Option{
		openaicompat.WithBaseURL(apiBase),
		openaicompat.WithAPIKey(apiKey),
		openaicompat.WithName(providerNameOrDefault(providerName)),
	}

	httpClient := buildHTTPClient(proxy, timeout)
	if httpClient != nil {
		opts = append(opts, openaicompat.WithHTTPClient(httpClient))
	}

	return openaicompat.New(opts...)
}

// ModelID returns the effective model ID to pass to Fantasy's LanguageModel.
// For providers that require a prefix to be stripped (e.g. moonshot/kimi-k2.5),
// this returns only the suffix after the first "/".
func ModelID(cfg *config.Config) string {
	model := cfg.Agents.Defaults.Model

	if _, after, ok := strings.Cut(model, "/"); ok {
		// Determine whether this provider wants the prefix stripped.
		providerName := strings.ToLower(cfg.Agents.Defaults.Provider)
		var entry *providerEntry
		if providerName != "" {
			entry = findProviderByName(providerName)
		}
		if entry == nil {
			entry = findProviderByModel(model)
		}
		if entry != nil && entry.stripPrefixOnModelID {
			return after
		}
	}

	return model
}

// resolveProvider determines the API key, base URL, and proxy for the current config.
// It first tries an explicit provider name, then falls back to model-name detection,
// and finally falls back to OpenRouter if a key is available.
func resolveProvider(cfg *config.Config, providerName, model string) (apiKey, apiBase, proxy string) {
	// Explicit provider name lookup.
	if providerName != "" {
		entry := findProviderByName(providerName)
		if entry != nil {
			key := entry.apiKey(cfg)
			base := entry.resolveBase(cfg)
			// vllm uses base URL as the signal instead of an API key.
			if key != "" || (providerName == "vllm" && base != "") {
				return key, base, entry.proxy(cfg)
			}
		}
	}

	// Model-name based detection.
	entry := findProviderByModel(model)
	if entry != nil {
		key := entry.apiKey(cfg)
		base := entry.resolveBase(cfg)
		if key != "" || (len(entry.names) > 0 && entry.names[0] == "vllm" && base != "") {
			return key, base, entry.proxy(cfg)
		}
	}

	// Last resort: if OpenRouter has a key, use it as a universal fallback.
	if cfg.Providers.OpenRouter.APIKey != "" {
		base := defaultIfEmpty(cfg.Providers.OpenRouter.APIBase, "https://openrouter.ai/api/v1")
		return cfg.Providers.OpenRouter.APIKey, base, cfg.Providers.OpenRouter.Proxy
	}

	return "", "", ""
}

// resolveProviderTimeout extracts the configured timeout for the matched provider.
func resolveProviderTimeout(cfg *config.Config, providerName, model string) time.Duration {
	var entry *providerEntry
	if providerName != "" {
		entry = findProviderByName(providerName)
	}
	if entry == nil {
		entry = findProviderByModel(model)
	}

	if entry != nil && entry.timeout != nil {
		if sec := entry.timeout(cfg); sec > 0 {
			return time.Duration(sec) * time.Second
		}
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
