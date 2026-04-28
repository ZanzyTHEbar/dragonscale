package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEditTool_EditFile_Success verifies successful file editing
func TestEditTool_EditFile_Success(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(testFile, []byte("Hello World\nThis is a test"), 0644)

	tool := NewEditFileTool(tmpDir, true)
	ctx := t.Context()
	args := map[string]interface{}{
		"path":     testFile,
		"old_text": "World",
		"new_text": "Universe",
	}

	result := tool.Execute(ctx, args)

	// Success should not be an error
	if result.IsError {
		t.Errorf("Expected success, got IsError=true: %s", result.ForLLM)
	}

	// Should return SilentResult
	if !result.Silent {
		t.Errorf("Expected Silent=true for EditFile, got false")
	}

	// ForUser should be empty (silent result)
	if result.ForUser != "" {
		t.Errorf("Expected ForUser to be empty for SilentResult, got: %s", result.ForUser)
	}

	// Verify file was actually edited
	content, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("Failed to read edited file: %v", err)
	}
	contentStr := string(content)
	if !strings.Contains(contentStr, "Hello Universe") {
		t.Errorf("Expected file to contain 'Hello Universe', got: %s", contentStr)
	}
	if strings.Contains(contentStr, "Hello World") {
		t.Errorf("Expected 'Hello World' to be replaced, got: %s", contentStr)
	}
}

// TestEditTool_EditFile_NotFound verifies error handling for non-existent file
func TestEditTool_EditFile_NotFound(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "nonexistent.txt")

	tool := NewEditFileTool(tmpDir, true)
	ctx := t.Context()
	args := map[string]interface{}{
		"path":     testFile,
		"old_text": "old",
		"new_text": "new",
	}

	result := tool.Execute(ctx, args)

	// Should return error result
	if !result.IsError {
		t.Errorf("Expected error for non-existent file")
	}

	// Should mention file not found
	if !strings.Contains(result.ForLLM, "not found") && !strings.Contains(result.ForUser, "not found") {
		t.Errorf("Expected 'file not found' message, got ForLLM: %s", result.ForLLM)
	}
}

// TestEditTool_EditFile_OldTextNotFound verifies error when old_text doesn't exist
func TestEditTool_EditFile_OldTextNotFound(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(testFile, []byte("Hello World"), 0644)

	tool := NewEditFileTool(tmpDir, true)
	ctx := t.Context()
	args := map[string]interface{}{
		"path":     testFile,
		"old_text": "Goodbye",
		"new_text": "Hello",
	}

	result := tool.Execute(ctx, args)

	// Should return error result
	if !result.IsError {
		t.Errorf("Expected error when old_text not found")
	}

	// Should mention old_text not found
	if !strings.Contains(result.ForLLM, "not found") && !strings.Contains(result.ForUser, "not found") {
		t.Errorf("Expected 'not found' message, got ForLLM: %s", result.ForLLM)
	}
}

// TestEditTool_EditFile_MultipleMatches verifies error when old_text appears multiple times
func TestEditTool_EditFile_MultipleMatches(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(testFile, []byte("test test test"), 0644)

	tool := NewEditFileTool(tmpDir, true)
	ctx := t.Context()
	args := map[string]interface{}{
		"path":     testFile,
		"old_text": "test",
		"new_text": "done",
	}

	result := tool.Execute(ctx, args)

	// Should return error result
	if !result.IsError {
		t.Errorf("Expected error when old_text appears multiple times")
	}

	// Should mention multiple occurrences
	if !strings.Contains(result.ForLLM, "times") && !strings.Contains(result.ForUser, "times") {
		t.Errorf("Expected 'multiple times' message, got ForLLM: %s", result.ForLLM)
	}
}

// TestEditTool_EditFile_OutsideAllowedDir verifies error when path is outside allowed directory
func TestEditTool_EditFile_OutsideAllowedDir(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	otherDir := t.TempDir()
	testFile := filepath.Join(otherDir, "test.txt")
	os.WriteFile(testFile, []byte("content"), 0644)

	tool := NewEditFileTool(tmpDir, true) // Restrict to tmpDir
	ctx := t.Context()
	args := map[string]interface{}{
		"path":     testFile,
		"old_text": "content",
		"new_text": "new",
	}

	result := tool.Execute(ctx, args)

	// Should return error result
	if !result.IsError {
		t.Errorf("Expected error when path is outside allowed directory")
	}

	// Should mention outside allowed directory
	if !strings.Contains(result.ForLLM, "outside") && !strings.Contains(result.ForUser, "outside") {
		t.Errorf("Expected 'outside allowed' message, got ForLLM: %s", result.ForLLM)
	}
}

func TestEditTool_EditFile_SymlinkInsideWorkspace(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	target := filepath.Join(tmpDir, "target.txt")
	link := filepath.Join(tmpDir, "link.txt")
	if err := os.WriteFile(target, []byte("Hello World"), 0644); err != nil {
		t.Fatalf("failed to write target file: %v", err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink not supported in this environment: %v", err)
	}

	tool := NewEditFileTool(tmpDir, true)
	result := tool.Execute(t.Context(), map[string]interface{}{
		"path":     link,
		"old_text": "World",
		"new_text": "DragonScale",
	})

	if result.IsError {
		t.Fatalf("expected symlink edit to succeed, got: %s", result.ForLLM)
	}

	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("failed to read target file: %v", err)
	}
	if string(content) != "Hello DragonScale" {
		t.Fatalf("expected target content to be updated, got: %s", string(content))
	}

	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("failed to stat symlink: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("expected link path to remain a symlink")
	}
}

// TestEditTool_EditFile_MissingPath verifies error handling for missing path
func TestEditTool_EditFile_MissingPath(t *testing.T) {
	t.Parallel()
	tool := NewEditFileTool("", false)
	ctx := t.Context()
	args := map[string]interface{}{
		"old_text": "old",
		"new_text": "new",
	}

	result := tool.Execute(ctx, args)

	// Should return error result
	if !result.IsError {
		t.Errorf("Expected error when path is missing")
	}
}

// TestEditTool_EditFile_MissingOldText verifies error handling for missing old_text
func TestEditTool_EditFile_MissingOldText(t *testing.T) {
	t.Parallel()
	tool := NewEditFileTool("", false)
	ctx := t.Context()
	args := map[string]interface{}{
		"path":     "/tmp/test.txt",
		"new_text": "new",
	}

	result := tool.Execute(ctx, args)

	// Should return error result
	if !result.IsError {
		t.Errorf("Expected error when old_text is missing")
	}
}

// TestEditTool_EditFile_MissingNewText verifies error handling for missing new_text
func TestEditTool_EditFile_MissingNewText(t *testing.T) {
	t.Parallel()
	tool := NewEditFileTool("", false)
	ctx := t.Context()
	args := map[string]interface{}{
		"path":     "/tmp/test.txt",
		"old_text": "old",
	}

	result := tool.Execute(ctx, args)

	// Should return error result
	if !result.IsError {
		t.Errorf("Expected error when new_text is missing")
	}
}

// TestEditTool_AppendFile_Success verifies successful file appending
func TestEditTool_AppendFile_Success(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(testFile, []byte("Initial content"), 0644)

	tool := NewAppendFileTool("", false)
	ctx := t.Context()
	args := map[string]interface{}{
		"path":    testFile,
		"content": "\nAppended content",
	}

	result := tool.Execute(ctx, args)

	// Success should not be an error
	if result.IsError {
		t.Errorf("Expected success, got IsError=true: %s", result.ForLLM)
	}

	// Should return SilentResult
	if !result.Silent {
		t.Errorf("Expected Silent=true for AppendFile, got false")
	}

	// ForUser should be empty (silent result)
	if result.ForUser != "" {
		t.Errorf("Expected ForUser to be empty for SilentResult, got: %s", result.ForUser)
	}

	// Verify content was actually appended
	content, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}
	contentStr := string(content)
	if !strings.Contains(contentStr, "Initial content") {
		t.Errorf("Expected original content to remain, got: %s", contentStr)
	}
	if !strings.Contains(contentStr, "Appended content") {
		t.Errorf("Expected appended content, got: %s", contentStr)
	}
}

func TestEditTool_AppendFile_SymlinkInsideWorkspace(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	target := filepath.Join(tmpDir, "target.txt")
	link := filepath.Join(tmpDir, "link.txt")
	if err := os.WriteFile(target, []byte("Initial content"), 0644); err != nil {
		t.Fatalf("failed to write target file: %v", err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink not supported in this environment: %v", err)
	}

	tool := NewAppendFileTool(tmpDir, true)
	result := tool.Execute(t.Context(), map[string]interface{}{
		"path":    link,
		"content": "\nAppended content",
	})

	if result.IsError {
		t.Fatalf("expected symlink append to succeed, got: %s", result.ForLLM)
	}

	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("failed to read target file: %v", err)
	}
	if string(content) != "Initial content\nAppended content" {
		t.Fatalf("expected target content to be appended, got: %s", string(content))
	}

	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("failed to stat symlink: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("expected link path to remain a symlink")
	}
}

// TestEditTool_AppendFile_MissingPath verifies error handling for missing path
func TestEditTool_AppendFile_MissingPath(t *testing.T) {
	t.Parallel()
	tool := NewAppendFileTool("", false)
	ctx := t.Context()
	args := map[string]interface{}{
		"content": "test",
	}

	result := tool.Execute(ctx, args)

	// Should return error result
	if !result.IsError {
		t.Errorf("Expected error when path is missing")
	}
}

// TestEditTool_AppendFile_MissingContent verifies error handling for missing content
func TestEditTool_AppendFile_MissingContent(t *testing.T) {
	t.Parallel()
	tool := NewAppendFileTool("", false)
	ctx := t.Context()
	args := map[string]interface{}{
		"path": "/tmp/test.txt",
	}

	result := tool.Execute(ctx, args)

	// Should return error result
	if !result.IsError {
		t.Errorf("Expected error when content is missing")
	}
}
