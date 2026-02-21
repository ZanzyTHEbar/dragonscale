package providertests

import (
	"net/http"
	"os"
	"testing"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/openai"
	"charm.land/x/vcr"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenAIResponsesCommon(t *testing.T) {
	t.Parallel()
	var pairs []builderPair
	for _, m := range openaiTestModels {
		pairs = append(pairs, builderPair{m.name, openAIReasoningBuilder(m.model), nil, nil})
	}
	testCommon(t, pairs)
}

func openAIReasoningBuilder(model string) builderFunc {
	return func(t *testing.T, r *vcr.Recorder) (fantasy.LanguageModel, error) {
		provider, err := openai.New(
			openai.WithAPIKey(os.Getenv("FANTASY_OPENAI_API_KEY")),
			openai.WithHTTPClient(&http.Client{Transport: r}),
			openai.WithUseResponsesAPI(),
		)
		if err != nil {
			return nil, err
		}
		return provider.LanguageModel(t.Context(), model)
	}
}

func TestOpenAIResponsesWithSummaryThinking(t *testing.T) {
	t.Parallel()
	opts := fantasy.ProviderOptions{
		openai.Name: &openai.ResponsesProviderOptions{
			Include: []openai.IncludeType{
				openai.IncludeReasoningEncryptedContent,
			},
			ReasoningEffort:  openai.ReasoningEffortOption(openai.ReasoningEffortHigh),
			ReasoningSummary: fantasy.Opt("auto"),
		},
	}
	var pairs []builderPair
	for _, m := range openaiTestModels {
		if !m.reasoning {
			continue
		}
		pairs = append(pairs, builderPair{m.name, openAIReasoningBuilder(m.model), opts, nil})
	}
	testThinking(t, pairs, testOpenAIResponsesThinkingWithSummaryThinking)
}

func TestOpenAIResponsesObjectGeneration(t *testing.T) {
	t.Parallel()
	var pairs []builderPair
	for _, m := range openaiTestModels {
		pairs = append(pairs, builderPair{m.name, openAIReasoningBuilder(m.model), nil, nil})
	}
	testObjectGeneration(t, pairs)
}

func testOpenAIResponsesThinkingWithSummaryThinking(t *testing.T, result *fantasy.AgentResult) {
	reasoningContentCount := 0
	encryptedData := 0
	// Test if we got the signature
	for _, step := range result.Steps {
		for _, msg := range step.Messages {
			for _, content := range msg.Content {
				if content.GetType() == fantasy.ContentTypeReasoning {
					reasoningContentCount += 1
					reasoningContent, ok := fantasy.AsContentType[fantasy.ReasoningPart](content)
					if !ok {
						continue
					}
					if len(reasoningContent.ProviderOptions) == 0 {
						continue
					}

					openaiReasoningMetadata, ok := reasoningContent.ProviderOptions[openai.Name]
					if !ok {
						continue
					}
					if typed, ok := openaiReasoningMetadata.(*openai.ResponsesReasoningMetadata); ok {
						require.NotEmpty(t, typed.EncryptedContent)
						encryptedData += 1
					}
				}
			}
		}
	}
	require.Greater(t, reasoningContentCount, 0)
	require.Greater(t, encryptedData, 0)
	assert.Empty(t, cmp.Diff(reasoningContentCount, encryptedData))
}
