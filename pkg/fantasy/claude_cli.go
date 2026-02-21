// DragonScale - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 DragonScale contributors

package fantasy

import (
	"bytes"
	"context"
	"fmt"
	"iter"
	"os/exec"
	"strings"

	"charm.land/fantasy"
	jsonv2 "github.com/go-json-experiment/json"
)

// claudeCliProvider implements fantasy.Provider using the claude CLI subprocess.
type claudeCliProvider struct {
	workspace string
}

// claudeCliModel implements fantasy.LanguageModel.
type claudeCliModel struct {
	workspace string
	modelID   string
}

func newClaudeCliProvider(workspace string) fantasy.Provider {
	return &claudeCliProvider{workspace: workspace}
}

func (p *claudeCliProvider) Name() string {
	return "claude-cli"
}

func (p *claudeCliProvider) LanguageModel(_ context.Context, modelID string) (fantasy.LanguageModel, error) {
	return &claudeCliModel{
		workspace: p.workspace,
		modelID:   modelID,
	}, nil
}

func (m *claudeCliModel) Provider() string { return "claude-cli" }
func (m *claudeCliModel) Model() string    { return m.modelID }

func (m *claudeCliModel) Generate(ctx context.Context, call fantasy.Call) (*fantasy.Response, error) {
	systemPrompt, userPrompt := extractPromptsFromCall(call)

	args := []string{"-p", "--output-format", "json", "--dangerously-skip-permissions", "--no-chrome"}
	if systemPrompt != "" {
		args = append(args, "--system-prompt", systemPrompt)
	}
	if m.modelID != "" && m.modelID != "claude-code" {
		args = append(args, "--model", m.modelID)
	}
	args = append(args, "-") // read from stdin

	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = m.workspace
	cmd.Stdin = bytes.NewReader([]byte(userPrompt))

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("claude CLI failed: %w", err)
	}

	text := extractTextFromCLIOutput(output)

	return &fantasy.Response{
		Content:      fantasy.ResponseContent{fantasy.TextContent{Text: text}},
		FinishReason: fantasy.FinishReasonStop,
	}, nil
}

func (m *claudeCliModel) Stream(ctx context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
	// Fall back to Generate for CLI provider — streaming not supported.
	resp, err := m.Generate(ctx, call)
	if err != nil {
		return nil, err
	}

	text := resp.Content.Text()
	return func(yield func(fantasy.StreamPart) bool) {
		// Emit the text as a delta followed by a finish signal.
		if text != "" {
			if !yield(fantasy.StreamPart{
				Type:  fantasy.StreamPartTypeTextDelta,
				Delta: text,
			}) {
				return
			}
		}
		yield(fantasy.StreamPart{
			Type:         fantasy.StreamPartTypeFinish,
			Usage:        resp.Usage,
			FinishReason: resp.FinishReason,
		})
	}, nil
}

func (m *claudeCliModel) GenerateObject(ctx context.Context, call fantasy.ObjectCall) (*fantasy.ObjectResponse, error) {
	return nil, fmt.Errorf("claude-cli does not support object generation")
}

func (m *claudeCliModel) StreamObject(ctx context.Context, call fantasy.ObjectCall) (fantasy.ObjectStreamResponse, error) {
	return nil, fmt.Errorf("claude-cli does not support object streaming")
}

// extractPromptsFromCall extracts system and user prompts from a Fantasy Call.
func extractPromptsFromCall(call fantasy.Call) (system, user string) {
	var systemParts, userParts []string

	for _, msg := range call.Prompt {
		text := extractText(msg.Content)
		switch msg.Role {
		case "system":
			systemParts = append(systemParts, text)
		case "user":
			userParts = append(userParts, text)
		case "assistant":
			// Include assistant responses for context
			userParts = append(userParts, "Assistant: "+text)
		}
	}

	return strings.Join(systemParts, "\n\n"), strings.Join(userParts, "\n\n")
}

func extractText(parts []fantasy.MessagePart) string {
	var texts []string
	for _, p := range parts {
		if tp, ok := p.(fantasy.TextPart); ok {
			texts = append(texts, tp.Text)
		}
	}
	return strings.Join(texts, "\n")
}

// extractTextFromCLIOutput parses the claude CLI JSON output.
func extractTextFromCLIOutput(output []byte) string {
	// Try JSON array format first (claude outputs array of objects)
	var results []struct {
		Type    string `json:"type"`
		Content string `json:"content"`
		Text    string `json:"text"`
	}
	if err := jsonv2.Unmarshal(output, &results); err == nil {
		var texts []string
		for _, r := range results {
			switch {
			case r.Content != "":
				texts = append(texts, r.Content)
			case r.Text != "":
				texts = append(texts, r.Text)
			}
		}
		if len(texts) > 0 {
			return strings.Join(texts, "\n")
		}
	}

	// Try single JSON object
	var single struct {
		Result string `json:"result"`
		Text   string `json:"text"`
	}
	if err := jsonv2.Unmarshal(output, &single); err == nil {
		if single.Result != "" {
			return single.Result
		}
		if single.Text != "" {
			return single.Text
		}
	}

	// Fallback: return raw output
	return strings.TrimSpace(string(output))
}

// Ensure iter.Seq is properly typed for the compiler.
var _ iter.Seq[fantasy.StreamPart] = fantasy.StreamResponse(nil)
