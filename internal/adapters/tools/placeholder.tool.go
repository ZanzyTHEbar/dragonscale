package tools

import (
	"fmt"
	"log"
	"time"
)

// --- Example Placeholder Tool ---

// PlaceholderTool implements ExecutableTool for demonstration.
type PlaceholderTool struct {
	toolName string
	toolDesc string
}

// NewPlaceholderTool creates a new PlaceholderTool.
func NewPlaceholderTool(name, description string) *PlaceholderTool {
	return &PlaceholderTool{
		toolName: name,
		toolDesc: description,
	}
}

// Execute implements the ExecutableTool interface.
func (pt *PlaceholderTool) Execute(input interface{}) (interface{}, error) {
	// Simulate processing time
	time.Sleep(100 * time.Millisecond)

	// Simple echo behavior for demonstration
	log.Printf("PlaceholderTool '%s' executing with input: %v", pt.toolName, input)

	// Return slightly modified input
	return fmt.Sprintf("Processed by %s: %v", pt.toolName, input), nil
}

// Name returns the name of the tool.
func (pt *PlaceholderTool) Name() string {
	return pt.toolName
}

// Description returns the description of the tool.
func (pt *PlaceholderTool) Description() string {
	return pt.toolDesc
}
