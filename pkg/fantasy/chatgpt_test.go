package fantasy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg/auth"
	"github.com/ZanzyTHEbar/dragonscale/pkg/config"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestChatGPTTransportRewritesResponsesRequest(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := auth.SetCredential(chatGPTOAuthProviderID, &auth.AuthCredential{AccessToken: "access-token", RefreshToken: "refresh-token", AccountID: "account-id", ExpiresAt: time.Now().Add(time.Hour), Provider: chatGPTOAuthProviderID, AuthMethod: "oauth"}); err != nil {
		t.Fatalf("SetCredential() error: %v", err)
	}

	var gotReq *http.Request
	var gotBody []byte
	transport := &chatGPTTransport{
		endpoint: "https://chatgpt.test/backend-api/codex/responses",
		base: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			gotReq = req
			gotBody, _ = io.ReadAll(req.Body)
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: req}, nil
		}),
	}

	req, err := http.NewRequest(http.MethodPost, "https://api.openai.com/v1/responses", strings.NewReader(`{"model":"gpt-5.5","max_output_tokens":128,"input":[]}`))
	if err != nil {
		t.Fatalf("NewRequest() error: %v", err)
	}
	req.Header.Set("Authorization", "Bearer dummy")
	req.Header.Set("Content-Type", "application/json")

	if _, err := transport.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip() error: %v", err)
	}
	if gotReq == nil {
		t.Fatal("base transport did not receive a request")
	}
	if gotReq.URL.String() != "https://chatgpt.test/backend-api/codex/responses" {
		t.Fatalf("rewritten URL = %s", gotReq.URL.String())
	}
	if got := gotReq.Header.Get("Authorization"); got != "Bearer access-token" {
		t.Fatalf("Authorization = %q, want bearer OAuth access token", got)
	}
	if got := gotReq.Header.Get("ChatGPT-Account-Id"); got != "account-id" {
		t.Fatalf("ChatGPT-Account-Id = %q, want account-id", got)
	}
	if got := gotReq.Header.Get("originator"); got != "codex_cli_rs" {
		t.Fatalf("originator = %q, want codex_cli_rs", got)
	}
	if got := gotReq.Header.Get("session_id"); got != "dragonscale" {
		t.Fatalf("session_id = %q, want dragonscale", got)
	}
	if got := gotReq.Header.Get("User-Agent"); !strings.HasPrefix(got, "opencode/dragonscale ") {
		t.Fatalf("User-Agent = %q, want opencode/dragonscale prefix", got)
	}

	var body map[string]any
	if err := json.Unmarshal(gotBody, &body); err != nil {
		t.Fatalf("unmarshal rewritten body: %v", err)
	}
	if _, ok := body["max_output_tokens"]; ok {
		t.Fatalf("max_output_tokens should be removed for ChatGPT Codex requests: %s", string(gotBody))
	}
	if got := body["instructions"]; got != chatGPTInstructions {
		t.Fatalf("instructions = %q, want default ChatGPT instructions", got)
	}
}

func TestChatGPTTransportUsesConfiguredOriginator(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := auth.SetCredential(chatGPTOAuthProviderID, &auth.AuthCredential{AccessToken: "access-token", RefreshToken: "refresh-token", ExpiresAt: time.Now().Add(time.Hour), Provider: chatGPTOAuthProviderID, AuthMethod: "oauth"}); err != nil {
		t.Fatalf("SetCredential() error: %v", err)
	}

	var gotOriginator string
	transport := &chatGPTTransport{
		endpoint:    "https://chatgpt.test/backend-api/codex/responses",
		oauthConfig: auth.OAuthProviderConfig{Originator: "custom_originator"},
		base: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			gotOriginator = req.Header.Get("originator")
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: req}, nil
		}),
	}

	req, err := http.NewRequest(http.MethodPost, "https://api.openai.com/v1/responses", strings.NewReader(`{"model":"gpt-5.5","input":[]}`))
	if err != nil {
		t.Fatalf("NewRequest() error: %v", err)
	}
	if _, err := transport.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip() error: %v", err)
	}
	if gotOriginator != "custom_originator" {
		t.Fatalf("originator = %q, want custom_originator", gotOriginator)
	}
}

func TestChatGPTTransportRejectsNonOAuthCredentialAtUseTime(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := auth.SetCredential(chatGPTOAuthProviderID, &auth.AuthCredential{AccessToken: "api-key-token", Provider: chatGPTOAuthProviderID, AuthMethod: "api_key"}); err != nil {
		t.Fatalf("SetCredential() error: %v", err)
	}

	called := false
	transport := &chatGPTTransport{
		endpoint: "https://chatgpt.test/backend-api/codex/responses",
		base: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			called = true
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: req}, nil
		}),
	}

	req, err := http.NewRequest(http.MethodPost, "https://api.openai.com/v1/responses", strings.NewReader(`{"model":"gpt-5.5","input":[]}`))
	if err != nil {
		t.Fatalf("NewRequest() error: %v", err)
	}
	if _, err := transport.RoundTrip(req); err == nil || !strings.Contains(err.Error(), "requires OpenAI OAuth credentials") {
		t.Fatalf("RoundTrip() error = %v, want OpenAI OAuth credential rejection", err)
	}
	if called {
		t.Fatal("base transport should not receive non-OAuth credentials")
	}
}

func TestChatGPTTransportRefreshesExpiredCredential(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := auth.SetCredential(chatGPTOAuthProviderID, &auth.AuthCredential{AccessToken: "expired-access-token", RefreshToken: "old-refresh-token", AccountID: "old-account-id", ExpiresAt: time.Now().Add(-time.Minute), Provider: chatGPTOAuthProviderID, AuthMethod: "oauth"}); err != nil {
		t.Fatalf("SetCredential() error: %v", err)
	}

	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/oauth/token" {
			http.NotFound(w, req)
			return
		}
		if err := req.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if got := req.FormValue("grant_type"); got != "refresh_token" {
			http.Error(w, "bad grant_type: "+got, http.StatusBadRequest)
			return
		}
		if got := req.FormValue("refresh_token"); got != "old-refresh-token" {
			http.Error(w, "bad refresh_token: "+got, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"refreshed-access-token","refresh_token":"new-refresh-token","expires_in":3600}`))
	}))
	defer authServer.Close()

	var gotAuth string
	transport := &chatGPTTransport{
		endpoint:    "https://chatgpt.test/backend-api/codex/responses",
		oauthConfig: auth.OAuthProviderConfig{Issuer: authServer.URL, ClientID: "test-client"},
		base: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			gotAuth = req.Header.Get("Authorization")
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: req}, nil
		}),
	}

	req, err := http.NewRequest(http.MethodPost, "https://api.openai.com/v1/responses", strings.NewReader(`{"model":"gpt-5.5","input":[]}`))
	if err != nil {
		t.Fatalf("NewRequest() error: %v", err)
	}
	if _, err := transport.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip() error: %v", err)
	}
	if gotAuth != "Bearer refreshed-access-token" {
		t.Fatalf("Authorization = %q, want refreshed access token", gotAuth)
	}

	stored, err := auth.GetCredential(chatGPTOAuthProviderID)
	if err != nil {
		t.Fatalf("GetCredential() error: %v", err)
	}
	if stored.AccessToken != "refreshed-access-token" || stored.RefreshToken != "new-refresh-token" || stored.AccountID != "old-account-id" {
		t.Fatalf("stored credential not refreshed/preserved correctly: %+v", stored)
	}
}

func TestChatGPTTransportRejectsNonCodexRequestPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := auth.SetCredential(chatGPTOAuthProviderID, &auth.AuthCredential{AccessToken: "access-token", RefreshToken: "refresh-token", ExpiresAt: time.Now().Add(time.Hour), Provider: chatGPTOAuthProviderID, AuthMethod: "oauth"}); err != nil {
		t.Fatalf("SetCredential() error: %v", err)
	}

	called := false
	transport := &chatGPTTransport{base: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		called = true
		return nil, nil
	})}
	req, err := http.NewRequest(http.MethodGet, "https://api.openai.com/v1/models", nil)
	if err != nil {
		t.Fatalf("NewRequest() error: %v", err)
	}
	if _, err := transport.RoundTrip(req); err == nil {
		t.Fatal("expected non-Codex path to be rejected")
	}
	if called {
		t.Fatal("base transport should not receive non-Codex requests")
	}
}

func TestOpenAIProviderDoesNotUseStoredOAuthToken(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := auth.SetCredential(chatGPTOAuthProviderID, &auth.AuthCredential{AccessToken: "chatgpt-oauth-token", RefreshToken: "refresh-token", ExpiresAt: time.Now().Add(time.Hour), Provider: chatGPTOAuthProviderID, AuthMethod: "oauth"}); err != nil {
		t.Fatalf("SetCredential() error: %v", err)
	}

	cfg := &config.Config{}
	cfg.Providers.OpenAI.AuthMethod = "oauth"
	apiKey, _, _ := resolveProvider(cfg, "openai", "gpt-5.5")
	if apiKey != "" {
		t.Fatalf("openai provider API key = %q, want no implicit ChatGPT OAuth token", apiKey)
	}
}

func TestResolveChatGPTCodexEndpointRejectsUnsafeEndpoint(t *testing.T) {
	if _, err := resolveChatGPTCodexEndpoint("https://example.com/backend-api/codex/responses"); err == nil {
		t.Fatal("expected non-chatgpt.com endpoint to be rejected")
	}
	if _, err := resolveChatGPTCodexEndpoint(chatGPTCodexEndpoint + "?next=https://example.com"); err == nil {
		t.Fatal("expected endpoint with query string to be rejected")
	}
	got, err := resolveChatGPTCodexEndpoint("")
	if err != nil {
		t.Fatalf("resolve default endpoint: %v", err)
	}
	if got != chatGPTCodexEndpoint {
		t.Fatalf("default endpoint = %q, want %q", got, chatGPTCodexEndpoint)
	}
}

func TestChatGPTProviderRejectsInvalidConfig(t *testing.T) {
	cfg := &config.Config{}
	cfg.Providers.ChatGPT.APIKey = "sk-not-supported"
	if _, err := newChatGPTProvider(cfg); err == nil || !strings.Contains(err.Error(), "does not support API keys") {
		t.Fatalf("newChatGPTProvider() error = %v, want API key rejection", err)
	}

	cfg = &config.Config{}
	cfg.Providers.ChatGPT.AuthMethod = "api_key"
	if _, err := newChatGPTProvider(cfg); err == nil || !strings.Contains(err.Error(), "only supports auth_method=oauth") {
		t.Fatalf("newChatGPTProvider() error = %v, want auth_method rejection", err)
	}
}

func TestSupportedChatGPTModelsAreResponsesModels(t *testing.T) {
	for _, model := range []string{"gpt-5.5", "gpt-5.4", "gpt-5.4-mini", "gpt-5.3-codex", "gpt-5.2"} {
		if !isSupportedChatGPTModel(model) {
			t.Fatalf("%s should be supported", model)
		}
	}
	for _, model := range []string{"gpt-4o-mini", "chatgpt-4o-latest", "kimi-k2.6"} {
		if isSupportedChatGPTModel(model) {
			t.Fatalf("%s should not be supported", model)
		}
	}
}
