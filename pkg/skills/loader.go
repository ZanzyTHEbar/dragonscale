package skills

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	jsonv2 "github.com/go-json-experiment/json"
)

var namePattern = regexp.MustCompile(`^[a-zA-Z0-9]+(-[a-zA-Z0-9]+)*$`)

const (
	MaxNameLength        = 64
	MaxDescriptionLength = 1024
)

type SkillMetadata struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags,omitzero"`
	Links       []string `json:"links,omitzero"`
	Domain      string   `json:"domain,omitzero"`
	IsMOC       bool     `json:"is_moc,omitzero"`
}

type SkillInfo struct {
	Name        string   `json:"name"`
	Path        string   `json:"path"`
	Source      string   `json:"source"`
	Description string   `json:"description"`
	Tags        []string `json:"tags,omitzero"`
	Domain      string   `json:"domain,omitzero"`
}

func (info SkillInfo) validate() error {
	var errs error
	if info.Name == "" {
		errs = errors.Join(errs, errors.New("name is required"))
	} else {
		if len(info.Name) > MaxNameLength {
			errs = errors.Join(errs, fmt.Errorf("name exceeds %d characters", MaxNameLength))
		}
		if !namePattern.MatchString(info.Name) {
			errs = errors.Join(errs, errors.New("name must be alphanumeric with hyphens"))
		}
	}

	if info.Description == "" {
		errs = errors.Join(errs, errors.New("description is required"))
	} else if len(info.Description) > MaxDescriptionLength {
		errs = errors.Join(errs, fmt.Errorf("description exceeds %d character", MaxDescriptionLength))
	}
	return errs
}

type SkillsLoader struct {
	primarySkills string // primary skills directory (XDG data dir or workspace/skills)
	globalSkills  string // user-level override skills (~/.config/dragonscale/skills)
	builtinSkills string // built-in skills (bundled with binary)
}

// NewSkillsLoader creates a loader that searches for skills in three directories
// with priority: primary > global > builtin. The primary directory is typically
// $XDG_DATA_HOME/dragonscale/skills; the global directory allows user overrides;
// and the builtin directory ships with the binary.
func NewSkillsLoader(primarySkillsDir string, globalSkills string, builtinSkills string) *SkillsLoader {
	return &SkillsLoader{
		primarySkills: primarySkillsDir,
		globalSkills:  globalSkills,
		builtinSkills: builtinSkills,
	}
}

// DirsMtime returns the latest modification time across all skill directories
// and their immediate children. Catches both file additions (directory mtime)
// and in-place edits of existing skill files (file mtime).
func (sl *SkillsLoader) DirsMtime() time.Time {
	var latest time.Time
	for _, dir := range []string{sl.primarySkills, sl.globalSkills, sl.builtinSkills} {
		if dir == "" {
			continue
		}
		info, err := os.Stat(dir)
		if err != nil {
			continue
		}
		if mt := info.ModTime(); mt.After(latest) {
			latest = mt
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			fi, err := e.Info()
			if err != nil {
				continue
			}
			if mt := fi.ModTime(); mt.After(latest) {
				latest = mt
			}
		}
	}
	return latest
}

func (sl *SkillsLoader) ListSkills() []SkillInfo {
	skills := make([]SkillInfo, 0)

	if sl.primarySkills != "" {
		if dirs, err := os.ReadDir(sl.primarySkills); err == nil {
			for _, dir := range dirs {
				if dir.IsDir() {
					skillFile := filepath.Join(sl.primarySkills, dir.Name(), "SKILL.md")
					if _, err := os.Stat(skillFile); err == nil {
						info := SkillInfo{
							Name:   dir.Name(),
							Path:   skillFile,
							Source: "workspace",
						}
						metadata := sl.getSkillMetadata(skillFile)
						if metadata != nil {
							info.Description = metadata.Description
							info.Name = metadata.Name
							info.Tags = metadata.Tags
							info.Domain = metadata.Domain
						}
						if err := info.validate(); err != nil {
							slog.Warn("invalid skill from workspace", "name", info.Name, "error", err)
							continue
						}
						skills = append(skills, info)
					}
				}
			}
		}
	}

	// 全局 skills (~/.dragonscale/skills) - 被 workspace skills 覆盖
	if sl.globalSkills != "" {
		if dirs, err := os.ReadDir(sl.globalSkills); err == nil {
			for _, dir := range dirs {
				if dir.IsDir() {
					skillFile := filepath.Join(sl.globalSkills, dir.Name(), "SKILL.md")
					if _, err := os.Stat(skillFile); err == nil {
						// 检查是否已被 workspace skills 覆盖
						exists := false
						for _, s := range skills {
							if s.Name == dir.Name() && s.Source == "workspace" {
								exists = true
								break
							}
						}
						if exists {
							continue
						}

						info := SkillInfo{
							Name:   dir.Name(),
							Path:   skillFile,
							Source: "global",
						}
						metadata := sl.getSkillMetadata(skillFile)
						if metadata != nil {
							info.Description = metadata.Description
							info.Name = metadata.Name
							info.Tags = metadata.Tags
							info.Domain = metadata.Domain
						}
						if err := info.validate(); err != nil {
							slog.Warn("invalid skill from global", "name", info.Name, "error", err)
							continue
						}
						skills = append(skills, info)
					}
				}
			}
		}
	}

	if sl.builtinSkills != "" {
		if dirs, err := os.ReadDir(sl.builtinSkills); err == nil {
			for _, dir := range dirs {
				if dir.IsDir() {
					skillFile := filepath.Join(sl.builtinSkills, dir.Name(), "SKILL.md")
					if _, err := os.Stat(skillFile); err == nil {
						// 检查是否已被 workspace 或 global skills 覆盖
						exists := false
						for _, s := range skills {
							if s.Name == dir.Name() && (s.Source == "workspace" || s.Source == "global") {
								exists = true
								break
							}
						}
						if exists {
							continue
						}

						info := SkillInfo{
							Name:   dir.Name(),
							Path:   skillFile,
							Source: "builtin",
						}
						metadata := sl.getSkillMetadata(skillFile)
						if metadata != nil {
							info.Description = metadata.Description
							info.Name = metadata.Name
							info.Tags = metadata.Tags
							info.Domain = metadata.Domain
						}
						if err := info.validate(); err != nil {
							slog.Warn("invalid skill from builtin", "name", info.Name, "error", err)
							continue
						}
						skills = append(skills, info)
					}
				}
			}
		}
	}

	return skills
}

func (sl *SkillsLoader) LoadSkill(name string) (string, bool) {
	if sl.primarySkills != "" {
		skillFile := filepath.Join(sl.primarySkills, name, "SKILL.md")
		if content, err := os.ReadFile(skillFile); err == nil {
			return sl.stripFrontmatter(string(content)), true
		}
	}

	// 2. 其次从全局 skills 加载 (~/.dragonscale/skills)
	if sl.globalSkills != "" {
		skillFile := filepath.Join(sl.globalSkills, name, "SKILL.md")
		if content, err := os.ReadFile(skillFile); err == nil {
			return sl.stripFrontmatter(string(content)), true
		}
	}

	// 3. 最后从内置 skills 加载
	if sl.builtinSkills != "" {
		skillFile := filepath.Join(sl.builtinSkills, name, "SKILL.md")
		if content, err := os.ReadFile(skillFile); err == nil {
			return sl.stripFrontmatter(string(content)), true
		}
	}

	return "", false
}

func (sl *SkillsLoader) LoadSkillsForContext(skillNames []string) string {
	if len(skillNames) == 0 {
		return ""
	}

	var parts []string
	for _, name := range skillNames {
		content, ok := sl.LoadSkill(name)
		if ok {
			parts = append(parts, fmt.Sprintf("### Skill: %s\n\n%s", name, content))
		}
	}

	return strings.Join(parts, "\n\n---\n\n")
}

func (sl *SkillsLoader) BuildSkillsSummary() string {
	allSkills := sl.ListSkills()
	if len(allSkills) == 0 {
		return ""
	}

	var lines []string
	lines = append(lines, "<skills>")
	for _, s := range allSkills {
		escapedName := escapeXML(s.Name)
		escapedDesc := escapeXML(s.Description)
		escapedPath := escapeXML(s.Path)

		lines = append(lines, "  <skill>")
		lines = append(lines, fmt.Sprintf("    <name>%s</name>", escapedName))
		lines = append(lines, fmt.Sprintf("    <description>%s</description>", escapedDesc))
		lines = append(lines, fmt.Sprintf("    <location>%s</location>", escapedPath))
		lines = append(lines, fmt.Sprintf("    <source>%s</source>", s.Source))
		if len(s.Tags) > 0 {
			lines = append(lines, fmt.Sprintf("    <tags>%s</tags>", escapeXML(strings.Join(s.Tags, ", "))))
		}
		if s.Domain != "" {
			lines = append(lines, fmt.Sprintf("    <domain>%s</domain>", escapeXML(s.Domain)))
		}
		lines = append(lines, "  </skill>")
	}
	lines = append(lines, "</skills>")

	return strings.Join(lines, "\n")
}

func (sl *SkillsLoader) getSkillMetadata(skillPath string) *SkillMetadata {
	content, err := os.ReadFile(skillPath)
	if err != nil {
		return nil
	}

	frontmatter := sl.extractFrontmatter(string(content))
	if frontmatter == "" {
		return &SkillMetadata{
			Name: filepath.Base(filepath.Dir(skillPath)),
		}
	}

	// Try JSON first (for backward compatibility)
	var jsonMeta SkillMetadata
	if err := jsonv2.Unmarshal([]byte(frontmatter), &jsonMeta); err == nil && jsonMeta.Name != "" {
		return &jsonMeta
	}

	// Fall back to simple YAML parsing
	yamlMeta := sl.parseSimpleYAML(frontmatter)
	meta := &SkillMetadata{
		Name:        yamlMeta["name"],
		Description: yamlMeta["description"],
		Domain:      yamlMeta["domain"],
	}
	if tags := yamlMeta["tags"]; tags != "" {
		meta.Tags = splitCSV(tags)
	}
	if links := yamlMeta["links"]; links != "" {
		meta.Links = splitCSV(links)
	}
	if yamlMeta["is_moc"] == "true" {
		meta.IsMOC = true
	}
	return meta
}

// parseSimpleYAML parses simple key: value YAML format
// Example: name: github\n description: "..."
func (sl *SkillsLoader) parseSimpleYAML(content string) map[string]string {
	result := make(map[string]string)

	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			// Remove quotes if present
			value = strings.Trim(value, "\"'")
			result[key] = value
		}
	}

	return result
}

func (sl *SkillsLoader) extractFrontmatter(content string) string {
	// (?s) enables DOTALL mode so . matches newlines
	// Match first ---, capture everything until next --- on its own line
	re := regexp.MustCompile(`(?s)^---\n(.*)\n---`)
	match := re.FindStringSubmatch(content)
	if len(match) > 1 {
		return match[1]
	}
	return ""
}

func (sl *SkillsLoader) stripFrontmatter(content string) string {
	re := regexp.MustCompile(`^---\n.*?\n---\n`)
	return re.ReplaceAllString(content, "")
}

func splitCSV(s string) []string {
	var result []string
	for _, item := range strings.Split(s, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			result = append(result, item)
		}
	}
	return result
}

func escapeXML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}
