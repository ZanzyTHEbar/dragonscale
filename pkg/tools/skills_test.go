package tools

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ZanzyTHEbar/dragonscale/pkg/skills"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeTestSkill(t *testing.T, dir, name, content string) {
	t.Helper()
	skillDir := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(skillDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0644))
}

func setupTestSkills(t *testing.T) *skills.SkillsLoader {
	t.Helper()
	tmp := t.TempDir()

	writeTestSkill(t, tmp, "risk-management", `---
name: risk-management
description: "Risk management fundamentals for trading"
tags: trading, risk
domain: finance
---
# Risk Management

See [[position-sizing]] for sizing rules.
`)

	writeTestSkill(t, tmp, "position-sizing", `---
name: position-sizing
description: "Position sizing strategies"
tags: trading, sizing
domain: finance
---
# Position Sizing

Based on [[risk-management]] principles.
`)

	writeTestSkill(t, tmp, "code-review", `---
name: code-review
description: "Code review best practices"
tags: engineering
domain: software
---
# Code Review

No links.
`)

	return skills.NewSkillsLoader("", "", tmp)
}

func TestSkillSearchTool(t *testing.T) {
	t.Parallel()
	loader := setupTestSkills(t)
	tool := NewSkillSearchTool(loader)

	assert.Empty(t, cmp.Diff("skill_search", tool.Name()))

	t.Run("finds matching skills", func(t *testing.T) {
		result := tool.Execute(t.Context(), map[string]interface{}{"query": "trading"})
		assert.False(t, result.IsError)
		assert.Contains(t, result.ForLLM, "risk-management")
		assert.Contains(t, result.ForLLM, "position-sizing")
	})

	t.Run("no results for unmatched query", func(t *testing.T) {
		result := tool.Execute(t.Context(), map[string]interface{}{"query": "kubernetes"})
		assert.False(t, result.IsError)
		assert.Contains(t, result.ForLLM, "No skills matched")
	})

	t.Run("error on empty query", func(t *testing.T) {
		result := tool.Execute(t.Context(), map[string]interface{}{"query": ""})
		assert.True(t, result.IsError)
	})
}

func TestSkillReadTool(t *testing.T) {
	t.Parallel()
	loader := setupTestSkills(t)
	tool := NewSkillReadTool(loader)

	assert.Empty(t, cmp.Diff("skill_read", tool.Name()))

	t.Run("reads existing skill", func(t *testing.T) {
		result := tool.Execute(t.Context(), map[string]interface{}{"name": "risk-management"})
		assert.False(t, result.IsError)
		assert.Contains(t, result.ForLLM, "Risk Management")
		assert.Contains(t, result.ForLLM, "position-sizing")
	})

	t.Run("normalizes path-like skill names", func(t *testing.T) {
		result := tool.Execute(t.Context(), map[string]interface{}{"name": ".assets/risk-management/SKILL.md"})
		assert.False(t, result.IsError)
		assert.Contains(t, result.ForLLM, "# Skill: risk-management")
	})

	t.Run("trims punctuation wrappers around skill names", func(t *testing.T) {
		result := tool.Execute(t.Context(), map[string]interface{}{"name": ":\trisk-management:"})
		assert.False(t, result.IsError)
		assert.Contains(t, result.ForLLM, "# Skill: risk-management")
	})

	t.Run("accepts alias argument names", func(t *testing.T) {
		result := tool.Execute(t.Context(), map[string]interface{}{"skill_name": "risk-management"})
		assert.False(t, result.IsError)
		assert.Contains(t, result.ForLLM, "# Skill: risk-management")
	})

	t.Run("falls back from descriptive query when unique", func(t *testing.T) {
		result := tool.Execute(t.Context(), map[string]interface{}{"query": "engineering"})
		assert.False(t, result.IsError)
		assert.Contains(t, result.ForLLM, "# Skill: code-review")
	})

	t.Run("error on missing skill", func(t *testing.T) {
		result := tool.Execute(t.Context(), map[string]interface{}{"name": "nonexistent"})
		assert.True(t, result.IsError)
		assert.Contains(t, result.ForLLM, "not found")
	})

	t.Run("error on empty name", func(t *testing.T) {
		result := tool.Execute(t.Context(), map[string]interface{}{"name": ""})
		assert.True(t, result.IsError)
	})
}

func TestSkillTraverseTool(t *testing.T) {
	t.Parallel()
	loader := setupTestSkills(t)
	tool := NewSkillTraverseTool(loader)

	assert.Empty(t, cmp.Diff("skill_traverse", tool.Name()))

	t.Run("traverses links at depth 1", func(t *testing.T) {
		result := tool.Execute(t.Context(), map[string]interface{}{"name": "risk-management"})
		assert.False(t, result.IsError)
		assert.Contains(t, result.ForLLM, "position-sizing")
	})

	t.Run("no links found", func(t *testing.T) {
		result := tool.Execute(t.Context(), map[string]interface{}{"name": "code-review"})
		assert.False(t, result.IsError)
		assert.Contains(t, result.ForLLM, "no outgoing links")
	})

	t.Run("error on missing skill", func(t *testing.T) {
		result := tool.Execute(t.Context(), map[string]interface{}{"name": "nonexistent"})
		assert.True(t, result.IsError)
	})

	t.Run("custom depth", func(t *testing.T) {
		result := tool.Execute(t.Context(), map[string]interface{}{
			"name":  "risk-management",
			"depth": float64(2),
		})
		assert.False(t, result.IsError)
		assert.Contains(t, result.ForLLM, "position-sizing")
	})
}
