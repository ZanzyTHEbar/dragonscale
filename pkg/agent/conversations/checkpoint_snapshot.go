package conversations

import (
	jsonv2 "github.com/go-json-experiment/json"

	"github.com/ZanzyTHEbar/dragonscale/pkg/messages"
)

const MaxCheckpointHydrationMessages = 200

// CheckpointSnapshot is the canonical runtime payload persisted into
// agent_run_states for checkpoint hydration.
type CheckpointSnapshot struct {
	SessionKey     string             `json:"session_key,omitempty"`
	ConversationID string             `json:"conversation_id,omitempty"`
	RunID          string             `json:"run_id,omitempty"`
	Event          string             `json:"event,omitempty"`
	StepCount      int                `json:"step_count,omitempty"`
	Messages       []messages.Message `json:"messages"`
}

func NewCheckpointSnapshot(sessionKey, conversationID, runID, event string, stepCount int, history []messages.Message) CheckpointSnapshot {
	return CheckpointSnapshot{
		SessionKey:     sessionKey,
		ConversationID: conversationID,
		RunID:          runID,
		Event:          event,
		StepCount:      stepCount,
		Messages:       cloneCheckpointMessages(history),
	}
}

func DecodeCheckpointSnapshot(raw []byte) (CheckpointSnapshot, error) {
	if len(raw) == 0 {
		return CheckpointSnapshot{}, nil
	}
	var snap CheckpointSnapshot
	if err := jsonv2.Unmarshal(raw, &snap); err != nil {
		return CheckpointSnapshot{}, err
	}
	snap.Messages = cloneCheckpointMessages(snap.Messages)
	return snap, nil
}

func HydrationMessages(history []messages.Message, limit int) []messages.Message {
	if limit <= 0 {
		limit = MaxCheckpointHydrationMessages
	}
	if len(history) > limit {
		history = history[len(history)-limit:]
	}
	return cloneCheckpointMessages(history)
}

func cloneCheckpointMessages(history []messages.Message) []messages.Message {
	if len(history) == 0 {
		return nil
	}
	cloned := make([]messages.Message, len(history))
	copy(cloned, history)
	for i := range cloned {
		if len(cloned[i].ToolCalls) == 0 {
			continue
		}
		toolCalls := make([]messages.ToolCall, len(cloned[i].ToolCalls))
		copy(toolCalls, cloned[i].ToolCalls)
		cloned[i].ToolCalls = toolCalls
	}
	return cloned
}
