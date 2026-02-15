package tools

import (
	"sort"
	"testing"
)

func TestProgressiveDisclosure_Disabled_AllToolsVisible(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&stubTool{name: "read_file", desc: "Read"})
	r.Register(&stubTool{name: "write_file", desc: "Write"})
	r.RegisterMetaTools()

	visible := r.ListVisible()
	// Should include all 4: read_file, write_file, tool_search, tool_call
	if len(visible) != 4 {
		t.Errorf("expected 4 visible tools, got %d: %v", len(visible), visible)
	}
}

func TestProgressiveDisclosure_Enabled_OnlyGateway(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&stubTool{name: "read_file", desc: "Read"})
	r.Register(&stubTool{name: "write_file", desc: "Write"})
	r.Register(&stubTool{name: "web_search", desc: "Search"})
	r.RegisterMetaTools()
	r.SetProgressiveDisclosure(true)

	visible := r.ListVisible()
	sort.Strings(visible)

	// Only gateway tools
	expected := []string{"tool_call", "tool_search"}
	if len(visible) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, visible)
	}
	for i, name := range expected {
		if visible[i] != name {
			t.Errorf("expected %s at index %d, got %s", name, i, visible[i])
		}
	}
}

func TestProgressiveDisclosure_MarkGateway(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&stubTool{name: "read_file", desc: "Read"})
	r.Register(&stubTool{name: "memory", desc: "Memory tool"})
	r.RegisterMetaTools()
	r.MarkGateway("memory")
	r.SetProgressiveDisclosure(true)

	visible := r.ListVisible()
	sort.Strings(visible)

	expected := []string{"memory", "tool_call", "tool_search"}
	if len(visible) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, visible)
	}
	for i, name := range expected {
		if visible[i] != name {
			t.Errorf("expected %s at index %d, got %s", name, i, visible[i])
		}
	}
}

func TestProgressiveDisclosure_GetVisibleDefinitions(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&stubTool{name: "read_file", desc: "Read"})
	r.Register(&stubTool{name: "write_file", desc: "Write"})
	r.RegisterMetaTools()

	// Full mode
	allDefs := r.GetVisibleDefinitions()
	if len(allDefs) != 4 {
		t.Errorf("full mode: expected 4 definitions, got %d", len(allDefs))
	}

	// Progressive mode
	r.SetProgressiveDisclosure(true)
	gatewayDefs := r.GetVisibleDefinitions()
	if len(gatewayDefs) != 2 {
		t.Errorf("progressive mode: expected 2 definitions, got %d", len(gatewayDefs))
	}
}

func TestProgressiveDisclosure_AllToolsStillDispatchable(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&stubTool{name: "read_file", desc: "Read"})
	r.RegisterMetaTools()
	r.SetProgressiveDisclosure(true)

	// read_file is hidden from LLM but still in registry
	tool, ok := r.Get("read_file")
	if !ok {
		t.Fatal("read_file should still exist in registry")
	}
	if tool.Name() != "read_file" {
		t.Errorf("expected read_file, got %s", tool.Name())
	}

	// tool_call should still dispatch to it
	tc, _ := r.Get("tool_call")
	result := tc.Execute(nil, map[string]interface{}{
		"tool_name": "read_file",
		"arguments": map[string]interface{}{},
	})
	if result.IsError {
		t.Errorf("tool_call should dispatch to hidden tools: %s", result.ForLLM)
	}
}

func TestProgressiveDisclosure_SearchFindsHiddenTools(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&stubTool{name: "read_file", desc: "Read a file from filesystem"})
	r.Register(&stubTool{name: "write_file", desc: "Write to a file"})
	r.RegisterMetaTools()
	r.SetProgressiveDisclosure(true)

	// Even though read_file is hidden from Fantasy, tool_search should find it
	ts, _ := r.Get("tool_search")
	result := ts.Execute(nil, map[string]interface{}{"query": "read"})

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}

	// Result should contain read_file
	if result.ForLLM == "" {
		t.Fatal("expected non-empty result")
	}
}

func TestProgressiveDisclosure_ToggleRuntime(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&stubTool{name: "alpha", desc: "A tool"})
	r.RegisterMetaTools()

	// Start in full mode
	if len(r.ListVisible()) != 3 {
		t.Fatal("expected 3 visible in full mode")
	}

	// Switch to progressive
	r.SetProgressiveDisclosure(true)
	if len(r.ListVisible()) != 2 {
		t.Errorf("expected 2 visible in progressive mode, got %d", len(r.ListVisible()))
	}

	// Switch back
	r.SetProgressiveDisclosure(false)
	if len(r.ListVisible()) != 3 {
		t.Errorf("expected 3 visible in full mode again, got %d", len(r.ListVisible()))
	}
}

func TestProgressiveDisclosure_MarkNonexistentGateway(t *testing.T) {
	r := NewToolRegistry()
	r.RegisterMetaTools()
	r.MarkGateway("nonexistent")
	r.SetProgressiveDisclosure(true)

	visible := r.ListVisible()
	for _, name := range visible {
		if name == "nonexistent" {
			t.Error("nonexistent tool should not appear in visible list")
		}
	}
}
