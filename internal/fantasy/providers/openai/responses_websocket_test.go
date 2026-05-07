package openai

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"charm.land/fantasy"
	"charm.land/fantasy/internal/testcmp"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

func TestResponsesWebSocketURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		base string
		want string
	}{
		{"default", "", "wss://api.openai.com/v1/responses"},
		{"https base", "https://api.openai.com/v1", "wss://api.openai.com/v1/responses"},
		{"http local", "http://127.0.0.1:8080/v1", "ws://127.0.0.1:8080/v1/responses"},
		{"already responses", "wss://api.openai.com/v1/responses", "wss://api.openai.com/v1/responses"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := responsesWebSocketURL(tt.base)
			require.NoError(t, err)
			testcmp.RequireEqual(t, tt.want, got)
		})
	}
}

func TestResponsesWebSocketURLRejectsNonOpenAIHosts(t *testing.T) {
	t.Parallel()

	if _, err := responsesWebSocketURL("https://proxy.example/v1"); err == nil {
		t.Fatal("expected non-OpenAI responses websocket host to be rejected")
	}
}

func TestResponsesStreamWebSocketLocalServer(t *testing.T) {
	received := make(chan struct {
		path    string
		headers http.Header
		body    map[string]any
	}, 1)

	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()

		var body map[string]any
		require.NoError(t, conn.ReadJSON(&body))
		received <- struct {
			path    string
			headers http.Header
			body    map[string]any
		}{path: r.URL.Path, headers: r.Header.Clone(), body: body}

		for _, event := range []map[string]any{
			{"type": "response.created", "response": map[string]any{"id": "resp_ws"}},
			{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"id": "msg_ws", "type": "message"}},
			{"type": "response.output_text.delta", "item_id": "msg_ws", "delta": "hello"},
			{"type": "response.output_item.done", "output_index": 0, "item": map[string]any{"id": "msg_ws", "type": "message"}},
			{"type": "response.completed", "response": map[string]any{"id": "resp_ws", "usage": map[string]any{"input_tokens": 11, "output_tokens": 2, "input_tokens_details": map[string]any{"cached_tokens": 3}, "output_tokens_details": map[string]any{"reasoning_tokens": 1}}}},
		} {
			require.NoError(t, conn.WriteJSON(event))
		}
	}))
	defer server.Close()

	provider, err := New(WithBaseURL(server.URL+"/v1"), WithAPIKey("test-key"), WithUseResponsesAPI(), WithResponsesWebSocket())
	require.NoError(t, err)
	lm, err := provider.LanguageModel(context.Background(), "gpt-5.5")
	require.NoError(t, err)
	stream, err := lm.Stream(context.Background(), fantasy.Call{Prompt: fantasy.Prompt{{Role: fantasy.MessageRoleUser, Content: []fantasy.MessagePart{fantasy.TextPart{Text: "hello"}}}}})
	require.NoError(t, err)

	var parts []fantasy.StreamPart
	stream(func(part fantasy.StreamPart) bool {
		parts = append(parts, part)
		return true
	})

	request := <-received
	testcmp.RequireEqual(t, "/v1/responses", request.path)
	testcmp.RequireEqual(t, "Bearer test-key", request.headers.Get("Authorization"))
	testcmp.RequireEqual(t, "response.create", request.body["type"])
	testcmp.RequireEqual(t, "gpt-5.5", request.body["model"])
	require.NotContains(t, request.body, "stream")
	require.NotContains(t, request.body, "background")

	var textDelta, finish *fantasy.StreamPart
	for i := range parts {
		switch parts[i].Type {
		case fantasy.StreamPartTypeTextDelta:
			textDelta = &parts[i]
		case fantasy.StreamPartTypeFinish:
			finish = &parts[i]
		case fantasy.StreamPartTypeError:
			t.Fatalf("unexpected stream error: %v", parts[i].Error)
		}
	}
	require.NotNil(t, textDelta)
	testcmp.RequireEqual(t, "hello", textDelta.Delta)
	require.NotNil(t, finish)
	testcmp.RequireEqual(t, fantasy.FinishReasonStop, finish.FinishReason)
	testcmp.RequireEqual(t, int64(8), finish.Usage.InputTokens)
	testcmp.RequireEqual(t, int64(2), finish.Usage.OutputTokens)
	testcmp.RequireEqual(t, int64(1), finish.Usage.ReasoningTokens)
	testcmp.RequireEqual(t, int64(3), finish.Usage.CacheReadTokens)
}

func TestResponsesWebSocketProxyAndDialErrorHelpers(t *testing.T) {
	proxyURL, err := url.Parse("http://proxy.example:8080")
	require.NoError(t, err)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(proxyURL)

	proxy := responsesWebSocketProxy(&http.Client{Transport: transport})
	got, err := proxy(&http.Request{URL: &url.URL{Scheme: "wss", Host: "api.openai.com"}})
	require.NoError(t, err)
	require.NotNil(t, got)
	testcmp.RequireEqual(t, proxyURL.String(), got.String())

	body := &closeTrackingReadCloser{Reader: strings.NewReader("bad handshake")}
	err = responsesWebSocketDialError(errors.New("dial failed"), &http.Response{Status: "403 Forbidden", Body: body})
	require.Error(t, err)
	require.True(t, body.closed)
}

type closeTrackingReadCloser struct {
	*strings.Reader
	closed bool
}

func (r *closeTrackingReadCloser) Close() error {
	r.closed = true
	return nil
}

var _ io.Closer = (*closeTrackingReadCloser)(nil)
