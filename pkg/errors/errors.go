package errors

import (
	"fmt"
	"time"
)

// ErrConfig represents a configuration validation error.
type ErrConfig struct {
	Field   string // config field path (e.g. "agents.defaults.model")
	Message string // human-readable description
}

func (e *ErrConfig) Error() string {
	return fmt.Sprintf("config error [%s]: %s", e.Field, e.Message)
}

// ErrProvider represents an error from an LLM provider.
type ErrProvider struct {
	Provider   string // provider name (e.g. "openai", "anthropic")
	StatusCode int    // HTTP status code (0 if not applicable)
	Message    string
	Cause      error
}

func (e *ErrProvider) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("provider error [%s] (HTTP %d): %s: %v", e.Provider, e.StatusCode, e.Message, e.Cause)
	}
	if e.StatusCode > 0 {
		return fmt.Sprintf("provider error [%s] (HTTP %d): %s", e.Provider, e.StatusCode, e.Message)
	}
	return fmt.Sprintf("provider error [%s]: %s", e.Provider, e.Message)
}

func (e *ErrProvider) Unwrap() error {
	return e.Cause
}

// ErrToolExecution represents an error during tool execution.
type ErrToolExecution struct {
	ToolName string
	Duration time.Duration
	Cause    error
}

func (e *ErrToolExecution) Error() string {
	return fmt.Sprintf("tool execution error [%s] (took %s): %v", e.ToolName, e.Duration, e.Cause)
}

func (e *ErrToolExecution) Unwrap() error {
	return e.Cause
}

// ErrRetryable wraps a transient error that may succeed on retry.
type ErrRetryable struct {
	Cause      error
	RetryAfter time.Duration // suggested delay before retry (0 = use default)
}

func (e *ErrRetryable) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("retryable error (retry after %s): %v", e.RetryAfter, e.Cause)
	}
	return fmt.Sprintf("retryable error: %v", e.Cause)
}

func (e *ErrRetryable) Unwrap() error {
	return e.Cause
}
