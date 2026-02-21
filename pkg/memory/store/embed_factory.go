package store

import (
	"fmt"
	"strings"

	"github.com/ZanzyTHEbar/dragonscale/pkg/config"
	"github.com/ZanzyTHEbar/dragonscale/pkg/logger"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
)

// NewEmbedderFromConfig creates an EmbeddingProvider from config, wrapped
// with the LRU CachedEmbedder. Returns nil if no provider is configured
// (FTS5-only search mode).
//
// The providersCfg parameter provides fallback API keys from the providers section
// when the embedding-specific config doesn't have its own key.
func NewEmbedderFromConfig(embCfg config.EmbeddingConfig, providersCfg config.ProvidersConfig) (memory.EmbeddingProvider, error) {
	provider := strings.ToLower(strings.TrimSpace(embCfg.Provider))
	if provider == "" {
		logger.InfoCF("memory", "No embedding provider configured, archival search will use FTS5 only", nil)
		return nil, nil
	}

	var inner memory.EmbeddingProvider

	switch provider {
	case "ollama":
		base := embCfg.APIBase
		if base == "" && providersCfg.Ollama.APIBase != "" {
			base = providersCfg.Ollama.APIBase
		}
		inner = NewOllamaEmbedder(OllamaEmbedderConfig{
			Base:  base,
			Model: embCfg.Model,
		})

	case "openai":
		apiKey := embCfg.APIKey
		if apiKey == "" {
			apiKey = providersCfg.OpenAI.APIKey
		}
		base := embCfg.APIBase
		if base == "" && providersCfg.OpenAI.APIBase != "" {
			base = providersCfg.OpenAI.APIBase
		}
		if apiKey == "" {
			return nil, fmt.Errorf("openai embedding provider requires an API key (set memory.embedding.api_key or providers.openai.api_key)")
		}
		inner = NewOpenAIEmbedder(OpenAIEmbedderConfig{
			Base:   base,
			Model:  embCfg.Model,
			APIKey: apiKey,
		})

	default:
		return nil, fmt.Errorf("unknown embedding provider %q (supported: ollama, openai)", provider)
	}

	cached := NewCachedEmbedder(inner, DefaultCachedEmbedderConfig())

	logger.InfoCF("memory", "Embedding provider initialized",
		map[string]interface{}{
			"provider": provider,
			"model":    inner.Model(),
		})

	return cached, nil
}
