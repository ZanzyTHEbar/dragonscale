package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
	memstore "github.com/ZanzyTHEbar/dragonscale/pkg/memory/store"
	"github.com/ZanzyTHEbar/dragonscale/pkg/messages"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyContextTreeSelection_UsesSemanticEmbeddings(t *testing.T) {
	t.Parallel()

	target := "SQL latency issue on write path"
	query := "database stalls"
	al := &AgentLoop{
		contextBuilder:   NewContextBuilder(filepath.Join(t.TempDir(), "workspace")),
		contextWindow:    4096,
		memoryStore:      memstore.New(nil, nil, fixtureEmbedder{query: {1, 0}, target: {1, 0}}, memstore.Config{ContextWindowTokens: 4096}),
		contextTreeCache: sync.Map{},
	}

	history := semanticSelectionHistory(target)
	tail := al.applyContextTreeSelection(t.Context(), "semantic-session", query, history)

	require.NotEmpty(t, tail)
	assert.Contains(t, al.contextBuilder.contextTreeBlock, target)
}

func TestApplyContextTreeSelection_PreservesSelectionAccessCounts(t *testing.T) {
	t.Parallel()

	target := "SQL latency issue on write path"
	query := "database stalls"
	al := &AgentLoop{
		contextBuilder:   NewContextBuilder(filepath.Join(t.TempDir(), "workspace")),
		contextWindow:    4096,
		memoryStore:      memstore.New(nil, nil, fixtureEmbedder{query: {1, 0}, target: {1, 0}}, memstore.Config{ContextWindowTokens: 4096}),
		contextTreeCache: sync.Map{},
	}

	history := semanticSelectionHistory(target)
	_ = al.applyContextTreeSelection(t.Context(), "semantic-session", query, history)
	firstCached, ok := al.contextTreeCache.Load("semantic-session")
	require.True(t, ok)
	firstEntry := firstCached.(contextTreeCacheEntry)
	require.NotEmpty(t, firstEntry.selectedKeys)
	firstKey := firstEntry.selectedKeys[0]
	firstCount := firstEntry.accessCounts[firstKey]

	_ = al.applyContextTreeSelection(t.Context(), "semantic-session", query, history)
	secondCached, ok := al.contextTreeCache.Load("semantic-session")
	require.True(t, ok)
	secondEntry := secondCached.(contextTreeCacheEntry)
	require.NotEmpty(t, secondEntry.selectedKeys)
	assert.Greater(t, secondEntry.accessCounts[firstKey], firstCount)
}

type fixtureEmbedder map[string]memory.Embedding

func (f fixtureEmbedder) Embed(_ context.Context, text string) (memory.Embedding, error) {
	if embedding, ok := f[text]; ok {
		return embedding, nil
	}
	return memory.Embedding{0, 1}, nil
}

func (f fixtureEmbedder) EmbedBatch(ctx context.Context, texts []string) ([]memory.Embedding, error) {
	out := make([]memory.Embedding, 0, len(texts))
	for _, text := range texts {
		embedding, err := f.Embed(ctx, text)
		if err != nil {
			return nil, err
		}
		out = append(out, embedding)
	}
	return out, nil
}

func (f fixtureEmbedder) Dimensions() int { return 2 }
func (f fixtureEmbedder) Model() string   { return "fixture" }

func semanticSelectionHistory(target string) []messages.Message {
	history := make([]messages.Message, 0, 40)
	for i := 0; i < 40; i++ {
		content := fmt.Sprintf("routine message %02d about unrelated topic", i)
		if i == 5 {
			content = target
		}
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		history = append(history, messages.Message{
			Role:    role,
			Content: content,
		})
	}
	return history
}
