package store

import (
	"context"
	jsonv2 "github.com/go-json-experiment/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestMemoryTool(t *testing.T) *MemoryTool {
	t.Helper()
	store := newTestStore(t, false)
	return NewMemoryTool(store, "agent-1", "session-1")
}

func executeAndParse(t *testing.T, tool *MemoryTool, input string) *MemoryToolResponse {
	t.Helper()
	ctx := context.Background()
	raw, err := tool.Execute(ctx, input)
	require.NoError(t, err)

	var resp MemoryToolResponse
	require.NoError(t, jsonv2.Unmarshal([]byte(raw), &resp))
	return &resp
}

func TestMemoryTool_WriteAndRead(t *testing.T) {
	tool := newTestMemoryTool(t)

	// Write
	resp := executeAndParse(t, tool, `{"action":"write","content":"Go interfaces are implicitly implemented.","sector":"semantic","tags":"golang"}`)
	assert.True(t, resp.Success)
	assert.Contains(t, resp.Message, "recall tier")

	// Extract ID from message
	// Message format: "Stored in recall tier with ID: <uuid>"
	var id string
	for _, part := range []string{resp.Message} {
		if idx := len("Stored in recall tier with ID: "); len(part) > idx {
			id = part[idx:]
		}
	}
	require.NotEmpty(t, id)

	// Read
	readResp := executeAndParse(t, tool, `{"action":"read","id":"`+id+`"}`)
	assert.True(t, readResp.Success)
	require.Len(t, readResp.Results, 1)
	assert.Contains(t, readResp.Results[0].Content, "interfaces")
}

func TestMemoryTool_WriteArchival(t *testing.T) {
	tool := newTestMemoryTool(t)

	resp := executeAndParse(t, tool, `{"action":"write","content":"Large document content for archival.","tier":"archival","source":"test"}`)
	assert.True(t, resp.Success)
	assert.Contains(t, resp.Message, "archival tier")
}

func TestMemoryTool_Search(t *testing.T) {
	tool := newTestMemoryTool(t)

	// Seed data
	executeAndParse(t, tool, `{"action":"write","content":"Go channels enable concurrent communication between goroutines.","sector":"semantic"}`)
	executeAndParse(t, tool, `{"action":"write","content":"Python uses asyncio for asynchronous programming.","sector":"semantic"}`)

	// Search
	resp := executeAndParse(t, tool, `{"action":"search","query":"goroutines","limit":5}`)
	assert.True(t, resp.Success)
	assert.NotEmpty(t, resp.Results)
	assert.Contains(t, resp.Results[0].Content, "goroutines")
}

func TestMemoryTool_Update(t *testing.T) {
	tool := newTestMemoryTool(t)

	// Write
	writeResp := executeAndParse(t, tool, `{"action":"write","content":"Initial content."}`)
	id := writeResp.Message[len("Stored in recall tier with ID: "):]

	// Update
	updateResp := executeAndParse(t, tool, `{"action":"update","id":"`+id+`","content":"Updated content with more detail."}`)
	assert.True(t, updateResp.Success)

	// Verify
	readResp := executeAndParse(t, tool, `{"action":"read","id":"`+id+`"}`)
	require.Len(t, readResp.Results, 1)
	assert.Contains(t, readResp.Results[0].Content, "Updated content")
}

func TestMemoryTool_Delete(t *testing.T) {
	tool := newTestMemoryTool(t)

	// Write
	writeResp := executeAndParse(t, tool, `{"action":"write","content":"Content to delete."}`)
	id := writeResp.Message[len("Stored in recall tier with ID: "):]

	// Delete
	delResp := executeAndParse(t, tool, `{"action":"delete","id":"`+id+`"}`)
	assert.True(t, delResp.Success)

	// Verify deleted
	readResp := executeAndParse(t, tool, `{"action":"read","id":"`+id+`"}`)
	assert.False(t, readResp.Success)
	assert.Contains(t, readResp.Message, "not found")
}

func TestMemoryTool_Status(t *testing.T) {
	tool := newTestMemoryTool(t)

	resp := executeAndParse(t, tool, `{"action":"status"}`)
	assert.True(t, resp.Success)
	require.NotNil(t, resp.Status)
	assert.Equal(t, "normal", resp.Status.PressureLevel)
	assert.Equal(t, 0, resp.Status.RecallItemCount)
}

func TestMemoryTool_InvalidAction(t *testing.T) {
	tool := newTestMemoryTool(t)

	resp := executeAndParse(t, tool, `{"action":"explode"}`)
	assert.False(t, resp.Success)
	assert.Contains(t, resp.Message, "unknown action")
}

func TestMemoryTool_InvalidJSON(t *testing.T) {
	tool := newTestMemoryTool(t)

	resp := executeAndParse(t, tool, `not json`)
	assert.False(t, resp.Success)
	assert.Contains(t, resp.Message, "invalid input")
}

func TestMemoryTool_MissingRequiredFields(t *testing.T) {
	tool := newTestMemoryTool(t)

	tests := []struct {
		name  string
		input string
		msg   string
	}{
		{"search no query", `{"action":"search"}`, "query is required"},
		{"read no id", `{"action":"read"}`, "id is required"},
		{"write no content", `{"action":"write"}`, "content is required"},
		{"update no id", `{"action":"update","content":"x"}`, "id and content are required"},
		{"update no content", `{"action":"update","id":"x"}`, "id and content are required"},
		{"delete no id", `{"action":"delete"}`, "id is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := executeAndParse(t, tool, tt.input)
			assert.False(t, resp.Success)
			assert.Contains(t, resp.Message, tt.msg)
		})
	}
}
