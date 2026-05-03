package tools

import (
	"context"
	"strings"
	"sync/atomic"
)

type ctxSessionKey struct{}
type ctxExecutionTarget struct{}
type ctxAsyncCallback struct{}
type ctxInjectedArgKeys struct{}
type ctxMessageSendTracker struct{}
type ctxToolCallID struct{}

type executionTarget struct {
	channel string
	chatID  string
}

type MessageSendTracker struct {
	sent atomic.Bool
}

// WithSessionKey annotates the execution context with the session key that
// session-scoped tools should use for this call tree. Passing an empty value
// intentionally clears any inherited session key for child calls.
func WithSessionKey(ctx context.Context, sessionKey string) context.Context {
	return context.WithValue(ctx, ctxSessionKey{}, strings.TrimSpace(sessionKey))
}

// SessionKeyFromContext returns the session key previously attached via
// WithSessionKey. Missing values are treated as empty.
func SessionKeyFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxSessionKey{}).(string)
	return strings.TrimSpace(v)
}

// WithToolCallID annotates the execution context with the current LLM
// tool_call.id so nested dispatch paths can preserve audit correlation. Passing
// an empty value intentionally clears any inherited tool_call.id for child calls.
func WithToolCallID(ctx context.Context, toolCallID string) context.Context {
	return context.WithValue(ctx, ctxToolCallID{}, strings.TrimSpace(toolCallID))
}

// ToolCallIDFromContext returns the current LLM tool_call.id, if one was
// attached to the execution context.
func ToolCallIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxToolCallID{}).(string)
	return strings.TrimSpace(v)
}

// ResolveSessionKey prefers an explicit session key carried in context and
// falls back to a legacy resolver when no per-call session key is present.
func ResolveSessionKey(ctx context.Context, fallback func() string) string {
	if sessionKey := SessionKeyFromContext(ctx); sessionKey != "" {
		return sessionKey
	}
	if fallback != nil {
		return strings.TrimSpace(fallback())
	}
	return ""
}

// WithExecutionTarget annotates the execution context with the channel/chat
// destination that contextual and async tools should treat as the current user
// target.
func WithExecutionTarget(ctx context.Context, channel, chatID string) context.Context {
	channel = strings.TrimSpace(channel)
	chatID = strings.TrimSpace(chatID)
	if channel == "" && chatID == "" {
		return ctx
	}
	return context.WithValue(ctx, ctxExecutionTarget{}, executionTarget{channel: channel, chatID: chatID})
}

// ExecutionTargetFromContext returns the channel/chat destination previously
// attached via WithExecutionTarget.
func ExecutionTargetFromContext(ctx context.Context) (channel, chatID string) {
	v, _ := ctx.Value(ctxExecutionTarget{}).(executionTarget)
	return v.channel, v.chatID
}

// ResolveExecutionTarget prefers a target carried in context and falls back to
// the provided defaults when the context does not specify one.
func ResolveExecutionTarget(ctx context.Context, fallbackChannel, fallbackChatID string) (string, string) {
	channel, chatID := ExecutionTargetFromContext(ctx)
	if channel == "" {
		channel = strings.TrimSpace(fallbackChannel)
	}
	if chatID == "" {
		chatID = strings.TrimSpace(fallbackChatID)
	}
	return channel, chatID
}

// WithAsyncCallback annotates the execution context with the async completion
// callback that async tools should invoke for background completions.
func WithAsyncCallback(ctx context.Context, cb AsyncCallback) context.Context {
	if cb == nil {
		return ctx
	}
	return context.WithValue(ctx, ctxAsyncCallback{}, cb)
}

// AsyncCallbackFromContext returns the async completion callback previously
// attached via WithAsyncCallback.
func AsyncCallbackFromContext(ctx context.Context) AsyncCallback {
	v, _ := ctx.Value(ctxAsyncCallback{}).(AsyncCallback)
	return v
}

// WithInjectedArgKeys annotates the execution context with argument keys that
// were injected by trusted runtime infrastructure, such as SecureBus secret
// delivery. Validation ignores these keys when they are not part of the
// user-facing parameter schema.
func WithInjectedArgKeys(ctx context.Context, keys ...string) context.Context {
	if len(keys) == 0 {
		return ctx
	}
	merged := make(map[string]struct{})
	for key := range InjectedArgKeysFromContext(ctx) {
		merged[key] = struct{}{}
	}
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		merged[key] = struct{}{}
	}
	if len(merged) == 0 {
		return ctx
	}
	return context.WithValue(ctx, ctxInjectedArgKeys{}, merged)
}

// InjectedArgKeysFromContext returns the trusted argument keys previously
// attached via WithInjectedArgKeys.
func InjectedArgKeysFromContext(ctx context.Context) map[string]struct{} {
	v, _ := ctx.Value(ctxInjectedArgKeys{}).(map[string]struct{})
	return v
}

// NewMessageSendTracker allocates per-execution message-send state used to
// suppress duplicate final replies when a direct message already went out.
func NewMessageSendTracker() *MessageSendTracker {
	return &MessageSendTracker{}
}

// WithMessageSendTracker attaches a per-execution message-send tracker.
func WithMessageSendTracker(ctx context.Context, tracker *MessageSendTracker) context.Context {
	if tracker == nil {
		return ctx
	}
	return context.WithValue(ctx, ctxMessageSendTracker{}, tracker)
}

// MessageSendTrackerFromContext returns the tracker attached via
// WithMessageSendTracker, if present.
func MessageSendTrackerFromContext(ctx context.Context) *MessageSendTracker {
	v, _ := ctx.Value(ctxMessageSendTracker{}).(*MessageSendTracker)
	return v
}

// MarkMessageSent records that the message tool already sent a direct reply in
// the current execution.
func MarkMessageSent(ctx context.Context) {
	if tracker := MessageSendTrackerFromContext(ctx); tracker != nil {
		tracker.sent.Store(true)
	}
}

// Sent reports whether a direct message was already sent in the tracked round.
func (t *MessageSendTracker) Sent() bool {
	if t == nil {
		return false
	}
	return t.sent.Load()
}
