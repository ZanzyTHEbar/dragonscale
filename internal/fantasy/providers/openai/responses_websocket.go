package openai

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/internal/httpheaders"
	"github.com/charmbracelet/openai-go/option"
	"github.com/charmbracelet/openai-go/responses"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

func (o responsesLanguageModel) streamWebSocket(ctx context.Context, params *responses.ResponseNewParams, warnings []fantasy.CallWarning, call fantasy.Call) fantasy.StreamResponse {
	return func(yield func(fantasy.StreamPart) bool) {
		if len(warnings) > 0 {
			if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeWarnings, Warnings: warnings}) {
				return
			}
		}

		wsURL, err := responsesWebSocketURL(o.transportConfig.baseURL)
		if err != nil {
			yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeError, Error: err})
			return
		}

		headers := o.responsesWebSocketHeaders(call)
		conn, resp, err := responsesWebSocketDialer(o.transportConfig).DialContext(ctx, wsURL, headers)
		if err != nil {
			yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeError, Error: responsesWebSocketDialError(err, resp)})
			return
		}
		defer conn.Close()
		done := make(chan struct{})
		defer close(done)
		go func() {
			select {
			case <-ctx.Done():
				_ = conn.Close()
			case <-done:
			}
		}()

		createEvent, err := responsesWebSocketCreateEvent(params)
		if err != nil {
			yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeError, Error: err})
			return
		}
		if err := conn.WriteJSON(createEvent); err != nil {
			yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeError, Error: fmt.Errorf("write responses websocket create event: %w", err)})
			return
		}

		state := responsesWebSocketStreamState{
			finishReason:     fantasy.FinishReasonUnknown,
			ongoingToolCalls: make(map[int64]*ongoingToolCall),
			activeReasoning:  make(map[string]*reasoningState),
		}

		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				if ctx.Err() != nil {
					err = ctx.Err()
				}
				yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeError, Error: fmt.Errorf("read responses websocket event: %w", err)})
				return
			}

			result, err := state.handle(data, yield)
			if err != nil {
				yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeError, Error: err})
				return
			}
			switch result {
			case responsesWebSocketHandleStopped:
				return
			case responsesWebSocketHandleComplete:
				yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, Usage: state.usage, FinishReason: state.finishReason, ProviderMetadata: responsesProviderMetadata(state.responseID)})
				return
			}
		}
	}
}

func responsesWebSocketURL(baseURL string) (string, error) {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultURL
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parse responses websocket base URL: %w", err)
	}
	if !responsesWebSocketAllowedHost(u.Hostname()) {
		return "", fmt.Errorf("refusing responses websocket connection to %q; base URL must be api.openai.com", u.Host)
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("unsupported responses websocket URL scheme %q", u.Scheme)
	}
	u.Path = strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(u.Path, "/responses") {
		u.Path += "/responses"
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func responsesWebSocketAllowedHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "api.openai.com" || host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func responsesWebSocketCreateEvent(params *responses.ResponseNewParams) (map[string]any, error) {
	data, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("marshal responses websocket create event: %w", err)
	}
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		return nil, fmt.Errorf("decode responses websocket create event: %w", err)
	}
	body["type"] = "response.create"
	delete(body, "stream")
	delete(body, "background")
	return body, nil
}

func (o responsesLanguageModel) responsesWebSocketHeaders(call fantasy.Call) http.Header {
	headers := http.Header{}
	for key, value := range o.transportConfig.headers {
		if value != "" {
			headers.Set(key, value)
		}
	}
	if o.transportConfig.apiKey != "" {
		headers.Set("Authorization", "Bearer "+o.transportConfig.apiKey)
	}
	if ua, ok := callUserAgent(call); ok {
		headers.Set("User-Agent", ua)
	}
	return headers
}

func responsesWebSocketDialer(config responsesTransportConfig) *websocket.Dialer {
	dialer := *websocket.DefaultDialer
	dialer.Proxy = config.proxy
	if config.netDialContext != nil {
		dialer.NetDialContext = config.netDialContext
	}
	if config.netDialTLSContext != nil {
		dialer.NetDialTLSContext = config.netDialTLSContext
	}
	if config.tlsClientConfig != nil {
		dialer.TLSClientConfig = config.tlsClientConfig
	}
	if config.handshakeTimeout > 0 {
		dialer.HandshakeTimeout = config.handshakeTimeout
	}
	return &dialer
}

func responsesWebSocketNetDialContext(client option.HTTPClient) func(context.Context, string, string) (net.Conn, error) {
	transport, ok := responsesWebSocketHTTPTransport(client)
	if !ok {
		return nil
	}
	return transport.DialContext
}

func responsesWebSocketNetDialTLSContext(client option.HTTPClient) func(context.Context, string, string) (net.Conn, error) {
	transport, ok := responsesWebSocketHTTPTransport(client)
	if !ok || transport.Proxy != nil {
		return nil
	}
	return transport.DialTLSContext
}

func responsesWebSocketProxy(client option.HTTPClient) func(*http.Request) (*url.URL, error) {
	transport, ok := responsesWebSocketHTTPTransport(client)
	if !ok {
		return http.ProxyFromEnvironment
	}
	return transport.Proxy
}

func responsesWebSocketTLSClientConfig(client option.HTTPClient) *tls.Config {
	transport, ok := responsesWebSocketHTTPTransport(client)
	if !ok {
		return nil
	}
	return transport.TLSClientConfig
}

func responsesWebSocketHandshakeTimeout(client option.HTTPClient) time.Duration {
	httpClient, ok := client.(*http.Client)
	if !ok || httpClient == nil || httpClient.Timeout <= 0 {
		return 0
	}
	return httpClient.Timeout
}

func responsesWebSocketHTTPTransport(client option.HTTPClient) (*http.Transport, bool) {
	httpClient, ok := client.(*http.Client)
	if !ok || httpClient == nil {
		return nil, false
	}
	transport, ok := httpClient.Transport.(*http.Transport)
	return transport, ok && transport != nil
}

func callUserAgent(call fantasy.Call) (string, bool) {
	return httpheaders.CallUserAgent(call.UserAgent)
}

func responsesWebSocketDialError(err error, resp *http.Response) error {
	if resp == nil {
		return fmt.Errorf("dial responses websocket: %w", err)
	}
	if resp.Body != nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
	return fmt.Errorf("dial responses websocket: %w (status: %s)", err, resp.Status)
}

type responsesWebSocketHandleResult int

const (
	responsesWebSocketHandleContinue responsesWebSocketHandleResult = iota
	responsesWebSocketHandleComplete
	responsesWebSocketHandleStopped
)

type responsesWebSocketStreamState struct {
	finishReason     fantasy.FinishReason
	usage            fantasy.Usage
	responseID       string
	hasFunctionCall  bool
	ongoingToolCalls map[int64]*ongoingToolCall
	activeReasoning  map[string]*reasoningState
}

type responsesWebSocketEvent struct {
	Type         string                  `json:"type"`
	Response     responses.Response      `json:"response"`
	OutputIndex  int64                   `json:"output_index"`
	Item         responsesWebSocketItem  `json:"item"`
	ItemID       string                  `json:"item_id"`
	Delta        string                  `json:"delta"`
	SummaryIndex int64                   `json:"summary_index"`
	Annotation   map[string]any          `json:"annotation"`
	Error        responsesWebSocketError `json:"error"`
}

type responsesWebSocketItem struct {
	ID               string                                  `json:"id"`
	Type             string                                  `json:"type"`
	Name             string                                  `json:"name"`
	CallID           string                                  `json:"call_id"`
	Arguments        any                                     `json:"arguments"`
	Action           responses.ResponseOutputItemUnionAction `json:"action"`
	EncryptedContent string                                  `json:"encrypted_content"`
}

type responsesWebSocketError struct {
	Message string `json:"message"`
	Code    string `json:"code"`
}

func (s *responsesWebSocketStreamState) handle(data []byte, yield func(fantasy.StreamPart) bool) (responsesWebSocketHandleResult, error) {
	var event responsesWebSocketEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return responsesWebSocketHandleContinue, fmt.Errorf("decode responses websocket event: %w", err)
	}

	switch event.Type {
	case "response.created":
		s.responseID = event.Response.ID
	case "response.output_item.added":
		if !s.handleOutputItemAdded(event, yield) {
			return responsesWebSocketHandleStopped, nil
		}
	case "response.output_item.done":
		if !s.handleOutputItemDone(event, yield) {
			return responsesWebSocketHandleStopped, nil
		}
	case "response.function_call_arguments.delta":
		if tc := s.ongoingToolCalls[event.OutputIndex]; tc != nil {
			if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeToolInputDelta, ID: tc.toolCallID, Delta: event.Delta}) {
				return responsesWebSocketHandleStopped, nil
			}
		}
	case "response.output_text.delta":
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextDelta, ID: event.ItemID, Delta: event.Delta}) {
			return responsesWebSocketHandleStopped, nil
		}
	case "response.output_text.annotation.added":
		if !s.handleAnnotation(event.Annotation, yield) {
			return responsesWebSocketHandleStopped, nil
		}
	case "response.reasoning_summary_part.added":
		state := s.activeReasoning[event.ItemID]
		if state != nil {
			state.metadata.Summary = append(state.metadata.Summary, "")
			if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeReasoningDelta, ID: event.ItemID, Delta: "\n", ProviderMetadata: fantasy.ProviderMetadata{Name: state.metadata}}) {
				return responsesWebSocketHandleStopped, nil
			}
		}
	case "response.reasoning_summary_text.delta":
		state := s.activeReasoning[event.ItemID]
		if state != nil {
			if len(state.metadata.Summary)-1 >= int(event.SummaryIndex) {
				state.metadata.Summary[event.SummaryIndex] += event.Delta
			}
			if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeReasoningDelta, ID: event.ItemID, Delta: event.Delta, ProviderMetadata: fantasy.ProviderMetadata{Name: state.metadata}}) {
				return responsesWebSocketHandleStopped, nil
			}
		}
	case "response.completed", "response.incomplete":
		s.responseID = event.Response.ID
		s.finishReason = mapResponsesFinishReason(event.Response.IncompleteDetails.Reason, s.hasFunctionCall)
		s.usage = responsesUsage(event.Response)
		return responsesWebSocketHandleComplete, nil
	case "response.failed":
		message := event.Response.Error.Message
		code := event.Response.Error.Code
		if message == "" {
			message = "response failed"
		}
		return responsesWebSocketHandleContinue, fmt.Errorf("response error: %s (code: %s)", message, code)
	case "error":
		message := event.Error.Message
		if message == "" {
			message = "unknown responses websocket error"
		}
		return responsesWebSocketHandleContinue, fmt.Errorf("response error: %s (code: %s)", message, event.Error.Code)
	}

	return responsesWebSocketHandleContinue, nil
}

func (s *responsesWebSocketStreamState) handleOutputItemAdded(event responsesWebSocketEvent, yield func(fantasy.StreamPart) bool) bool {
	switch event.Item.Type {
	case "function_call":
		s.ongoingToolCalls[event.OutputIndex] = &ongoingToolCall{toolName: event.Item.Name, toolCallID: event.Item.CallID}
		return yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeToolInputStart, ID: event.Item.CallID, ToolCallName: event.Item.Name})
	case "web_search_call":
		return yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeToolInputStart, ID: event.Item.ID, ToolCallName: "web_search", ProviderExecuted: true})
	case "message":
		return yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextStart, ID: event.Item.ID})
	case "reasoning":
		metadata := &ResponsesReasoningMetadata{ItemID: event.Item.ID, Summary: []string{}}
		if event.Item.EncryptedContent != "" {
			metadata.EncryptedContent = &event.Item.EncryptedContent
		}
		s.activeReasoning[event.Item.ID] = &reasoningState{metadata: metadata}
		return yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeReasoningStart, ID: event.Item.ID, ProviderMetadata: fantasy.ProviderMetadata{Name: metadata}})
	}
	return true
}

func (s *responsesWebSocketStreamState) handleOutputItemDone(event responsesWebSocketEvent, yield func(fantasy.StreamPart) bool) bool {
	switch event.Item.Type {
	case "function_call":
		tc := s.ongoingToolCalls[event.OutputIndex]
		if tc == nil {
			return true
		}
		delete(s.ongoingToolCalls, event.OutputIndex)
		s.hasFunctionCall = true
		return yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeToolInputEnd, ID: event.Item.CallID}) && yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeToolCall, ID: event.Item.CallID, ToolCallName: event.Item.Name, ToolCallInput: responsesWebSocketArgumentString(event.Item.Arguments)})
	case "web_search_call":
		return yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeToolInputEnd, ID: event.Item.ID}) && yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeToolCall, ID: event.Item.ID, ToolCallName: "web_search", ProviderExecuted: true}) && yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeToolResult, ID: event.Item.ID, ToolCallName: "web_search", ProviderExecuted: true, ProviderMetadata: fantasy.ProviderMetadata{Name: webSearchCallToMetadata(event.Item.ID, event.Item.Action)}})
	case "message":
		return yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextEnd, ID: event.Item.ID})
	case "reasoning":
		state := s.activeReasoning[event.Item.ID]
		if state == nil {
			return true
		}
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeReasoningEnd, ID: event.Item.ID, ProviderMetadata: fantasy.ProviderMetadata{Name: state.metadata}}) {
			return false
		}
		delete(s.activeReasoning, event.Item.ID)
	}
	return true
}

func (s *responsesWebSocketStreamState) handleAnnotation(annotation map[string]any, yield func(fantasy.StreamPart) bool) bool {
	switch annotationType, _ := annotation["type"].(string); annotationType {
	case "url_citation":
		urlValue, _ := annotation["url"].(string)
		title, _ := annotation["title"].(string)
		return yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeSource, ID: uuid.NewString(), SourceType: fantasy.SourceTypeURL, URL: urlValue, Title: title})
	case "file_citation":
		title := "Document"
		if filename, ok := annotation["filename"].(string); ok && filename != "" {
			title = filename
		}
		return yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeSource, ID: uuid.NewString(), SourceType: fantasy.SourceTypeDocument, Title: title})
	}
	return true
}

func responsesWebSocketArgumentString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case nil:
		return ""
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return ""
		}
		return string(data)
	}
}
