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
	defaultOpenAIBase  = "https://api.openai.com/v1"
	defaultOpenAIModel = "text-embedding-3-small"
)

// OpenAIEmbedder implements memory.EmbeddingProvider using the OpenAI /v1/embeddings API.
// Compatible with OpenAI, Azure OpenAI, OpenRouter, and any OpenAI-compatible endpoint.
type OpenAIEmbedder struct {
	base   string
	model  string
	apiKey string
	dims   int
	client *http.Client
}

// OpenAIEmbedderConfig configures the OpenAI embedding provider.
type OpenAIEmbedderConfig struct {
	Base   string // API base URL. Empty uses "https://api.openai.com/v1".
	Model  string // Model name. Empty uses "text-embedding-3-small".
	APIKey string // Required API key.
	Dims   int    // Expected dimensions. Zero auto-detects on first call.
}

// NewOpenAIEmbedder creates an OpenAI-compatible embedding provider.
func NewOpenAIEmbedder(cfg OpenAIEmbedderConfig) *OpenAIEmbedder {
	base := cfg.Base
	if base == "" {
		base = defaultOpenAIBase
	}
	model := cfg.Model
	if model == "" {
		model = defaultOpenAIModel
	}
	return &OpenAIEmbedder{
		base:   base,
		model:  model,
		apiKey: cfg.APIKey,
		dims:   cfg.Dims,
		client: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

type openAIEmbedRequest struct {
	Input          interface{} `json:"input"` // string or []string
	Model          string      `json:"model"`
	EncodingFormat string      `json:"encoding_format,omitempty"`
}

type openAIEmbedResponse struct {
	Data  []openAIEmbedData `json:"data"`
	Usage openAIEmbedUsage  `json:"usage"`
}

type openAIEmbedData struct {
	Index     int       `json:"index"`
	Embedding []float32 `json:"embedding"`
}

type openAIEmbedUsage struct {
	PromptTokens int `json:"prompt_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

func (o *OpenAIEmbedder) Embed(ctx context.Context, text string) (memory.Embedding, error) {
	vecs, err := o.call(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("openai returned no embeddings")
	}
	return vecs[0], nil
}

func (o *OpenAIEmbedder) EmbedBatch(ctx context.Context, texts []string) ([]memory.Embedding, error) {
	return o.call(ctx, texts)
}

func (o *OpenAIEmbedder) call(ctx context.Context, texts []string) ([]memory.Embedding, error) {
	var input interface{}
	if len(texts) == 1 {
		input = texts[0]
	} else {
		input = texts
	}

	body, err := json.Marshal(openAIEmbedRequest{
		Input:          input,
		Model:          o.model,
		EncodingFormat: "float",
	})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.base+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if o.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+o.apiKey)
	}

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openai returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result openAIEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	embeddings := make([]memory.Embedding, len(result.Data))
	for _, d := range result.Data {
		if d.Index < len(embeddings) {
			embeddings[d.Index] = memory.Embedding(d.Embedding)
		}
	}

	if o.dims == 0 && len(embeddings) > 0 && len(embeddings[0]) > 0 {
		o.dims = len(embeddings[0])
	}

	return embeddings, nil
}

func (o *OpenAIEmbedder) Dimensions() int { return o.dims }
func (o *OpenAIEmbedder) Model() string   { return o.model }
