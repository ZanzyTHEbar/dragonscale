package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg/bus"
	"github.com/ZanzyTHEbar/dragonscale/pkg/constants"
	"github.com/ZanzyTHEbar/dragonscale/pkg/logger"
	"github.com/ZanzyTHEbar/dragonscale/pkg/tools"
	"github.com/ZanzyTHEbar/dragonscale/pkg/utils"
)

func (al *AgentLoop) RecordLastChannel(ctx context.Context, channel string) error {
	return al.state.SetLastChannel(ctx, channel)
}

func (al *AgentLoop) RecordLastChatID(ctx context.Context, chatID string) error {
	return al.state.SetLastChatID(ctx, chatID)
}

func (al *AgentLoop) ProcessDirect(ctx context.Context, content, sessionKey string) (string, error) {
	return al.ProcessDirectWithChannel(ctx, content, sessionKey, "cli", "direct")
}

func (al *AgentLoop) ProcessDirectWithChannel(ctx context.Context, content, sessionKey, channel, chatID string) (string, error) {
	roundTracker := tools.NewMessageSendTracker()
	ctx = tools.WithMessageSendTracker(ctx, roundTracker)

	msg := bus.InboundMessage{
		Channel:    channel,
		SenderID:   "cron",
		ChatID:     chatID,
		Content:    content,
		SessionKey: sessionKey,
	}

	response, err := al.processMessage(ctx, msg)
	if err != nil || response == "" || roundTracker.Sent() {
		return response, err
	}
	al.bus.PublishOutbound(bus.OutboundMessage{Channel: channel, ChatID: chatID, Content: response})
	return response, nil
}

// ProcessDirectStreaming processes a message with streaming token delivery.
// Text deltas are published to the bus as StreamDelta messages in real time.
func (al *AgentLoop) ProcessDirectStreaming(ctx context.Context, content, sessionKey, channel, chatID string) (string, error) {
	roundTracker := tools.NewMessageSendTracker()
	ctx = tools.WithMessageSendTracker(ctx, roundTracker)

	msg := bus.InboundMessage{
		Channel:    channel,
		SenderID:   "user",
		ChatID:     chatID,
		Content:    content,
		SessionKey: sessionKey,
	}

	if msg.SessionKey != "" && msg.SessionKey != "heartbeat" && !strings.HasPrefix(msg.SessionKey, "heartbeat:") {
		if err := al.state.SetLastSessionKeyForTarget(ctx, msg.Channel, msg.ChatID, msg.SessionKey); err != nil {
			logger.WarnCF("agent", "Failed to record last session key", map[string]interface{}{"error": err.Error(), "session_key": msg.SessionKey})
		}
	}

	return al.runAgentLoop(ctx, processOptions{
		SessionKey:    msg.SessionKey,
		Channel:       msg.Channel,
		ChatID:        msg.ChatID,
		SenderID:      msg.SenderID,
		UserMessage:   msg.Content,
		EnableSummary: true,
		SendResponse:  false,
		Streaming:     true,
	})
}

// ProcessHeartbeat processes a heartbeat request without session history.
// Each heartbeat is independent and doesn't accumulate context.
// It injects the active session's summary so the agent has awareness of
// recent user conversation context.
func (al *AgentLoop) ProcessHeartbeat(ctx context.Context, content, channel, chatID string) (string, error) {
	sourceSessionKey := ""
	if al.state != nil {
		sourceSessionKey = al.state.GetLastSessionKeyForTarget(channel, chatID)
	}
	if sourceSessionKey != "" {
		if summary := al.sessions.GetSummary(sourceSessionKey); summary != "" {
			content = content + "\n\n## Recent User Context\n" + summary
		}
	}
	heartbeatSessionKey := fmt.Sprintf("heartbeat:%d", time.Now().UnixNano())
	return al.runAgentLoop(ctx, processOptions{
		SessionKey:    heartbeatSessionKey,
		Channel:       channel,
		ChatID:        chatID,
		UserMessage:   content,
		EnableSummary: false,
		SendResponse:  false,
		NoHistory:     true,
	})
}

func (al *AgentLoop) processMessage(ctx context.Context, msg bus.InboundMessage) (string, error) {
	// Add message preview to log (show full content for error messages)
	var logContent string
	if strings.Contains(msg.Content, "Error:") || strings.Contains(msg.Content, "error") {
		logContent = msg.Content // Full content for errors
	} else {
		logContent = utils.Truncate(msg.Content, 80)
	}
	logger.InfoCF("agent", fmt.Sprintf("Processing message from %s:%s: %s", msg.Channel, msg.SenderID, logContent),
		map[string]interface{}{
			"channel":     msg.Channel,
			"chat_id":     msg.ChatID,
			"sender_id":   msg.SenderID,
			"session_key": msg.SessionKey,
		})

	// Route system messages to processSystemMessage
	if msg.Channel == "system" {
		return al.processSystemMessage(ctx, msg)
	}

	// Check for commands
	if response, handled := al.handleCommand(ctx, msg); handled {
		return response, nil
	}

	// Process as user message
	if msg.SessionKey != "" && msg.SessionKey != "heartbeat" && !strings.HasPrefix(msg.SessionKey, "heartbeat:") {
		if err := al.state.SetLastSessionKeyForTarget(ctx, msg.Channel, msg.ChatID, msg.SessionKey); err != nil {
			logger.WarnCF("agent", "Failed to record last session key", map[string]interface{}{"error": err.Error(), "session_key": msg.SessionKey})
		}
	}
	return al.runAgentLoop(ctx, processOptions{
		SessionKey:    msg.SessionKey,
		Channel:       msg.Channel,
		ChatID:        msg.ChatID,
		UserMessage:   msg.Content,
		EnableSummary: true,
		SendResponse:  false,
	})
}

func (al *AgentLoop) processSystemMessage(_ context.Context, msg bus.InboundMessage) (string, error) {
	// Verify this is a system message
	if msg.Channel != "system" {
		return "", fmt.Errorf("processSystemMessage called with non-system message channel: %s", msg.Channel)
	}

	logger.InfoCF("agent", "Processing system message",
		map[string]interface{}{
			"sender_id": msg.SenderID,
			"chat_id":   msg.ChatID,
		})

	// Parse origin channel from chat_id (format: "channel:chat_id")
	var originChannel string
	if idx := strings.Index(msg.ChatID, ":"); idx > 0 {
		originChannel = msg.ChatID[:idx]
	} else {
		// Fallback
		originChannel = "cli"
	}

	// Extract subagent result from message content
	// Format: "Task 'label' completed.\n\nResult:\n<actual content>"
	content := msg.Content
	if idx := strings.Index(content, "Result:\n"); idx >= 0 {
		content = content[idx+8:] // Extract just the result part
	}

	// Skip internal channels - only log, don't send to user
	if constants.IsInternalChannel(originChannel) {
		logger.InfoCF("agent", "Subagent completed (internal channel)",
			map[string]interface{}{
				"sender_id":   msg.SenderID,
				"content_len": len(content),
				"channel":     originChannel,
			})
		return "", nil
	}

	// Agent acts as dispatcher only - subagent handles user interaction via message tool
	// Don't forward result here, subagent should use message tool to communicate with user
	logger.InfoCF("agent", "Subagent completed",
		map[string]interface{}{
			"sender_id":   msg.SenderID,
			"channel":     originChannel,
			"content_len": len(content),
		})

	// Agent only logs, does not respond to user
	return "", nil
}
