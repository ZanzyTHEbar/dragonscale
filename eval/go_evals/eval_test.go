package go_evals

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sessions"), 0755)
	return dir
}

func TestToolRegistry_AllToolsHaveSchema(t *testing.T) {
	workspace := testWorkspace(t)

	cfg := config.DefaultConfig()
	cfg.Tools.ProgressiveDisclosure = false

	registry := tools.NewToolRegistry()
	registry.Register(tools.NewReadFileTool(workspace, false))
	registry.Register(tools.NewWriteFileTool(workspace, false))
	registry.Register(tools.NewListDirTool(workspace, false))
	registry.Register(tools.NewEditFileTool(workspace, false))
	registry.Register(tools.NewExecTool(workspace, false))

	allTools := registry.List()
	require.Greater(t, len(allTools), 0, "registry should have tools")

	for _, name := range allTools {
		tool, ok := registry.Get(name)
		require.True(t, ok, "tool %s should be gettable", name)

		schema := tools.ToolToSchema(tool)
		assert.NotNil(t, schema, "tool %s should have schema", name)

		fn, ok := schema["function"].(map[string]interface{})
		require.True(t, ok, "tool %s schema should have function key", name)
		assert.NotEmpty(t, fn["name"], "tool %s should have a name", name)
		assert.NotEmpty(t, fn["description"], "tool %s should have a description", name)
	}
}

func TestToolExecution_ReadFile_NonExistent(t *testing.T) {
	workspace := testWorkspace(t)

	readTool := tools.NewReadFileTool(workspace, true)
	result := readTool.Execute(context.Background(), map[string]interface{}{
		"path": "nonexistent_file_12345.txt",
	})

	require.NotNil(t, result)
	assert.True(t, result.IsError, "reading non-existent file should return error")
}

func TestToolExecution_WriteAndReadFile(t *testing.T) {
	workspace := testWorkspace(t)

	writeTool := tools.NewWriteFileTool(workspace, true)
	writeResult := writeTool.Execute(context.Background(), map[string]interface{}{
		"path":    "eval_test.txt",
		"content": "hello from eval test",
	})
	require.NotNil(t, writeResult)
	assert.False(t, writeResult.IsError, "write should succeed: %s", writeResult.ForLLM)

	readTool := tools.NewReadFileTool(workspace, true)
	readResult := readTool.Execute(context.Background(), map[string]interface{}{
		"path": "eval_test.txt",
	})
	require.NotNil(t, readResult)
	assert.False(t, readResult.IsError, "read should succeed")
	assert.Contains(t, readResult.ForLLM, "hello from eval test")
}

func TestToolExecution_ExecBlocking(t *testing.T) {
	workspace := testWorkspace(t)

	execTool := tools.NewExecTool(workspace, false)
	result := execTool.Execute(context.Background(), map[string]interface{}{
		"command": "echo picoclaw-eval-test",
	})

	require.NotNil(t, result)
	assert.False(t, result.IsError, "echo should succeed")
	assert.Contains(t, result.ForLLM, "picoclaw-eval-test")
}

func TestToolExecution_ListDir(t *testing.T) {
	workspace := testWorkspace(t)

	os.WriteFile(filepath.Join(workspace, "file_a.txt"), []byte("a"), 0644)
	os.WriteFile(filepath.Join(workspace, "file_b.txt"), []byte("b"), 0644)

	listTool := tools.NewListDirTool(workspace, true)
	result := listTool.Execute(context.Background(), map[string]interface{}{
		"path": ".",
	})

	require.NotNil(t, result)
	assert.False(t, result.IsError)
	assert.Contains(t, result.ForLLM, "file_a.txt")
	assert.Contains(t, result.ForLLM, "file_b.txt")
}

func TestToolRegistry_ProgressiveDisclosure(t *testing.T) {
	workspace := testWorkspace(t)

	registry := tools.NewToolRegistry()
	registry.Register(tools.NewReadFileTool(workspace, false))
	registry.Register(tools.NewWriteFileTool(workspace, false))
	registry.Register(tools.NewExecTool(workspace, false))

	registry.RegisterMetaTools()
	registry.SetProgressiveDisclosure(true)

	visible := registry.ListVisible()

	hasToolSearch := false
	hasToolCall := false
	for _, name := range visible {
		if name == "tool_search" {
			hasToolSearch = true
		}
		if name == "tool_call" {
			hasToolCall = true
		}
	}

	assert.True(t, hasToolSearch, "tool_search should be visible in progressive mode")
	assert.True(t, hasToolCall, "tool_call should be visible in progressive mode")
	assert.LessOrEqual(t, len(visible), 3, "progressive mode should hide most tools")
}
