package skills

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseWikilinks(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		content string
		want    []string
	}{
		{
			name:    "single link",
			content: "See [[risk-management]] for details.",
			want:    []string{"risk-management"},
		},
		{
			name:    "multiple links",
			content: "Use [[position-sizing]] and [[technical-analysis]] together.",
			want:    []string{"position-sizing", "technical-analysis"},
		},
		{
			name:    "duplicate links deduplicated",
			content: "Read [[alpha]] first, then [[beta]], then revisit [[alpha]].",
			want:    []string{"alpha", "beta"},
		},
		{
			name:    "no links",
			content: "This has no wikilinks at all.",
			want:    nil,
		},
		{
			name:    "empty content",
			content: "",
			want:    nil,
		},
		{
			name:    "link with spaces trimmed",
			content: "See [[ spaced-link ]] here.",
			want:    []string{"spaced-link"},
		},
		{
			name:    "nested brackets ignored",
			content: "Not a link: [not [real]] but [[actual-link]] is.",
			want:    []string{"actual-link"},
		},
		{
			name:    "multiline content",
			content: "# Title\n\nSee [[skill-a]].\n\nAlso [[skill-b]] and [[skill-c]].\n",
			want:    []string{"skill-a", "skill-b", "skill-c"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseWikilinks(tc.content)
			assert.Empty(t, cmp.Diff(tc.want, got))
		})
	}
}

func TestMergeUnique(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		a, b []string
		want []string
	}{
		{"both empty", nil, nil, []string{}},
		{"a only", []string{"x"}, nil, []string{"x"}},
		{"b only", nil, []string{"y"}, []string{"y"}},
		{"overlap", []string{"a", "b"}, []string{"b", "c"}, []string{"a", "b", "c"}},
		{"no overlap", []string{"a"}, []string{"b"}, []string{"a", "b"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeUnique(tc.a, tc.b)
			assert.Empty(t, cmp.Diff(tc.want, got))
		})
	}
}

func writeSkill(t *testing.T, dir, name, content string) {
	t.Helper()
	skillDir := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(skillDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0644))
}

func TestBuildGraph_Basic(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	writeSkill(t, tmp, "risk-management", `---
name: risk-management
description: "Risk management fundamentals"
tags: trading, risk
domain: finance
---
# Risk Management

See [[position-sizing]] for sizing rules.
Also check [[technical-analysis]].
`)

	writeSkill(t, tmp, "position-sizing", `---
name: position-sizing
description: "Position sizing strategies"
tags: trading, sizing
domain: finance
---
# Position Sizing

Based on [[risk-management]] principles.
`)

	writeSkill(t, tmp, "technical-analysis", `---
name: technical-analysis
description: "Technical analysis patterns"
tags: trading, charts
domain: finance
---
# Technical Analysis

No wikilinks here.
`)

	sl := NewSkillsLoader("", "", tmp)
	g := sl.BuildGraph()

	assert.Len(t, g.Nodes, 3)

	rm := g.GetNode("risk-management")
	require.NotNil(t, rm)
	assert.Empty(t, cmp.Diff([]string{"trading", "risk"}, rm.Tags))
	assert.Empty(t, cmp.Diff("finance", rm.Domain))
	assert.Contains(t, rm.Links, "position-sizing")
	assert.Contains(t, rm.Links, "technical-analysis")

	ps := g.GetNode("position-sizing")
	require.NotNil(t, ps)
	assert.Contains(t, ps.Links, "risk-management")

	ta := g.GetNode("technical-analysis")
	require.NotNil(t, ta)
	assert.Empty(t, ta.Links)
}

func TestBuildGraph_FrontmatterLinksAndWikilinks(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	writeSkill(t, tmp, "alpha", `---
name: alpha
description: "Alpha skill"
links: beta
---
Content with [[gamma]] link.
`)

	writeSkill(t, tmp, "beta", `---
name: beta
description: "Beta skill"
---
No links.
`)

	writeSkill(t, tmp, "gamma", `---
name: gamma
description: "Gamma skill"
---
No links.
`)

	sl := NewSkillsLoader("", "", tmp)
	g := sl.BuildGraph()

	alpha := g.GetNode("alpha")
	require.NotNil(t, alpha)
	assert.Contains(t, alpha.Links, "beta")
	assert.Contains(t, alpha.Links, "gamma")
}

func TestTraverseFrom(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	writeSkill(t, tmp, "a", `---
name: a
description: "Start node"
---
Links to [[b]] and [[c]].
`)
	writeSkill(t, tmp, "b", `---
name: b
description: "Mid node"
---
Links to [[d]].
`)
	writeSkill(t, tmp, "c", `---
name: c
description: "Leaf node"
---
No outgoing links.
`)
	writeSkill(t, tmp, "d", `---
name: d
description: "Deep node"
---
No outgoing links.
`)

	sl := NewSkillsLoader("", "", tmp)
	g := sl.BuildGraph()

	depth1 := g.TraverseFrom("a", 1)
	names1 := nodeNames(depth1)
	assert.Contains(t, names1, "b")
	assert.Contains(t, names1, "c")
	assert.NotContains(t, names1, "d")

	depth2 := g.TraverseFrom("a", 2)
	names2 := nodeNames(depth2)
	assert.Contains(t, names2, "b")
	assert.Contains(t, names2, "c")
	assert.Contains(t, names2, "d")

	assert.Nil(t, g.TraverseFrom("a", 0))
	assert.Nil(t, g.TraverseFrom("nonexistent", 1))
}

func TestSearchSkills(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	writeSkill(t, tmp, "risk-management", `---
name: risk-management
description: "Risk management fundamentals for trading"
tags: trading, risk
domain: finance
---
Content.
`)
	writeSkill(t, tmp, "code-review", `---
name: code-review
description: "Code review best practices"
tags: engineering
domain: software
---
Content.
`)
	writeSkill(t, tmp, "market-psychology", `---
name: market-psychology
description: "Psychology of markets and trading behavior"
tags: trading, psychology
domain: finance
---
Content.
`)

	sl := NewSkillsLoader("", "", tmp)
	g := sl.BuildGraph()

	results := g.SearchSkills("trading")
	require.True(t, len(results) >= 2)
	names := nodeNames(skillNodesToSlice(results))
	assert.Contains(t, names, "risk-management")
	assert.Contains(t, names, "market-psychology")

	results = g.SearchSkills("software engineering")
	require.True(t, len(results) >= 1)
	assert.Empty(t, cmp.Diff("code-review", results[0].Name))

	results = g.SearchSkills("")
	assert.Empty(t, results)
}

func TestListMOCs(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	writeSkill(t, tmp, "trading-moc", `---
name: trading-moc
description: "Map of trading skills"
is_moc: true
domain: finance
---
Overview of [[risk-management]] and [[position-sizing]].
`)
	writeSkill(t, tmp, "risk-management", `---
name: risk-management
description: "Risk basics"
---
Content.
`)

	sl := NewSkillsLoader("", "", tmp)
	g := sl.BuildGraph()

	mocs := g.ListMOCs()
	assert.Len(t, mocs, 1)
	assert.Empty(t, cmp.Diff("trading-moc", mocs[0].Name))
	assert.True(t, mocs[0].IsMOC)
}

func TestGetIndex(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	writeSkill(t, tmp, "index", `---
name: index
description: "Root index of the skill graph"
---
Start here: [[trading-moc]], [[engineering-moc]].
`)
	writeSkill(t, tmp, "trading-moc", `---
name: trading-moc
description: "Trading overview"
---
Content.
`)

	sl := NewSkillsLoader("", "", tmp)
	g := sl.BuildGraph()

	idx := g.GetIndex()
	require.NotNil(t, idx)
	assert.Empty(t, cmp.Diff("index", idx.Name))
	assert.True(t, idx.IsIndex)
	assert.Contains(t, idx.Links, "trading-moc")
	assert.Contains(t, idx.Links, "engineering-moc")
}

func TestGetIndex_None(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	writeSkill(t, tmp, "some-skill", `---
name: some-skill
description: "Not an index"
---
Content.
`)

	sl := NewSkillsLoader("", "", tmp)
	g := sl.BuildGraph()
	assert.Nil(t, g.GetIndex())
}

func TestExtendedFrontmatter_YAML(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	writeSkill(t, tmp, "yaml-skill", `---
name: yaml-skill
description: A YAML frontmatter skill
tags: alpha, beta
domain: testing
is_moc: true
---
Content with [[some-link]].
`)

	sl := NewSkillsLoader("", "", tmp)
	skills := sl.ListSkills()
	require.Len(t, skills, 1)

	s := skills[0]
	assert.Empty(t, cmp.Diff("yaml-skill", s.Name))
	assert.Empty(t, cmp.Diff([]string{"alpha", "beta"}, s.Tags))
	assert.Empty(t, cmp.Diff("testing", s.Domain))
}

func nodeNames(nodes []*SkillNode) []string {
	names := make([]string, len(nodes))
	for i, n := range nodes {
		names[i] = n.Name
	}
	return names
}

func skillNodesToSlice(nodes []*SkillNode) []*SkillNode {
	return nodes
}
