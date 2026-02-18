package skills

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed templates/*
var templateFS embed.FS

// AvailableTemplates returns the names of all embedded domain skill graph templates.
func AvailableTemplates() []string {
	entries, err := fs.ReadDir(templateFS, "templates")
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names
}

// InstallTemplate copies an embedded domain skill graph template into the
// target skills directory. Each subdirectory under templates/<name>/ becomes
// a skill directory containing a SKILL.md.
func InstallTemplate(templateName, targetSkillsDir string) error {
	root := filepath.Join("templates", templateName)
	if _, err := fs.Stat(templateFS, root); err != nil {
		return fmt.Errorf("template '%s' not found (available: %v)", templateName, AvailableTemplates())
	}

	return fs.WalkDir(templateFS, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, _ := filepath.Rel(root, path)
		dest := filepath.Join(targetSkillsDir, rel)

		if d.IsDir() {
			return os.MkdirAll(dest, 0755)
		}

		data, err := templateFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", path, err)
		}

		return os.WriteFile(dest, data, 0644)
	})
}
