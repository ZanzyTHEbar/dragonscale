// DragonScale - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 DragonScale contributors

package fantasy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"time"

	fantasysdk "charm.land/fantasy"
	fantasyopenai "charm.land/fantasy/providers/openai"
	"github.com/ZanzyTHEbar/dragonscale/pkg"
	"github.com/ZanzyTHEbar/dragonscale/pkg/auth"
	"github.com/ZanzyTHEbar/dragonscale/pkg/config"
)

const (
	chatGPTOAuthProviderID = "openai"
	chatGPTDummyAPIKey     = "opencode-oauth-dummy-key"
	chatGPTOpenAIBaseURL   = "https://api.openai.com/v1"
	chatGPTCodexEndpoint   = "https://chatgpt.com/backend-api/codex/responses"
	chatGPTInstructions    = "You are a helpful coding assistant. Follow the user's instructions concisely."
)

type chatGPTCredentialGetter func(string) (*auth.AuthCredential, error)
type chatGPTCredentialSetter func(string, *auth.AuthCredential) error

type chatGPTTransport struct {
	base          http.RoundTripper
	endpoint      string
	oauthConfig   auth.OAuthProviderConfig
	oauthClient   *http.Client
	getCredential chatGPTCredentialGetter
	setCredential chatGPTCredentialSetter
}

func newChatGPTProvider(cfg *config.Config) (fantasysdk.Provider, error) {
	if err := validateChatGPTProviderConfig(cfg.Providers.ChatGPT); err != nil {
		return nil, err
	}
	if ok, err := chatGPTOAuthAvailable(); err != nil {
		return nil, fmt.Errorf("reading ChatGPT OAuth credential: %w", err)
	} else if !ok {
		return nil, fmt.Errorf("chatgpt provider requires OpenAI OAuth credentials; run `dragonscale auth login --provider openai`")
	}
	modelID := chatGPTModelID(cfg.Agents.Defaults.Model)
	if !isSupportedChatGPTModel(modelID) {
		return nil, fmt.Errorf("chatgpt provider only supports ChatGPT Codex Responses models; use gpt-5.5, gpt-5.4, gpt-5.4-mini, gpt-5.3-codex, or gpt-5.2 (got %q)", modelID)
	}
	endpoint, err := resolveChatGPTCodexEndpoint(cfg.Providers.ChatGPT.APIBase)
	if err != nil {
		return nil, err
	}

	timeout := resolveProviderTimeout(cfg, "chatgpt", cfg.Agents.Defaults.Model)
	client := newChatGPTHTTPClient(cfg.Providers.ChatGPT.Proxy, timeout, endpoint, auth.OpenAIOAuthConfig())

	return fantasyopenai.New(
		fantasyopenai.WithBaseURL(chatGPTOpenAIBaseURL),
		fantasyopenai.WithAPIKey(chatGPTDummyAPIKey),
		fantasyopenai.WithName("chatgpt"),
		fantasyopenai.WithUseResponsesAPI(),
		fantasyopenai.WithHTTPClient(client),
	)
}

func validateChatGPTProviderConfig(provider config.ProviderConfig) error {
	if strings.TrimSpace(provider.APIKey) != "" {
		return fmt.Errorf("chatgpt provider does not support API keys; run `dragonscale auth login --provider openai` for ChatGPT Plus/Pro OAuth, or use provider `openai` for OpenAI Platform API keys")
	}
	if method := strings.TrimSpace(provider.AuthMethod); method != "" && method != "oauth" {
		return fmt.Errorf("chatgpt provider only supports auth_method=oauth (got %q)", method)
	}
	return nil
}

func chatGPTOAuthAvailable() (bool, error) {
	cred, err := auth.GetCredential(chatGPTOAuthProviderID)
	if err != nil || cred == nil {
		return false, err
	}
	if cred.AuthMethod != "oauth" {
		return false, nil
	}
	return cred.RefreshToken != "" || (cred.AccessToken != "" && !cred.IsExpired()), nil
}

func chatGPTModelID(model string) string {
	if _, after, ok := strings.Cut(model, "/"); ok {
		return after
	}
	return model
}

func resolveChatGPTCodexEndpoint(endpoint string) (string, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return chatGPTCodexEndpoint, nil
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("invalid ChatGPT Codex endpoint: %w", err)
	}
	if parsed.Scheme != "https" || parsed.Host != "chatgpt.com" || parsed.Path != "/backend-api/codex/responses" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("refusing ChatGPT OAuth token forwarding to %q; chatgpt.api_base must be empty or %s", endpoint, chatGPTCodexEndpoint)
	}
	return parsed.String(), nil
}

func isSupportedChatGPTModel(modelID string) bool {
	if !fantasyopenai.IsResponsesModel(modelID) {
		return false
	}
	switch modelID {
	case "gpt-5.5", "gpt-5.2", "gpt-5.3-codex", "gpt-5.4", "gpt-5.4-mini":
		return true
	}
	if !strings.HasPrefix(modelID, "gpt-") {
		return false
	}
	version := strings.TrimPrefix(modelID, "gpt-")
	end := 0
	for end < len(version) {
		ch := version[end]
		if (ch < '0' || ch > '9') && ch != '.' {
			break
		}
		end++
	}
	if end == 0 {
		return false
	}
	value, err := strconv.ParseFloat(version[:end], 64)
	return err == nil && value > 5.4
}

func newChatGPTHTTPClient(proxy string, timeout time.Duration, endpoint string, oauthConfig auth.OAuthProviderConfig) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if proxy != "" {
		proxyURL, err := url.Parse(proxy)
		if err == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	}

	return &http.Client{
		Timeout: timeout,
		Transport: &chatGPTTransport{
			base:          transport,
			endpoint:      endpoint,
			oauthConfig:   oauthConfig,
			oauthClient:   &http.Client{Timeout: timeout, Transport: transport},
			getCredential: auth.GetCredential,
			setCredential: auth.SetCredential,
		},
	}
}

func (t *chatGPTTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	if !shouldRewriteChatGPTRequest(clone) {
		return nil, fmt.Errorf("refusing to send ChatGPT OAuth token to non-Codex request path %q", clone.URL.EscapedPath())
	}
	cred, err := t.resolveCredential()
	if err != nil {
		return nil, err
	}

	endpoint, err := url.Parse(defaultIfEmpty(t.endpoint, chatGPTCodexEndpoint))
	if err != nil {
		return nil, fmt.Errorf("invalid ChatGPT Codex endpoint: %w", err)
	}
	clone.URL = endpoint
	clone.Host = ""

	clone.Header = clone.Header.Clone()
	deleteHeaderCaseInsensitive(clone.Header, "authorization")
	clone.Header.Set("Authorization", "Bearer "+cred.AccessToken)
	if cred.AccountID != "" {
		clone.Header.Set("ChatGPT-Account-Id", cred.AccountID)
	}
	originator := t.oauthConfig.Originator
	if originator == "" {
		originator = auth.OpenAIOAuthConfig().Originator
	}
	if originator != "" {
		clone.Header.Set("originator", originator)
	}
	clone.Header.Set("User-Agent", chatGPTUserAgent())
	if clone.Header.Get("session_id") == "" {
		clone.Header.Set("session_id", pkg.NAME)
	}

	if clone.Body != nil {
		body, err := io.ReadAll(clone.Body)
		if err != nil {
			return nil, err
		}
		_ = clone.Body.Close()
		body = sanitizeChatGPTBody(body)
		clone.Body = io.NopCloser(bytes.NewReader(body))
		clone.ContentLength = int64(len(body))
		clone.Header.Set("Content-Length", fmt.Sprint(len(body)))
	}

	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(clone)
}

func (t *chatGPTTransport) resolveCredential() (*auth.AuthCredential, error) {
	getCredential := t.getCredential
	if getCredential == nil {
		getCredential = auth.GetCredential
	}
	setCredential := t.setCredential
	if setCredential == nil {
		setCredential = auth.SetCredential
	}

	cred, err := getCredential(chatGPTOAuthProviderID)
	if err != nil {
		return nil, fmt.Errorf("reading ChatGPT OAuth credential: %w", err)
	}
	if cred == nil {
		return nil, fmt.Errorf("chatgpt provider requires OpenAI OAuth credentials; run `dragonscale auth login --provider openai`")
	}
	if cred.AuthMethod != "oauth" {
		return nil, fmt.Errorf("chatgpt provider requires OpenAI OAuth credentials; run `dragonscale auth login --provider openai`")
	}
	if cred.AccessToken == "" || cred.NeedsRefresh() {
		if cred.RefreshToken == "" {
			return nil, fmt.Errorf("chatgpt OAuth credential has no refresh token; run `dragonscale auth login --provider openai`")
		}
		oauthConfig := t.oauthConfig
		if oauthConfig.Issuer == "" {
			oauthConfig = auth.OpenAIOAuthConfig()
		}
		refreshed, err := auth.RefreshAccessTokenWithClient(cred, oauthConfig, t.oauthClient)
		if err != nil {
			return nil, fmt.Errorf("refreshing ChatGPT OAuth token: %w", err)
		}
		if err := setCredential(chatGPTOAuthProviderID, refreshed); err != nil {
			return nil, fmt.Errorf("saving refreshed ChatGPT OAuth token: %w", err)
		}
		cred = refreshed
	}
	if cred.AccessToken == "" {
		return nil, fmt.Errorf("chatgpt OAuth credential has no access token; run `dragonscale auth login --provider openai`")
	}
	return cred, nil
}

func shouldRewriteChatGPTRequest(req *http.Request) bool {
	if req.Method != http.MethodPost {
		return false
	}
	if strings.ToLower(req.URL.Hostname()) != "api.openai.com" {
		return false
	}
	switch req.URL.EscapedPath() {
	case "/v1/responses", "/responses":
		return true
	default:
		return false
	}
}

func deleteHeaderCaseInsensitive(headers http.Header, name string) {
	for key := range headers {
		if strings.EqualFold(key, name) {
			delete(headers, key)
		}
	}
}

func sanitizeChatGPTBody(body []byte) []byte {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body
	}
	delete(payload, "max_output_tokens")
	delete(payload, "max_tokens")
	delete(payload, "max_completion_tokens")
	if instructions, ok := payload["instructions"].(string); !ok || strings.TrimSpace(instructions) == "" {
		payload["instructions"] = chatGPTInstructions
	}
	updated, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return updated
}

func chatGPTUserAgent() string {
	return fmt.Sprintf("opencode/%s (%s; %s)", pkg.NAME, runtime.GOOS, runtime.GOARCH)
}
