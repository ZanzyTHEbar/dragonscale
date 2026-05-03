//go:build integration && containers

package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	defaultOllamaContainerImage = "ollama/ollama:0.12.6"
	ollamaContainerTimeout      = 15 * time.Minute
	ollamaStartupTimeout        = 5 * time.Minute
	ollamaModelPullTimeout      = 10 * time.Minute
)

func TestOllamaContainerReadiness(t *testing.T) {
	if os.Getenv("DRAGONSCALE_RUN_CONTAINER_TESTS") != "1" {
		t.Skip("set DRAGONSCALE_RUN_CONTAINER_TESTS=1 to run Docker-backed container tests")
	}

	ctx, cancel := context.WithTimeout(t.Context(), ollamaContainerTimeout)
	defer cancel()

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        ollamaContainerImage(),
			ExposedPorts: []string{"11434/tcp"},
			WaitingFor:   wait.ForHTTP("/api/tags").WithPort("11434/tcp").WithStartupTimeout(ollamaStartupTimeout),
		},
		Started: true,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		terminateCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := container.Terminate(terminateCtx); err != nil {
			t.Logf("terminate Ollama container: %v", err)
		}
	})

	baseURL, err := container.Endpoint(ctx, "http")
	require.NoError(t, err)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/tags", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	model := os.Getenv("DRAGONSCALE_OLLAMA_CONTAINER_MODEL")
	if model == "" {
		t.Log("readiness smoke passed; set DRAGONSCALE_OLLAMA_CONTAINER_MODEL to also exercise OllamaEmbedder against a pulled model")
		return
	}

	pullCtx, cancel := context.WithTimeout(ctx, ollamaModelPullTimeout)
	defer cancel()
	require.NoError(t, pullOllamaModel(pullCtx, baseURL, model))

	embedder := NewOllamaEmbedder(OllamaEmbedderConfig{Base: baseURL, Model: model})
	embedding, err := embedder.Embed(ctx, "container smoke test")
	require.NoError(t, err)
	require.NotEmpty(t, embedding)
}

func ollamaContainerImage() string {
	if image := os.Getenv("DRAGONSCALE_OLLAMA_CONTAINER_IMAGE"); image != "" {
		return image
	}
	return defaultOllamaContainerImage
}

func pullOllamaModel(ctx context.Context, baseURL, model string) error {
	body, err := json.Marshal(map[string]any{
		"name":   model,
		"stream": false,
	})
	if err != nil {
		return fmt.Errorf("marshal pull request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/pull", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create pull request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("pull model %q: %w", model, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("pull model %q returned %d: %s", model, resp.StatusCode, string(respBody))
	}
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read pull response for %q: %w", model, err)
	}
	if len(respBody) == 0 {
		return nil
	}
	var pullResp struct {
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(respBody, &pullResp); err != nil {
		return fmt.Errorf("decode pull response for %q: %w", model, err)
	}
	if pullResp.Error != "" {
		return fmt.Errorf("pull model %q: %s", model, pullResp.Error)
	}
	return nil
}
