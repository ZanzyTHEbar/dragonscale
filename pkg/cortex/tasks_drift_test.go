package cortex

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
	"github.com/stretchr/testify/require"
)

type fakeDriftSource struct {
	activeAgents []string
	recallItems  []*memory.RecallItem
}

func (f *fakeDriftSource) ListActiveAgents(_ context.Context, _ time.Time) ([]string, error) {
	out := make([]string, len(f.activeAgents))
	copy(out, f.activeAgents)
	return out, nil
}

func (f *fakeDriftSource) ListRecallItems(_ context.Context, agentID, sessionKey string, limit, offset int) ([]*memory.RecallItem, error) {
	filtered := make([]*memory.RecallItem, 0, len(f.recallItems))
	for _, item := range f.recallItems {
		if item.AgentID != agentID {
			continue
		}
		if sessionKey != "" && item.SessionKey != sessionKey {
			continue
		}
		filtered = append(filtered, item)
	}

	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].CreatedAt.After(filtered[j].CreatedAt)
	})

	if offset >= len(filtered) {
		return []*memory.RecallItem{}, nil
	}
	filtered = filtered[offset:]
	if limit < len(filtered) {
		filtered = filtered[:limit]
	}
	return filtered, nil
}

func (f *fakeDriftSource) InsertRecallItem(_ context.Context, item *memory.RecallItem) error {
	copied := *item
	f.recallItems = append(f.recallItems, &copied)
	return nil
}

func TestMemoryDriftAdapter_GetDomainsAndActivity(t *testing.T) {
	t.Parallel()
	now := time.Now()
	source := &fakeDriftSource{
		activeAgents: []string{"agent-1"},
		recallItems: []*memory.RecallItem{
			{
				ID:         ids.New(),
				AgentID:    "agent-1",
				SessionKey: "s1",
				Role:       "assistant",
				Sector:     memory.Sector("tasks"),
				Importance: 0.8,
				CreatedAt:  now.Add(-5 * time.Minute),
				UpdatedAt:  now.Add(-5 * time.Minute),
			},
			{
				ID:         ids.New(),
				AgentID:    "agent-1",
				SessionKey: "s1",
				Role:       "tool",
				Sector:     memory.Sector("tasks"),
				Importance: 0.4,
				Tags:       "tool_call",
				CreatedAt:  now.Add(-3 * time.Minute),
				UpdatedAt:  now.Add(-3 * time.Minute),
			},
			{
				ID:         ids.New(),
				AgentID:    "agent-1",
				SessionKey: "s2",
				Role:       "assistant",
				Sector:     memory.Sector("knowledge"),
				Importance: 0.9,
				CreatedAt:  now.Add(-2 * time.Minute),
				UpdatedAt:  now.Add(-2 * time.Minute),
			},
		},
	}

	adapter := NewMemoryDriftAdapter(source)

	domains, err := adapter.GetDomains(t.Context(), "agent-1")
	require.NoError(t, err)
	require.Equal(t, []string{"knowledge", "tasks"}, domains)

	activity, err := adapter.GetDomainActivity(t.Context(), "agent-1", "tasks", now.Add(-1*time.Hour))
	require.NoError(t, err)
	require.Equal(t, 2, activity.MessageCount)
	require.Equal(t, 2, activity.MemoryCount)
	require.Equal(t, 1, activity.ToolCallCount)
	require.InDelta(t, 0.6, activity.AvgImportance, 0.0001)
	require.False(t, activity.LastActivity.IsZero())
}

func TestMemoryDriftAdapter_StoreGetStatusAndHistory(t *testing.T) {
	t.Parallel()
	now := time.Now()
	source := &fakeDriftSource{
		activeAgents: []string{"agent-1"},
	}
	adapter := NewMemoryDriftAdapter(source)

	require.NoError(t, adapter.StoreDriftStatus(t.Context(), &DomainDrift{
		DomainID:       "tasks",
		AgentID:        "agent-1",
		Status:         StatusDrifting,
		Score:          0.2,
		DetectedAt:     now.Add(-2 * time.Minute),
		Recommendation: "re-engage",
	}))
	require.NoError(t, adapter.StoreDriftStatus(t.Context(), &DomainDrift{
		DomainID:       "tasks",
		AgentID:        "agent-1",
		Status:         StatusActive,
		Score:          0.7,
		DetectedAt:     now.Add(-1 * time.Minute),
		Recommendation: "stable",
	}))

	status, err := adapter.GetDriftStatus(t.Context(), "agent-1", "tasks")
	require.NoError(t, err)
	require.Equal(t, StatusActive, status.Status)
	require.InDelta(t, 0.7, status.Score, 0.0001)

	history, err := adapter.GetHistoricalMetrics(t.Context(), "agent-1", "tasks", 5)
	require.NoError(t, err)
	require.Len(t, history, 2)
	require.True(t, history[0].Period.Before(history[1].Period))
	require.InDelta(t, 0.2, history[0].Score, 0.0001)
	require.InDelta(t, 0.7, history[1].Score, 0.0001)

	agents, err := adapter.ListActiveAgents(t.Context(), now.Add(-1*time.Hour))
	require.NoError(t, err)
	require.Equal(t, []string{"agent-1"}, agents)
}

func TestMemoryDriftAdapter_ExcludesSyntheticDriftRecords(t *testing.T) {
	t.Parallel()
	now := time.Now()
	source := &fakeDriftSource{
		activeAgents: []string{"agent-1"},
		recallItems: []*memory.RecallItem{
			{
				ID:         ids.New(),
				AgentID:    "agent-1",
				SessionKey: "session-1",
				Role:       "assistant",
				Sector:     memory.Sector("tasks"),
				Importance: 0.7,
				CreatedAt:  now.Add(-10 * time.Minute),
				UpdatedAt:  now.Add(-10 * time.Minute),
			},
			{
				ID:         ids.New(),
				AgentID:    "agent-1",
				SessionKey: driftSessionKey,
				Role:       "system",
				Sector:     memory.SectorReflective,
				Importance: 0.9,
				Tags:       driftTagPrefix + ",domain:tasks,status:active",
				CreatedAt:  now.Add(-5 * time.Minute),
				UpdatedAt:  now.Add(-5 * time.Minute),
			},
			{
				ID:         ids.New(),
				AgentID:    "agent-1",
				SessionKey: "session-2",
				Role:       "assistant",
				Sector:     memory.SectorReflective,
				Importance: 0.4,
				Tags:       driftTagPrefix + ",domain:tasks,status:drifting",
				CreatedAt:  now.Add(-4 * time.Minute),
				UpdatedAt:  now.Add(-4 * time.Minute),
			},
		},
	}
	adapter := NewMemoryDriftAdapter(source)

	domains, err := adapter.GetDomains(t.Context(), "agent-1")
	require.NoError(t, err)
	require.Equal(t, []string{"tasks"}, domains)

	activity, err := adapter.GetDomainActivity(t.Context(), "agent-1", "tasks", now.Add(-1*time.Hour))
	require.NoError(t, err)
	require.Equal(t, 1, activity.MessageCount)
	require.Equal(t, 1, activity.MemoryCount)
}

func TestMemoryDriftAdapter_PaginatesRecallItems(t *testing.T) {
	t.Parallel()
	now := time.Now()
	items := make([]*memory.RecallItem, 0, driftPageSize+50)
	for i := 0; i < driftPageSize+50; i++ {
		items = append(items, &memory.RecallItem{
			ID:         ids.New(),
			AgentID:    "agent-1",
			SessionKey: "session-1",
			Role:       "assistant",
			Sector:     memory.Sector("tasks"),
			Importance: 0.5,
			CreatedAt:  now.Add(-time.Duration(i) * time.Second),
			UpdatedAt:  now.Add(-time.Duration(i) * time.Second),
		})
	}
	source := &fakeDriftSource{
		activeAgents: []string{"agent-1"},
		recallItems:  items,
	}
	adapter := NewMemoryDriftAdapter(source)

	activity, err := adapter.GetDomainActivity(t.Context(), "agent-1", "tasks", now.Add(-24*time.Hour))
	require.NoError(t, err)
	require.Equal(t, driftPageSize+50, activity.MessageCount)
}
