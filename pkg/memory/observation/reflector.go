package observation

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

// ReflectorConfig controls when and how the Reflector triggers.
type ReflectorConfig struct {
	TokenThreshold int // Observation block token count to trigger reflection (default 40000)
}

func DefaultReflectorConfig() ReflectorConfig {
	return ReflectorConfig{
		TokenThreshold: 40000,
	}
}

// Reflector garbage-collects low-priority observations when the block exceeds
// its token threshold. This is the only operation that invalidates the full
// prompt cache (rare by design).
type Reflector struct {
	callModel ModelFunc
	cfg       ReflectorConfig
}

func NewReflector(callModel ModelFunc, cfg ReflectorConfig) *Reflector {
	return &Reflector{
		callModel: callModel,
		cfg:       cfg,
	}
}

// ShouldReflect returns true if the observation block exceeds the threshold.
func (r *Reflector) ShouldReflect(observations []Observation) bool {
	block := FormatBlock(observations)
	return EstimateTokens(block) >= r.cfg.TokenThreshold
}

// Reflect asks the LLM to select observations worth keeping.
// Returns the pruned observation list.
func (r *Reflector) Reflect(ctx context.Context, observations []Observation) ([]Observation, error) {
	if len(observations) == 0 {
		return nil, nil
	}

	prompt := buildReflectorPrompt(observations)

	resp, err := r.callModel(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("reflector LLM call: %w", err)
	}

	kept := parseKeptIndices(resp, observations)

	slog.Info("reflector GC",
		"before", len(observations),
		"after", len(kept),
		"dropped", len(observations)-len(kept))

	return kept, nil
}

func buildReflectorPrompt(observations []Observation) string {
	var sb strings.Builder

	sb.WriteString(`You are a memory curator. Review the observations below and decide which to KEEP.

Rules:
- KEEP all critical observations
- KEEP notable observations that are still relevant
- DROP informational observations that are outdated or redundant
- DROP observations that have been superseded by newer ones
- Aim to reduce the list by 30-50%

For each observation, output KEEP or DROP followed by the index number:
KEEP 0
DROP 1
KEEP 2
...

## Observations:

`)

	for i, o := range observations {
		sb.WriteString(fmt.Sprintf("[%d] %s %s — %s (%s)\n",
			i,
			o.Priority.Emoji(),
			o.Priority,
			o.Content,
			o.RelativeDate,
		))
	}

	return sb.String()
}

func parseKeptIndices(response string, observations []Observation) []Observation {
	kept := make(map[int]bool)

	for _, line := range strings.Split(response, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var action string
		var idx int
		if _, err := fmt.Sscanf(line, "%s %d", &action, &idx); err != nil {
			continue
		}

		if strings.EqualFold(action, "KEEP") && idx >= 0 && idx < len(observations) {
			kept[idx] = true
		}
	}

	// If parsing failed or nothing was kept, keep all critical observations at minimum
	if len(kept) == 0 {
		slog.Warn("reflector produced no valid KEEP instructions, preserving all critical observations")
		for i, o := range observations {
			if o.Priority == PriorityCritical {
				kept[i] = true
			}
		}
	}

	var result []Observation
	for i, o := range observations {
		if kept[i] {
			result = append(result, o)
		}
	}
	return result
}
