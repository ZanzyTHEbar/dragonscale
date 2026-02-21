package delegate

import (
	"context"
	"testing"

	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeDoc(agentID, name, category, content string) *memory.AgentDocument {
	return &memory.AgentDocument{
		ID:       ids.New(),
		AgentID:  agentID,
		Name:     name,
		Category: category,
		Content:  content,
	}
}

func TestLibSQLDelegate_GetDocument(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		setup       func(t *testing.T, d *LibSQLDelegate, ctx context.Context)
		agentID     string
		docName     string
		wantNil     bool
		wantContent string
	}{
		{
			name:    "missing document returns nil",
			agentID: "a1",
			docName: "nonexistent",
			wantNil: true,
		},
		{
			name: "returns stored document",
			setup: func(t *testing.T, d *LibSQLDelegate, ctx context.Context) {
				require.NoError(t, d.UpsertDocument(ctx, makeDoc("a1", "README", "core", "hello world")))
			},
			agentID:     "a1",
			docName:     "README",
			wantContent: "hello world",
		},
		{
			name: "agent isolation — different agent sees nil",
			setup: func(t *testing.T, d *LibSQLDelegate, ctx context.Context) {
				require.NoError(t, d.UpsertDocument(ctx, makeDoc("a1", "SECRET", "core", "mine")))
			},
			agentID: "a2",
			docName: "SECRET",
			wantNil: true,
		},
		{
			name: "document with special characters in content",
			setup: func(t *testing.T, d *LibSQLDelegate, ctx context.Context) {
				require.NoError(t, d.UpsertDocument(ctx, makeDoc("a1", "config.json", "config", `{"key":"val","nested":{"a":1}}`)))
			},
			agentID:     "a1",
			docName:     "config.json",
			wantContent: `{"key":"val","nested":{"a":1}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newTestDelegate(t)
			ctx := t.Context()
			if tt.setup != nil {
				tt.setup(t, d, ctx)
			}
			doc, err := d.GetDocument(ctx, tt.agentID, tt.docName)
			require.NoError(t, err)
			if tt.wantNil {
				assert.Nil(t, doc)
				return
			}
			require.NotNil(t, doc)
			assert.Empty(t, cmp.Diff(&memory.AgentDocument{
				AgentID:  tt.agentID,
				Name:     tt.docName,
				Content:  tt.wantContent,
				IsActive: true,
			}, doc, cmpopts.IgnoreFields(memory.AgentDocument{}, "ID", "Category", "Version", "CreatedAt", "UpdatedAt")))
		})
	}
}

func TestLibSQLDelegate_UpsertDocument(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		ops         []*memory.AgentDocument
		agentID     string
		docName     string
		wantContent string
	}{
		{
			name:        "insert new document",
			ops:         []*memory.AgentDocument{makeDoc("a1", "doc1", "cat", "first")},
			agentID:     "a1",
			docName:     "doc1",
			wantContent: "first",
		},
		{
			name: "overwrite existing document content",
			ops: []*memory.AgentDocument{
				makeDoc("a1", "doc1", "cat", "original"),
				{ID: ids.New(), AgentID: "a1", Name: "doc1", Category: "cat", Content: "updated"},
			},
			agentID:     "a1",
			docName:     "doc1",
			wantContent: "updated",
		},
		{
			name: "same name different agents are independent",
			ops: []*memory.AgentDocument{
				makeDoc("a1", "shared", "cat", "from-a1"),
				makeDoc("a2", "shared", "cat", "from-a2"),
			},
			agentID:     "a1",
			docName:     "shared",
			wantContent: "from-a1",
		},
		{
			name:        "empty content is valid",
			ops:         []*memory.AgentDocument{makeDoc("a1", "empty", "cat", "")},
			agentID:     "a1",
			docName:     "empty",
			wantContent: "",
		},
		{
			name:        "large content roundtrip",
			ops:         []*memory.AgentDocument{makeDoc("a1", "big", "cat", longValue(8192))},
			agentID:     "a1",
			docName:     "big",
			wantContent: longValue(8192),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newTestDelegate(t)
			ctx := t.Context()
			for _, doc := range tt.ops {
				require.NoError(t, d.UpsertDocument(ctx, doc))
			}
			got, err := d.GetDocument(ctx, tt.agentID, tt.docName)
			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Empty(t, cmp.Diff(tt.wantContent, got.Content))
		})
	}
}

func TestLibSQLDelegate_DeleteDocument(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		setup   func(t *testing.T, d *LibSQLDelegate, ctx context.Context)
		agentID string
		docName string
	}{
		{
			name:    "delete nonexistent is idempotent",
			agentID: "a1",
			docName: "nope",
		},
		{
			name: "delete existing document",
			setup: func(t *testing.T, d *LibSQLDelegate, ctx context.Context) {
				require.NoError(t, d.UpsertDocument(ctx, makeDoc("a1", "doomed", "cat", "bye")))
			},
			agentID: "a1",
			docName: "doomed",
		},
		{
			name: "delete only affects target agent",
			setup: func(t *testing.T, d *LibSQLDelegate, ctx context.Context) {
				require.NoError(t, d.UpsertDocument(ctx, makeDoc("a1", "shared", "cat", "a1-doc")))
				require.NoError(t, d.UpsertDocument(ctx, makeDoc("a2", "shared", "cat", "a2-doc")))
			},
			agentID: "a1",
			docName: "shared",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newTestDelegate(t)
			ctx := t.Context()
			if tt.setup != nil {
				tt.setup(t, d, ctx)
			}
			require.NoError(t, d.DeleteDocument(ctx, tt.agentID, tt.docName))

			got, err := d.GetDocument(ctx, tt.agentID, tt.docName)
			require.NoError(t, err)
			assert.Nil(t, got, "document should be gone after delete")

			if tt.name == "delete only affects target agent" {
				other, err := d.GetDocument(ctx, "a2", "shared")
				require.NoError(t, err)
				require.NotNil(t, other, "other agent's document must survive")
				assert.Empty(t, cmp.Diff("a2-doc", other.Content))
			}
		})
	}
}

func TestLibSQLDelegate_ListDocumentsByCategory(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		setup    func(t *testing.T, d *LibSQLDelegate, ctx context.Context)
		agentID  string
		category string
		wantLen  int
		wantName string // first result name, if any
	}{
		{
			name:     "empty store returns empty slice",
			agentID:  "a1",
			category: "core",
			wantLen:  0,
		},
		{
			name: "filters by category",
			setup: func(t *testing.T, d *LibSQLDelegate, ctx context.Context) {
				require.NoError(t, d.UpsertDocument(ctx, makeDoc("a1", "d1", "core", "one")))
				require.NoError(t, d.UpsertDocument(ctx, makeDoc("a1", "d2", "core", "two")))
				require.NoError(t, d.UpsertDocument(ctx, makeDoc("a1", "d3", "config", "three")))
			},
			agentID:  "a1",
			category: "core",
			wantLen:  2,
		},
		{
			name: "agent isolation in category listing",
			setup: func(t *testing.T, d *LibSQLDelegate, ctx context.Context) {
				require.NoError(t, d.UpsertDocument(ctx, makeDoc("a1", "d1", "core", "a1-val")))
				require.NoError(t, d.UpsertDocument(ctx, makeDoc("a2", "d2", "core", "a2-val")))
			},
			agentID:  "a1",
			category: "core",
			wantLen:  1,
			wantName: "d1",
		},
		{
			name: "no match returns empty slice",
			setup: func(t *testing.T, d *LibSQLDelegate, ctx context.Context) {
				require.NoError(t, d.UpsertDocument(ctx, makeDoc("a1", "d1", "core", "val")))
			},
			agentID:  "a1",
			category: "nonexistent",
			wantLen:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newTestDelegate(t)
			ctx := t.Context()
			if tt.setup != nil {
				tt.setup(t, d, ctx)
			}
			docs, err := d.ListDocumentsByCategory(ctx, tt.agentID, tt.category)
			require.NoError(t, err)
			assert.Len(t, docs, tt.wantLen)
			if tt.wantName != "" && len(docs) > 0 {
				assert.Empty(t, cmp.Diff(tt.wantName, docs[0].Name))
			}
		})
	}
}

func TestLibSQLDelegate_ListAllDocuments(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		setup   func(t *testing.T, d *LibSQLDelegate, ctx context.Context)
		agentID string
		wantLen int
	}{
		{
			name:    "empty store returns empty slice",
			agentID: "a1",
			wantLen: 0,
		},
		{
			name: "returns all documents for agent",
			setup: func(t *testing.T, d *LibSQLDelegate, ctx context.Context) {
				require.NoError(t, d.UpsertDocument(ctx, makeDoc("a1", "d1", "core", "one")))
				require.NoError(t, d.UpsertDocument(ctx, makeDoc("a1", "d2", "config", "two")))
				require.NoError(t, d.UpsertDocument(ctx, makeDoc("a1", "d3", "skills", "three")))
			},
			agentID: "a1",
			wantLen: 3,
		},
		{
			name: "agent isolation — counts only own docs",
			setup: func(t *testing.T, d *LibSQLDelegate, ctx context.Context) {
				require.NoError(t, d.UpsertDocument(ctx, makeDoc("a1", "d1", "core", "a1")))
				require.NoError(t, d.UpsertDocument(ctx, makeDoc("a2", "d2", "core", "a2")))
				require.NoError(t, d.UpsertDocument(ctx, makeDoc("a2", "d3", "core", "a2b")))
			},
			agentID: "a1",
			wantLen: 1,
		},
		{
			name: "multiple categories all returned",
			setup: func(t *testing.T, d *LibSQLDelegate, ctx context.Context) {
				cats := []string{"core", "config", "skills", "templates"}
				for i, c := range cats {
					require.NoError(t, d.UpsertDocument(ctx, makeDoc("a1", kvKey("doc-", i), c, "val")))
				}
			},
			agentID: "a1",
			wantLen: 4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newTestDelegate(t)
			ctx := t.Context()
			if tt.setup != nil {
				tt.setup(t, d, ctx)
			}
			docs, err := d.ListAllDocuments(ctx, tt.agentID)
			require.NoError(t, err)
			assert.Len(t, docs, tt.wantLen)
		})
	}
}
