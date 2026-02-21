package store

import (
	"net/http"
	"net/http/httptest"
	"testing"

	jsonv2 "github.com/go-json-experiment/json"

	"github.com/ZanzyTHEbar/dragonscale/pkg/config"
)

func TestNewEmbedderFromConfig_EmptyProvider(t *testing.T) {
	t.Parallel()
	emb, err := NewEmbedderFromConfig(config.EmbeddingConfig{}, config.ProvidersConfig{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if emb != nil {
		t.Error("expected nil embedder for empty provider")
	}
}

func TestNewEmbedderFromConfig_UnknownProvider(t *testing.T) {
	t.Parallel()
	_, err := NewEmbedderFromConfig(config.EmbeddingConfig{Provider: "nonexistent"}, config.ProvidersConfig{})
	if err == nil {
		t.Error("expected error for unknown provider")
	}
}

func TestNewEmbedderFromConfig_OpenAI_NoKey(t *testing.T) {
	t.Parallel()
	_, err := NewEmbedderFromConfig(
		config.EmbeddingConfig{Provider: "openai"},
		config.ProvidersConfig{},
	)
	if err == nil {
		t.Error("expected error when OpenAI has no API key")
	}
}

func TestNewEmbedderFromConfig_OpenAI_FallbackKey(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		respData, _ := jsonv2.Marshal(map[string]interface{}{
			"data": []map[string]interface{}{
				{"index": 0, "embedding": []float32{0.1, 0.2, 0.3}},
			},
		})
		w.Write(respData)
	}))
	defer srv.Close()

	emb, err := NewEmbedderFromConfig(
		config.EmbeddingConfig{
			Provider: "openai",
			APIBase:  srv.URL,
		},
		config.ProvidersConfig{
			OpenAI: config.OpenAIProviderConfig{
				ProviderConfig: config.ProviderConfig{APIKey: "test-key"},
			},
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if emb == nil {
		t.Fatal("expected non-nil embedder")
	}
	if emb.Model() != defaultOpenAIModel {
		t.Errorf("expected default model %q, got %q", defaultOpenAIModel, emb.Model())
	}
}

func TestNewEmbedderFromConfig_Ollama(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		respData, _ := jsonv2.Marshal(map[string]interface{}{
			"embeddings": [][]float32{{0.1, 0.2, 0.3}},
		})
		w.Write(respData)
	}))
	defer srv.Close()

	emb, err := NewEmbedderFromConfig(
		config.EmbeddingConfig{
			Provider: "ollama",
			APIBase:  srv.URL,
			Model:    "test-embed",
		},
		config.ProvidersConfig{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if emb == nil {
		t.Fatal("expected non-nil embedder")
	}
	if emb.Model() != "test-embed" {
		t.Errorf("expected 'test-embed', got %q", emb.Model())
	}
}

func TestNewEmbedderFromConfig_Ollama_FallbackBase(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		respData, _ := jsonv2.Marshal(map[string]interface{}{
			"embeddings": [][]float32{{0.5}},
		})
		w.Write(respData)
	}))
	defer srv.Close()

	emb, err := NewEmbedderFromConfig(
		config.EmbeddingConfig{Provider: "ollama"},
		config.ProvidersConfig{
			Ollama: config.ProviderConfig{APIBase: srv.URL},
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if emb == nil {
		t.Fatal("expected non-nil embedder")
	}
}

func TestNewEmbedderFromConfig_CaseInsensitive(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		respData, _ := jsonv2.Marshal(map[string]interface{}{
			"embeddings": [][]float32{{0.1}},
		})
		w.Write(respData)
	}))
	defer srv.Close()

	emb, err := NewEmbedderFromConfig(
		config.EmbeddingConfig{Provider: "  Ollama  ", APIBase: srv.URL},
		config.ProvidersConfig{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if emb == nil {
		t.Fatal("expected non-nil embedder for case-insensitive 'Ollama'")
	}
}
