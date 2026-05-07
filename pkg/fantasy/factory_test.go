package fantasy

import (
	"strings"
	"testing"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg/auth"
	"github.com/ZanzyTHEbar/dragonscale/pkg/config"
)

func TestShouldUseOpenAIResponsesWebSocket(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers.OpenAI.APIKey = "openai-key"
	cfg.Providers.OpenAI.ResponsesWebSocket = true

	if !shouldUseOpenAIResponsesWebSocket(cfg, "openai", "gpt-5.5", "openai-key") {
		t.Fatal("expected explicit OpenAI provider to use Responses WebSocket")
	}
	if !shouldUseOpenAIResponsesWebSocket(cfg, "", "openai/gpt-5.5", "openai-key") {
		t.Fatal("expected OpenAI model prefix to use Responses WebSocket")
	}
	if shouldUseOpenAIResponsesWebSocket(cfg, "openrouter", "openrouter/openai/gpt-5.5", "openai-key") {
		t.Fatal("did not expect OpenRouter to use OpenAI Responses WebSocket")
	}
	if shouldUseOpenAIResponsesWebSocket(cfg, "openai", "gpt-5.5", "openrouter-key") {
		t.Fatal("did not expect mismatched API key to use OpenAI Responses WebSocket")
	}
	if shouldUseOpenAIResponsesWebSocket(cfg, "", "opencode-zen/gpt-5.5", "openai-key") {
		t.Fatal("did not expect OpenCode-prefixed GPT model to use OpenAI Responses WebSocket")
	}
}

func TestModelIDStripsProviderPrefixes(t *testing.T) {
	for _, tt := range []struct {
		provider string
		model    string
		want     string
	}{
		{"", "openai/gpt-5.5", "gpt-5.5"},
		{"", "opencode-go/kimi-k2.6", "kimi-k2.6"},
		{"", "opencode-zen/gpt-5.5", "gpt-5.5"},
		{"chatgpt", "chatgpt/gpt-5.5", "gpt-5.5"},
		{"opencode-go", "opencode-go/kimi-k2.6", "kimi-k2.6"},
		{"opencode-zen", "opencode-zen/gpt-5.5", "gpt-5.5"},
	} {
		t.Run(tt.model, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.Agents.Defaults.Provider = tt.provider
			cfg.Agents.Defaults.Model = tt.model
			if got := ModelID(cfg); got != tt.want {
				t.Fatalf("ModelID() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveProviderPrefersModelPrefixesBeforeSubstrings(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers.OpenCode.APIKey = "opencode-key"
	cfg.Providers.OpenAI.APIKey = "openai-key"
	cfg.Providers.Moonshot.APIKey = "moonshot-key"

	_, goBase, _ := resolveProvider(cfg, "", "opencode-go/kimi-k2.6")
	if goBase != "https://opencode.ai/zen/go/v1" {
		t.Fatalf("opencode-go/kimi-k2.6 API base = %q, want OpenCode Go base", goBase)
	}

	_, zenBase, _ := resolveProvider(cfg, "", "opencode-zen/gpt-5.5")
	if zenBase != "https://opencode.ai/zen/v1" {
		t.Fatalf("opencode-zen/gpt-5.5 API base = %q, want OpenCode Zen base", zenBase)
	}
}

func TestOpenAIResponsesWebSocketRequiresDirectOpenAIBase(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers.OpenAI.APIKey = "openai-key"
	cfg.Providers.OpenAI.ResponsesWebSocket = true
	cfg.Providers.OpenAI.APIBase = "https://proxy.example/v1"

	if shouldUseOpenAIResponsesWebSocket(cfg, "openai", "gpt-5.5", "openai-key") {
		t.Fatal("did not expect OpenAI Responses WebSocket for custom OpenAI API base")
	}

	cfg.Providers.OpenAI.APIBase = "https://api.openai.com/v1/"
	if !shouldUseOpenAIResponsesWebSocket(cfg, "openai", "gpt-5.5", "openai-key") {
		t.Fatal("expected OpenAI Responses WebSocket for default OpenAI API base")
	}
}

func TestCreateProviderRoutesChatGPTWithStoredOAuth(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := auth.SetCredential(chatGPTOAuthProviderID, &auth.AuthCredential{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		ExpiresAt:    time.Now().Add(time.Hour),
		Provider:     chatGPTOAuthProviderID,
		AuthMethod:   "oauth",
	}); err != nil {
		t.Fatalf("SetCredential() error: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Provider = "chatgpt"
	cfg.Agents.Defaults.Model = "chatgpt/gpt-5.5"

	provider, err := CreateProvider(cfg)
	if err != nil {
		t.Fatalf("CreateProvider() error: %v", err)
	}
	if provider.Name() != "chatgpt" {
		t.Fatalf("provider.Name() = %q, want chatgpt", provider.Name())
	}
}

func TestCreateProviderUnknownProviderSuggestsClosestProvider(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Provider = "opencdoe-go"
	cfg.Agents.Defaults.Model = "kimi-k2.6"

	_, err := CreateProvider(cfg)
	if err == nil {
		t.Fatal("expected unknown provider error")
	}
	msg := err.Error()
	for _, want := range []string{"unknown provider \"opencdoe-go\"", "did you mean \"opencode-go\"", "known providers:"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q missing %q", msg, want)
		}
	}
}

func TestCreateProviderMissingCredentialsDiagnostic(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Provider = "openai"
	cfg.Agents.Defaults.Model = "gpt-5.5"

	_, err := CreateProvider(cfg)
	if err == nil {
		t.Fatal("expected missing credentials error")
	}
	msg := err.Error()
	for _, want := range []string{"provider \"openai\" is missing credentials", "source: missing", "configured providers: none", "DRAGONSCALE_PROVIDERS_OPENAI_API_KEY"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q missing %q", msg, want)
		}
	}
}

func TestCreateProviderAmbiguousCachedModelDiagnostic(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	cachePath, err := ModelCatalogPath()
	if err != nil {
		t.Fatalf("ModelCatalogPath() error: %v", err)
	}
	now := time.Now().UTC()
	if err := saveModelCatalog(cachePath, &ModelCatalog{
		FetchedAt:  now,
		TTLSeconds: int64(defaultModelCatalogTTL.Seconds()),
		Providers: map[string]ModelCatalogProvider{
			"openai":       {Name: "openai", APIBase: "https://api.openai.com/v1", FetchedAt: now, Models: []ModelCatalogModel{{ID: "shared-new-model"}}},
			"opencode-zen": {Name: "opencode-zen", APIBase: "https://opencode.ai/zen/v1", FetchedAt: now, Models: []ModelCatalogModel{{ID: "shared-new-model"}}},
		},
	}); err != nil {
		t.Fatalf("saveModelCatalog() error: %v", err)
	}
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Model = "shared-new-model"
	cfg.Providers.OpenAI.APIKey = "openai-key"
	cfg.Providers.OpenCode.APIKey = "opencode-key"

	_, err = CreateProvider(cfg)
	if err == nil {
		t.Fatal("expected ambiguous cached model error")
	}
	msg := err.Error()
	for _, want := range []string{"available from multiple configured providers", "openai(config)", "opencode-zen(config)", "set agents.defaults.provider"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q missing %q", msg, want)
		}
	}
}
