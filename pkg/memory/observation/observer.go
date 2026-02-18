package observation

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// ModelFunc is a function that sends a prompt to an LLM and returns the text response.
// This decouples the observation package from the fantasy/model layer.
type ModelFunc func(ctx context.Context, prompt string) (string, error)

// ObserverConfig controls when and how the Observer triggers.
type ObserverConfig struct {
	TokenThreshold int // Uncompressed tail token count to trigger observation (default 30000)
}

func DefaultObserverConfig() ObserverConfig {
	return ObserverConfig{
		TokenThreshold: 30000,
	}
}

// Observer compresses raw conversation into prioritized observations.
// It fires when the uncompressed tail exceeds the token threshold.
type Observer struct {
	callModel ModelFunc
	cfg       ObserverConfig
}

func NewObserver(callModel ModelFunc, cfg ObserverConfig) *Observer {
	return &Observer{
		callModel: callModel,
		cfg:       cfg,
	}
}

// ShouldObserve returns true if the uncompressed tail has exceeded the token threshold.
func (o *Observer) ShouldObserve(tailMessages []MessagePair) bool {
	return EstimateMessagesTokens(tailMessages) >= o.cfg.TokenThreshold
}

// Observe compresses the given messages into a list of new observations.
// The LLM is asked to extract key insights, decisions, and facts.
func (o *Observer) Observe(ctx context.Context, messages []MessagePair, existingObs []Observation) ([]Observation, error) {
	if len(messages) == 0 {
		return nil, nil
	}

	prompt := buildObserverPrompt(messages, existingObs)

	resp, err := o.callModel(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("observer LLM call: %w", err)
	}

	return parseObservations(resp, time.Now()), nil
}

func buildObserverPrompt(messages []MessagePair, existing []Observation) string {
	var sb strings.Builder

	sb.WriteString(`You are an observation extractor. Analyze the conversation below and extract key observations.

For each observation, output one line in this exact format:
PRIORITY|CONTENT

Where PRIORITY is one of: critical, notable, informational

Rules:
- Extract 3-10 observations from the conversation
- Critical: decisions made, errors encountered, important commitments
- Notable: useful information learned, preferences expressed, patterns identified
- Informational: context details, minor facts, status updates
- Be concise: each observation should be 1-2 sentences max
- Focus on facts and insights, not conversation flow
- Do NOT include observations that duplicate existing ones

`)

	if len(existing) > 0 {
		sb.WriteString("## Existing observations (do not duplicate):\n")
		for _, o := range existing {
			sb.WriteString(fmt.Sprintf("- %s\n", o.Content))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## Conversation to analyze:\n\n")
	for _, m := range messages {
		sb.WriteString(fmt.Sprintf("[%s]: %s\n\n", m.Role, m.Content))
	}

	return sb.String()
}

func parseObservations(response string, now time.Time) []Observation {
	var observations []Observation

	for _, line := range strings.Split(response, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, "|", 2)
		if len(parts) != 2 {
			continue
		}

		priority := parsePriority(strings.TrimSpace(parts[0]))
		content := strings.TrimSpace(parts[1])
		if content == "" {
			continue
		}

		observations = append(observations, NewObservation(content, priority, now, now))
	}

	if len(observations) == 0 {
		slog.Warn("observer produced no parseable observations from LLM response")
	}

	return observations
}

func parsePriority(s string) Priority {
	switch strings.ToLower(s) {
	case "critical":
		return PriorityCritical
	case "notable":
		return PriorityNotable
	case "informational":
		return PriorityInformational
	default:
		return PriorityInformational
	}
}
