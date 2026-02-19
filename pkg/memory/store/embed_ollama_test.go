package store

import (
	"context"
	jsonv2 "github.com/go-json-experiment/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOllamaEmbedder_Embed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			t.Errorf("expected /api/embed, got %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}

		var req ollamaEmbedRequest
		jsonv2.UnmarshalRead(r.Body, &req)
		if req.Model != "test-model" {
			t.Errorf("expected model 'test-model', got %q", req.Model)
		}

		respData, _ := jsonv2.Marshal(ollamaEmbedResponse{
			Embeddings: [][]float32{{0.1, 0.2, 0.3, 0.4}},
		})
		w.Write(respData)
	}))
	defer srv.Close()

	e := NewOllamaEmbedder(OllamaEmbedderConfig{
		Base:  srv.URL,
		Model: "test-model",
	})

	vec, err := e.Embed(context.Background(), "hello world")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vec) != 4 {
		t.Fatalf("expected 4 dims, got %d", len(vec))
	}
	if vec[0] != 0.1 {
		t.Errorf("expected 0.1, got %f", vec[0])
	}

	// Dimensions should auto-detect
	if e.Dimensions() != 4 {
		t.Errorf("expected 4 dims, got %d", e.Dimensions())
	}
}

func TestOllamaEmbedder_EmbedBatch(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		respData, _ := jsonv2.Marshal(ollamaEmbedResponse{
			Embeddings: [][]float32{{float32(callCount) * 0.1}},
		})
		w.Write(respData)
	}))
	defer srv.Close()

	e := NewOllamaEmbedder(OllamaEmbedderConfig{Base: srv.URL})

	vecs, err := e.EmbedBatch(context.Background(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	if len(vecs) != 3 {
		t.Fatalf("expected 3 vectors, got %d", len(vecs))
	}
	if callCount != 3 {
		t.Errorf("expected 3 API calls (sequential), got %d", callCount)
	}
}

func TestOllamaEmbedder_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("model not found"))
	}))
	defer srv.Close()

	e := NewOllamaEmbedder(OllamaEmbedderConfig{Base: srv.URL})
	_, err := e.Embed(context.Background(), "test")
	if err == nil {
		t.Error("expected error for server error response")
	}
}

func TestOllamaEmbedder_EmptyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		respData, _ := jsonv2.Marshal(ollamaEmbedResponse{Embeddings: [][]float32{}})
		w.Write(respData)
	}))
	defer srv.Close()

	e := NewOllamaEmbedder(OllamaEmbedderConfig{Base: srv.URL})
	_, err := e.Embed(context.Background(), "test")
	if err == nil {
		t.Error("expected error for empty embeddings")
	}
}

func TestOllamaEmbedder_DefaultModel(t *testing.T) {
	e := NewOllamaEmbedder(OllamaEmbedderConfig{})
	if e.Model() != defaultOllamaModel {
		t.Errorf("expected %q, got %q", defaultOllamaModel, e.Model())
	}
}
