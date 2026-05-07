package openai

import (
	"cmp"
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"charm.land/fantasy"
)

const (
	liveProviderTestsEnv            = "DRAGONSCALE_RUN_LIVE_PROVIDER_TESTS"
	liveOpenAIResponsesWebSocketEnv = "DRAGONSCALE_RUN_LIVE_OPENAI_RESPONSES_WS_TESTS"
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

func TestLiveOpenAIResponsesWebSocketStream(t *testing.T) {
	if !liveEnvEnabled(liveOpenAIResponsesWebSocketEnv) {
		t.Skipf("set %s=true or %s=true to run live OpenAI Responses WebSocket integration tests", liveProviderTestsEnv, liveOpenAIResponsesWebSocketEnv)
	}

	apiKey := cmp.Or(strings.TrimSpace(os.Getenv("FANTASY_OPENAI_API_KEY")), strings.TrimSpace(os.Getenv("OPENAI_API_KEY")))
	if apiKey == "" {
		apiKey = requireLiveEnv(t, liveOpenAIResponsesWebSocketEnv, "FANTASY_OPENAI_API_KEY")
	}
	model := strings.TrimSpace(os.Getenv("DRAGONSCALE_LIVE_OPENAI_RESPONSES_WS_MODEL"))
	if model == "" {
		model = "gpt-5.5"
	}

	provider, err := New(
		WithAPIKey(apiKey),
		WithUseResponsesAPI(),
		WithResponsesWebSocket(),
	)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	lm, err := provider.LanguageModel(ctx, model)
	if err != nil {
		t.Fatalf("LanguageModel() error: %v", err)
	}
	stream, err := lm.Stream(ctx, fantasy.Call{
		Prompt: fantasy.Prompt{{
			Role: fantasy.MessageRoleUser,
			Content: []fantasy.MessagePart{
				fantasy.TextPart{Text: "Reply with a short confirmation for a live WebSocket integration test."},
			},
		}},
	})
	if err != nil {
		t.Fatalf("Stream() error: %v", err)
	}

	var text strings.Builder
	var sawFinish bool
	stream(func(part fantasy.StreamPart) bool {
		switch part.Type {
		case fantasy.StreamPartTypeTextDelta:
			text.WriteString(part.Delta)
		case fantasy.StreamPartTypeError:
			t.Fatalf("stream error: %v", part.Error)
		case fantasy.StreamPartTypeFinish:
			sawFinish = true
		}
		return true
	})

	if strings.TrimSpace(text.String()) == "" {
		t.Fatal("expected non-empty OpenAI Responses WebSocket stream text")
	}
	if !sawFinish {
		t.Fatal("expected OpenAI Responses WebSocket stream finish event")
	}
}
