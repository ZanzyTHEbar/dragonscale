package fantasy

import (
	"fmt"
	jsonv2 "github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"
)

// UnmarshalJSONFrom implements jsonv2.UnmarshalerFrom for Call.
func (c *Call) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var aux struct {
		Prompt           Prompt                    `json:"prompt"`
		MaxOutputTokens  *int64                    `json:"max_output_tokens"`
		Temperature      *float64                  `json:"temperature"`
		TopP             *float64                  `json:"top_p"`
		TopK             *int64                    `json:"top_k"`
		PresencePenalty  *float64                  `json:"presence_penalty"`
		FrequencyPenalty *float64                  `json:"frequency_penalty"`
		Tools            []jsontext.Value          `json:"tools"`
		ToolChoice       *ToolChoice               `json:"tool_choice"`
		ProviderOptions  map[string]jsontext.Value `json:"provider_options"`
	}

	if err := jsonv2.UnmarshalDecode(dec, &aux); err != nil {
		return err
	}

	c.Prompt = aux.Prompt
	c.MaxOutputTokens = aux.MaxOutputTokens
	c.Temperature = aux.Temperature
	c.TopP = aux.TopP
	c.TopK = aux.TopK
	c.PresencePenalty = aux.PresencePenalty
	c.FrequencyPenalty = aux.FrequencyPenalty
	c.ToolChoice = aux.ToolChoice

	// Unmarshal Tools slice
	c.Tools = make([]Tool, len(aux.Tools))
	for i, rawTool := range aux.Tools {
		tool, err := UnmarshalTool([]byte(rawTool))
		if err != nil {
			return fmt.Errorf("failed to unmarshal tool at index %d: %w", i, err)
		}
		c.Tools[i] = tool
	}

	// Unmarshal ProviderOptions
	if len(aux.ProviderOptions) > 0 {
		options, err := UnmarshalProviderOptions(aux.ProviderOptions)
		if err != nil {
			return err
		}
		c.ProviderOptions = options
	}

	return nil
}

// UnmarshalJSONFrom implements jsonv2.UnmarshalerFrom for Response.
func (r *Response) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var aux struct {
		Content          jsontext.Value            `json:"content"`
		FinishReason     FinishReason              `json:"finish_reason"`
		Usage            Usage                     `json:"usage"`
		Warnings         []CallWarning             `json:"warnings"`
		ProviderMetadata map[string]jsontext.Value `json:"provider_metadata"`
	}

	if err := jsonv2.UnmarshalDecode(dec, &aux); err != nil {
		return err
	}

	r.FinishReason = aux.FinishReason
	r.Usage = aux.Usage
	r.Warnings = aux.Warnings

	var rawContent []jsontext.Value
	if err := jsonv2.Unmarshal([]byte(aux.Content), &rawContent); err != nil {
		return err
	}

	content := make([]Content, len(rawContent))
	for i, rawItem := range rawContent {
		item, err := UnmarshalContent([]byte(rawItem))
		if err != nil {
			return fmt.Errorf("failed to unmarshal content at index %d: %w", i, err)
		}
		content[i] = item
	}
	r.Content = content

	// Unmarshal ProviderMetadata
	if len(aux.ProviderMetadata) > 0 {
		metadata, err := UnmarshalProviderMetadata(aux.ProviderMetadata)
		if err != nil {
			return err
		}
		r.ProviderMetadata = metadata
	}

	return nil
}

// MarshalJSONTo implements jsonv2.MarshalerTo for StreamPart.
func (s StreamPart) MarshalJSONTo(enc *jsontext.Encoder) error {
	type alias StreamPart
	aux := struct {
		alias
		Error string `json:"error,omitzero"`
	}{
		alias: (alias)(s),
	}

	if s.Error != nil {
		aux.Error = s.Error.Error()
	}

	aux.alias.Error = nil

	return jsonv2.MarshalEncode(enc, aux)
}

// UnmarshalJSONFrom implements jsonv2.UnmarshalerFrom for StreamPart.
func (s *StreamPart) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	type alias StreamPart
	aux := struct {
		*alias
		Error            string                    `json:"error"`
		ProviderMetadata map[string]jsontext.Value `json:"provider_metadata"`
	}{
		alias: (*alias)(s),
	}

	if err := jsonv2.UnmarshalDecode(dec, &aux); err != nil {
		return err
	}

	if aux.Error != "" {
		s.Error = fmt.Errorf("%s", aux.Error)
	}

	if len(aux.ProviderMetadata) > 0 {
		metadata, err := UnmarshalProviderMetadata(aux.ProviderMetadata)
		if err != nil {
			return err
		}
		s.ProviderMetadata = metadata
	}

	return nil
}
