package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"charm.land/fantasy"
	"github.com/sipeed/picoclaw/pkg/ids"
	"github.com/sipeed/picoclaw/pkg/memory/sqlc"
	"github.com/sipeed/picoclaw/pkg/pcerrors"
)

const defaultToolMaxConcurrency = 4

type ctxStepIndexKey struct{}

func WithStepIndex(ctx context.Context, stepIndex int) context.Context {
	return context.WithValue(ctx, ctxStepIndexKey{}, stepIndex)
}

func StepIndexFromCtx(ctx context.Context) int {
	v := ctx.Value(ctxStepIndexKey{})
	if v == nil {
		return 0
	}
	if i, ok := v.(int); ok {
		return i
	}
	return 0
}

// OffloadingToolRuntime wraps a base ToolRuntime and applies tool result
// offloading policy:
//   - Always offload full results to KV delegate.
//   - If result is below threshold, keep it inline as-is.
//   - If above threshold, truncate inline output and include an index/instructions.
//   - Store chunked payload for targeted retrieval.
type OffloadingToolRuntime struct {
	Base fantasy.ToolRuntime

	KV      KVDelegate
	Queries *sqlc.Queries

	ConversationID ids.UUID
	RunID          ids.UUID

	ThresholdChars int
	ChunkChars     int
}

func (r OffloadingToolRuntime) Execute(ctx context.Context, tools []fantasy.AgentTool, toolCalls []fantasy.ToolCallContent, _ func(result fantasy.ToolResultContent) error) ([]fantasy.ToolResultContent, error) {
	if len(toolCalls) == 0 {
		return nil, nil
	}
	if r.Base == nil {
		r.Base = fantasy.DAGToolRuntime{MaxConcurrency: defaultToolMaxConcurrency}
	}
	if r.KV == nil {
		return nil, pcerrors.New(pcerrors.CodeFailedPrecondition, "KV delegate is nil")
	}
	if r.Queries == nil {
		return nil, pcerrors.New(pcerrors.CodeFailedPrecondition, "db queries is nil")
	}
	if r.ConversationID.IsZero() || r.RunID.IsZero() {
		return nil, pcerrors.New(pcerrors.CodeInvalidArgument, "conversation_id/run_id is required")
	}

	threshold := r.ThresholdChars
	if threshold <= 0 {
		threshold = 4_000
	}
	chunkChars := r.ChunkChars
	if chunkChars <= 0 {
		chunkChars = 2_000
	}

	stepIndex := StepIndexFromCtx(ctx)

	results, err := r.Base.Execute(ctx, tools, toolCalls, nil)
	if err != nil {
		return nil, err
	}

	for i := range results {
		tc := toolCalls[i]
		res := results[i]

		fullKey := toolResultFullKey(r.ConversationID, r.RunID, stepIndex, tc.ToolCallID)

		payload, payloadType, payloadText := toolResultPayload(res)
		b, marshalErr := json.Marshal(payload)
		if marshalErr != nil {
			b = []byte(`{"error":"failed to marshal tool result payload"}`)
			payloadType = "error"
		}

		if putErr := r.KV.Put(ctx, fullKey, b); putErr != nil {
			return nil, putErr
		}

		preview := payloadText
		chunkCount := int64(0)

		if payloadType == "text" && len([]rune(payloadText)) > threshold {
			chunks := chunkString(payloadText, chunkChars)
			chunkCount = int64(len(chunks))

			for ci, chunk := range chunks {
				chunkKey := toolResultChunkKey(r.ConversationID, r.RunID, stepIndex, tc.ToolCallID, ci)
				if putErr := r.KV.Put(ctx, chunkKey, []byte(chunk)); putErr != nil {
					return nil, putErr
				}
			}

			preview = truncateRunes(payloadText, threshold)
			preview = strings.TrimSpace(preview) + "\n\n" +
				"[TRUNCATED]\n" +
				"- run_id: " + r.RunID.String() + "\n" +
				"- tool_call_id: " + tc.ToolCallID + "\n" +
				"- chunk_count: " + strconv.FormatInt(chunkCount, 10) + "\n" +
				"Use tool_result_search to retrieve more (prefer chunk ranges)."

			results[i].Result = fantasy.ToolResultOutputContentText{Text: preview}
		}

		if len([]rune(preview)) > 8_000 {
			preview = truncateRunes(preview, 8_000)
		}

		meta := map[string]any{
			"result_type": payloadType,
			"step_index":  stepIndex,
		}
		metaJSON, _ := json.Marshal(meta)

		var previewPtr *string
		if strings.TrimSpace(preview) != "" {
			previewPtr = &preview
		}

		_, dbErr := r.Queries.AddAgentToolResult(ctx, sqlc.AddAgentToolResultParams{
			ID:             ids.New(),
			ConversationID: r.ConversationID,
			RunID:          r.RunID,
			StepIndex:      int64(stepIndex),
			ToolCallID:     tc.ToolCallID,
			ToolName:       tc.ToolName,
			FullKey:        fullKey,
			Preview:        previewPtr,
			ChunkCount:     chunkCount,
			MetadataJson:   metaJSON,
		})
		if dbErr != nil {
			return nil, dbErr
		}
	}

	return results, nil
}

func toolResultFullKey(conversationID, runID ids.UUID, stepIndex int, toolCallID string) string {
	return "tool_results/" + conversationID.String() + "/" + runID.String() + "/step_" + strconv.Itoa(stepIndex) + "/" + sanitizeKeyPart(toolCallID) + "/full.json"
}

func toolResultChunkKey(conversationID, runID ids.UUID, stepIndex int, toolCallID string, chunkIndex int) string {
	return "tool_results/" + conversationID.String() + "/" + runID.String() + "/step_" + strconv.Itoa(stepIndex) + "/" + sanitizeKeyPart(toolCallID) + "/chunks/" + fmt.Sprintf("%06d.txt", chunkIndex)
}

func sanitizeKeyPart(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "empty"
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "._")
	if out == "" {
		return "empty"
	}
	return out
}

func toolResultPayload(res fantasy.ToolResultContent) (payload map[string]any, payloadType string, payloadText string) {
	payloadType = "unknown"
	payloadText = ""

	switch v := res.Result.(type) {
	case fantasy.ToolResultOutputContentText:
		payloadType = "text"
		payloadText = v.Text
		payload = map[string]any{
			"type": "text",
			"text": v.Text,
		}
	case fantasy.ToolResultOutputContentMedia:
		payloadType = "media"
		payloadText = v.Text
		payload = map[string]any{
			"type":       "media",
			"text":       v.Text,
			"media_type": v.MediaType,
			"data":       v.Data,
		}
	case fantasy.ToolResultOutputContentError:
		payloadType = "error"
		errS := ""
		if v.Error != nil {
			errS = v.Error.Error()
		}
		payloadText = errS
		payload = map[string]any{
			"type":  "error",
			"error": errS,
		}
	default:
		payload = map[string]any{
			"type":  "unknown",
			"value": fmt.Sprintf("%v", res.Result),
		}
		payloadText = fmt.Sprintf("%v", res.Result)
	}

	payload["tool_call_id"] = res.ToolCallID
	payload["tool_name"] = res.ToolName
	payload["provider_executed"] = res.ProviderExecuted
	payload["client_metadata"] = res.ClientMetadata
	payload["provider_metadata"] = res.ProviderMetadata

	return payload, payloadType, payloadText
}

func chunkString(s string, chunkSize int) []string {
	if chunkSize <= 0 {
		return []string{s}
	}
	r := []rune(s)
	if len(r) == 0 {
		return []string{""}
	}
	out := make([]string, 0, (len(r)/chunkSize)+1)
	for i := 0; i < len(r); i += chunkSize {
		end := i + chunkSize
		if end > len(r) {
			end = len(r)
		}
		out = append(out, string(r[i:end]))
	}
	return out
}

func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
