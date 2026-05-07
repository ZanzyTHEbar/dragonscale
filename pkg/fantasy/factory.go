// DragonScale - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 DragonScale contributors

package fantasy

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"fmt"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/openaicompat"
	"github.com/ZanzyTHEbar/dragonscale/pkg"
	"github.com/ZanzyTHEbar/dragonscale/pkg/config"
	"github.com/openai/openai-go/v2/option"
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
	// modelCatalog enables OpenAI-compatible GET /models refresh for this entry.
	modelCatalog bool
	// modelCatalogPublic allows catalog refresh without a configured API key.
	modelCatalogPublic bool
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
		modelCatalog:  true,
		apiKey:        func(c *config.Config) string { return c.Providers.Groq.APIKey },
		apiBase:       func(c *config.Config) string { return c.Providers.Groq.APIBase },
		defaultBase:   "https://api.groq.com/openai/v1",
		proxy:         func(c *config.Config) string { return c.Providers.Groq.Proxy },
		timeout:       func(c *config.Config) int { return c.Providers.Groq.Timeout },
	},
	{
		names:                []string{"openai", "gpt"},
		modelPrefixes:        []string{"openai/"},
		modelContains:        []string{"gpt"},
		stripPrefixOnModelID: true,
		modelCatalog:         true,
		apiKey:               func(c *config.Config) string { return c.Providers.OpenAI.APIKey },
		apiBase:              func(c *config.Config) string { return c.Providers.OpenAI.APIBase },
		defaultBase:          "https://api.openai.com/v1",
		proxy:                func(c *config.Config) string { return c.Providers.OpenAI.Proxy },
		timeout:              func(c *config.Config) int { return c.Providers.OpenAI.Timeout },
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
		modelCatalog:  true,
		apiKey:        func(c *config.Config) string { return c.Providers.OpenRouter.APIKey },
		apiBase:       func(c *config.Config) string { return c.Providers.OpenRouter.APIBase },
		defaultBase:   "https://openrouter.ai/api/v1",
		proxy:         func(c *config.Config) string { return c.Providers.OpenRouter.Proxy },
		timeout:       func(c *config.Config) int { return c.Providers.OpenRouter.Timeout },
	},
	{
		names:         []string{"zhipu", "glm"},
		modelContains: []string{"glm", "zhipu", "zai"},
		modelCatalog:  true,
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
		names:        []string{"vllm"},
		modelCatalog: true,
		apiKey:       func(c *config.Config) string { return c.Providers.VLLM.APIKey },
		apiBase:      func(c *config.Config) string { return c.Providers.VLLM.APIBase },
		proxy:        func(c *config.Config) string { return c.Providers.VLLM.Proxy },
		timeout:      func(c *config.Config) int { return c.Providers.VLLM.Timeout },
	},
	{
		names:        []string{"shengsuanyun"},
		modelCatalog: true,
		apiKey:       func(c *config.Config) string { return c.Providers.ShengSuanYun.APIKey },
		apiBase:      func(c *config.Config) string { return c.Providers.ShengSuanYun.APIBase },
		defaultBase:  "https://router.shengsuanyun.com/api/v1",
		proxy:        func(c *config.Config) string { return c.Providers.ShengSuanYun.Proxy },
		timeout:      func(c *config.Config) int { return c.Providers.ShengSuanYun.Timeout },
	},
	{
		names:        []string{"deepseek"},
		modelCatalog: true,
		apiKey:       func(c *config.Config) string { return c.Providers.DeepSeek.APIKey },
		apiBase:      func(c *config.Config) string { return c.Providers.DeepSeek.APIBase },
		defaultBase:  "https://api.deepseek.com/v1",
		proxy:        func(c *config.Config) string { return c.Providers.DeepSeek.Proxy },
		timeout:      func(c *config.Config) int { return c.Providers.DeepSeek.Timeout },
	},
	{
		names:                []string{"nvidia"},
		modelPrefixes:        []string{"nvidia/"},
		modelContains:        []string{"nvidia"},
		stripPrefixOnModelID: true,
		modelCatalog:         true,
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
		modelCatalog:         true,
		apiKey:               func(c *config.Config) string { return c.Providers.Moonshot.APIKey },
		apiBase:              func(c *config.Config) string { return c.Providers.Moonshot.APIBase },
		defaultBase:          "https://api.moonshot.cn/v1",
		proxy:                func(c *config.Config) string { return c.Providers.Moonshot.Proxy },
		timeout:              func(c *config.Config) int { return c.Providers.Moonshot.Timeout },
	},
	{
		names:                []string{"opencode-go", "opencode_go", "oc-go"},
		modelPrefixes:        []string{"opencode-go/", "opencode_go/", "oc-go/"},
		stripPrefixOnModelID: true,
		modelCatalog:         true,
		modelCatalogPublic:   true,
		apiKey:               func(c *config.Config) string { return c.Providers.OpenCode.APIKey },
		apiBase:              func(c *config.Config) string { return c.Providers.OpenCode.APIBase },
		defaultBase:          "https://opencode.ai/zen/go/v1",
		proxy:                func(c *config.Config) string { return c.Providers.OpenCode.Proxy },
		timeout:              func(c *config.Config) int { return c.Providers.OpenCode.Timeout },
	},
	{
		names:                []string{"opencode-zen", "opencode_zen", "oc-zen"},
		modelPrefixes:        []string{"opencode-zen/", "opencode_zen/", "oc-zen/"},
		stripPrefixOnModelID: true,
		modelCatalog:         true,
		modelCatalogPublic:   true,
		apiKey:               func(c *config.Config) string { return c.Providers.OpenCode.APIKey },
		apiBase:              func(c *config.Config) string { return c.Providers.OpenCode.APIBase },
		defaultBase:          "https://opencode.ai/zen/v1",
		proxy:                func(c *config.Config) string { return c.Providers.OpenCode.Proxy },
		timeout:              func(c *config.Config) int { return c.Providers.OpenCode.Timeout },
	},
	{
		names:                []string{"chatgpt"},
		modelPrefixes:        []string{"chatgpt/"},
		stripPrefixOnModelID: true,
		apiKey:               func(c *config.Config) string { return c.Providers.ChatGPT.APIKey },
		apiBase:              func(c *config.Config) string { return c.Providers.ChatGPT.APIBase },
		defaultBase:          chatGPTCodexEndpoint,
		proxy:                func(c *config.Config) string { return c.Providers.ChatGPT.Proxy },
		timeout:              func(c *config.Config) int { return c.Providers.ChatGPT.Timeout },
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
			if strings.HasPrefix(lower, prefix) {
				return e
			}
		}
	}
	for i := range providerRegistry {
		e := &providerRegistry[i]
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

// CreateProvider builds a Fantasy provider from DragonScale config.
func CreateProvider(cfg *config.Config) (fantasy.Provider, error) {
	providerName := strings.ToLower(cfg.Agents.Defaults.Provider)
	model := cfg.Agents.Defaults.Model

	// Special non-openaicompat providers are handled first.
	if providerName != "" {
		switch providerName {
		case "claude-cli", "claudecode", "claude-code":
			sandbox := cfg.SandboxPath()
			return newClaudeCliProvider(sandbox), nil
		case "chatgpt":
			return newChatGPTProvider(cfg)
		}
		if findProviderByName(providerName) == nil {
			return nil, unknownProviderError(providerName)
		}
	}
	if strings.HasPrefix(model, "chatgpt/") {
		return newChatGPTProvider(cfg)
	}
	if providerName == "" && findProviderByModel(model) == nil {
		if entries := configuredCachedProviderEntries(cfg, model); len(entries) > 1 {
			return nil, ambiguousCachedModelError(cfg, model, entries)
		}
	}

	apiKey, apiBase, proxy := resolveProvider(cfg, providerName, model)
	if apiKey == "" && apiBase == "" {
		return nil, providerResolutionError(cfg, providerName, model)
	}
	if apiBase == "" {
		return nil, fmt.Errorf("no API base configured for provider %s (model: %s)", providerDiagnosticName(providerName, model), model)
	}

	timeout := resolveProviderTimeout(cfg, providerName, model)

	opts := []openaicompat.Option{
		openaicompat.WithBaseURL(apiBase),
		openaicompat.WithAPIKey(apiKey),
		openaicompat.WithName(providerNameOrDefault(providerName)),
	}
	if shouldUseOpenAIResponsesWebSocket(cfg, providerName, model, apiKey) {
		opts = append(opts, openaicompat.WithUseResponsesAPI(), openaicompat.WithResponsesWebSocket())
	}

	httpClient := buildHTTPClient(proxy, timeout)
	if httpClient != nil {
		opts = append(opts, openaicompat.WithHTTPClient(httpClient))
	}

	return openaicompat.New(opts...)
}

func shouldUseOpenAIResponsesWebSocket(cfg *config.Config, providerName, model, apiKey string) bool {
	if cfg == nil || !cfg.Providers.OpenAI.ResponsesWebSocket {
		return false
	}
	if strings.TrimSpace(cfg.Providers.OpenAI.APIKey) == "" || apiKey != cfg.Providers.OpenAI.APIKey {
		return false
	}
	if !isDefaultOpenAIResponsesWebSocketBase(cfg.Providers.OpenAI.APIBase) {
		return false
	}
	var entry *providerEntry
	if providerName != "" {
		entry = findProviderByName(providerName)
	}
	if entry == nil {
		entry = findProviderByModel(model)
	}
	return entry != nil && len(entry.names) > 0 && entry.names[0] == "openai"
}

func isDefaultOpenAIResponsesWebSocketBase(apiBase string) bool {
	apiBase = strings.TrimSpace(apiBase)
	if apiBase == "" {
		return true
	}
	parsed, err := url.Parse(apiBase)
	if err != nil {
		return false
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed.Scheme == "https" && parsed.Host == "api.openai.com" && parsed.Path == "/v1"
}

func unknownProviderError(providerName string) error {
	known := knownProviderNames()
	msg := fmt.Sprintf("unknown provider %q", providerName)
	if suggestion := closestString(providerName, known); suggestion != "" {
		msg += fmt.Sprintf("; did you mean %q?", suggestion)
	}
	msg += "; known providers: " + strings.Join(known, ", ")
	return fmt.Errorf("%s", msg)
}

func providerResolutionError(cfg *config.Config, providerName, model string) error {
	entry := providerDiagnosticEntry(providerName, model)
	configured := configuredProviderSummary(cfg)
	if entry == nil {
		msg := fmt.Sprintf("no provider configured for model %q", model)
		if suggestion := closestString(model, knownModelHints()); suggestion != "" {
			msg += fmt.Sprintf("; closest known route is %q", suggestion)
		}
		return fmt.Errorf("%s; configured providers: %s", msg, configured)
	}
	return fmt.Errorf("provider %q is missing credentials or base URL for model %q (source: missing); configured providers: %s; set %s", entry.canonicalName(), model, configured, strings.Join(providerEnvHints(entry), " or "))
}

func configuredCachedProviderEntries(cfg *config.Config, model string) []*providerEntry {
	entries := findProvidersByCachedModel(model)
	usable := make([]*providerEntry, 0, len(entries))
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		key := entry.apiKey(cfg)
		base := entry.resolveBase(cfg)
		if key != "" || (entry.canonicalName() == "vllm" && base != "") {
			usable = append(usable, entry)
		}
	}
	return usable
}

func ambiguousCachedModelError(cfg *config.Config, model string, entries []*providerEntry) error {
	providers := make([]string, 0, len(entries))
	for _, entry := range entries {
		providers = append(providers, fmt.Sprintf("%s(%s)", entry.canonicalName(), providerConfiguredSource(cfg, entry.canonicalName())))
	}
	return fmt.Errorf("model %q is available from multiple configured providers: %s; set agents.defaults.provider or prefix the model", model, strings.Join(providers, ", "))
}

func providerConfiguredSource(cfg *config.Config, providerName string) string {
	if cfg == nil {
		return "unknown"
	}
	for _, info := range cfg.Providers.ConfiguredProviderInfos() {
		if info.Name == providerName {
			return info.Source
		}
	}
	return "unknown"
}

func providerDiagnosticEntry(providerName, model string) *providerEntry {
	if providerName != "" {
		return findProviderByName(providerName)
	}
	if entry := findProviderByModel(model); entry != nil {
		return entry
	}
	entries := findProvidersByCachedModel(model)
	if len(entries) > 0 {
		return entries[0]
	}
	return nil
}

func providerDiagnosticName(providerName, model string) string {
	if providerName != "" {
		return fmt.Sprintf("%q", providerName)
	}
	if entry := providerDiagnosticEntry(providerName, model); entry != nil {
		return fmt.Sprintf("%q", entry.canonicalName())
	}
	return "(unknown)"
}

func configuredProviderSummary(cfg *config.Config) string {
	if cfg == nil {
		return "none"
	}
	infos := cfg.Providers.ConfiguredProviderInfos()
	if len(infos) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(infos))
	for _, info := range infos {
		parts = append(parts, fmt.Sprintf("%s(%s)", info.Name, info.Source))
	}
	return strings.Join(parts, ", ")
}

func providerEnvHints(entry *providerEntry) []string {
	if entry == nil {
		return nil
	}
	switch entry.canonicalName() {
	case "opencode-go", "opencode-zen":
		return []string{"DRAGONSCALE_PROVIDERS_OPENCODE_API_KEY"}
	case "vllm", "ollama":
		return []string{"DRAGONSCALE_PROVIDERS_" + providerEnvName(entry.canonicalName()) + "_API_BASE"}
	default:
		return []string{"DRAGONSCALE_PROVIDERS_" + providerEnvName(entry.canonicalName()) + "_API_KEY"}
	}
}

func providerEnvName(provider string) string {
	provider = strings.ToUpper(strings.ReplaceAll(provider, "-", "_"))
	return strings.ReplaceAll(provider, " ", "_")
}

func knownProviderNames() []string {
	seen := map[string]bool{}
	var names []string
	for i := range providerRegistry {
		for _, name := range providerRegistry[i].names {
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	return names
}

func knownModelHints() []string {
	seen := map[string]bool{}
	var hints []string
	for i := range providerRegistry {
		entry := &providerRegistry[i]
		for _, prefix := range entry.modelPrefixes {
			if !seen[prefix] {
				seen[prefix] = true
				hints = append(hints, prefix)
			}
		}
		for _, contains := range entry.modelContains {
			if !seen[contains] {
				seen[contains] = true
				hints = append(hints, contains)
			}
		}
	}
	if catalog, err := LoadFreshModelCatalog("", defaultModelCatalogTTL); err == nil {
		for _, provider := range catalog.Providers {
			for _, model := range provider.Models {
				if !seen[model.ID] {
					seen[model.ID] = true
					hints = append(hints, model.ID)
				}
			}
		}
	}
	return hints
}

func closestString(input string, candidates []string) string {
	input = strings.ToLower(strings.TrimSpace(input))
	if input == "" || len(candidates) == 0 {
		return ""
	}
	best := ""
	bestDistance := 1 << 30
	for _, candidate := range candidates {
		distance := editDistance(input, strings.ToLower(candidate))
		if distance < bestDistance {
			best = candidate
			bestDistance = distance
		}
	}
	if bestDistance > max(3, len(input)/2) {
		return ""
	}
	return best
}

func editDistance(a, b string) int {
	if a == "" {
		return len(b)
	}
	if b == "" {
		return len(a)
	}
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 0
			if a[i-1] != b[j-1] {
				cost = 1
			}
			cur[j] = min(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
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

	// Cached model catalog fallback. This is disk-only and intentionally runs
	// after static routing so agent startup never performs model-list network I/O.
	for _, cachedEntry := range findProvidersByCachedModel(model) {
		key := cachedEntry.apiKey(cfg)
		base := cachedEntry.resolveBase(cfg)
		if key != "" || (len(cachedEntry.names) > 0 && cachedEntry.names[0] == "vllm" && base != "") {
			return key, base, cachedEntry.proxy(cfg)
		}
	}

	// Last resort: if OpenRouter has a key, use it as a universal fallback.
	if cfg.Providers.OpenRouter.APIKey != "" {
		base := defaultIfEmpty(cfg.Providers.OpenRouter.APIBase, "https://openrouter.ai/api/v1")
		return cfg.Providers.OpenRouter.APIKey, base, cfg.Providers.OpenRouter.Proxy
	}

	return "", "", ""
}

func findProvidersByCachedModel(model string) []*providerEntry {
	catalog, err := LoadFreshModelCatalog("", defaultModelCatalogTTL)
	if err != nil {
		return nil
	}
	entries := make([]*providerEntry, 0, 4)
	for i := range providerRegistry {
		entry := &providerRegistry[i]
		if catalog.ProviderHasModel(entry.canonicalName(), model, defaultModelCatalogTTL) {
			entries = append(entries, entry)
		}
	}
	return entries
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
		return pkg.NAME
	}
	return name
}
