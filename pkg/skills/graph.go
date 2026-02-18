package skills

import (
	"regexp"
	"sort"
	"strings"
)

var wikilinkPattern = regexp.MustCompile(`\[\[([^\]]+)\]\]`)

// SkillNode extends SkillInfo with graph-aware metadata extracted
// from wikilinks in the skill body and explicit links in frontmatter.
type SkillNode struct {
	SkillInfo
	Links   []string `json:"links,omitempty"`
	IsMOC   bool     `json:"is_moc,omitempty"`
	IsIndex bool     `json:"is_index,omitempty"`
}

// SkillGraph is a directed graph of skill nodes connected by wikilinks
// and explicit frontmatter links. Edges point from a skill to the
// skills it references.
type SkillGraph struct {
	Nodes map[string]*SkillNode
	Edges map[string][]string
}

// BuildGraph constructs the full skill graph from all discovered skills.
// It resolves both explicit frontmatter links and [[wikilink]] references
// found in skill content.
func (sl *SkillsLoader) BuildGraph() *SkillGraph {
	g := &SkillGraph{
		Nodes: make(map[string]*SkillNode),
		Edges: make(map[string][]string),
	}

	allSkills := sl.ListSkills()
	metaByPath := make(map[string]*SkillMetadata, len(allSkills))

	for _, info := range allSkills {
		meta := sl.getSkillMetadata(info.Path)
		metaByPath[info.Path] = meta

		node := &SkillNode{SkillInfo: info}

		if meta != nil {
			node.IsMOC = meta.IsMOC
			node.Links = append(node.Links, meta.Links...)
		}

		if strings.EqualFold(info.Name, "index") {
			node.IsIndex = true
		}

		g.Nodes[info.Name] = node
	}

	for name, node := range g.Nodes {
		content, ok := sl.LoadSkill(name)
		if !ok {
			continue
		}
		wikilinks := ParseWikilinks(content)
		node.Links = mergeUnique(node.Links, wikilinks)
		g.Edges[name] = node.Links
	}

	return g
}

// ParseWikilinks extracts [[skill-name]] references from markdown content.
// Returns deduplicated skill names in order of first appearance.
func ParseWikilinks(content string) []string {
	matches := wikilinkPattern.FindAllStringSubmatch(content, -1)
	seen := make(map[string]bool, len(matches))
	var links []string
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		name := strings.TrimSpace(match[1])
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		links = append(links, name)
	}
	return links
}

// TraverseFrom follows links from a starting node up to the given depth
// using BFS. Returns reachable nodes excluding the starting node.
func (g *SkillGraph) TraverseFrom(name string, depth int) []*SkillNode {
	if depth <= 0 || g.Nodes[name] == nil {
		return nil
	}

	type entry struct {
		name  string
		depth int
	}

	visited := map[string]bool{name: true}
	queue := []entry{{name, 0}}
	var result []*SkillNode

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		if cur.name != name {
			if node := g.Nodes[cur.name]; node != nil {
				result = append(result, node)
			}
		}

		if cur.depth >= depth {
			continue
		}

		for _, link := range g.Edges[cur.name] {
			if visited[link] {
				continue
			}
			visited[link] = true
			queue = append(queue, entry{link, cur.depth + 1})
		}
	}

	return result
}

// SearchSkills performs fuzzy matching against skill names, descriptions,
// tags, and domains. Results are scored and returned in descending relevance.
func (g *SkillGraph) SearchSkills(query string) []*SkillNode {
	query = strings.ToLower(query)
	tokens := strings.Fields(query)
	if len(tokens) == 0 {
		return nil
	}

	type scored struct {
		node  *SkillNode
		score int
	}

	var results []scored

	for _, node := range g.Nodes {
		nameLower := strings.ToLower(node.Name)
		descLower := strings.ToLower(node.Description)
		domainLower := strings.ToLower(node.Domain)

		score := 0
		for _, token := range tokens {
			if strings.Contains(nameLower, token) {
				score += 3
			}
			if strings.Contains(descLower, token) {
				score += 2
			}
			if strings.Contains(domainLower, token) {
				score += 2
			}
			for _, tag := range node.Tags {
				if strings.Contains(strings.ToLower(tag), token) {
					score += 2
				}
			}
		}

		if score > 0 {
			results = append(results, scored{node, score})
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	nodes := make([]*SkillNode, len(results))
	for i, r := range results {
		nodes[i] = r.node
	}
	return nodes
}

// LinksFrom returns direct link targets from a named skill node.
func (g *SkillGraph) LinksFrom(name string) []string {
	return g.Edges[name]
}

// GetNode returns a skill node by name, or nil if not found.
func (g *SkillGraph) GetNode(name string) *SkillNode {
	return g.Nodes[name]
}

// ListMOCs returns all Map of Content nodes in the graph.
func (g *SkillGraph) ListMOCs() []*SkillNode {
	var mocs []*SkillNode
	for _, node := range g.Nodes {
		if node.IsMOC {
			mocs = append(mocs, node)
		}
	}
	return mocs
}

// GetIndex returns the root index node, or nil if none exists.
func (g *SkillGraph) GetIndex() *SkillNode {
	for _, node := range g.Nodes {
		if node.IsIndex {
			return node
		}
	}
	return nil
}

func mergeUnique(a, b []string) []string {
	seen := make(map[string]bool, len(a))
	for _, s := range a {
		seen[s] = true
	}
	result := make([]string, len(a))
	copy(result, a)
	for _, s := range b {
		if seen[s] {
			continue
		}
		seen[s] = true
		result = append(result, s)
	}
	return result
}
