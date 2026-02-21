package tools

import (
	"context"
	"sort"
	"testing"
)

func TestProgressiveDisclosure_OnlyGatewayVisible(t *testing.T) {
	t.Parallel()
	r := NewToolRegistry()
	r.Register(&stubTool{name: "read_file", desc: "Read"})
	r.Register(&stubTool{name: "write_file", desc: "Write"})
	r.Register(&stubTool{name: "web_search", desc: "Search"})
	r.RegisterMetaTools()

	visible := r.ListVisible()
	sort.Strings(visible)

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
	t.Parallel()
	r := NewToolRegistry()
	r.Register(&stubTool{name: "read_file", desc: "Read"})
	r.Register(&stubTool{name: "memory", desc: "Memory tool"})
	r.RegisterMetaTools()
	r.MarkGateway("memory")

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
	t.Parallel()
	r := NewToolRegistry()
	r.Register(&stubTool{name: "read_file", desc: "Read"})
	r.Register(&stubTool{name: "write_file", desc: "Write"})
	r.RegisterMetaTools()

	gatewayDefs := r.GetVisibleDefinitions()
	if len(gatewayDefs) != 2 {
		t.Errorf("expected 2 gateway definitions, got %d", len(gatewayDefs))
	}
}

func TestProgressiveDisclosure_AllToolsStillDispatchable(t *testing.T) {
	t.Parallel()
	r := NewToolRegistry()
	r.Register(&stubTool{name: "read_file", desc: "Read"})
	r.RegisterMetaTools()

	tool, ok := r.Get("read_file")
	if !ok {
		t.Fatal("read_file should still exist in registry")
	}
	if tool.Name() != "read_file" {
		t.Errorf("expected read_file, got %s", tool.Name())
	}

	tc, _ := r.Get("tool_call")
	result := tc.Execute(context.TODO(), map[string]interface{}{
		"tool_name": "read_file",
		"arguments": map[string]interface{}{},
	})
	if result.IsError {
		t.Errorf("tool_call should dispatch to hidden tools: %s", result.ForLLM)
	}
}

func TestProgressiveDisclosure_SearchFindsHiddenTools(t *testing.T) {
	t.Parallel()
	r := NewToolRegistry()
	r.Register(&stubTool{name: "read_file", desc: "Read a file from filesystem"})
	r.Register(&stubTool{name: "write_file", desc: "Write to a file"})
	r.RegisterMetaTools()

	ts, _ := r.Get("tool_search")
	result := ts.Execute(context.TODO(), map[string]interface{}{"query": "read"})

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}

	if result.ForLLM == "" {
		t.Fatal("expected non-empty result")
	}
}

func TestProgressiveDisclosure_MarkNonexistentGateway(t *testing.T) {
	t.Parallel()
	r := NewToolRegistry()
	r.RegisterMetaTools()
	r.MarkGateway("nonexistent")

	visible := r.ListVisible()
	for _, name := range visible {
		if name == "nonexistent" {
			t.Error("nonexistent tool should not appear in visible list")
		}
	}
}
