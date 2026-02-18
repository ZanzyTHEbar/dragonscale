package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/sipeed/picoclaw/pkg/memory"
)

const (
	defaultOllamaBase  = "http://localhost:11434"
	defaultOllamaModel = "nomic-embed-text"
)

// OllamaEmbedder implements memory.EmbeddingProvider using Ollama's /api/embed endpoint.
type OllamaEmbedder struct {
	base   string
	model  string
	dims   int
	client *http.Client
}

// OllamaEmbedderConfig configures the Ollama embedding provider.
type OllamaEmbedderConfig struct {
	Base  string // API base URL. Empty uses default (http://localhost:11434).
	Model string // Model name. Empty uses "nomic-embed-text".
	Dims  int    // Expected dimensions. Zero auto-detects on first call.
}

// NewOllamaEmbedder creates an Ollama embedding provider.
func NewOllamaEmbedder(cfg OllamaEmbedderConfig) *OllamaEmbedder {
	base := cfg.Base
	if base == "" {
		base = defaultOllamaBase
	}
	model := cfg.Model
	if model == "" {
		model = defaultOllamaModel
	}
	return &OllamaEmbedder{
		base:  base,
		model: model,
		dims:  cfg.Dims,
		client: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

type ollamaEmbedRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type ollamaEmbedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

func (o *OllamaEmbedder) Embed(ctx context.Context, text string) (memory.Embedding, error) {
	vecs, err := o.embedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("ollama returned no embeddings")
	}
	return vecs[0], nil
}

func (o *OllamaEmbedder) EmbedBatch(ctx context.Context, texts []string) ([]memory.Embedding, error) {
	return o.embedBatch(ctx, texts)
}

func (o *OllamaEmbedder) embedBatch(ctx context.Context, texts []string) ([]memory.Embedding, error) {
	results := make([]memory.Embedding, len(texts))
	for i, text := range texts {
		vec, err := o.embedSingle(ctx, text)
		if err != nil {
			return nil, fmt.Errorf("embed text %d: %w", i, err)
		}
		results[i] = vec

		if o.dims == 0 && len(vec) > 0 {
			o.dims = len(vec)
		}
	}
	return results, nil
}

func (o *OllamaEmbedder) embedSingle(ctx context.Context, text string) (memory.Embedding, error) {
	body, err := json.Marshal(ollamaEmbedRequest{
		Model: o.model,
		Input: text,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.base+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result ollamaEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(result.Embeddings) == 0 {
		return nil, fmt.Errorf("ollama returned empty embeddings array")
	}

	return memory.Embedding(result.Embeddings[0]), nil
}

func (o *OllamaEmbedder) Dimensions() int { return o.dims }
func (o *OllamaEmbedder) Model() string   { return o.model }
