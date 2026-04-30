package tools

import (
	"context"
	"fmt"
	"strings"
)

// EditFileTool edits a file by replacing old_text with new_text.
// The old_text must be non-empty and exist exactly once in the file.
type EditFileTool struct {
	fs fileSystem
}

// NewEditFileTool creates a new EditFileTool with optional directory restriction.
func NewEditFileTool(allowedDir string, restrict bool) *EditFileTool {
	return &EditFileTool{fs: newFileSystem(allowedDir, restrict)}
}

func (t *EditFileTool) Name() string {
	return "edit_file"
}

func (t *EditFileTool) Description() string {
	return "Edit a file by replacing old_text with new_text. The old_text must be non-empty and must exist exactly once in the file. Example: {\"path\":\"edit_target.txt\",\"old_text\":\"world\",\"new_text\":\"dragonscale\"}."
}

func (t *EditFileTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type":        "string",
				"description": "The file path to edit",
			},
			"old_text": map[string]interface{}{
				"type":        "string",
				"description": "The non-empty exact text to find and replace",
			},
			"new_text": map[string]interface{}{
				"type":        "string",
				"description": "The text to replace with",
			},
		},
		"required": []string{"path", "old_text", "new_text"},
	}
}

// Capabilities declares that EditFileTool requires read-write filesystem access.
func (t *EditFileTool) Capabilities() ToolCapabilities {
	return ToolCapabilities{
		Filesystem: []PathRule{{Pattern: "**", Mode: "rw"}},
	}
}

func (t *EditFileTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	path, ok := args["path"].(string)
	if !ok {
		return ErrorResult("path is required")
	}

	oldText, ok := args["old_text"].(string)
	if !ok {
		return ErrorResult("old_text is required")
	}

	newText, ok := args["new_text"].(string)
	if !ok {
		return ErrorResult("new_text is required")
	}

	if err := editFile(defaultFileSystem(t.fs), path, oldText, newText); err != nil {
		return ErrorResult(err.Error())
	}

	return SilentResult(fmt.Sprintf("File edited: %s", path))
}

func editFile(sysFs fileSystem, path, oldText, newText string) error {
	if oldText == "" {
		return fmt.Errorf("old_text must not be empty")
	}

	content, err := sysFs.ReadFile(path)
	if err != nil {
		return err
	}

	newContent, err := replaceEditContent(content, oldText, newText)
	if err != nil {
		return err
	}

	return sysFs.WriteFile(path, newContent)
}

func replaceEditContent(content []byte, oldText, newText string) ([]byte, error) {
	if oldText == "" {
		return nil, fmt.Errorf("old_text must not be empty")
	}

	contentStr := string(content)

	if !strings.Contains(contentStr, oldText) {
		return nil, fmt.Errorf("old_text not found in file. Make sure it matches exactly")
	}

	count := strings.Count(contentStr, oldText)
	if count > 1 {
		return nil, fmt.Errorf("old_text appears %d times. Please provide more context to make it unique", count)
	}

	newContent := strings.Replace(contentStr, oldText, newText, 1)
	return []byte(newContent), nil
}

type AppendFileTool struct {
	fs fileSystem
}

func NewAppendFileTool(workspace string, restrict bool) *AppendFileTool {
	return &AppendFileTool{fs: newFileSystem(workspace, restrict)}
}

func (t *AppendFileTool) Name() string {
	return "append_file"
}

func (t *AppendFileTool) Description() string {
	return "Append content to the end of a file. Example: {\"path\":\"append_test.txt\",\"content\":\"line two\\n\"}."
}

func (t *AppendFileTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type":        "string",
				"description": "The file path to append to",
			},
			"content": map[string]interface{}{
				"type":        "string",
				"description": "The content to append",
			},
		},
		"required": []string{"path", "content"},
	}
}

// Capabilities declares that AppendFileTool requires read-write filesystem access.
func (t *AppendFileTool) Capabilities() ToolCapabilities {
	return ToolCapabilities{
		Filesystem: []PathRule{{Pattern: "**", Mode: "rw"}},
	}
}

func (t *AppendFileTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	path, ok := args["path"].(string)
	if !ok {
		return ErrorResult("path is required")
	}

	content, ok := args["content"].(string)
	if !ok {
		return ErrorResult("content is required")
	}

	if err := appendFile(defaultFileSystem(t.fs), path, content); err != nil {
		return ErrorResult(err.Error())
	}

	return SilentResult(fmt.Sprintf("Appended to %s", path))
}

func appendFile(sysFs fileSystem, path, appendContent string) error {
	return sysFs.AppendFile(path, []byte(appendContent))
}
