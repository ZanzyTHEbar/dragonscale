package fantasy

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	fantasysdk "charm.land/fantasy"
	"github.com/ZanzyTHEbar/dragonscale/pkg/auth"
	"github.com/ZanzyTHEbar/dragonscale/pkg/config"
)

const (
	liveProviderTestsEnv        = "DRAGONSCALE_RUN_LIVE_PROVIDER_TESTS"
	liveChatGPTProviderTestsEnv = "DRAGONSCALE_RUN_LIVE_CHATGPT_TESTS"
)

func liveEnvEnabled(name string) bool {
	enabled, err := strconv.ParseBool(os.Getenv(liveProviderTestsEnv))
	if err == nil && enabled {
		return true
	}
	enabled, err = strconv.ParseBool(os.Getenv(name))
	return err == nil && enabled
}

func requireLiveEnv(t *testing.T, optInEnv, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Fatalf("%s=true or %s=true requires %s", liveProviderTestsEnv, optInEnv, name)
	}
	return value
}

func TestLiveChatGPTCodexOAuthResponses(t *testing.T) {
	if !liveEnvEnabled(liveChatGPTProviderTestsEnv) {
		t.Skipf("set %s=true or %s=true to run live ChatGPT provider integration tests", liveProviderTestsEnv, liveChatGPTProviderTestsEnv)
	}

	refreshToken := requireLiveEnv(t, liveChatGPTProviderTestsEnv, "DRAGONSCALE_LIVE_CHATGPT_REFRESH_TOKEN")
	accessToken := strings.TrimSpace(os.Getenv("DRAGONSCALE_LIVE_CHATGPT_ACCESS_TOKEN"))
	accountID := strings.TrimSpace(os.Getenv("DRAGONSCALE_LIVE_CHATGPT_ACCOUNT_ID"))
	model := strings.TrimSpace(os.Getenv("DRAGONSCALE_LIVE_CHATGPT_MODEL"))
	if model == "" {
		model = "gpt-5.5"
	}

	t.Setenv("HOME", t.TempDir())
	if err := auth.SetCredential(chatGPTOAuthProviderID, &auth.AuthCredential{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		AccountID:    accountID,
		ExpiresAt:    time.Now().Add(-time.Minute),
		Provider:     chatGPTOAuthProviderID,
		AuthMethod:   "oauth",
	}); err != nil {
		t.Fatalf("SetCredential() error: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Provider = "chatgpt"
	cfg.Agents.Defaults.Model = model
	cfg.Providers.ChatGPT.AuthMethod = "oauth"
	cfg.Providers.ChatGPT.Timeout = 120

	provider, err := CreateProvider(cfg)
	if err != nil {
		t.Fatalf("CreateProvider() error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	lm, err := provider.LanguageModel(ctx, ModelID(cfg))
	if err != nil {
		t.Fatalf("LanguageModel() error: %v", err)
	}
	stream, err := lm.Stream(ctx, fantasysdk.Call{
		Prompt: fantasysdk.Prompt{{
			Role: fantasysdk.MessageRoleUser,
			Content: []fantasysdk.MessagePart{
				fantasysdk.TextPart{Text: "Reply with a short confirmation for a live integration test."},
			},
		}},
	})
	if err != nil {
		t.Fatalf("Stream() error: %v", err)
	}

	var text strings.Builder
	var sawFinish bool
	stream(func(part fantasysdk.StreamPart) bool {
		switch part.Type {
		case fantasysdk.StreamPartTypeTextDelta:
			text.WriteString(part.Delta)
		case fantasysdk.StreamPartTypeError:
			t.Fatalf("stream error: %v", part.Error)
		case fantasysdk.StreamPartTypeFinish:
			sawFinish = true
		}
		return true
	})

	if strings.TrimSpace(text.String()) == "" {
		t.Fatal("expected non-empty ChatGPT Codex stream text")
	}
	if !sawFinish {
		t.Fatal("expected ChatGPT Codex stream finish event")
	}

	stored, err := auth.GetCredential(chatGPTOAuthProviderID)
	if err != nil {
		t.Fatalf("GetCredential() error: %v", err)
	}
	if stored == nil || stored.AccessToken == "" || stored.NeedsRefresh() {
		t.Fatal("expected refreshed ChatGPT OAuth credential to be stored")
	}
}
