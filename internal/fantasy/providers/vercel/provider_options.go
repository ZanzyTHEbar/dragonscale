// Package vercel provides an implementation of the fantasy AI SDK for Vercel AI Gateway.
package vercel

import (
	jsonv2 "github.com/go-json-experiment/json"

	"charm.land/fantasy"
)

// Global type identifiers for Vercel-specific provider data.
const (
	TypeProviderOptions  = Name + ".options"
	TypeProviderMetadata = Name + ".metadata"
)

// Register Vercel provider-specific types with the global registry.
func init() {
	fantasy.RegisterProviderType(TypeProviderOptions, func(data []byte) (fantasy.ProviderOptionsData, error) {
		var v ProviderOptions
		if err := jsonv2.Unmarshal(data, &v); err != nil {
			return nil, err
		}
		return &v, nil
	})
	fantasy.RegisterProviderType(TypeProviderMetadata, func(data []byte) (fantasy.ProviderOptionsData, error) {
		var v ProviderMetadata
		if err := jsonv2.Unmarshal(data, &v); err != nil {
			return nil, err
		}
		return &v, nil
	})
}

// ReasoningEffort represents the reasoning effort level for Vercel AI Gateway.
type ReasoningEffort string

const (
	// ReasoningEffortNone disables reasoning.
	ReasoningEffortNone ReasoningEffort = "none"
	// ReasoningEffortMinimal represents minimal reasoning effort (~10% of max_tokens).
	ReasoningEffortMinimal ReasoningEffort = "minimal"
	// ReasoningEffortLow represents low reasoning effort (~20% of max_tokens).
	ReasoningEffortLow ReasoningEffort = "low"
	// ReasoningEffortMedium represents medium reasoning effort (~50% of max_tokens).
	ReasoningEffortMedium ReasoningEffort = "medium"
	// ReasoningEffortHigh represents high reasoning effort (~80% of max_tokens).
	ReasoningEffortHigh ReasoningEffort = "high"
	// ReasoningEffortXHigh represents extra high reasoning effort (~95% of max_tokens).
	ReasoningEffortXHigh ReasoningEffort = "xhigh"
)

// ReasoningOptions represents reasoning configuration for Vercel AI Gateway.
type ReasoningOptions struct {
	// Enabled enables reasoning output. When true, the model will provide its reasoning process.
	Enabled *bool `json:"enabled,omitzero"`
	// MaxTokens is the maximum number of tokens to allocate for reasoning.
	// Cannot be used with Effort.
	MaxTokens *int64 `json:"max_tokens,omitzero"`
	// Effort controls reasoning effort level.
	// Mutually exclusive with MaxTokens.
	Effort *ReasoningEffort `json:"effort,omitzero"`
	// Exclude excludes reasoning content from the response but still generates it internally.
	Exclude *bool `json:"exclude,omitzero"`
}

// GatewayProviderOptions represents provider routing preferences for Vercel AI Gateway.
type GatewayProviderOptions struct {
	// Order is the list of provider slugs to try in order (e.g. ["vertex", "anthropic"]).
	Order []string `json:"order,omitzero"`
	// Models is the list of fallback models to try if the primary model fails.
	Models []string `json:"models,omitzero"`
}

// BYOKCredential represents a single provider credential for BYOK.
type BYOKCredential struct {
	APIKey string `json:"apiKey,omitzero"`
}

// BYOKOptions represents Bring Your Own Key options for Vercel AI Gateway.
type BYOKOptions struct {
	Anthropic map[string][]BYOKCredential `json:"anthropic,omitzero"`
	OpenAI    map[string][]BYOKCredential `json:"openai,omitzero"`
	Vertex    map[string][]BYOKCredential `json:"vertex,omitzero"`
	Bedrock   map[string][]BYOKCredential `json:"bedrock,omitzero"`
}

// ProviderOptions represents additional options for Vercel AI Gateway provider.
type ProviderOptions struct {
	// Reasoning configuration for models that support extended thinking.
	Reasoning *ReasoningOptions `json:"reasoning,omitzero"`
	// ProviderOptions for gateway routing preferences.
	ProviderOptions *GatewayProviderOptions `json:"providerOptions,omitzero"`
	// BYOK for request-scoped provider credentials.
	BYOK *BYOKOptions `json:"byok,omitzero"`
	// User is a unique identifier representing your end-user.
	User *string `json:"user,omitzero"`
	// LogitBias modifies the likelihood of specified tokens appearing in the completion.
	LogitBias map[string]int64 `json:"logit_bias,omitzero"`
	// LogProbs returns the log probabilities of the tokens.
	LogProbs *bool `json:"logprobs,omitzero"`
	// TopLogProbs is the number of top log probabilities to return.
	TopLogProbs *int64 `json:"top_logprobs,omitzero"`
	// ParallelToolCalls enables parallel function calling during tool use.
	ParallelToolCalls *bool `json:"parallel_tool_calls,omitzero"`
	// ExtraBody for additional request body fields.
	ExtraBody map[string]any `json:"extra_body,omitzero"`
}

// Options implements the ProviderOptionsData interface for ProviderOptions.
func (*ProviderOptions) Options() {}

// MarshalJSON implements custom JSON marshaling with type info for ProviderOptions.
func (o ProviderOptions) MarshalJSON() ([]byte, error) {
	type plain ProviderOptions
	return fantasy.MarshalProviderType(TypeProviderOptions, plain(o))
}

// UnmarshalJSON implements custom JSON unmarshaling with type info for ProviderOptions.
func (o *ProviderOptions) UnmarshalJSON(data []byte) error {
	type plain ProviderOptions
	var p plain
	if err := fantasy.UnmarshalProviderType(data, &p); err != nil {
		return err
	}
	*o = ProviderOptions(p)
	return nil
}

// ProviderMetadata represents metadata from Vercel AI Gateway provider.
type ProviderMetadata struct {
	Provider string `json:"provider,omitzero"`
}

// Options implements the ProviderOptionsData interface for ProviderMetadata.
func (*ProviderMetadata) Options() {}

// MarshalJSON implements custom JSON marshaling with type info for ProviderMetadata.
func (m ProviderMetadata) MarshalJSON() ([]byte, error) {
	type plain ProviderMetadata
	return fantasy.MarshalProviderType(TypeProviderMetadata, plain(m))
}

// UnmarshalJSON implements custom JSON unmarshaling with type info for ProviderMetadata.
func (m *ProviderMetadata) UnmarshalJSON(data []byte) error {
	type plain ProviderMetadata
	var p plain
	if err := fantasy.UnmarshalProviderType(data, &p); err != nil {
		return err
	}
	*m = ProviderMetadata(p)
	return nil
}

// ReasoningDetail represents a reasoning detail from Vercel AI Gateway.
type ReasoningDetail struct {
	ID        string `json:"id,omitzero"`
	Type      string `json:"type,omitzero"`
	Text      string `json:"text,omitzero"`
	Data      string `json:"data,omitzero"`
	Format    string `json:"format,omitzero"`
	Summary   string `json:"summary,omitzero"`
	Signature string `json:"signature,omitzero"`
	Index     int    `json:"index"`
}

// ReasoningData represents reasoning data from Vercel AI Gateway response.
type ReasoningData struct {
	Reasoning        string            `json:"reasoning,omitzero"`
	ReasoningDetails []ReasoningDetail `json:"reasoning_details,omitzero"`
}

// ReasoningEffortOption creates a pointer to a ReasoningEffort value.
func ReasoningEffortOption(e ReasoningEffort) *ReasoningEffort {
	return &e
}

// NewProviderOptions creates new provider options for Vercel.
func NewProviderOptions(opts *ProviderOptions) fantasy.ProviderOptions {
	return fantasy.ProviderOptions{
		Name: opts,
	}
}

// ParseOptions parses provider options from a map for Vercel.
func ParseOptions(data map[string]any) (*ProviderOptions, error) {
	var options ProviderOptions
	if err := fantasy.ParseOptions(data, &options); err != nil {
		return nil, err
	}
	return &options, nil
}
