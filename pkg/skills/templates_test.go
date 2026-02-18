package skills

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAvailableTemplates(t *testing.T) {
	templates := AvailableTemplates()
	assert.Contains(t, templates, "trading")
	assert.Contains(t, templates, "legal")
	assert.Contains(t, templates, "company")
}

func TestInstallTemplate(t *testing.T) {
	tmp := t.TempDir()

	err := InstallTemplate("trading", tmp)
	require.NoError(t, err)

	indexSkill := filepath.Join(tmp, "index", "SKILL.md")
	assert.FileExists(t, indexSkill)

	riskSkill := filepath.Join(tmp, "risk-management", "SKILL.md")
	assert.FileExists(t, riskSkill)

	content, err := os.ReadFile(riskSkill)
	require.NoError(t, err)
	assert.Contains(t, string(content), "risk-management")
	assert.Contains(t, string(content), "[[position-sizing]]")
}

func TestInstallTemplate_BuildsValidGraph(t *testing.T) {
	tmp := t.TempDir()
	require.NoError(t, InstallTemplate("trading", tmp))

	sl := NewSkillsLoader("", "", tmp)
	g := sl.BuildGraph()

	assert.True(t, len(g.Nodes) >= 4, "trading template should have at least 4 skills")

	idx := g.GetIndex()
	require.NotNil(t, idx, "trading template should have an index node")
	assert.True(t, idx.IsIndex)
	assert.True(t, idx.IsMOC)
	assert.True(t, len(idx.Links) >= 3, "index should link to at least 3 skills")

	rm := g.GetNode("risk-management")
	require.NotNil(t, rm)
	assert.Equal(t, "finance", rm.Domain)
	assert.Contains(t, rm.Tags, "trading")
}

func TestInstallTemplate_NotFound(t *testing.T) {
	tmp := t.TempDir()
	err := InstallTemplate("nonexistent", tmp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}
