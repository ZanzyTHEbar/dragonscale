package go_evals

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ZanzyTHEbar/dragonscale/pkg/config"
	"github.com/ZanzyTHEbar/dragonscale/pkg/tools"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sessions"), 0755)
	return dir
}

func allFileTools(workspace string) []tools.Tool {
	return []tools.Tool{
		tools.NewReadFileTool(workspace, true),
		tools.NewWriteFileTool(workspace, true),
		tools.NewListDirTool(workspace, true),
		tools.NewEditFileTool(workspace, true),
		tools.NewAppendFileTool(workspace, true),
		tools.NewExecTool(workspace, false),
		tools.NewMessageTool(),
		tools.NewWebSearchTool(tools.WebSearchToolOptions{DuckDuckGoEnabled: true}),
		tools.NewWebFetchTool(50000),
	}
}

// ---------------------------------------------------------------------------
// Tool schema validation
// ---------------------------------------------------------------------------

func TestToolRegistry_AllToolsHaveSchema(t *testing.T) {
	t.Parallel()
	workspace := testWorkspace(t)

	registry := tools.NewToolRegistry()
	for _, tool := range allFileTools(workspace) {
		registry.Register(tool)
	}

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

		params, ok := fn["parameters"].(map[string]interface{})
		require.True(t, ok, "tool %s should have parameters map", name)
		assert.NotNil(t, params["type"], "tool %s parameters should have type", name)
	}
}

func TestToolRegistry_SchemaPropertiesAreTyped(t *testing.T) {
	t.Parallel()
	workspace := testWorkspace(t)

	for _, tool := range allFileTools(workspace) {
		t.Run(tool.Name(), func(t *testing.T) {
			params := tool.Parameters()
			props, ok := params["properties"].(map[string]interface{})
			if !ok {
				return
			}
			for propName, propVal := range props {
				propMap, ok := propVal.(map[string]interface{})
				require.True(t, ok, "property %s should be a map", propName)
				assert.NotEmpty(t, propMap["type"], "property %s should have a type", propName)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Tool execution: file operations
// ---------------------------------------------------------------------------

func TestToolExecution_ReadFile_NonExistent(t *testing.T) {
	t.Parallel()
	workspace := testWorkspace(t)

	readTool := tools.NewReadFileTool(workspace, true)
	result := readTool.Execute(t.Context(), map[string]interface{}{
		"path": "nonexistent_file_12345.txt",
	})

	require.NotNil(t, result)
	assert.True(t, result.IsError, "reading non-existent file should return error")
}

func TestToolExecution_WriteAndReadFile(t *testing.T) {
	t.Parallel()
	workspace := testWorkspace(t)

	writeTool := tools.NewWriteFileTool(workspace, true)
	writeResult := writeTool.Execute(t.Context(), map[string]interface{}{
		"path":    "eval_test.txt",
		"content": "hello from eval test",
	})
	require.NotNil(t, writeResult)
	assert.False(t, writeResult.IsError, "write should succeed: %s", writeResult.ForLLM)

	readTool := tools.NewReadFileTool(workspace, true)
	readResult := readTool.Execute(t.Context(), map[string]interface{}{
		"path": "eval_test.txt",
	})
	require.NotNil(t, readResult)
	assert.False(t, readResult.IsError, "read should succeed")
	assert.Contains(t, readResult.ForLLM, "hello from eval test")
}

func TestToolExecution_ExecBlocking(t *testing.T) {
	t.Parallel()
	workspace := testWorkspace(t)

	execTool := tools.NewExecTool(workspace, false)
	result := execTool.Execute(t.Context(), map[string]interface{}{
		"command": "echo dragonscale-eval-test",
	})

	require.NotNil(t, result)
	assert.False(t, result.IsError, "echo should succeed")
	assert.Contains(t, result.ForLLM, "dragonscale-eval-test")
}

func TestToolExecution_ListDir(t *testing.T) {
	t.Parallel()
	workspace := testWorkspace(t)

	os.WriteFile(filepath.Join(workspace, "file_a.txt"), []byte("a"), 0644)
	os.WriteFile(filepath.Join(workspace, "file_b.txt"), []byte("b"), 0644)

	listTool := tools.NewListDirTool(workspace, true)
	result := listTool.Execute(t.Context(), map[string]interface{}{
		"path": ".",
	})

	require.NotNil(t, result)
	assert.False(t, result.IsError)
	assert.Contains(t, result.ForLLM, "file_a.txt")
	assert.Contains(t, result.ForLLM, "file_b.txt")
}

func TestToolExecution_EditFile(t *testing.T) {
	t.Parallel()
	workspace := testWorkspace(t)

	writeTool := tools.NewWriteFileTool(workspace, true)
	writeTool.Execute(t.Context(), map[string]interface{}{
		"path":    "edit_target.txt",
		"content": "hello world foo bar",
	})

	editTool := tools.NewEditFileTool(workspace, true)
	result := editTool.Execute(t.Context(), map[string]interface{}{
		"path":     "edit_target.txt",
		"old_text": "world",
		"new_text": "dragonscale",
	})
	require.NotNil(t, result)
	assert.False(t, result.IsError, "edit should succeed: %s", result.ForLLM)

	readTool := tools.NewReadFileTool(workspace, true)
	readResult := readTool.Execute(t.Context(), map[string]interface{}{
		"path": "edit_target.txt",
	})
	assert.Contains(t, readResult.ForLLM, "dragonscale")
	assert.NotContains(t, readResult.ForLLM, "world")
}

func TestToolExecution_AppendFile(t *testing.T) {
	t.Parallel()
	workspace := testWorkspace(t)

	writeTool := tools.NewWriteFileTool(workspace, true)
	writeTool.Execute(t.Context(), map[string]interface{}{
		"path":    "append_target.txt",
		"content": "line one\n",
	})

	appendTool := tools.NewAppendFileTool(workspace, true)
	result := appendTool.Execute(t.Context(), map[string]interface{}{
		"path":    "append_target.txt",
		"content": "line two\n",
	})
	require.NotNil(t, result)
	assert.False(t, result.IsError, "append should succeed: %s", result.ForLLM)

	readTool := tools.NewReadFileTool(workspace, true)
	readResult := readTool.Execute(t.Context(), map[string]interface{}{
		"path": "append_target.txt",
	})
	assert.Contains(t, readResult.ForLLM, "line one")
	assert.Contains(t, readResult.ForLLM, "line two")
}

// ---------------------------------------------------------------------------
// Workspace restriction enforcement
// ---------------------------------------------------------------------------

func TestToolExecution_ReadFile_WorkspaceRestriction(t *testing.T) {
	t.Parallel()
	workspace := testWorkspace(t)

	readTool := tools.NewReadFileTool(workspace, true)
	result := readTool.Execute(t.Context(), map[string]interface{}{
		"path": "/etc/passwd",
	})
	require.NotNil(t, result)
	assert.True(t, result.IsError, "reading outside workspace should be rejected")
}

func TestToolExecution_WriteFile_WorkspaceRestriction(t *testing.T) {
	t.Parallel()
	workspace := testWorkspace(t)

	writeTool := tools.NewWriteFileTool(workspace, true)
	result := writeTool.Execute(t.Context(), map[string]interface{}{
		"path":    "/tmp/escape_test.txt",
		"content": "should not write",
	})
	require.NotNil(t, result)
	assert.True(t, result.IsError, "writing outside workspace should be rejected")
}

func TestToolExecution_ReadFile_PathTraversal(t *testing.T) {
	t.Parallel()
	workspace := testWorkspace(t)

	readTool := tools.NewReadFileTool(workspace, true)
	result := readTool.Execute(t.Context(), map[string]interface{}{
		"path": "../../../../etc/hostname",
	})
	require.NotNil(t, result)
	assert.True(t, result.IsError, "path traversal should be rejected")
}

func TestToolExecution_Unrestricted(t *testing.T) {
	t.Parallel()
	workspace := testWorkspace(t)

	tmpFile := filepath.Join(os.TempDir(), "dragonscale_unrestricted_test.txt")
	os.WriteFile(tmpFile, []byte("unrestricted content"), 0644)
	defer os.Remove(tmpFile)

	readTool := tools.NewReadFileTool(workspace, false)
	result := readTool.Execute(t.Context(), map[string]interface{}{
		"path": tmpFile,
	})
	require.NotNil(t, result)
	assert.False(t, result.IsError, "unrestricted mode should allow reading outside workspace")
	assert.Contains(t, result.ForLLM, "unrestricted content")
}

// ---------------------------------------------------------------------------
// Progressive disclosure
// ---------------------------------------------------------------------------

func TestToolRegistry_ProgressiveDisclosure(t *testing.T) {
	t.Parallel()
	workspace := testWorkspace(t)

	registry := tools.NewToolRegistry()
	registry.Register(tools.NewReadFileTool(workspace, false))
	registry.Register(tools.NewWriteFileTool(workspace, false))
	registry.Register(tools.NewExecTool(workspace, false))

	registry.RegisterMetaTools()

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

	assert.True(t, hasToolSearch, "tool_search should be visible")
	assert.True(t, hasToolCall, "tool_call should be visible")
	assert.LessOrEqual(t, len(visible), 3, "only gateway tools should be visible")
}

func TestToolSearch_FindsReadFile(t *testing.T) {
	t.Parallel()
	workspace := testWorkspace(t)

	registry := tools.NewToolRegistry()
	registry.Register(tools.NewReadFileTool(workspace, true))
	registry.Register(tools.NewWriteFileTool(workspace, true))
	registry.Register(tools.NewExecTool(workspace, false))
	registry.RegisterMetaTools()

	searchTool := tools.NewToolSearchTool(registry)
	result := searchTool.Execute(t.Context(), map[string]interface{}{
		"query": "read file",
	})

	require.NotNil(t, result)
	assert.False(t, result.IsError)
	assert.Contains(t, result.ForLLM, "read_file")
}

func TestToolSearch_ListsAll(t *testing.T) {
	t.Parallel()
	workspace := testWorkspace(t)

	registry := tools.NewToolRegistry()
	registry.Register(tools.NewReadFileTool(workspace, true))
	registry.Register(tools.NewWriteFileTool(workspace, true))
	registry.RegisterMetaTools()

	searchTool := tools.NewToolSearchTool(registry)
	result := searchTool.Execute(t.Context(), map[string]interface{}{
		"query": "",
	})

	require.NotNil(t, result)
	assert.Contains(t, result.ForLLM, "read_file")
	assert.Contains(t, result.ForLLM, "write_file")
}

func TestToolSearch_NoResults(t *testing.T) {
	t.Parallel()
	workspace := testWorkspace(t)

	registry := tools.NewToolRegistry()
	registry.Register(tools.NewReadFileTool(workspace, true))
	registry.RegisterMetaTools()

	searchTool := tools.NewToolSearchTool(registry)
	result := searchTool.Execute(t.Context(), map[string]interface{}{
		"query": "xyzzy_nonexistent_tool",
	})

	require.NotNil(t, result)
	assert.Contains(t, result.ForLLM, "No tools")
}

func TestToolCall_DispatchesCorrectly(t *testing.T) {
	t.Parallel()
	workspace := testWorkspace(t)

	os.WriteFile(filepath.Join(workspace, "dispatch_test.txt"), []byte("dispatch ok"), 0644)

	registry := tools.NewToolRegistry()
	registry.Register(tools.NewReadFileTool(workspace, true))
	registry.RegisterMetaTools()

	callTool := tools.NewToolCallTool(registry)
	result := callTool.Execute(t.Context(), map[string]interface{}{
		"tool_name": "read_file",
		"arguments": map[string]interface{}{
			"path": "dispatch_test.txt",
		},
	})

	require.NotNil(t, result)
	assert.False(t, result.IsError, "tool_call dispatch should succeed: %s", result.ForLLM)
	assert.Contains(t, result.ForLLM, "dispatch ok")
}

func TestToolCall_RejectsRecursion(t *testing.T) {
	t.Parallel()
	workspace := testWorkspace(t)

	registry := tools.NewToolRegistry()
	registry.Register(tools.NewReadFileTool(workspace, true))
	registry.RegisterMetaTools()

	callTool := tools.NewToolCallTool(registry)
	result := callTool.Execute(t.Context(), map[string]interface{}{
		"tool_name": "tool_call",
	})

	require.NotNil(t, result)
	assert.True(t, result.IsError, "recursive tool_call should be rejected")
}

func TestToolCall_RejectsUnknownTool(t *testing.T) {
	t.Parallel()
	registry := tools.NewToolRegistry()
	registry.RegisterMetaTools()

	callTool := tools.NewToolCallTool(registry)
	result := callTool.Execute(t.Context(), map[string]interface{}{
		"tool_name": "nonexistent_tool_xyz",
	})

	require.NotNil(t, result)
	assert.True(t, result.IsError, "unknown tool should be rejected")
}

func TestToolCall_MissingToolName(t *testing.T) {
	t.Parallel()
	registry := tools.NewToolRegistry()
	registry.RegisterMetaTools()

	callTool := tools.NewToolCallTool(registry)
	result := callTool.Execute(t.Context(), map[string]interface{}{})

	require.NotNil(t, result)
	assert.True(t, result.IsError, "missing tool_name should be rejected")
}

func TestToolCall_StringArguments(t *testing.T) {
	t.Parallel()
	workspace := testWorkspace(t)

	os.WriteFile(filepath.Join(workspace, "str_args.txt"), []byte("string args ok"), 0644)

	registry := tools.NewToolRegistry()
	registry.Register(tools.NewReadFileTool(workspace, true))
	registry.RegisterMetaTools()

	callTool := tools.NewToolCallTool(registry)
	result := callTool.Execute(t.Context(), map[string]interface{}{
		"tool_name": "read_file",
		"arguments": `{"path": "str_args.txt"}`,
	})

	require.NotNil(t, result)
	assert.False(t, result.IsError, "string arguments should be parsed: %s", result.ForLLM)
	assert.Contains(t, result.ForLLM, "string args ok")
}

// ---------------------------------------------------------------------------
// Gateway tool marking
// ---------------------------------------------------------------------------

func TestToolRegistry_GatewayMarking(t *testing.T) {
	t.Parallel()
	workspace := testWorkspace(t)

	registry := tools.NewToolRegistry()
	registry.Register(tools.NewReadFileTool(workspace, true))
	registry.Register(tools.NewWriteFileTool(workspace, true))
	registry.Register(tools.NewMessageTool())
	registry.RegisterMetaTools()

	registry.MarkGateway("message")

	visible := registry.ListVisible()

	visibleSet := make(map[string]bool)
	for _, name := range visible {
		visibleSet[name] = true
	}

	assert.True(t, visibleSet["tool_search"], "tool_search should be visible")
	assert.True(t, visibleSet["tool_call"], "tool_call should be visible")
	assert.True(t, visibleSet["message"], "marked gateway tool should be visible")
	assert.False(t, visibleSet["read_file"], "non-gateway tool should be hidden")
	assert.False(t, visibleSet["write_file"], "non-gateway tool should be hidden")
}

// ---------------------------------------------------------------------------
// Config loading
// ---------------------------------------------------------------------------

func TestConfig_DefaultValues(t *testing.T) {
	t.Parallel()
	cfg := config.DefaultConfig()

	assert.True(t, cfg.Agents.Defaults.RestrictToSandbox, "restrict to sandbox should be on by default")
	assert.Empty(t, cmp.Diff(20, cfg.Agents.Defaults.MaxToolIterations), "max tool iterations default")
	assert.Empty(t, cmp.Diff(0.7, cfg.Agents.Defaults.Temperature), "temperature default")
	assert.Empty(t, cmp.Diff(8192, cfg.Agents.Defaults.MaxTokens), "max tokens default")
	assert.Empty(t, cmp.Diff(768, cfg.Memory.EmbeddingDims), "embedding dims default")
	assert.Empty(t, cmp.Diff(4000, cfg.Memory.OffloadThresholdTokens), "offload threshold default")
}

func TestConfig_LoadEvalConfigs(t *testing.T) {
	t.Parallel()
	evalDir := filepath.Join("..", "..", "eval", "configs")

	tests := []struct {
		name             string
		file             string
		expectIterations int
	}{
		{"default", "default.json", 20},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(evalDir, tc.file)
			cfg, err := config.LoadConfig(path)
			require.NoError(t, err, "config should load without error")

			assert.True(t, cfg.Agents.Defaults.RestrictToSandbox, "restrict_to_sandbox should always be true for eval")
			assert.Empty(t, cmp.Diff(tc.expectIterations, cfg.Agents.Defaults.MaxToolIterations), "max_tool_iterations")
		})
	}
}

func TestConfig_MissingFileReturnsDefaults(t *testing.T) {
	t.Parallel()
	cfg, err := config.LoadConfig("/nonexistent/path/config.json")
	require.NoError(t, err, "missing config should return defaults, not error")
	assert.Empty(t, cmp.Diff(768, cfg.Memory.EmbeddingDims), "should have default embedding dims")
}

// ---------------------------------------------------------------------------
// Exec tool boundary conditions
// ---------------------------------------------------------------------------

func TestToolExecution_ExecTimeout(t *testing.T) {
	t.Parallel()
	workspace := testWorkspace(t)

	execTool := tools.NewExecTool(workspace, false)
	ctx, cancel := context.WithTimeout(t.Context(), 1)
	defer cancel()

	result := execTool.Execute(ctx, map[string]interface{}{
		"command": "sleep 30",
	})

	require.NotNil(t, result)
}

func TestToolExecution_ExecEmptyCommand(t *testing.T) {
	t.Parallel()
	workspace := testWorkspace(t)

	execTool := tools.NewExecTool(workspace, false)
	result := execTool.Execute(t.Context(), map[string]interface{}{
		"command": "",
	})

	require.NotNil(t, result)
	assert.True(t, result.IsError, "empty command should be an error")
}

// ---------------------------------------------------------------------------
// Tool definition JSON roundtrip
// ---------------------------------------------------------------------------

func TestToolSchema_JSONRoundtrip(t *testing.T) {
	t.Parallel()
	workspace := testWorkspace(t)

	for _, tool := range allFileTools(workspace) {
		t.Run(tool.Name(), func(t *testing.T) {
			schema := tools.ToolToSchema(tool)
			data, err := json.Marshal(schema)
			require.NoError(t, err, "schema should marshal to JSON")

			var parsed map[string]interface{}
			err = json.Unmarshal(data, &parsed)
			require.NoError(t, err, "schema JSON should parse back")

			fn := parsed["function"].(map[string]interface{})
			assert.Empty(t, cmp.Diff(tool.Name(), fn["name"]))
		})
	}
}
