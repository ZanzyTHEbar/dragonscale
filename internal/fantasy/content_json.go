package fantasy

import (
	"errors"
	"fmt"
	jsonv2 "github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"
)

// contentJSON is a helper type for JSON serialization of Content in Response.
type contentJSON struct {
	Type string         `json:"type"`
	Data jsontext.Value `json:"data"`
}

// messagePartJSON is a helper type for JSON serialization of MessagePart.
type messagePartJSON struct {
	Type string         `json:"type"`
	Data jsontext.Value `json:"data"`
}

// toolResultOutputJSON is a helper type for JSON serialization of ToolResultOutputContent.
type toolResultOutputJSON struct {
	Type string         `json:"type"`
	Data jsontext.Value `json:"data"`
}

// toolJSON is a helper type for JSON serialization of Tool.
type toolJSON struct {
	Type string         `json:"type"`
	Data jsontext.Value `json:"data"`
}

// MarshalJSON implements json.Marshaler for TextContent.
func (t TextContent) MarshalJSON() ([]byte, error) {
	dataBytes, err := jsonv2.Marshal(struct {
		Text             string           `json:"text"`
		ProviderMetadata ProviderMetadata `json:"provider_metadata,omitempty"`
	}{
		Text:             t.Text,
		ProviderMetadata: t.ProviderMetadata,
	})
	if err != nil {
		return nil, err
	}

	return jsonv2.Marshal(contentJSON{
		Type: string(ContentTypeText),
		Data: jsontext.Value(dataBytes),
	})
}

// UnmarshalJSON implements json.Unmarshaler for TextContent.
func (t *TextContent) UnmarshalJSON(data []byte) error {
	var cj contentJSON
	if err := jsonv2.Unmarshal(data, &cj); err != nil {
		return err
	}

	var aux struct {
		Text             string                    `json:"text"`
		ProviderMetadata map[string]jsontext.Value `json:"provider_metadata,omitempty"`
	}

	if err := jsonv2.Unmarshal(cj.Data, &aux); err != nil {
		return err
	}

	t.Text = aux.Text

	if len(aux.ProviderMetadata) > 0 {
		metadata, err := UnmarshalProviderMetadata(aux.ProviderMetadata)
		if err != nil {
			return err
		}
		t.ProviderMetadata = metadata
	}

	return nil
}

// MarshalJSON implements json.Marshaler for ReasoningContent.
func (r ReasoningContent) MarshalJSON() ([]byte, error) {
	dataBytes, err := jsonv2.Marshal(struct {
		Text             string           `json:"text"`
		ProviderMetadata ProviderMetadata `json:"provider_metadata,omitempty"`
	}{
		Text:             r.Text,
		ProviderMetadata: r.ProviderMetadata,
	})
	if err != nil {
		return nil, err
	}

	return jsonv2.Marshal(contentJSON{
		Type: string(ContentTypeReasoning),
		Data: jsontext.Value(dataBytes),
	})
}

// UnmarshalJSON implements json.Unmarshaler for ReasoningContent.
func (r *ReasoningContent) UnmarshalJSON(data []byte) error {
	var cj contentJSON
	if err := jsonv2.Unmarshal(data, &cj); err != nil {
		return err
	}

	var aux struct {
		Text             string                    `json:"text"`
		ProviderMetadata map[string]jsontext.Value `json:"provider_metadata,omitempty"`
	}

	if err := jsonv2.Unmarshal(cj.Data, &aux); err != nil {
		return err
	}

	r.Text = aux.Text

	if len(aux.ProviderMetadata) > 0 {
		metadata, err := UnmarshalProviderMetadata(aux.ProviderMetadata)
		if err != nil {
			return err
		}
		r.ProviderMetadata = metadata
	}

	return nil
}

// MarshalJSONV2 implements jsonv2.MarshalerTo for FileContent.
func (f FileContent) MarshalJSONTo(enc *jsontext.Encoder) error {
	dataBytes, err := jsonv2.Marshal(struct {
		MediaType        string           `json:"media_type"`
		Data             []byte           `json:"data"`
		ProviderMetadata ProviderMetadata `json:"provider_metadata,omitzero"`
	}{
		MediaType:        f.MediaType,
		Data:             f.Data,
		ProviderMetadata: f.ProviderMetadata,
	})
	if err != nil {
		return err
	}

	return jsonv2.MarshalEncode(enc, contentJSON{
		Type: string(ContentTypeFile),
		Data: jsontext.Value(dataBytes),
	})
}

// UnmarshalJSON implements json.Unmarshaler for FileContent.
func (f *FileContent) UnmarshalJSON(data []byte) error {
	var cj contentJSON
	if err := jsonv2.Unmarshal(data, &cj); err != nil {
		return err
	}

	var aux struct {
		MediaType        string                    `json:"media_type"`
		Data             []byte                    `json:"data"`
		ProviderMetadata map[string]jsontext.Value `json:"provider_metadata,omitempty"`
	}

	if err := jsonv2.Unmarshal(cj.Data, &aux); err != nil {
		return err
	}

	f.MediaType = aux.MediaType
	f.Data = aux.Data

	if len(aux.ProviderMetadata) > 0 {
		metadata, err := UnmarshalProviderMetadata(aux.ProviderMetadata)
		if err != nil {
			return err
		}
		f.ProviderMetadata = metadata
	}

	return nil
}

// MarshalJSON implements json.Marshaler for SourceContent.
func (s SourceContent) MarshalJSON() ([]byte, error) {
	dataBytes, err := jsonv2.Marshal(struct {
		SourceType       SourceType       `json:"source_type"`
		ID               string           `json:"id"`
		URL              string           `json:"url,omitempty"`
		Title            string           `json:"title,omitempty"`
		MediaType        string           `json:"media_type,omitempty"`
		Filename         string           `json:"filename,omitempty"`
		ProviderMetadata ProviderMetadata `json:"provider_metadata,omitempty"`
	}{
		SourceType:       s.SourceType,
		ID:               s.ID,
		URL:              s.URL,
		Title:            s.Title,
		MediaType:        s.MediaType,
		Filename:         s.Filename,
		ProviderMetadata: s.ProviderMetadata,
	})
	if err != nil {
		return nil, err
	}

	return jsonv2.Marshal(contentJSON{
		Type: string(ContentTypeSource),
		Data: jsontext.Value(dataBytes),
	})
}

// UnmarshalJSON implements json.Unmarshaler for SourceContent.
func (s *SourceContent) UnmarshalJSON(data []byte) error {
	var cj contentJSON
	if err := jsonv2.Unmarshal(data, &cj); err != nil {
		return err
	}

	var aux struct {
		SourceType       SourceType                `json:"source_type"`
		ID               string                    `json:"id"`
		URL              string                    `json:"url,omitempty"`
		Title            string                    `json:"title,omitempty"`
		MediaType        string                    `json:"media_type,omitempty"`
		Filename         string                    `json:"filename,omitempty"`
		ProviderMetadata map[string]jsontext.Value `json:"provider_metadata,omitempty"`
	}

	if err := jsonv2.Unmarshal(cj.Data, &aux); err != nil {
		return err
	}

	s.SourceType = aux.SourceType
	s.ID = aux.ID
	s.URL = aux.URL
	s.Title = aux.Title
	s.MediaType = aux.MediaType
	s.Filename = aux.Filename

	if len(aux.ProviderMetadata) > 0 {
		metadata, err := UnmarshalProviderMetadata(aux.ProviderMetadata)
		if err != nil {
			return err
		}
		s.ProviderMetadata = metadata
	}

	return nil
}

// MarshalJSON implements json.Marshaler for ToolCallContent.
func (t ToolCallContent) MarshalJSON() ([]byte, error) {
	var validationErrMsg *string
	if t.ValidationError != nil {
		msg := t.ValidationError.Error()
		validationErrMsg = &msg
	}
	dataBytes, err := jsonv2.Marshal(struct {
		ToolCallID       string           `json:"tool_call_id"`
		ToolName         string           `json:"tool_name"`
		Input            string           `json:"input"`
		ProviderExecuted bool             `json:"provider_executed"`
		ProviderMetadata ProviderMetadata `json:"provider_metadata,omitempty"`
		Invalid          bool             `json:"invalid,omitempty"`
		ValidationError  *string          `json:"validation_error,omitempty"`
	}{
		ToolCallID:       t.ToolCallID,
		ToolName:         t.ToolName,
		Input:            t.Input,
		ProviderExecuted: t.ProviderExecuted,
		ProviderMetadata: t.ProviderMetadata,
		Invalid:          t.Invalid,
		ValidationError:  validationErrMsg,
	})
	if err != nil {
		return nil, err
	}

	return jsonv2.Marshal(contentJSON{
		Type: string(ContentTypeToolCall),
		Data: jsontext.Value(dataBytes),
	})
}

// UnmarshalJSONV2 implements jsonv2.UnmarshalerFrom for ToolCallContent.
func (t *ToolCallContent) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var cj contentJSON
	if err := jsonv2.UnmarshalDecode(dec, &cj); err != nil {
		return err
	}

	var aux struct {
		ToolCallID       string                    `json:"tool_call_id"`
		ToolName         string                    `json:"tool_name"`
		Input            string                    `json:"input"`
		ProviderExecuted bool                      `json:"provider_executed"`
		ProviderMetadata map[string]jsontext.Value `json:"provider_metadata,omitzero"`
		Invalid          bool                      `json:"invalid,omitzero"`
		ValidationError  *string                   `json:"validation_error,omitzero"`
	}

	if err := jsonv2.Unmarshal(cj.Data, &aux); err != nil {
		return err
	}

	t.ToolCallID = aux.ToolCallID
	t.ToolName = aux.ToolName
	t.Input = aux.Input
	t.ProviderExecuted = aux.ProviderExecuted
	t.Invalid = aux.Invalid
	if aux.ValidationError != nil {
		t.ValidationError = errors.New(*aux.ValidationError)
	}

	if len(aux.ProviderMetadata) > 0 {
		metadata, err := UnmarshalProviderMetadata(aux.ProviderMetadata)
		if err != nil {
			return err
		}
		t.ProviderMetadata = metadata
	}

	return nil
}

// MarshalJSONV2 implements jsonv2.MarshalerTo for ToolResultContent.
func (t ToolResultContent) MarshalJSONTo(enc *jsontext.Encoder) error {
	dataBytes, err := jsonv2.Marshal(struct {
		ToolCallID       string                  `json:"tool_call_id"`
		ToolName         string                  `json:"tool_name"`
		Result           ToolResultOutputContent `json:"result"`
		ClientMetadata   string                  `json:"client_metadata,omitzero"`
		ProviderExecuted bool                    `json:"provider_executed"`
		ProviderMetadata ProviderMetadata        `json:"provider_metadata,omitzero"`
	}{
		ToolCallID:       t.ToolCallID,
		ToolName:         t.ToolName,
		Result:           t.Result,
		ClientMetadata:   t.ClientMetadata,
		ProviderExecuted: t.ProviderExecuted,
		ProviderMetadata: t.ProviderMetadata,
	})
	if err != nil {
		return err
	}

	return jsonv2.MarshalEncode(enc, contentJSON{
		Type: string(ContentTypeToolResult),
		Data: jsontext.Value(dataBytes),
	})
}

// UnmarshalJSONV2 implements jsonv2.UnmarshalerFrom for ToolResultContent.
func (t *ToolResultContent) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var cj contentJSON
	if err := jsonv2.UnmarshalDecode(dec, &cj); err != nil {
		return err
	}

	var aux struct {
		ToolCallID       string                    `json:"tool_call_id"`
		ToolName         string                    `json:"tool_name"`
		Result           jsontext.Value            `json:"result"`
		ClientMetadata   string                    `json:"client_metadata,omitzero"`
		ProviderExecuted bool                      `json:"provider_executed"`
		ProviderMetadata map[string]jsontext.Value `json:"provider_metadata,omitzero"`
	}

	if err := jsonv2.Unmarshal(cj.Data, &aux); err != nil {
		return err
	}

	t.ToolCallID = aux.ToolCallID
	t.ToolName = aux.ToolName
	t.ClientMetadata = aux.ClientMetadata
	t.ProviderExecuted = aux.ProviderExecuted

	// Unmarshal the Result field
	result, err := UnmarshalToolResultOutputContent([]byte(aux.Result))
	if err != nil {
		return fmt.Errorf("failed to unmarshal tool result output: %w", err)
	}
	t.Result = result

	if len(aux.ProviderMetadata) > 0 {
		metadata, err := UnmarshalProviderMetadata(aux.ProviderMetadata)
		if err != nil {
			return err
		}
		t.ProviderMetadata = metadata
	}

	return nil
}

// MarshalJSONV2 implements jsonv2.MarshalerTo for ToolResultOutputContentText.
func (t ToolResultOutputContentText) MarshalJSONTo(enc *jsontext.Encoder) error {
	type alias ToolResultOutputContentText
	dataBytes, err := jsonv2.Marshal(alias(t))
	if err != nil {
		return err
	}

	return jsonv2.MarshalEncode(enc, toolResultOutputJSON{
		Type: string(ToolResultContentTypeText),
		Data: jsontext.Value(dataBytes),
	})
}

// UnmarshalJSONV2 implements jsonv2.UnmarshalerFrom for ToolResultOutputContentText.
func (t *ToolResultOutputContentText) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var tr toolResultOutputJSON
	if err := jsonv2.UnmarshalDecode(dec, &tr); err != nil {
		return err
	}

	type alias ToolResultOutputContentText
	var temp alias

	if err := jsonv2.Unmarshal([]byte(tr.Data), &temp); err != nil {
		return err
	}

	*t = ToolResultOutputContentText(temp)
	return nil
}

// MarshalJSONV2 implements jsonv2.MarshalerTo for ToolResultOutputContentError.
func (t ToolResultOutputContentError) MarshalJSONTo(enc *jsontext.Encoder) error {
	errMsg := ""
	if t.Error != nil {
		errMsg = t.Error.Error()
	}
	dataBytes, err := jsonv2.Marshal(struct {
		Error string `json:"error"`
	}{
		Error: errMsg,
	})
	if err != nil {
		return err
	}

	return jsonv2.MarshalEncode(enc, toolResultOutputJSON{
		Type: string(ToolResultContentTypeError),
		Data: jsontext.Value(dataBytes),
	})
}

// UnmarshalJSONV2 implements jsonv2.UnmarshalerFrom for ToolResultOutputContentError.
func (t *ToolResultOutputContentError) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var tr toolResultOutputJSON
	if err := jsonv2.UnmarshalDecode(dec, &tr); err != nil {
		return err
	}

	var temp struct {
		Error string `json:"error"`
	}

	if err := jsonv2.Unmarshal([]byte(tr.Data), &temp); err != nil {
		return err
	}
	if temp.Error != "" {
		t.Error = errors.New(temp.Error)
	}
	return nil
}

// MarshalJSONV2 implements jsonv2.MarshalerTo for ToolResultOutputContentMedia.
func (t ToolResultOutputContentMedia) MarshalJSONTo(enc *jsontext.Encoder) error {
	type alias ToolResultOutputContentMedia
	dataBytes, err := jsonv2.Marshal(alias(t))
	if err != nil {
		return err
	}

	return jsonv2.MarshalEncode(enc, toolResultOutputJSON{
		Type: string(ToolResultContentTypeMedia),
		Data: jsontext.Value(dataBytes),
	})
}

// UnmarshalJSONV2 implements jsonv2.UnmarshalerFrom for ToolResultOutputContentMedia.
func (t *ToolResultOutputContentMedia) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var tr toolResultOutputJSON
	if err := jsonv2.UnmarshalDecode(dec, &tr); err != nil {
		return err
	}

	type alias ToolResultOutputContentMedia
	var temp alias

	if err := jsonv2.Unmarshal([]byte(tr.Data), &temp); err != nil {
		return err
	}

	*t = ToolResultOutputContentMedia(temp)
	return nil
}

// MarshalJSONV2 implements jsonv2.MarshalerTo for TextPart.
func (t TextPart) MarshalJSONTo(enc *jsontext.Encoder) error {
	dataBytes, err := jsonv2.Marshal(struct {
		Text            string          `json:"text"`
		ProviderOptions ProviderOptions `json:"provider_options,omitzero"`
	}{
		Text:            t.Text,
		ProviderOptions: t.ProviderOptions,
	})
	if err != nil {
		return err
	}

	return jsonv2.MarshalEncode(enc, messagePartJSON{
		Type: string(ContentTypeText),
		Data: jsontext.Value(dataBytes),
	})
}

// UnmarshalJSONV2 implements jsonv2.UnmarshalerFrom for TextPart.
func (t *TextPart) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var mpj messagePartJSON
	if err := jsonv2.UnmarshalDecode(dec, &mpj); err != nil {
		return err
	}

	var aux struct {
		Text            string                    `json:"text"`
		ProviderOptions map[string]jsontext.Value `json:"provider_options,omitzero"`
	}

	if err := jsonv2.Unmarshal([]byte(mpj.Data), &aux); err != nil {
		return err
	}

	t.Text = aux.Text

	if len(aux.ProviderOptions) > 0 {
		options, err := UnmarshalProviderOptions(aux.ProviderOptions)
		if err != nil {
			return err
		}
		t.ProviderOptions = options
	}

	return nil
}

// MarshalJSONV2 implements jsonv2.MarshalerTo for ReasoningPart.
func (r ReasoningPart) MarshalJSONTo(enc *jsontext.Encoder) error {
	dataBytes, err := jsonv2.Marshal(struct {
		Text            string          `json:"text"`
		ProviderOptions ProviderOptions `json:"provider_options,omitzero"`
	}{
		Text:            r.Text,
		ProviderOptions: r.ProviderOptions,
	})
	if err != nil {
		return err
	}

	return jsonv2.MarshalEncode(enc, messagePartJSON{
		Type: string(ContentTypeReasoning),
		Data: jsontext.Value(dataBytes),
	})
}

// UnmarshalJSONV2 implements jsonv2.UnmarshalerFrom for ReasoningPart.
func (r *ReasoningPart) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var mpj messagePartJSON
	if err := jsonv2.UnmarshalDecode(dec, &mpj); err != nil {
		return err
	}

	var aux struct {
		Text            string                    `json:"text"`
		ProviderOptions map[string]jsontext.Value `json:"provider_options,omitzero"`
	}

	if err := jsonv2.Unmarshal([]byte(mpj.Data), &aux); err != nil {
		return err
	}

	r.Text = aux.Text

	if len(aux.ProviderOptions) > 0 {
		options, err := UnmarshalProviderOptions(aux.ProviderOptions)
		if err != nil {
			return err
		}
		r.ProviderOptions = options
	}

	return nil
}

// MarshalJSONV2 implements jsonv2.MarshalerTo for FilePart.
func (f FilePart) MarshalJSONTo(enc *jsontext.Encoder) error {
	dataBytes, err := jsonv2.Marshal(struct {
		Filename        string          `json:"filename"`
		Data            []byte          `json:"data"`
		MediaType       string          `json:"media_type"`
		ProviderOptions ProviderOptions `json:"provider_options,omitzero"`
	}{
		Filename:        f.Filename,
		Data:            f.Data,
		MediaType:       f.MediaType,
		ProviderOptions: f.ProviderOptions,
	})
	if err != nil {
		return err
	}

	return jsonv2.MarshalEncode(enc, messagePartJSON{
		Type: string(ContentTypeFile),
		Data: jsontext.Value(dataBytes),
	})
}

// UnmarshalJSONV2 implements jsonv2.UnmarshalerFrom for FilePart.
func (f *FilePart) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var mpj messagePartJSON
	if err := jsonv2.UnmarshalDecode(dec, &mpj); err != nil {
		return err
	}

	var aux struct {
		Filename        string                    `json:"filename"`
		Data            []byte                    `json:"data"`
		MediaType       string                    `json:"media_type"`
		ProviderOptions map[string]jsontext.Value `json:"provider_options,omitzero"`
	}

	if err := jsonv2.Unmarshal([]byte(mpj.Data), &aux); err != nil {
		return err
	}

	f.Filename = aux.Filename
	f.Data = aux.Data
	f.MediaType = aux.MediaType

	if len(aux.ProviderOptions) > 0 {
		options, err := UnmarshalProviderOptions(aux.ProviderOptions)
		if err != nil {
			return err
		}
		f.ProviderOptions = options
	}

	return nil
}

// MarshalJSONV2 implements jsonv2.MarshalerTo for ToolCallPart.
func (t ToolCallPart) MarshalJSONTo(enc *jsontext.Encoder) error {
	dataBytes, err := jsonv2.Marshal(struct {
		ToolCallID       string          `json:"tool_call_id"`
		ToolName         string          `json:"tool_name"`
		Input            string          `json:"input"`
		ProviderExecuted bool            `json:"provider_executed"`
		ProviderOptions  ProviderOptions `json:"provider_options,omitzero"`
	}{
		ToolCallID:       t.ToolCallID,
		ToolName:         t.ToolName,
		Input:            t.Input,
		ProviderExecuted: t.ProviderExecuted,
		ProviderOptions:  t.ProviderOptions,
	})
	if err != nil {
		return err
	}

	return jsonv2.MarshalEncode(enc, messagePartJSON{
		Type: string(ContentTypeToolCall),
		Data: jsontext.Value(dataBytes),
	})
}

// UnmarshalJSONV2 implements jsonv2.UnmarshalerFrom for ToolCallPart.
func (t *ToolCallPart) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var mpj messagePartJSON
	if err := jsonv2.UnmarshalDecode(dec, &mpj); err != nil {
		return err
	}

	var aux struct {
		ToolCallID       string                    `json:"tool_call_id"`
		ToolName         string                    `json:"tool_name"`
		Input            string                    `json:"input"`
		ProviderExecuted bool                      `json:"provider_executed"`
		ProviderOptions  map[string]jsontext.Value `json:"provider_options,omitzero"`
	}

	if err := jsonv2.Unmarshal([]byte(mpj.Data), &aux); err != nil {
		return err
	}

	t.ToolCallID = aux.ToolCallID
	t.ToolName = aux.ToolName
	t.Input = aux.Input
	t.ProviderExecuted = aux.ProviderExecuted

	if len(aux.ProviderOptions) > 0 {
		options, err := UnmarshalProviderOptions(aux.ProviderOptions)
		if err != nil {
			return err
		}
		t.ProviderOptions = options
	}

	return nil
}

// MarshalJSONV2 implements jsonv2.MarshalerTo for ToolResultPart.
func (t ToolResultPart) MarshalJSONTo(enc *jsontext.Encoder) error {
	dataBytes, err := jsonv2.Marshal(struct {
		ToolCallID      string                  `json:"tool_call_id"`
		Output          ToolResultOutputContent `json:"output"`
		ProviderOptions ProviderOptions         `json:"provider_options,omitzero"`
	}{
		ToolCallID:      t.ToolCallID,
		Output:          t.Output,
		ProviderOptions: t.ProviderOptions,
	})
	if err != nil {
		return err
	}

	return jsonv2.MarshalEncode(enc, messagePartJSON{
		Type: string(ContentTypeToolResult),
		Data: jsontext.Value(dataBytes),
	})
}

// UnmarshalJSONV2 implements jsonv2.UnmarshalerFrom for ToolResultPart.
func (t *ToolResultPart) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var mpj messagePartJSON
	if err := jsonv2.UnmarshalDecode(dec, &mpj); err != nil {
		return err
	}

	var aux struct {
		ToolCallID      string                    `json:"tool_call_id"`
		Output          jsontext.Value            `json:"output"`
		ProviderOptions map[string]jsontext.Value `json:"provider_options,omitzero"`
	}

	if err := jsonv2.Unmarshal([]byte(mpj.Data), &aux); err != nil {
		return err
	}

	t.ToolCallID = aux.ToolCallID

	// Unmarshal the Output field
	output, err := UnmarshalToolResultOutputContent([]byte(aux.Output))
	if err != nil {
		return fmt.Errorf("failed to unmarshal tool result output: %w", err)
	}
	t.Output = output

	if len(aux.ProviderOptions) > 0 {
		options, err := UnmarshalProviderOptions(aux.ProviderOptions)
		if err != nil {
			return err
		}
		t.ProviderOptions = options
	}

	return nil
}

// UnmarshalJSONV2 implements jsonv2.UnmarshalerFrom for Message.
func (m *Message) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var aux struct {
		Role            MessageRole               `json:"role"`
		Content         []jsontext.Value          `json:"content"`
		ProviderOptions map[string]jsontext.Value `json:"provider_options"`
	}

	if err := jsonv2.UnmarshalDecode(dec, &aux); err != nil {
		return err
	}

	m.Role = aux.Role

	m.Content = make([]MessagePart, len(aux.Content))
	for i, rawPart := range aux.Content {
		part, err := UnmarshalMessagePart([]byte(rawPart))
		if err != nil {
			return fmt.Errorf("failed to unmarshal message part at index %d: %w", i, err)
		}
		m.Content[i] = part
	}

	if len(aux.ProviderOptions) > 0 {
		options, err := UnmarshalProviderOptions(aux.ProviderOptions)
		if err != nil {
			return err
		}
		m.ProviderOptions = options
	}

	return nil
}

// MarshalJSONV2 implements jsonv2.MarshalerTo for FunctionTool.
func (f FunctionTool) MarshalJSONTo(enc *jsontext.Encoder) error {
	dataBytes, err := jsonv2.Marshal(struct {
		Name            string          `json:"name"`
		Description     string          `json:"description"`
		InputSchema     map[string]any  `json:"input_schema"`
		ProviderOptions ProviderOptions `json:"provider_options,omitzero"`
	}{
		Name:            f.Name,
		Description:     f.Description,
		InputSchema:     f.InputSchema,
		ProviderOptions: f.ProviderOptions,
	})
	if err != nil {
		return err
	}

	return jsonv2.MarshalEncode(enc, toolJSON{
		Type: string(ToolTypeFunction),
		Data: jsontext.Value(dataBytes),
	})
}

// UnmarshalJSONV2 implements jsonv2.UnmarshalerFrom for FunctionTool.
func (f *FunctionTool) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var tj toolJSON
	if err := jsonv2.UnmarshalDecode(dec, &tj); err != nil {
		return err
	}

	var aux struct {
		Name            string                    `json:"name"`
		Description     string                    `json:"description"`
		InputSchema     map[string]any            `json:"input_schema"`
		ProviderOptions map[string]jsontext.Value `json:"provider_options,omitzero"`
	}

	if err := jsonv2.Unmarshal([]byte(tj.Data), &aux); err != nil {
		return err
	}

	f.Name = aux.Name
	f.Description = aux.Description
	f.InputSchema = aux.InputSchema

	if len(aux.ProviderOptions) > 0 {
		options, err := UnmarshalProviderOptions(aux.ProviderOptions)
		if err != nil {
			return err
		}
		f.ProviderOptions = options
	}

	return nil
}

// MarshalJSONV2 implements jsonv2.MarshalerTo for ProviderDefinedTool.
func (p ProviderDefinedTool) MarshalJSONTo(enc *jsontext.Encoder) error {
	type alias ProviderDefinedTool
	dataBytes, err := jsonv2.Marshal(alias(p))
	if err != nil {
		return err
	}

	return jsonv2.MarshalEncode(enc, toolJSON{
		Type: string(ToolTypeProviderDefined),
		Data: jsontext.Value(dataBytes),
	})
}

// UnmarshalJSONV2 implements jsonv2.UnmarshalerFrom for ProviderDefinedTool.
func (p *ProviderDefinedTool) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var tj toolJSON
	if err := jsonv2.UnmarshalDecode(dec, &tj); err != nil {
		return err
	}

	type alias ProviderDefinedTool
	var aux alias

	if err := jsonv2.Unmarshal([]byte(tj.Data), &aux); err != nil {
		return err
	}

	*p = ProviderDefinedTool(aux)

	return nil
}

// UnmarshalTool unmarshals JSON into the appropriate Tool type.
func UnmarshalTool(data []byte) (Tool, error) {
	var tj toolJSON
	if err := jsonv2.Unmarshal(data, &tj); err != nil {
		return nil, err
	}

	switch ToolType(tj.Type) {
	case ToolTypeFunction:
		var tool FunctionTool
		if err := jsonv2.Unmarshal(data, &tool); err != nil {
			return nil, err
		}
		return tool, nil
	case ToolTypeProviderDefined:
		var tool ProviderDefinedTool
		if err := jsonv2.Unmarshal(data, &tool); err != nil {
			return nil, err
		}
		return tool, nil
	default:
		return nil, fmt.Errorf("unknown tool type: %s", tj.Type)
	}
}

// UnmarshalContent unmarshals JSON into the appropriate Content type.
func UnmarshalContent(data []byte) (Content, error) {
	var cj contentJSON
	if err := jsonv2.Unmarshal(data, &cj); err != nil {
		return nil, err
	}

	switch ContentType(cj.Type) {
	case ContentTypeText:
		var content TextContent
		if err := jsonv2.Unmarshal(data, &content); err != nil {
			return nil, err
		}
		return content, nil
	case ContentTypeReasoning:
		var content ReasoningContent
		if err := jsonv2.Unmarshal(data, &content); err != nil {
			return nil, err
		}
		return content, nil
	case ContentTypeFile:
		var content FileContent
		if err := jsonv2.Unmarshal(data, &content); err != nil {
			return nil, err
		}
		return content, nil
	case ContentTypeSource:
		var content SourceContent
		if err := jsonv2.Unmarshal(data, &content); err != nil {
			return nil, err
		}
		return content, nil
	case ContentTypeToolCall:
		var content ToolCallContent
		if err := jsonv2.Unmarshal(data, &content); err != nil {
			return nil, err
		}
		return content, nil
	case ContentTypeToolResult:
		var content ToolResultContent
		if err := jsonv2.Unmarshal(data, &content); err != nil {
			return nil, err
		}
		return content, nil
	default:
		return nil, fmt.Errorf("unknown content type: %s", cj.Type)
	}
}

// UnmarshalMessagePart unmarshals JSON into the appropriate MessagePart type.
func UnmarshalMessagePart(data []byte) (MessagePart, error) {
	var mpj messagePartJSON
	if err := jsonv2.Unmarshal(data, &mpj); err != nil {
		return nil, err
	}

	switch ContentType(mpj.Type) {
	case ContentTypeText:
		var part TextPart
		if err := jsonv2.Unmarshal(data, &part); err != nil {
			return nil, err
		}
		return part, nil
	case ContentTypeReasoning:
		var part ReasoningPart
		if err := jsonv2.Unmarshal(data, &part); err != nil {
			return nil, err
		}
		return part, nil
	case ContentTypeFile:
		var part FilePart
		if err := jsonv2.Unmarshal(data, &part); err != nil {
			return nil, err
		}
		return part, nil
	case ContentTypeToolCall:
		var part ToolCallPart
		if err := jsonv2.Unmarshal(data, &part); err != nil {
			return nil, err
		}
		return part, nil
	case ContentTypeToolResult:
		var part ToolResultPart
		if err := jsonv2.Unmarshal(data, &part); err != nil {
			return nil, err
		}
		return part, nil
	default:
		return nil, fmt.Errorf("unknown message part type: %s", mpj.Type)
	}
}

// UnmarshalToolResultOutputContent unmarshals JSON into the appropriate ToolResultOutputContent type.
func UnmarshalToolResultOutputContent(data []byte) (ToolResultOutputContent, error) {
	var troj toolResultOutputJSON
	if err := jsonv2.Unmarshal(data, &troj); err != nil {
		return nil, err
	}

	switch ToolResultContentType(troj.Type) {
	case ToolResultContentTypeText:
		var content ToolResultOutputContentText
		if err := jsonv2.Unmarshal(data, &content); err != nil {
			return nil, err
		}
		return content, nil
	case ToolResultContentTypeError:
		var content ToolResultOutputContentError
		if err := jsonv2.Unmarshal(data, &content); err != nil {
			return nil, err
		}
		return content, nil
	case ToolResultContentTypeMedia:
		var content ToolResultOutputContentMedia
		if err := jsonv2.Unmarshal(data, &content); err != nil {
			return nil, err
		}
		return content, nil
	default:
		return nil, fmt.Errorf("unknown tool result output content type: %s", troj.Type)
	}
}
