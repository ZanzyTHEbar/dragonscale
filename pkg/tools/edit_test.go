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

func TestEditTool_EditFile_EmptyOldTextRejected(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("Hello World"), 0o644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	tool := NewEditFileTool(tmpDir, true)
	result := tool.Execute(t.Context(), map[string]interface{}{
		"path":     testFile,
		"old_text": "",
		"new_text": "ignored",
	})

	if !result.IsError {
		t.Fatal("expected error when old_text is empty")
	}
	if !strings.Contains(result.ForLLM, "must not be empty") {
		t.Fatalf("expected explicit empty old_text error, got %q", result.ForLLM)
	}

	content, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}
	if string(content) != "Hello World" {
		t.Fatalf("expected file to remain unchanged, got %q", string(content))
	}
}

func TestEditTool_EditFile_EmptyOldTextDoesNotWriteEmptyFile(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "empty.txt")
	if err := os.WriteFile(testFile, []byte(""), 0o644); err != nil {
		t.Fatalf("failed to write empty file: %v", err)
	}

	tool := NewEditFileTool(tmpDir, true)
	result := tool.Execute(t.Context(), map[string]interface{}{
		"path":     testFile,
		"old_text": "",
		"new_text": "unexpected",
	})

	if !result.IsError {
		t.Fatal("expected error when old_text is empty for empty file")
	}
	if !strings.Contains(result.ForLLM, "must not be empty") {
		t.Fatalf("expected explicit empty old_text error, got %q", result.ForLLM)
	}

	content, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}
	if string(content) != "" {
		t.Fatalf("expected empty file to remain unchanged, got %q", string(content))
	}
}

func TestEditTool_EditFile_EmptyOldTextPreemptsMissingFile(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "missing.txt")

	tool := NewEditFileTool(tmpDir, true)
	result := tool.Execute(t.Context(), map[string]interface{}{
		"path":     testFile,
		"old_text": "",
		"new_text": "ignored",
	})

	if !result.IsError {
		t.Fatal("expected error when old_text is empty")
	}
	if !strings.Contains(result.ForLLM, "must not be empty") {
		t.Fatalf("expected empty old_text error to win, got %q", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "not found") {
		t.Fatalf("expected empty old_text error before filesystem lookup, got %q", result.ForLLM)
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

func TestEditTool_EditFile_RejectsSymlinkEscape(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0755); err != nil {
		t.Fatalf("failed to create workspace: %v", err)
	}

	secret := filepath.Join(root, "secret.txt")
	if err := os.WriteFile(secret, []byte("top secret"), 0644); err != nil {
		t.Fatalf("failed to write secret file: %v", err)
	}

	link := filepath.Join(workspace, "leak.txt")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("symlink not supported in this environment: %v", err)
	}

	tool := NewEditFileTool(workspace, true)
	result := tool.Execute(t.Context(), map[string]interface{}{
		"path":     link,
		"old_text": "top",
		"new_text": "public",
	})

	if !result.IsError {
		t.Fatal("expected symlink escape edit to be blocked")
	}
	if !strings.Contains(result.ForLLM, "symlink resolves outside workspace") {
		t.Fatalf("expected symlink escape error, got: %s", result.ForLLM)
	}

	content, err := os.ReadFile(secret)
	if err != nil {
		t.Fatalf("failed to read secret file: %v", err)
	}
	if string(content) != "top secret" {
		t.Fatalf("expected target content to remain unchanged, got: %s", string(content))
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

func TestEditTool_AppendFile_RejectsSymlinkEscape(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0755); err != nil {
		t.Fatalf("failed to create workspace: %v", err)
	}

	secret := filepath.Join(root, "secret.txt")
	if err := os.WriteFile(secret, []byte("top secret"), 0644); err != nil {
		t.Fatalf("failed to write secret file: %v", err)
	}

	link := filepath.Join(workspace, "leak.txt")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("symlink not supported in this environment: %v", err)
	}

	tool := NewAppendFileTool(workspace, true)
	result := tool.Execute(t.Context(), map[string]interface{}{
		"path":    link,
		"content": "\npublic",
	})

	if !result.IsError {
		t.Fatal("expected symlink escape append to be blocked")
	}
	if !strings.Contains(result.ForLLM, "symlink resolves outside workspace") {
		t.Fatalf("expected symlink escape error, got: %s", result.ForLLM)
	}

	content, err := os.ReadFile(secret)
	if err != nil {
		t.Fatalf("failed to read secret file: %v", err)
	}
	if string(content) != "top secret" {
		t.Fatalf("expected target content to remain unchanged, got: %s", string(content))
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
