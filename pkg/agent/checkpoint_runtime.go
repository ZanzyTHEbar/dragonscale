package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	fantasy "charm.land/fantasy"
	"github.com/ZanzyTHEbar/dragonscale/pkg"
	"github.com/ZanzyTHEbar/dragonscale/pkg/agent/conversations"
	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	"github.com/ZanzyTHEbar/dragonscale/pkg/logger"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
	memsqlc "github.com/ZanzyTHEbar/dragonscale/pkg/memory/sqlc"
	"github.com/ZanzyTHEbar/dragonscale/pkg/messages"
	jsonv2 "github.com/go-json-experiment/json"
)

const checkpointTurnCompletedEvent = "turn.completed"

type sessionMessageLister interface {
	ListSessionMessages(ctx context.Context, agentID, sessionKey, role string, limit int) ([]*memory.RecallItem, error)
}

func checkpointNameForRun(runID ids.UUID) string {
	return fmt.Sprintf("run-%s-complete", runID.String())
}

func (al *AgentLoop) persistRunCheckpoint(ctx context.Context, opts processOptions, metrics agentRunMetrics) {
	if al == nil || al.stateStore == nil || al.queries == nil {
		return
	}
	if opts.ConversationID.IsZero() || opts.RunID.IsZero() {
		return
	}
	persistCtx := context.WithoutCancel(ctx)

	history := al.sessions.GetHistory(opts.SessionKey)
	snapshot := conversations.NewCheckpointSnapshot(
		opts.SessionKey,
		opts.ConversationID.String(),
		opts.RunID.String(),
		checkpointTurnCompletedEvent,
		metrics.StepCount,
		history,
	)

	meta := map[string]any{
		"event":           checkpointTurnCompletedEvent,
		"session_key":     opts.SessionKey,
		"conversation_id": opts.ConversationID.String(),
		"run_id":          opts.RunID.String(),
		"step_count":      metrics.StepCount,
		"tool_calls":      metrics.ToolCalls,
		"errors":          metrics.Errors,
	}

	runState, err := al.stateStore.AddRunState(persistCtx, opts.RunID, metrics.StepCount, fantasy.ReActStateDone, snapshot)
	if err != nil {
		logger.WarnCF("agent", "Failed to persist checkpointable run snapshot", map[string]any{
			"session_key": opts.SessionKey,
			"run_id":      opts.RunID.String(),
			"error":       err.Error(),
		})
	} else {
		checkpointName := checkpointNameForRun(opts.RunID)
		meta["checkpoint_name"] = checkpointName
		meta["run_state_id"] = runState.ID.String()

		checkpointStore := NewCheckpointStore(al.queries)
		if _, err := checkpointStore.CreateCheckpoint(persistCtx, opts.ConversationID, checkpointName, runState.ID, meta); err != nil {
			logger.WarnCF("agent", "Failed to create runtime checkpoint", map[string]any{
				"session_key": opts.SessionKey,
				"run_id":      opts.RunID.String(),
				"checkpoint":  checkpointName,
				"error":       err.Error(),
			})
		}
	}

	if _, err := al.stateStore.UpdateRunStatus(persistCtx, opts.RunID, "completed", meta); err != nil {
		logger.WarnCF("agent", "Failed to update run completion status", map[string]any{
			"session_key": opts.SessionKey,
			"run_id":      opts.RunID.String(),
			"error":       err.Error(),
		})
	}
}

func (al *AgentLoop) persistFailedRun(ctx context.Context, opts processOptions, reason error) {
	if al == nil || al.stateStore == nil || opts.RunID.IsZero() {
		return
	}
	persistCtx := context.WithoutCancel(ctx)
	meta := map[string]any{
		"session_key": opts.SessionKey,
	}
	if !opts.ConversationID.IsZero() {
		meta["conversation_id"] = opts.ConversationID.String()
	}
	if reason != nil {
		meta["error"] = reason.Error()
		meta["reason"] = classifyRunFailure(reason)
	}
	if _, err := al.stateStore.UpdateRunStatus(persistCtx, opts.RunID, "failed", meta); err != nil {
		logger.WarnCF("agent", "Failed to update run failure status", map[string]any{
			"session_key": opts.SessionKey,
			"run_id":      opts.RunID.String(),
			"error":       err.Error(),
		})
	}
}

func classifyRunFailure(err error) string {
	if err == nil {
		return "failed"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	return "failed"
}

func (al *AgentLoop) RestoreSessionFromCheckpoint(ctx context.Context, sessionKey, checkpointName string) error {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return fmt.Errorf("session key is required")
	}
	checkpointName = strings.TrimSpace(checkpointName)
	if checkpointName == "" {
		return fmt.Errorf("checkpoint name is required")
	}

	conversationID, err := al.lookupConversationIDForSession(ctx, sessionKey)
	if err != nil {
		return err
	}

	store := conversations.New(al.queries)
	_, snapshot, err := store.LoadCheckpointSnapshot(ctx, conversationID, checkpointName)
	if err != nil {
		return err
	}

	if err := al.replaceSessionHistory(ctx, sessionKey, snapshot.Messages); err != nil {
		return err
	}
	al.conversationIDs.Store(sessionKey, conversationID)
	if err := al.persistConversationBinding(ctx, sessionKey, conversationID); err != nil {
		return err
	}
	return nil
}

func (al *AgentLoop) ForkSessionFromCheckpoint(ctx context.Context, sourceSessionKey, checkpointName, forkSessionKey string) (ids.UUID, error) {
	sourceSessionKey = strings.TrimSpace(sourceSessionKey)
	if sourceSessionKey == "" {
		return ids.UUID{}, fmt.Errorf("source session key is required")
	}
	checkpointName = strings.TrimSpace(checkpointName)
	if checkpointName == "" {
		return ids.UUID{}, fmt.Errorf("checkpoint name is required")
	}
	forkSessionKey = strings.TrimSpace(forkSessionKey)
	if forkSessionKey == "" {
		return ids.UUID{}, fmt.Errorf("fork session key is required")
	}

	conversationID, err := al.lookupConversationIDForSession(ctx, sourceSessionKey)
	if err != nil {
		return ids.UUID{}, err
	}

	store := conversations.New(al.queries)
	title := forkSessionKey
	conv, err := store.ForkFromCheckpoint(ctx, conversations.ForkFromCheckpointParams{
		FromConversationID: conversationID.String(),
		CheckpointName:     checkpointName,
		Title:              &title,
	})
	if err != nil {
		return ids.UUID{}, err
	}

	_, snapshot, err := store.LoadCheckpointSnapshot(ctx, conversationID, checkpointName)
	if err != nil {
		return ids.UUID{}, err
	}

	if err := al.replaceSessionHistory(ctx, forkSessionKey, snapshot.Messages); err != nil {
		return ids.UUID{}, err
	}
	al.conversationIDs.Store(forkSessionKey, conv.ID)
	if err := al.persistConversationBinding(ctx, forkSessionKey, conv.ID); err != nil {
		return ids.UUID{}, err
	}
	return conv.ID, nil
}

func (al *AgentLoop) lookupConversationIDForSession(ctx context.Context, sessionKey string) (ids.UUID, error) {
	if al == nil || al.queries == nil {
		return ids.UUID{}, fmt.Errorf("runtime persistence is not initialized")
	}
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return ids.UUID{}, fmt.Errorf("session key is required")
	}
	if cached, ok := al.conversationIDs.Load(sessionKey); ok {
		return cached, nil
	}
	if boundID, err := al.loadBoundConversationID(ctx, sessionKey); err == nil && !boundID.IsZero() {
		if _, err := al.queries.GetAgentConversation(ctx, memsqlc.GetAgentConversationParams{ID: boundID}); err == nil {
			al.conversationIDs.Store(sessionKey, boundID)
			return boundID, nil
		}
	} else if err != nil {
		return ids.UUID{}, fmt.Errorf("lookup conversation binding for session %q: %w", sessionKey, err)
	}

	conv, err := al.queries.GetLatestAgentConversationByTitle(ctx, memsqlc.GetLatestAgentConversationByTitleParams{Title: &sessionKey})
	if err != nil {
		return ids.UUID{}, fmt.Errorf("lookup conversation for session %q: %w", sessionKey, err)
	}
	al.conversationIDs.Store(sessionKey, conv.ID)
	if err := al.persistConversationBinding(ctx, sessionKey, conv.ID); err != nil {
		return ids.UUID{}, fmt.Errorf("persist conversation binding for session %q: %w", sessionKey, err)
	}
	return conv.ID, nil
}

func (al *AgentLoop) replaceSessionHistory(ctx context.Context, sessionKey string, history []messages.Message) error {
	if al == nil || al.sessions == nil {
		return fmt.Errorf("session manager is not initialized")
	}
	restored := conversations.HydrationMessages(history, conversations.MaxCheckpointHydrationMessages)

	if lister, ok := al.memDelegate.(sessionMessageLister); ok && al.memDelegate != nil {
		limit := 32
		if count, err := al.memDelegate.CountRecallItems(ctx, pkg.NAME, sessionKey); err == nil && count+32 > limit {
			limit = count + 32
		}
		if rows, err := lister.ListSessionMessages(ctx, pkg.NAME, sessionKey, "", limit); err == nil {
			for _, item := range rows {
				if err := al.memDelegate.SoftDeleteRecallItem(ctx, pkg.NAME, item.ID); err != nil {
					logger.WarnCF("agent", "Failed to suppress prior session message during checkpoint restore", map[string]any{
						"session_key": sessionKey,
						"message_id":  item.ID.String(),
						"error":       err.Error(),
					})
				}
			}
		} else {
			logger.WarnCF("agent", "Failed to list prior session messages during checkpoint restore", map[string]any{
				"session_key": sessionKey,
				"error":       err.Error(),
			})
		}
	}

	al.sessions.ReplaceHistory(sessionKey, restored, "")
	if session := al.sessions.GetOrCreate(sessionKey); session != nil {
		session.Messages = conversations.HydrationMessages(restored, len(restored))
		session.Summary = ""
		session.Updated = time.Now().UTC()
	}

	for _, msg := range restored {
		if err := al.persistCheckpointSessionMessage(ctx, sessionKey, msg); err != nil {
			return err
		}
	}
	al.contextTreeCache.Delete(sessionKey)
	return al.sessions.Save(sessionKey)
}

func (al *AgentLoop) persistCheckpointSessionMessage(ctx context.Context, sessionKey string, msg messages.Message) error {
	if al.memDelegate == nil {
		return nil
	}

	now := time.Now().UTC()
	item := &memory.RecallItem{
		ID:         ids.New(),
		AgentID:    pkg.NAME,
		SessionKey: sessionKey,
		Role:       msg.Role,
		Sector:     memory.SectorEpisodic,
		Importance: 0.5,
		Salience:   0.5,
		DecayRate:  0.01,
		Content:    msg.Content,
		Tags:       "session-message",
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := al.memDelegate.InsertRecallItem(ctx, item); err != nil {
		return fmt.Errorf("persist restored recall item: %w", err)
	}

	immutableWriter, ok := al.memDelegate.(interface {
		InsertImmutableMessage(ctx context.Context, msg *memory.ImmutableMessage) error
	})
	if !ok {
		return nil
	}

	imMsg := &memory.ImmutableMessage{
		ID:            ids.New(),
		SessionKey:    sessionKey,
		Role:          msg.Role,
		Content:       msg.Content,
		ToolCallID:    msg.ToolCallID,
		ToolCalls:     checkpointToolCallsJSON(msg),
		TokenEstimate: estimateCheckpointMessageTokens(msg.Content),
	}
	if err := immutableWriter.InsertImmutableMessage(ctx, imMsg); err != nil {
		return fmt.Errorf("persist restored immutable message: %w", err)
	}
	return nil
}

func estimateCheckpointMessageTokens(content string) int {
	return (len(content) + 3) / 4
}

func checkpointToolCallsJSON(msg messages.Message) string {
	if len(msg.ToolCalls) == 0 {
		return ""
	}
	b, err := jsonv2.Marshal(msg.ToolCalls)
	if err != nil {
		return ""
	}
	return string(b)
}
