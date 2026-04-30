package tools

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// maxWriteBytes is the maximum file size the write tool will accept.
const maxWriteBytes = 10 * 1024 * 1024 // 10 MiB

// sensitivePatterns are filename patterns that should never be read or written
// by the agent. The check is case-insensitive on the base filename.
var sensitivePatterns = []string{
	".env",
	".env.local",
	".env.production",
	"id_rsa",
	"id_ed25519",
	"id_ecdsa",
	"id_dsa",
	"credentials.json",
	"service-account.json",
	"secrets.yaml",
	"secrets.yml",
	".npmrc",
	".pypirc",
	".netrc",
}

func isSensitiveFile(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	for _, pat := range sensitivePatterns {
		if base == pat {
			return true
		}
	}
	return false
}

// validatePath ensures the given path is within the workspace if restrict is true.
// It rejects paths containing null bytes and blocks access to sensitive credential files.
func validatePath(path, workspace string, restrict bool) (string, error) {
	if strings.ContainsRune(path, 0) {
		return "", fmt.Errorf("access denied: path contains null byte")
	}

	if workspace == "" {
		if restrict {
			return "", fmt.Errorf("workspace is not defined")
		}
		return path, nil
	}

	absWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		return "", fmt.Errorf("failed to resolve workspace path: %w", err)
	}

	var absPath string
	if filepath.IsAbs(path) {
		absPath = filepath.Clean(path)
	} else {
		absPath, err = filepath.Abs(filepath.Join(absWorkspace, path))
		if err != nil {
			return "", fmt.Errorf("failed to resolve file path: %w", err)
		}
	}

	if restrict && isSensitiveFile(absPath) {
		return "", fmt.Errorf("access denied: path targets a sensitive file")
	}

	if restrict {
		if !isWithinWorkspace(absPath, absWorkspace) {
			return "", fmt.Errorf("access denied: path is outside the workspace")
		}

		workspaceReal := absWorkspace
		if resolved, err := filepath.EvalSymlinks(absWorkspace); err == nil {
			workspaceReal = resolved
		}

		if resolved, err := filepath.EvalSymlinks(absPath); err == nil {
			if !isWithinWorkspace(resolved, workspaceReal) {
				return "", fmt.Errorf("access denied: symlink resolves outside workspace")
			}
		} else if os.IsNotExist(err) {
			if parentResolved, err := resolveExistingAncestor(filepath.Dir(absPath)); err == nil {
				if !isWithinWorkspace(parentResolved, workspaceReal) {
					return "", fmt.Errorf("access denied: symlink resolves outside workspace")
				}
			} else if !os.IsNotExist(err) {
				return "", fmt.Errorf("failed to resolve path: %w", err)
			}
		} else {
			return "", fmt.Errorf("failed to resolve path: %w", err)
		}
	}

	return absPath, nil
}

func resolveExistingAncestor(path string) (string, error) {
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		if resolved, err := filepath.EvalSymlinks(current); err == nil {
			return resolved, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		if filepath.Dir(current) == current {
			return "", os.ErrNotExist
		}
	}
}

func resolveExistingAncestorPair(path string) (string, string, error) {
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		if resolved, err := filepath.EvalSymlinks(current); err == nil {
			return current, resolved, nil
		} else if !os.IsNotExist(err) {
			return "", "", err
		}
		if filepath.Dir(current) == current {
			return "", "", os.ErrNotExist
		}
	}
}

func isWithinWorkspace(candidate, workspace string) bool {
	rel, err := filepath.Rel(filepath.Clean(workspace), filepath.Clean(candidate))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func restrictedRelPath(path, workspace string) (string, error) {
	absPath, err := validatePath(path, workspace, true)
	if err != nil {
		return "", err
	}

	absWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		return "", fmt.Errorf("failed to resolve workspace path: %w", err)
	}
	workspaceRoot := absWorkspace
	if resolved, err := filepath.EvalSymlinks(absWorkspace); err == nil {
		workspaceRoot = resolved
	}

	canonicalPath := absPath
	if resolved, err := filepath.EvalSymlinks(absPath); err == nil {
		canonicalPath = resolved
	} else if os.IsNotExist(err) {
		ancestorPath, ancestorResolved, err := resolveExistingAncestorPair(filepath.Dir(absPath))
		if err != nil {
			return "", fmt.Errorf("failed to resolve path: %w", err)
		}
		suffix, err := filepath.Rel(ancestorPath, absPath)
		if err != nil {
			return "", fmt.Errorf("failed to resolve relative path: %w", err)
		}
		canonicalPath = filepath.Join(ancestorResolved, suffix)
	} else {
		return "", fmt.Errorf("failed to resolve path: %w", err)
	}

	relPath, err := filepath.Rel(workspaceRoot, canonicalPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve relative path: %w", err)
	}
	relPath = filepath.Clean(relPath)
	if relPath == "." {
		return relPath, nil
	}
	if !filepath.IsLocal(relPath) {
		return "", fmt.Errorf("access denied: path is outside the workspace")
	}
	return relPath, nil
}

type fileSystem interface {
	ReadFile(path string) ([]byte, error)
	WriteFile(path string, data []byte) error
	AppendFile(path string, data []byte) error
	ReadDir(path string) ([]os.DirEntry, error)
}

func newFileSystem(workspace string, restrict bool) fileSystem {
	if restrict {
		return &sandboxFs{workspace: workspace}
	}
	return &hostFs{}
}

type hostFs struct{}

func defaultFileSystem(fs fileSystem) fileSystem {
	if fs != nil {
		return fs
	}
	return &hostFs{}
}

func (h *hostFs) ReadFile(path string) ([]byte, error) {
	resolvedPath, err := validatePath(path, "", false)
	if err != nil {
		return nil, err
	}

	content, err := os.ReadFile(resolvedPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	return content, nil
}

func hostWritePerm(path string) (os.FileMode, error) {
	perm := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		perm = info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return 0, fmt.Errorf("failed to inspect file: %w", err)
	}
	return perm, nil
}

func (h *hostFs) WriteFile(path string, data []byte) error {
	resolvedPath, err := validatePath(path, "", false)
	if err != nil {
		return err
	}

	dir := filepath.Dir(resolvedPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	perm, err := hostWritePerm(resolvedPath)
	if err != nil {
		return err
	}

	f, err := os.OpenFile(resolvedPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}
	return nil
}

func (h *hostFs) AppendFile(path string, data []byte) error {
	resolvedPath, err := validatePath(path, "", false)
	if err != nil {
		return err
	}

	dir := filepath.Dir(resolvedPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	f, err := os.OpenFile(resolvedPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("failed to append to file: %w", err)
	}
	return nil
}

func (h *hostFs) ReadDir(path string) ([]os.DirEntry, error) {
	resolvedPath, err := validatePath(path, "", false)
	if err != nil {
		return nil, err
	}
	return os.ReadDir(resolvedPath)
}

type sandboxFs struct {
	workspace string
}

func (s *sandboxFs) open(path string) (*os.Root, string, error) {
	if strings.TrimSpace(s.workspace) == "" {
		return nil, "", fmt.Errorf("workspace is not defined")
	}

	workspace, err := filepath.Abs(s.workspace)
	if err != nil {
		return nil, "", fmt.Errorf("failed to resolve workspace path: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(workspace); err == nil {
		workspace = resolved
	}

	relPath, err := restrictedRelPath(path, s.workspace)
	if err != nil {
		return nil, "", err
	}

	root, err := os.OpenRoot(workspace)
	if err != nil {
		return nil, "", fmt.Errorf("failed to open workspace: %w", err)
	}
	return root, relPath, nil
}

func (s *sandboxFs) ReadFile(path string) ([]byte, error) {
	root, relPath, err := s.open(path)
	if err != nil {
		return nil, err
	}
	defer root.Close()

	content, err := root.ReadFile(relPath)
	if err != nil {
		if os.IsPermission(err) || strings.Contains(err.Error(), "escapes from parent") {
			return nil, fmt.Errorf("failed to read file: access denied: %w", err)
		}
		if os.IsNotExist(err) || strings.Contains(strings.ToLower(err.Error()), "no such file") {
			return nil, fmt.Errorf("failed to read file: file not found: %w", err)
		}
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	return content, nil
}

func sandboxWritePerm(root *os.Root, relPath string) (os.FileMode, error) {
	perm := os.FileMode(0o644)
	if info, err := root.Stat(relPath); err == nil {
		perm = info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return 0, fmt.Errorf("failed to inspect file: %w", err)
	}
	return perm, nil
}

func (s *sandboxFs) WriteFile(path string, data []byte) error {
	root, relPath, err := s.open(path)
	if err != nil {
		return err
	}
	defer root.Close()

	dir := filepath.Dir(relPath)
	if dir != "." {
		if err := root.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("failed to create directory: %w", err)
		}
	}

	perm, err := sandboxWritePerm(root, relPath)
	if err != nil {
		return err
	}

	f, err := root.OpenFile(relPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}
	return nil
}

func (s *sandboxFs) AppendFile(path string, data []byte) error {
	root, relPath, err := s.open(path)
	if err != nil {
		return err
	}
	defer root.Close()

	dir := filepath.Dir(relPath)
	if dir != "." {
		if err := root.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("failed to create directory: %w", err)
		}
	}

	f, err := root.OpenFile(relPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		if os.IsPermission(err) || strings.Contains(err.Error(), "escapes from parent") {
			return fmt.Errorf("failed to append to file: access denied: %w", err)
		}
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("failed to append to file: %w", err)
	}
	return nil
}

func (s *sandboxFs) ReadDir(path string) ([]os.DirEntry, error) {
	root, relPath, err := s.open(path)
	if err != nil {
		return nil, err
	}
	defer root.Close()

	return fs.ReadDir(root.FS(), relPath)
}

type ReadFileTool struct {
	fs fileSystem
}

func NewReadFileTool(workspace string, restrict bool) *ReadFileTool {
	return &ReadFileTool{fs: newFileSystem(workspace, restrict)}
}

func (t *ReadFileTool) Name() string {
	return "read_file"
}

func (t *ReadFileTool) Description() string {
	return "Read the contents of a regular workspace file. Use this to verify file state after writes or edits. For skills, use skill_read instead of read_file."
}

func (t *ReadFileTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type":        "string",
				"description": "Path to the file to read",
			},
		},
		"required": []string{"path"},
	}
}

// Capabilities declares that ReadFileTool requires read-only filesystem access.
func (t *ReadFileTool) Capabilities() ToolCapabilities {
	return ToolCapabilities{
		Filesystem: []PathRule{{Pattern: "**", Mode: "r"}},
	}
}

func (t *ReadFileTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	path, ok := args["path"].(string)
	if !ok {
		return ErrorResult("path is required")
	}

	content, err := defaultFileSystem(t.fs).ReadFile(path)
	if err != nil {
		return ErrorResult(err.Error())
	}

	return NewToolResult(string(content))
}

type WriteFileTool struct {
	fs fileSystem
}

func NewWriteFileTool(workspace string, restrict bool) *WriteFileTool {
	return &WriteFileTool{fs: newFileSystem(workspace, restrict)}
}

func (t *WriteFileTool) Name() string {
	return "write_file"
}

func (t *WriteFileTool) Description() string {
	return "Create or fully overwrite a file with new content. Use edit_file for targeted replacements and append_file for adding content to the end."
}

func (t *WriteFileTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type":        "string",
				"description": "Path to the file to write",
			},
			"content": map[string]interface{}{
				"type":        "string",
				"description": "Content to write to the file",
			},
		},
		"required": []string{"path", "content"},
	}
}

// Capabilities declares that WriteFileTool requires read-write filesystem access.
func (t *WriteFileTool) Capabilities() ToolCapabilities {
	return ToolCapabilities{
		Filesystem: []PathRule{{Pattern: "**", Mode: "rw"}},
	}
}

func (t *WriteFileTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	path, ok := args["path"].(string)
	if !ok {
		return ErrorResult("path is required")
	}

	content, ok := args["content"].(string)
	if !ok {
		return ErrorResult("content is required")
	}

	if len(content) > maxWriteBytes {
		return ErrorResult(fmt.Sprintf("content too large: %d bytes (max %d)", len(content), maxWriteBytes))
	}

	if err := defaultFileSystem(t.fs).WriteFile(path, []byte(content)); err != nil {
		return ErrorResult(err.Error())
	}

	preview := strings.TrimSpace(content)
	if len(preview) > 80 {
		preview = preview[:80] + "..."
	}
	if preview == "" {
		return SilentResult(fmt.Sprintf("File written: %s (empty content)", path))
	}
	return SilentResult(fmt.Sprintf("File written: %s (content preview: %q)", path, preview))
}

type ListDirTool struct {
	fs fileSystem
}

func NewListDirTool(workspace string, restrict bool) *ListDirTool {
	return &ListDirTool{fs: newFileSystem(workspace, restrict)}
}

func (t *ListDirTool) Name() string {
	return "list_dir"
}

func (t *ListDirTool) Description() string {
	return "List files and directories in a path"
}

func (t *ListDirTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type":        "string",
				"description": "Path to list",
			},
		},
		"required": []string{"path"},
	}
}

// Capabilities declares that ListDirTool requires read-only filesystem access.
func (t *ListDirTool) Capabilities() ToolCapabilities {
	return ToolCapabilities{
		Filesystem: []PathRule{{Pattern: "**", Mode: "r"}},
	}
}

func (t *ListDirTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	path, ok := args["path"].(string)
	if !ok {
		path = "."
	}

	entries, err := defaultFileSystem(t.fs).ReadDir(path)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to read directory: %v", err))
	}

	var result strings.Builder
	for _, entry := range entries {
		if entry.IsDir() {
			result.WriteString("DIR:  " + entry.Name() + "\n")
		} else {
			result.WriteString("FILE: " + entry.Name() + "\n")
		}
	}

	return NewToolResult(result.String())
}
