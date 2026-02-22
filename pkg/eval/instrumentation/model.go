package instrumentation

import (
	"context"
	"encoding/json"
	"time"

	fantasy "charm.land/fantasy"
)

type InstrumentedCall struct {
	duration  time.Duration
	toolCalls []InstrumentedToolCall
	usage     fantasy.Usage
}

type InstrumentedToolCall struct {
	Name   string                 `json:"-"`
	Args   map[string]interface{} `json:"-"`
	Result string                 `json:"-"`
}

type InstrumentedLanguageModel struct {
	inner      fantasy.LanguageModel
	calls      []InstrumentedCall
	totalUsage fantasy.Usage
}

var _ fantasy.LanguageModel = (*InstrumentedLanguageModel)(nil)

func Wrap(inner fantasy.LanguageModel) *InstrumentedLanguageModel {
	return &InstrumentedLanguageModel{
		inner: inner,
	}
}

func (m *InstrumentedLanguageModel) Provider() string { return m.inner.Provider() }
func (m *InstrumentedLanguageModel) Model() string    { return m.inner.Model() }

func (m *InstrumentedLanguageModel) GenerateObject(ctx context.Context, call fantasy.ObjectCall) (*fantasy.ObjectResponse, error) {
	return m.inner.GenerateObject(ctx, call)
}

func (m *InstrumentedLanguageModel) StreamObject(ctx context.Context, call fantasy.ObjectCall) (fantasy.ObjectStreamResponse, error) {
	return m.inner.StreamObject(ctx, call)
}

func (m *InstrumentedLanguageModel) Generate(ctx context.Context, call fantasy.Call) (*fantasy.Response, error) {
	start := time.Now()
	resp, err := m.inner.Generate(ctx, call)
	duration := time.Since(start)

	instrCall := InstrumentedCall{duration: duration}
	if err == nil && resp != nil {
		instrCall.usage = resp.Usage
		m.totalUsage.InputTokens += resp.Usage.InputTokens
		m.totalUsage.OutputTokens += resp.Usage.OutputTokens
		m.totalUsage.TotalTokens += resp.Usage.TotalTokens
		m.totalUsage.ReasoningTokens += resp.Usage.ReasoningTokens
		m.totalUsage.CacheReadTokens += resp.Usage.CacheReadTokens

		for _, tc := range resp.Content.ToolCalls() {
			var args map[string]interface{}
			_ = json.Unmarshal([]byte(tc.Input), &args)
			instrCall.toolCalls = append(instrCall.toolCalls, InstrumentedToolCall{
				Name: tc.ToolName,
				Args: args,
			})
		}
	}

	m.calls = append(m.calls, instrCall)
	return resp, err
}

func (m *InstrumentedLanguageModel) Stream(ctx context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
	start := time.Now()
	stream, err := m.inner.Stream(ctx, call)
	if err != nil {
		m.calls = append(m.calls, InstrumentedCall{duration: time.Since(start)})
		return nil, err
	}

	instrCall := InstrumentedCall{}
	wrappedStream := func(yield func(fantasy.StreamPart) bool) {
		stream(func(part fantasy.StreamPart) bool {
			switch part.Type {
			case fantasy.StreamPartTypeFinish:
				instrCall.usage = part.Usage
				m.totalUsage.InputTokens += part.Usage.InputTokens
				m.totalUsage.OutputTokens += part.Usage.OutputTokens
				m.totalUsage.TotalTokens += part.Usage.TotalTokens
				m.totalUsage.ReasoningTokens += part.Usage.ReasoningTokens
				m.totalUsage.CacheReadTokens += part.Usage.CacheReadTokens
			case fantasy.StreamPartTypeToolCall:
				var args map[string]interface{}
				_ = json.Unmarshal([]byte(part.ToolCallInput), &args)
				instrCall.toolCalls = append(instrCall.toolCalls, InstrumentedToolCall{
					Name: part.ToolCallName,
					Args: args,
				})
			}
			return yield(part)
		})
		instrCall.duration = time.Since(start)
		m.calls = append(m.calls, instrCall)
	}

	return wrappedStream, nil
}

func (m *InstrumentedLanguageModel) Calls() []InstrumentedCall {
	return m.calls
}

func (m *InstrumentedLanguageModel) Usage() fantasy.Usage {
	return m.totalUsage
}
