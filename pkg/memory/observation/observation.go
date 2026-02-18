// Package observation implements Mastra-style Observational Memory.
//
// Raw conversation is compressed into prioritized observations that form a
// stable, prompt-cacheable prefix in the system prompt. Two background agents
// maintain the observation block:
//
//   - Observer: fires when the uncompressed tail exceeds a token threshold,
//     compressing recent messages into new observations.
//   - Reflector: fires when the observation block itself exceeds a threshold,
//     garbage-collecting low-priority observations.
//
// Each observation carries a three-date model: observation date (when created),
// referenced date (when the event occurred), and a human-readable relative date.
package observation

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Priority encodes the importance of an observation for prompt display.
type Priority string

const (
	PriorityCritical      Priority = "critical"      // 🔴
	PriorityNotable       Priority = "notable"        // 🟡
	PriorityInformational Priority = "informational"  // 🔵
)

func (p Priority) Emoji() string {
	switch p {
	case PriorityCritical:
		return "🔴"
	case PriorityNotable:
		return "🟡"
	case PriorityInformational:
		return "🔵"
	default:
		return "🔵"
	}
}

// Observation is a single compressed insight extracted from conversation.
type Observation struct {
	Content      string   `json:"content"`
	Priority     Priority `json:"priority"`
	ObservedAt   int64    `json:"observed_at"`   // Unix timestamp: when observation was created
	ReferencedAt int64    `json:"referenced_at"` // Unix timestamp: when the referenced event occurred
	RelativeDate string   `json:"relative_date"` // Human-readable: "2 days ago", "today", etc.
}

// ObservedTime returns ObservedAt as time.Time.
func (o Observation) ObservedTime() time.Time {
	return time.Unix(o.ObservedAt, 0)
}

// ReferencedTime returns ReferencedAt as time.Time.
func (o Observation) ReferencedTime() time.Time {
	return time.Unix(o.ReferencedAt, 0)
}

// NewObservation creates an observation with the three-date model.
// referenceTime is when the observed event happened; observeTime is now.
func NewObservation(content string, priority Priority, referenceTime, observeTime time.Time) Observation {
	return Observation{
		Content:      content,
		Priority:     priority,
		ObservedAt:   observeTime.Unix(),
		ReferencedAt: referenceTime.Unix(),
		RelativeDate: relativeDate(referenceTime, observeTime),
	}
}

// FormatBlock renders the entire observation list as a single text block
// suitable for system prompt injection. Format per line:
//
//	DATE EMOJI HH:MM observation_text
func FormatBlock(observations []Observation) string {
	if len(observations) == 0 {
		return ""
	}

	var sb strings.Builder
	for _, o := range observations {
		ref := o.ReferencedTime()
		sb.WriteString(fmt.Sprintf("%s %s %s %s\n",
			ref.Format("2006-01-02"),
			o.Priority.Emoji(),
			ref.Format("15:04"),
			o.Content,
		))
	}
	return sb.String()
}

// MarshalObservations serializes observations to JSON for KV storage.
func MarshalObservations(obs []Observation) (string, error) {
	data, err := json.Marshal(obs)
	if err != nil {
		return "", fmt.Errorf("marshal observations: %w", err)
	}
	return string(data), nil
}

// UnmarshalObservations deserializes observations from KV storage.
func UnmarshalObservations(data string) ([]Observation, error) {
	if data == "" {
		return nil, nil
	}
	var obs []Observation
	if err := json.Unmarshal([]byte(data), &obs); err != nil {
		return nil, fmt.Errorf("unmarshal observations: %w", err)
	}
	return obs, nil
}

func relativeDate(ref, now time.Time) string {
	diff := now.Sub(ref)
	switch {
	case diff < time.Minute:
		return "just now"
	case diff < time.Hour:
		m := int(diff.Minutes())
		if m == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", m)
	case diff < 24*time.Hour:
		h := int(diff.Hours())
		if h == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", h)
	case diff < 48*time.Hour:
		return "yesterday"
	case diff < 7*24*time.Hour:
		d := int(diff.Hours() / 24)
		return fmt.Sprintf("%d days ago", d)
	case diff < 30*24*time.Hour:
		w := int(diff.Hours() / (24 * 7))
		if w == 1 {
			return "1 week ago"
		}
		return fmt.Sprintf("%d weeks ago", w)
	default:
		return ref.Format("2006-01-02")
	}
}
