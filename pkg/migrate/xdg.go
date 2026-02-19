package migrate

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/sipeed/picoclaw/pkg/config"
)

// MigrateToXDG moves files from the legacy ~/.picoclaw/workspace/ layout to
// XDG-compliant directories. It is idempotent: files are only copied if they
// don't already exist at the destination, and it never deletes the source.
//
// Layout mapping:
//
//	~/.picoclaw/workspace/AGENT.md     → $XDG_CONFIG_HOME/picoclaw/identity/AGENT.md
//	~/.picoclaw/workspace/IDENTITY.md  → $XDG_CONFIG_HOME/picoclaw/identity/IDENTITY.md
//	~/.picoclaw/workspace/SOUL.md      → $XDG_CONFIG_HOME/picoclaw/identity/SOUL.md
//	~/.picoclaw/workspace/USER.md      → $XDG_CONFIG_HOME/picoclaw/identity/USER.md
//	~/.picoclaw/workspace/skills/*     → $XDG_DATA_HOME/picoclaw/skills/*
//	~/.picoclaw/workspace/memory/*.db  → $XDG_DATA_HOME/picoclaw/picoclaw.db
//	~/.picoclaw/workspace/*            → $XDG_DATA_HOME/picoclaw/sandbox/* (remaining files)
func MigrateToXDG(legacyWorkspace string) error {
	if legacyWorkspace == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("resolve home: %w", err)
		}
		legacyWorkspace = filepath.Join(home, ".picoclaw", "workspace")
	}

	if _, err := os.Stat(legacyWorkspace); os.IsNotExist(err) {
		return nil
	}

	identityDir, err := config.IdentityDir()
	if err != nil {
		return fmt.Errorf("resolve identity dir: %w", err)
	}
	skillsDir, err := config.SkillsDir()
	if err != nil {
		return fmt.Errorf("resolve skills dir: %w", err)
	}
	dataDir, err := config.DataDir()
	if err != nil {
		return fmt.Errorf("resolve data dir: %w", err)
	}
	sandboxDir, err := config.SandboxDir()
	if err != nil {
		return fmt.Errorf("resolve sandbox dir: %w", err)
	}

	identityFiles := map[string]bool{
		"AGENT.md": true, "IDENTITY.md": true,
		"SOUL.md": true, "USER.md": true,
	}

	for name := range identityFiles {
		src := filepath.Join(legacyWorkspace, name)
		dst := filepath.Join(identityDir, name)
		if err := copyIfMissing(src, dst); err != nil {
			return fmt.Errorf("migrate %s: %w", name, err)
		}
	}

	legacySkills := filepath.Join(legacyWorkspace, "skills")
	if info, err := os.Stat(legacySkills); err == nil && info.IsDir() {
		if err := copyDirIfMissing(legacySkills, skillsDir); err != nil {
			return fmt.Errorf("migrate skills: %w", err)
		}
	}

	legacyDB := filepath.Join(legacyWorkspace, "memory", "picoclaw.db")
	if _, err := os.Stat(legacyDB); err == nil {
		newDB := filepath.Join(dataDir, "picoclaw.db")
		if err := copyIfMissing(legacyDB, newDB); err != nil {
			return fmt.Errorf("migrate database: %w", err)
		}
	}

	skipPrefixes := []string{"AGENT.md", "IDENTITY.md", "SOUL.md", "USER.md"}

	entries, err := os.ReadDir(legacyWorkspace)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		name := e.Name()

		if identityFiles[name] {
			continue
		}
		if name == "skills" || name == "memory" {
			continue
		}

		skip := false
		for _, p := range skipPrefixes {
			if strings.EqualFold(name, p) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}

		src := filepath.Join(legacyWorkspace, name)
		dst := filepath.Join(sandboxDir, name)
		if e.IsDir() {
			if err := copyDirIfMissing(src, dst); err != nil {
				return fmt.Errorf("migrate %s: %w", name, err)
			}
		} else {
			if err := copyIfMissing(src, dst); err != nil {
				return fmt.Errorf("migrate %s: %w", name, err)
			}
		}
	}

	return nil
}

func copyIfMissing(src, dst string) error {
	if _, err := os.Stat(src); os.IsNotExist(err) {
		return nil
	}
	if _, err := os.Stat(dst); err == nil {
		return nil
	}

	os.MkdirAll(filepath.Dir(dst), 0o700)

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

func copyDirIfMissing(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)

		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		return copyIfMissing(path, target)
	})
}
