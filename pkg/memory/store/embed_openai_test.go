package store

import (
	"context"
	jsonv2 "github.com/go-json-experiment/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAIEmbedder_Embed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" {
			t.Errorf("expected /embeddings, got %s", r.URL.Path)
		}

		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-key" {
			t.Errorf("expected 'Bearer test-key', got %q", auth)
		}

		var req openAIEmbedRequest
		jsonv2.UnmarshalRead(r.Body, &req)
		if req.Model != "test-embed" {
			t.Errorf("expected model 'test-embed', got %q", req.Model)
		}

		respData, _ := jsonv2.Marshal(openAIEmbedResponse{
			Data: []openAIEmbedData{
				{Index: 0, Embedding: []float32{0.5, 0.6, 0.7}},
			},
		})
		w.Write(respData)
	}))
	defer srv.Close()

	e := NewOpenAIEmbedder(OpenAIEmbedderConfig{
		Base:   srv.URL,
		Model:  "test-embed",
		APIKey: "test-key",
	})

	vec, err := e.Embed(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vec) != 3 {
		t.Fatalf("expected 3 dims, got %d", len(vec))
	}
	if vec[0] != 0.5 {
		t.Errorf("expected 0.5, got %f", vec[0])
	}
	if e.Dimensions() != 3 {
		t.Errorf("expected 3 dims auto-detected, got %d", e.Dimensions())
	}
}

func TestOpenAIEmbedder_EmbedBatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req openAIEmbedRequest
		jsonv2.UnmarshalRead(r.Body, &req)

		// Batch request should send array
		texts, ok := req.Input.([]interface{})
		if !ok {
			t.Fatalf("expected array input for batch, got %T", req.Input)
		}

		data := make([]openAIEmbedData, len(texts))
		for i := range texts {
			data[i] = openAIEmbedData{
				Index:     i,
				Embedding: []float32{float32(i) * 0.1},
			}
		}

		respData, _ := jsonv2.Marshal(openAIEmbedResponse{Data: data})
		w.Write(respData)
	}))
	defer srv.Close()

	e := NewOpenAIEmbedder(OpenAIEmbedderConfig{
		Base:   srv.URL,
		APIKey: "k",
	})

	vecs, err := e.EmbedBatch(context.Background(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	if len(vecs) != 3 {
		t.Fatalf("expected 3 vectors, got %d", len(vecs))
	}
}

func TestOpenAIEmbedder_SingleTextNotArray(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req openAIEmbedRequest
		jsonv2.UnmarshalRead(r.Body, &req)

		// Single text should be sent as string, not array
		if _, ok := req.Input.(string); !ok {
			t.Errorf("expected string input for single text, got %T", req.Input)
		}

		respData, _ := jsonv2.Marshal(openAIEmbedResponse{
			Data: []openAIEmbedData{{Index: 0, Embedding: []float32{1.0}}},
		})
		w.Write(respData)
	}))
	defer srv.Close()

	e := NewOpenAIEmbedder(OpenAIEmbedderConfig{Base: srv.URL, APIKey: "k"})
	_, err := e.Embed(context.Background(), "single")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
}

func TestOpenAIEmbedder_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}))
	defer srv.Close()

	e := NewOpenAIEmbedder(OpenAIEmbedderConfig{Base: srv.URL, APIKey: "bad"})
	_, err := e.Embed(context.Background(), "test")
	if err == nil {
		t.Error("expected error for 401 response")
	}
}

func TestOpenAIEmbedder_DefaultModel(t *testing.T) {
	e := NewOpenAIEmbedder(OpenAIEmbedderConfig{APIKey: "k"})
	if e.Model() != defaultOpenAIModel {
		t.Errorf("expected %q, got %q", defaultOpenAIModel, e.Model())
	}
}

func TestOpenAIEmbedder_NoAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("expected no auth header when key is empty")
		}
		respData, _ := jsonv2.Marshal(openAIEmbedResponse{
			Data: []openAIEmbedData{{Index: 0, Embedding: []float32{1.0}}},
		})
		w.Write(respData)
	}))
	defer srv.Close()

	e := NewOpenAIEmbedder(OpenAIEmbedderConfig{Base: srv.URL})
	_, err := e.Embed(context.Background(), "test")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
}
