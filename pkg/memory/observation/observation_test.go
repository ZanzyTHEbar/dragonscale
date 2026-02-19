package observation

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewObservation_ThreeDateModel(t *testing.T) {
	ref := time.Date(2026, 2, 16, 10, 0, 0, 0, time.UTC)
	obs := time.Date(2026, 2, 18, 14, 30, 0, 0, time.UTC)

	o := NewObservation("User prefers Go over Rust", PriorityNotable, ref, obs)

	assert.Equal(t, "User prefers Go over Rust", o.Content)
	assert.Equal(t, PriorityNotable, o.Priority)
	assert.Equal(t, ref.Unix(), o.ReferencedAt)
	assert.Equal(t, obs.Unix(), o.ObservedAt)
	assert.Equal(t, "2 days ago", o.RelativeDate)
}

func TestRelativeDate(t *testing.T) {
	now := time.Date(2026, 2, 18, 14, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		ref  time.Time
		want string
	}{
		{"just now", now.Add(-30 * time.Second), "just now"},
		{"1 minute ago", now.Add(-1 * time.Minute), "1 minute ago"},
		{"5 minutes ago", now.Add(-5 * time.Minute), "5 minutes ago"},
		{"1 hour ago", now.Add(-1 * time.Hour), "1 hour ago"},
		{"3 hours ago", now.Add(-3 * time.Hour), "3 hours ago"},
		{"yesterday", now.Add(-30 * time.Hour), "yesterday"},
		{"3 days ago", now.Add(-3 * 24 * time.Hour), "3 days ago"},
		{"1 week ago", now.Add(-7 * 24 * time.Hour), "1 week ago"},
		{"3 weeks ago", now.Add(-21 * 24 * time.Hour), "3 weeks ago"},
		{"old date", now.Add(-60 * 24 * time.Hour), "2025-12-20"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := relativeDate(tc.ref, now)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestPriorityEmoji(t *testing.T) {
	assert.Equal(t, "🔴", PriorityCritical.Emoji())
	assert.Equal(t, "🟡", PriorityNotable.Emoji())
	assert.Equal(t, "🔵", PriorityInformational.Emoji())
	assert.Equal(t, "🔵", Priority("unknown").Emoji())
}

func TestFormatBlock(t *testing.T) {
	now := time.Date(2026, 2, 18, 14, 30, 0, 0, time.UTC)
	obs := []Observation{
		NewObservation("Decision: use SQLite", PriorityCritical, now, now),
		NewObservation("Prefers Go", PriorityNotable, now.Add(-time.Hour), now),
	}

	block := FormatBlock(obs)
	assert.Contains(t, block, "🔴")
	assert.Contains(t, block, "🟡")
	assert.Contains(t, block, "Decision: use SQLite")
	assert.Contains(t, block, "Prefers Go")
	assert.Contains(t, block, "2026-02-18")
}

func TestFormatBlock_Empty(t *testing.T) {
	assert.Equal(t, "", FormatBlock(nil))
	assert.Equal(t, "", FormatBlock([]Observation{}))
}

func TestMarshalUnmarshalRoundTrip(t *testing.T) {
	now := time.Now()
	obs := []Observation{
		NewObservation("Fact A", PriorityCritical, now, now),
		NewObservation("Fact B", PriorityInformational, now.Add(-time.Hour), now),
	}

	data, err := MarshalObservations(obs)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	parsed, err := UnmarshalObservations(data)
	require.NoError(t, err)
	assert.Len(t, parsed, 2)
	assert.Equal(t, "Fact A", parsed[0].Content)
	assert.Equal(t, PriorityCritical, parsed[0].Priority)
}

func TestUnmarshalObservations_Empty(t *testing.T) {
	obs, err := UnmarshalObservations("")
	assert.NoError(t, err)
	assert.Nil(t, obs)
}

func TestEstimateTokens(t *testing.T) {
	tokens := EstimateTokens("Hello, world!")
	assert.True(t, tokens > 0)
	assert.True(t, tokens < 20)
}

func TestParseObservations(t *testing.T) {
	now := time.Now()
	response := `critical|User decided to migrate to SQLite
notable|Prefers hexagonal architecture
informational|Uses VS Code as primary editor
invalid line without pipe
notable|`

	obs := parseObservations(response, now)
	assert.Len(t, obs, 3)
	assert.Equal(t, PriorityCritical, obs[0].Priority)
	assert.Equal(t, "User decided to migrate to SQLite", obs[0].Content)
	assert.Equal(t, PriorityNotable, obs[1].Priority)
	assert.Equal(t, PriorityInformational, obs[2].Priority)
}

func TestObserver_ShouldObserve(t *testing.T) {
	mockModel := func(_ context.Context, _ string) (string, error) {
		return "", nil
	}

	o := NewObserver(mockModel, ObserverConfig{TokenThreshold: 100})

	small := []MessagePair{{Role: "user", Content: "Hi"}}
	assert.False(t, o.ShouldObserve(small))

	large := []MessagePair{{Role: "user", Content: strings.Repeat("word ", 200)}}
	assert.True(t, o.ShouldObserve(large))
}

func TestObserver_Observe(t *testing.T) {
	mockModel := func(_ context.Context, prompt string) (string, error) {
		return "critical|Important decision made\nnotable|User preference noted", nil
	}

	o := NewObserver(mockModel, DefaultObserverConfig())

	msgs := []MessagePair{
		{Role: "user", Content: "I want to use SQLite for everything"},
		{Role: "assistant", Content: "Good choice for embedded use cases"},
	}

	obs, err := o.Observe(context.Background(), msgs, nil)
	require.NoError(t, err)
	assert.Len(t, obs, 2)
	assert.Equal(t, PriorityCritical, obs[0].Priority)
}

func TestReflector_ShouldReflect(t *testing.T) {
	mockModel := func(_ context.Context, _ string) (string, error) {
		return "", nil
	}

	r := NewReflector(mockModel, ReflectorConfig{TokenThreshold: 100})

	small := []Observation{NewObservation("Small fact", PriorityInformational, time.Now(), time.Now())}
	assert.False(t, r.ShouldReflect(small))

	var large []Observation
	for i := 0; i < 50; i++ {
		large = append(large, NewObservation(strings.Repeat("word ", 20), PriorityInformational, time.Now(), time.Now()))
	}
	assert.True(t, r.ShouldReflect(large))
}

func TestReflector_Reflect(t *testing.T) {
	mockModel := func(_ context.Context, prompt string) (string, error) {
		return "KEEP 0\nDROP 1\nKEEP 2", nil
	}

	r := NewReflector(mockModel, DefaultReflectorConfig())
	now := time.Now()

	obs := []Observation{
		NewObservation("Critical fact", PriorityCritical, now, now),
		NewObservation("Old info", PriorityInformational, now.Add(-24*time.Hour), now),
		NewObservation("Notable thing", PriorityNotable, now, now),
	}

	kept, err := r.Reflect(context.Background(), obs)
	require.NoError(t, err)
	assert.Len(t, kept, 2)
	assert.Equal(t, "Critical fact", kept[0].Content)
	assert.Equal(t, "Notable thing", kept[1].Content)
}

func TestReflector_ReflectFallbackKeepsCritical(t *testing.T) {
	mockModel := func(_ context.Context, _ string) (string, error) {
		return "garbage output", nil
	}

	r := NewReflector(mockModel, DefaultReflectorConfig())
	now := time.Now()

	obs := []Observation{
		NewObservation("Must keep", PriorityCritical, now, now),
		NewObservation("Can drop", PriorityInformational, now, now),
	}

	kept, err := r.Reflect(context.Background(), obs)
	require.NoError(t, err)
	assert.Len(t, kept, 1)
	assert.Equal(t, "Must keep", kept[0].Content)
}

func TestParsePriority(t *testing.T) {
	assert.Equal(t, PriorityCritical, parsePriority("critical"))
	assert.Equal(t, PriorityCritical, parsePriority("CRITICAL"))
	assert.Equal(t, PriorityNotable, parsePriority("notable"))
	assert.Equal(t, PriorityInformational, parsePriority("informational"))
	assert.Equal(t, PriorityInformational, parsePriority("unknown"))
}

func TestParseKeptIndices(t *testing.T) {
	now := time.Now()
	obs := []Observation{
		NewObservation("A", PriorityCritical, now, now),
		NewObservation("B", PriorityNotable, now, now),
		NewObservation("C", PriorityInformational, now, now),
	}

	tests := []struct {
		name     string
		response string
		wantLen  int
	}{
		{"normal", "KEEP 0\nDROP 1\nKEEP 2", 2},
		{"all keep", "KEEP 0\nKEEP 1\nKEEP 2", 3},
		{"all drop", "DROP 0\nDROP 1\nDROP 2", 1}, // Fallback keeps critical
		{"invalid output", "blah blah", 1},        // Fallback keeps critical
		{"out of range", "KEEP 99", 1},            // Fallback keeps critical
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := parseKeptIndices(tc.response, obs)
			assert.Len(t, result, tc.wantLen, fmt.Sprintf("response: %q", tc.response))
		})
	}
}
