package tools

import (
	"context"
	"fmt"
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

func isWithinWorkspace(candidate, workspace string) bool {
	rel, err := filepath.Rel(filepath.Clean(workspace), filepath.Clean(candidate))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

type ReadFileTool struct {
	workspace string
	restrict  bool
}

func NewReadFileTool(workspace string, restrict bool) *ReadFileTool {
	return &ReadFileTool{workspace: workspace, restrict: restrict}
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

	resolvedPath, err := validatePath(path, t.workspace, t.restrict)
	if err != nil {
		return ErrorResult(err.Error())
	}

	content, err := os.ReadFile(resolvedPath)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to read file: %v", err))
	}

	return NewToolResult(string(content))
}

type WriteFileTool struct {
	workspace string
	restrict  bool
}

func NewWriteFileTool(workspace string, restrict bool) *WriteFileTool {
	return &WriteFileTool{workspace: workspace, restrict: restrict}
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

	resolvedPath, err := validatePath(path, t.workspace, t.restrict)
	if err != nil {
		return ErrorResult(err.Error())
	}

	dir := filepath.Dir(resolvedPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return ErrorResult(fmt.Sprintf("failed to create directory: %v", err))
	}

	if err := os.WriteFile(resolvedPath, []byte(content), 0644); err != nil {
		return ErrorResult(fmt.Sprintf("failed to write file: %v", err))
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
	workspace string
	restrict  bool
}

func NewListDirTool(workspace string, restrict bool) *ListDirTool {
	return &ListDirTool{workspace: workspace, restrict: restrict}
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

	resolvedPath, err := validatePath(path, t.workspace, t.restrict)
	if err != nil {
		return ErrorResult(err.Error())
	}

	entries, err := os.ReadDir(resolvedPath)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to read directory: %v", err))
	}

	result := ""
	for _, entry := range entries {
		if entry.IsDir() {
			result += "DIR:  " + entry.Name() + "\n"
		} else {
			result += "FILE: " + entry.Name() + "\n"
		}
	}

	return NewToolResult(result)
}
