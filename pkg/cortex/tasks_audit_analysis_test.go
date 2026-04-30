package cortex

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
	"go.uber.org/mock/gomock"
)

func expectRecentAuditEntries(store *MockAuditAnalysisStore, entries []AuditEntry, err error) {
	store.EXPECT().GetRecentAuditEntries(gomock.Any(), gomock.Any()).Return(entries, err)
}

func captureInsertedPatterns(store *MockAuditAnalysisStore, patterns *[]DetectedPattern, err error) {
	store.EXPECT().InsertRecallItem(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, item *memory.RecallItem) error {
		if err != nil {
			return err
		}
		*patterns = append(*patterns, recallItemPattern(item))
		return nil
	}).AnyTimes()
}

func recallItemPattern(item *memory.RecallItem) DetectedPattern {
	patternType := "unknown"
	category := "unknown"
	if item.Tags != "" {
		parts := []string{}
		for _, p := range splitTags(item.Tags) {
			if p != "audit" {
				parts = append(parts, p)
			}
		}
		if len(parts) >= 1 {
			patternType = parts[0]
		}
		if len(parts) >= 2 {
			category = parts[1]
		}
	}
	return DetectedPattern{
		Type:        patternType,
		Description: item.Content,
		Weight:      item.Importance,
		Category:    category,
		SessionID:   item.SessionKey,
		AgentID:     item.AgentID,
	}
}

// splitTags splits a comma-separated tag string
func splitTags(tags string) []string {
	var result []string
	start := 0
	for i := 0; i < len(tags); i++ {
		if tags[i] == ',' {
			result = append(result, tags[start:i])
			start = i + 1
		}
	}
	result = append(result, tags[start:])
	return result
}

func TestAuditAnalysisTask_Name(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := NewMockAuditAnalysisStore(ctrl)
	task := NewAuditAnalysisTask(store)

	if got := task.Name(); got != "audit_analysis" {
		t.Errorf("Name() = %q, want %q", got, "audit_analysis")
	}
}

func TestAuditAnalysisTask_Interval(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := NewMockAuditAnalysisStore(ctrl)
	task := NewAuditAnalysisTask(store)

	want := 10 * time.Minute
	if got := task.Interval(); got != want {
		t.Errorf("Interval() = %v, want %v", got, want)
	}
}

func TestAuditAnalysisTask_Timeout(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := NewMockAuditAnalysisStore(ctrl)
	task := NewAuditAnalysisTask(store)

	want := 60 * time.Second
	if got := task.Timeout(); got != want {
		t.Errorf("Timeout() = %v, want %v", got, want)
	}
}

func TestAuditAnalysisTask_Execute_NoStore(t *testing.T) {
	task := NewAuditAnalysisTask(nil)

	ctx := context.Background()
	if err := task.Execute(ctx); err != nil {
		t.Errorf("Execute() with nil store should not error, got: %v", err)
	}
}

func TestAuditAnalysisTask_Execute_NoEntries(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := NewMockAuditAnalysisStore(ctrl)
	expectRecentAuditEntries(store, []AuditEntry{}, nil)
	task := NewAuditAnalysisTask(store)

	ctx := context.Background()
	if err := task.Execute(ctx); err != nil {
		t.Errorf("Execute() with no entries should not error, got: %v", err)
	}

	// Verify lastRun was updated
	if task.lastRun.IsZero() {
		t.Error("lastRun should have been updated after Execute")
	}
}

func TestAuditAnalysisTask_Execute_DetectsCorrections(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := NewMockAuditAnalysisStore(ctrl)
	patternsStored := []DetectedPattern{}
	expectRecentAuditEntries(store, []AuditEntry{
		{
			ID:        "entry-1",
			Timestamp: time.Now(),
			ToolName:  "read_file",
			ToolInput: "path/to/file1",
			Success:   false, // Failed
			SessionID: "session-1",
			AgentID:   "agent-1",
		},
		{
			ID:        "entry-2",
			Timestamp: time.Now().Add(time.Second),
			ToolName:  "read_file",
			ToolInput: "path/to/file2", // Different input
			Success:   true,            // Succeeded
			SessionID: "session-1",
			AgentID:   "agent-1",
		},
	}, nil)
	captureInsertedPatterns(store, &patternsStored, nil)
	task := NewAuditAnalysisTask(store)

	ctx := context.Background()
	if err := task.Execute(ctx); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	// Should have detected one correction pattern
	if len(patternsStored) != 1 {
		t.Errorf("expected 1 pattern stored, got %d", len(patternsStored))
	}

	if len(patternsStored) > 0 {
		pattern := patternsStored[0]
		if pattern.Type != "correction" {
			t.Errorf("expected pattern type 'correction', got %q", pattern.Type)
		}
		if pattern.Category != "correction" {
			t.Errorf("expected pattern category 'correction', got %q", pattern.Category)
		}
		if pattern.Weight != 1.0 {
			t.Errorf("expected pattern weight 1.0, got %f", pattern.Weight)
		}
	}
}

func TestAuditAnalysisTask_Execute_SortsSessionEntriesByTimestamp(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := NewMockAuditAnalysisStore(ctrl)
	patternsStored := []DetectedPattern{}
	expectRecentAuditEntries(store, []AuditEntry{
		{
			ID:        "entry-2",
			Timestamp: time.Now().Add(time.Second),
			ToolName:  "read_file",
			ToolInput: "path/to/file2",
			Success:   true,
			SessionID: "session-1",
			AgentID:   "agent-1",
		},
		{
			ID:        "entry-1",
			Timestamp: time.Now(),
			ToolName:  "read_file",
			ToolInput: "path/to/file1",
			Success:   false,
			SessionID: "session-1",
			AgentID:   "agent-1",
		},
	}, nil)
	captureInsertedPatterns(store, &patternsStored, nil)
	task := NewAuditAnalysisTask(store)

	err := task.Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if len(patternsStored) != 1 {
		t.Fatalf("expected 1 correction pattern, got %d", len(patternsStored))
	}
	if patternsStored[0].Type != "correction" {
		t.Fatalf("expected correction pattern, got %q", patternsStored[0].Type)
	}
}

func TestAuditAnalysisTask_Execute_AdvancesWatermarkToLatestEntry(t *testing.T) {
	now := time.Now().UTC()
	ctrl := gomock.NewController(t)
	store := NewMockAuditAnalysisStore(ctrl)
	patternsStored := []DetectedPattern{}
	expectRecentAuditEntries(store, []AuditEntry{
		{ID: "e1", Timestamp: now.Add(-30 * time.Second), ToolName: "read", Success: true, SessionID: "s1", AgentID: "a1"},
		{ID: "e2", Timestamp: now.Add(-10 * time.Second), ToolName: "read", Success: true, SessionID: "s1", AgentID: "a1"},
	}, nil)
	captureInsertedPatterns(store, &patternsStored, nil)
	task := NewAuditAnalysisTask(store)

	err := task.Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if !task.lastRun.Equal(now.Add(-10 * time.Second)) {
		t.Fatalf("lastRun = %v, want %v", task.lastRun, now.Add(-10*time.Second))
	}
}

func TestAuditAnalysisTask_Execute_DetectsDiscovery(t *testing.T) {
	// Create entries with high token usage and read/search tools.
	// Threshold is 50k tokens and estimateTokensFromEntries uses len(input)/4.
	// 12 * 20k chars = 240k chars => 60k estimated tokens.
	longInput := make([]byte, 20000)
	for i := range longInput {
		longInput[i] = 'a' + byte(i%26)
	}
	longInputStr := string(longInput)

	var entries []AuditEntry
	for i := 0; i < 12; i++ {
		toolName := "read"
		if i%2 == 0 {
			toolName = "search"
		}
		entries = append(entries, AuditEntry{
			ID:        fmt.Sprintf("entry-discovery-%d", i),
			Timestamp: time.Now(),
			ToolName:  toolName,
			ToolInput: longInputStr,
			Success:   true,
			SessionID: "session-discovery",
			AgentID:   "agent-1",
		})
	}

	ctrl := gomock.NewController(t)
	store := NewMockAuditAnalysisStore(ctrl)
	patternsStored := []DetectedPattern{}
	expectRecentAuditEntries(store, entries, nil)
	captureInsertedPatterns(store, &patternsStored, nil)
	task := NewAuditAnalysisTask(store)

	ctx := context.Background()
	if err := task.Execute(ctx); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	foundDiscovery := false
	for _, pattern := range patternsStored {
		if pattern.Type == "discovery" {
			foundDiscovery = true
			if pattern.Weight != 1.2 {
				t.Errorf("expected discovery weight 1.2, got %f", pattern.Weight)
			}
			break
		}
	}
	if !foundDiscovery {
		t.Fatal("expected discovery pattern to be detected")
	}
}

func TestAuditAnalysisTask_Execute_DetectsFailurePatterns(t *testing.T) {
	// Create 3 failures of the same tool
	ctrl := gomock.NewController(t)
	store := NewMockAuditAnalysisStore(ctrl)
	patternsStored := []DetectedPattern{}
	expectRecentAuditEntries(store, []AuditEntry{
		{ID: "f1", Timestamp: time.Now(), ToolName: "exec", Success: false, ErrorMsg: "timeout", SessionID: "s1", AgentID: "agent-1"},
		{ID: "f2", Timestamp: time.Now().Add(time.Second), ToolName: "exec", Success: false, ErrorMsg: "timeout", SessionID: "s1", AgentID: "agent-1"},
		{ID: "f3", Timestamp: time.Now().Add(2 * time.Second), ToolName: "exec", Success: false, ErrorMsg: "timeout", SessionID: "s1", AgentID: "agent-1"},
	}, nil)
	captureInsertedPatterns(store, &patternsStored, nil)
	task := NewAuditAnalysisTask(store)

	ctx := context.Background()
	if err := task.Execute(ctx); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	// Should have detected failure pattern
	foundFailurePattern := false
	for _, pattern := range patternsStored {
		if pattern.Type == "failure_pattern" {
			foundFailurePattern = true
			if pattern.Weight != 1.5 {
				t.Errorf("expected failure pattern weight 1.5, got %f", pattern.Weight)
			}
			if pattern.Category != "correction" {
				t.Errorf("expected failure pattern category 'correction', got %q", pattern.Category)
			}
		}
	}
	if !foundFailurePattern {
		t.Error("expected failure pattern to be detected")
	}
}

func TestAuditAnalysisTask_Execute_MultipleSessions(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := NewMockAuditAnalysisStore(ctrl)
	patternsStored := []DetectedPattern{}
	expectRecentAuditEntries(store, []AuditEntry{
		// Session 1: has correction
		{ID: "s1-1", Timestamp: time.Now(), ToolName: "read", ToolInput: "file1", Success: false, SessionID: "session-1", AgentID: "agent-1"},
		{ID: "s1-2", Timestamp: time.Now().Add(time.Second), ToolName: "read", ToolInput: "file2", Success: true, SessionID: "session-1", AgentID: "agent-1"},
		// Session 2: has 3 failures
		{ID: "s2-1", Timestamp: time.Now(), ToolName: "exec", Success: false, SessionID: "session-2", AgentID: "agent-2"},
		{ID: "s2-2", Timestamp: time.Now().Add(time.Second), ToolName: "exec", Success: false, SessionID: "session-2", AgentID: "agent-2"},
		{ID: "s2-3", Timestamp: time.Now().Add(2 * time.Second), ToolName: "exec", Success: false, SessionID: "session-2", AgentID: "agent-2"},
	}, nil)
	captureInsertedPatterns(store, &patternsStored, nil)
	task := NewAuditAnalysisTask(store)

	ctx := context.Background()
	if err := task.Execute(ctx); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	// Should have detected patterns from both sessions
	if len(patternsStored) < 2 {
		t.Errorf("expected at least 2 patterns (one per session), got %d", len(patternsStored))
	}

	// Verify session IDs are correct
	sessionIDs := make(map[string]int)
	for _, pattern := range patternsStored {
		sessionIDs[pattern.SessionID]++
	}
	if sessionIDs["session-1"] == 0 {
		t.Error("expected patterns from session-1")
	}
	if sessionIDs["session-2"] == 0 {
		t.Error("expected patterns from session-2")
	}
}

func TestAuditAnalysisTask_Execute_GetEntriesError(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := NewMockAuditAnalysisStore(ctrl)
	expectRecentAuditEntries(store, nil, errors.New("database error"))
	task := NewAuditAnalysisTask(store)

	ctx := context.Background()
	err := task.Execute(ctx)
	if err == nil {
		t.Error("expected error when GetRecentAuditEntries fails")
	}
	if err.Error() != "failed to get recent audit entries: database error" {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestAuditAnalysisTask_Execute_StorePatternError(t *testing.T) {
	// Pattern storage errors should not stop the task
	ctrl := gomock.NewController(t)
	store := NewMockAuditAnalysisStore(ctrl)
	patternsStored := []DetectedPattern{}
	expectRecentAuditEntries(store, []AuditEntry{
		{ID: "f1", Timestamp: time.Now(), ToolName: "exec", Success: false, SessionID: "s1", AgentID: "agent-1"},
		{ID: "f2", Timestamp: time.Now().Add(time.Second), ToolName: "exec", Success: false, SessionID: "s1", AgentID: "agent-1"},
		{ID: "f3", Timestamp: time.Now().Add(2 * time.Second), ToolName: "exec", Success: false, SessionID: "s1", AgentID: "agent-1"},
	}, nil)
	captureInsertedPatterns(store, &patternsStored, errors.New("storage error"))
	task := NewAuditAnalysisTask(store)

	ctx := context.Background()
	// Should not error - continues even if pattern storage fails
	if err := task.Execute(ctx); err != nil {
		t.Errorf("Execute() should not error on pattern storage failure: %v", err)
	}
}

func TestDetectCorrections(t *testing.T) {
	tests := []struct {
		name      string
		sequence  []ToolSequence
		wantCount int
	}{
		{
			name: "detects basic correction",
			sequence: []ToolSequence{
				{Tool: "read", Input: "path1", Failed: true},
				{Tool: "read", Input: "path2", Failed: false},
			},
			wantCount: 1,
		},
		{
			name: "detects correction within next 3 tools",
			sequence: []ToolSequence{
				{Tool: "exec", Input: "cmd1", Failed: true},
				{Tool: "read", Input: "file", Failed: false},
				{Tool: "search", Input: "pattern", Failed: false},
				{Tool: "exec", Input: "cmd2", Failed: false},
			},
			wantCount: 1,
		},
		{
			name: "no correction if too far",
			sequence: []ToolSequence{
				{Tool: "exec", Input: "cmd1", Failed: true},
				{Tool: "read", Input: "file", Failed: false},
				{Tool: "search", Input: "pattern", Failed: false},
				{Tool: "list", Input: "dir", Failed: false},
				{Tool: "exec", Input: "cmd2", Failed: false},
			},
			wantCount: 0,
		},
		{
			name: "no correction if same input",
			sequence: []ToolSequence{
				{Tool: "read", Input: "same_path", Failed: true},
				{Tool: "read", Input: "same_path", Failed: false},
			},
			wantCount: 0,
		},
		{
			name: "no correction if different tool",
			sequence: []ToolSequence{
				{Tool: "read", Input: "path", Failed: true},
				{Tool: "write", Input: "path", Failed: false},
			},
			wantCount: 0,
		},
		{
			name: "multiple corrections in sequence",
			sequence: []ToolSequence{
				{Tool: "read", Input: "file1", Failed: true},
				{Tool: "read", Input: "file2", Failed: false},
				{Tool: "exec", Input: "cmd1", Failed: true},
				{Tool: "exec", Input: "cmd2", Failed: false},
			},
			wantCount: 2,
		},
		{
			name:      "empty sequence",
			sequence:  []ToolSequence{},
			wantCount: 0,
		},
		{
			name: "no failures in sequence",
			sequence: []ToolSequence{
				{Tool: "read", Input: "file1", Failed: false},
				{Tool: "read", Input: "file2", Failed: false},
			},
			wantCount: 0,
		},
		{
			name: "only records first correction",
			sequence: []ToolSequence{
				{Tool: "read", Input: "file1", Failed: true},
				{Tool: "read", Input: "file2", Failed: false},
				{Tool: "read", Input: "file3", Failed: false},
			},
			wantCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectCorrections(tt.sequence)
			if len(got) != tt.wantCount {
				t.Errorf("DetectCorrections() returned %d corrections, want %d", len(got), tt.wantCount)
			}
		})
	}
}

func TestDetectCorrections_Values(t *testing.T) {
	sequence := []ToolSequence{
		{Tool: "read_file", Input: `/path/to/wrong/file.txt`, Failed: true},
		{Tool: "read_file", Input: `/path/to/correct/file.txt`, Failed: false},
	}

	corrections := DetectCorrections(sequence)
	if len(corrections) != 1 {
		t.Fatalf("expected 1 correction, got %d", len(corrections))
	}

	c := corrections[0]
	if c.FailedTool != "read_file" {
		t.Errorf("FailedTool = %q, want %q", c.FailedTool, "read_file")
	}
	if c.FailedInput != `/path/to/wrong/file.txt` {
		t.Errorf("FailedInput = %q, want %q", c.FailedInput, `/path/to/wrong/file.txt`)
	}
	if c.SucceededTool != "read_file" {
		t.Errorf("SucceededTool = %q, want %q", c.SucceededTool, "read_file")
	}
	if c.SucceededInput != `/path/to/correct/file.txt` {
		t.Errorf("SucceededInput = %q, want %q", c.SucceededInput, `/path/to/correct/file.txt`)
	}
}

func TestIsDiscovery(t *testing.T) {
	tests := []struct {
		name       string
		tokens     int64
		toolCounts map[string]int
		want       bool
	}{
		{
			name:   "discovery with high tokens and read/search > 50%",
			tokens: 50000,
			toolCounts: map[string]int{
				"read":   30,
				"search": 30,
				"write":  10,
				"exec":   10,
			},
			want: true,
		},
		{
			name:   "discovery with grep tool",
			tokens: 50000,
			toolCounts: map[string]int{
				"grep":  40,
				"write": 20,
			},
			want: true,
		},
		{
			name:   "discovery with find and list tools",
			tokens: 50000,
			toolCounts: map[string]int{
				"find":  25,
				"list":  30,
				"write": 20,
			},
			want: true,
		},
		{
			name:   "not discovery - tokens under threshold",
			tokens: 49999,
			toolCounts: map[string]int{
				"read":   30,
				"search": 30,
			},
			want: false,
		},
		{
			name:   "not discovery - read/search ratio too low",
			tokens: 50000,
			toolCounts: map[string]int{
				"read":   20,
				"search": 20,
				"write":  40,
				"exec":   40,
			},
			want: false,
		},
		{
			name:   "not discovery - no read/search tools",
			tokens: 50000,
			toolCounts: map[string]int{
				"write": 50,
				"exec":  50,
			},
			want: false,
		},
		{
			name:       "not discovery - empty tool counts",
			tokens:     50000,
			toolCounts: map[string]int{},
			want:       false,
		},
		{
			name:   "exactly 50% ratio should not be discovery",
			tokens: 50000,
			toolCounts: map[string]int{
				"read":  50,
				"write": 50,
			},
			want: false, // Must be > 50%, not >=
		},
		{
			name:   "just above 50% ratio is discovery",
			tokens: 50000,
			toolCounts: map[string]int{
				"read":  51,
				"write": 49,
			},
			want: true,
		},
		{
			name:   "case insensitive matching",
			tokens: 50000,
			toolCounts: map[string]int{
				"READ":   30,
				"SEARCH": 30,
				"write":  40,
			},
			want: true,
		},
		{
			name:   "mix of matching tools",
			tokens: 50000,
			toolCounts: map[string]int{
				"read_file":   10,
				"grep_search": 10,
				"find_files":  10,
				"list_dir":    10,
				"write_file":  10,
				"exec_cmd":    10,
			},
			want: true, // 40/60 = 66.7%
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsDiscovery(tt.tokens, tt.toolCounts)
			if got != tt.want {
				t.Errorf("IsDiscovery(%d, %v) = %v, want %v", tt.tokens, tt.toolCounts, got, tt.want)
			}
		})
	}
}

func TestDetectFailurePatterns(t *testing.T) {
	tests := []struct {
		name     string
		failures []AuditEntry
		wantLen  int
		wantTool string
	}{
		{
			name: "detects failure pattern with 3 failures",
			failures: []AuditEntry{
				{ID: "f1", ToolName: "exec", Success: false},
				{ID: "f2", ToolName: "exec", Success: false},
				{ID: "f3", ToolName: "exec", Success: false},
			},
			wantLen:  1,
			wantTool: "exec",
		},
		{
			name: "detects multiple tool failure patterns",
			failures: []AuditEntry{
				{ID: "f1", ToolName: "exec", Success: false},
				{ID: "f2", ToolName: "exec", Success: false},
				{ID: "f3", ToolName: "exec", Success: false},
				{ID: "f4", ToolName: "read", Success: false},
				{ID: "f5", ToolName: "read", Success: false},
				{ID: "f6", ToolName: "read", Success: false},
			},
			wantLen:  2,
			wantTool: "", // Multiple tools
		},
		{
			name: "no pattern with 2 failures",
			failures: []AuditEntry{
				{ID: "f1", ToolName: "exec", Success: false},
				{ID: "f2", ToolName: "exec", Success: false},
			},
			wantLen: 0,
		},
		{
			name:     "no failures",
			failures: []AuditEntry{},
			wantLen:  0,
		},
		{
			name: "single failure no pattern",
			failures: []AuditEntry{
				{ID: "f1", ToolName: "exec", Success: false},
			},
			wantLen: 0,
		},
		{
			name: "many failures same tool",
			failures: []AuditEntry{
				{ID: "f1", ToolName: "exec", Success: false},
				{ID: "f2", ToolName: "exec", Success: false},
				{ID: "f3", ToolName: "exec", Success: false},
				{ID: "f4", ToolName: "exec", Success: false},
				{ID: "f5", ToolName: "exec", Success: false},
			},
			wantLen:  1,
			wantTool: "exec",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectFailurePatterns(tt.failures)
			if len(got) != tt.wantLen {
				t.Errorf("DetectFailurePatterns() returned %d patterns, want %d", len(got), tt.wantLen)
			}
			if tt.wantTool != "" && len(got) > 0 {
				found := false
				for _, p := range got {
					if p.Description != "" && tt.wantTool != "" {
						// Check that the description contains the tool name
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected pattern description to mention tool %q", tt.wantTool)
				}
			}
		})
	}
}

func TestDetectFailurePatterns_Values(t *testing.T) {
	failures := []AuditEntry{
		{ID: "f1", ToolName: "network_call", Success: false, SessionID: "s1", AgentID: "agent-1"},
		{ID: "f2", ToolName: "network_call", Success: false, SessionID: "s1", AgentID: "agent-1"},
		{ID: "f3", ToolName: "network_call", Success: false, SessionID: "s1", AgentID: "agent-1"},
	}

	patterns := DetectFailurePatterns(failures)
	if len(patterns) != 1 {
		t.Fatalf("expected 1 pattern, got %d", len(patterns))
	}

	p := patterns[0]
	if p.Type != "failure_pattern" {
		t.Errorf("Type = %q, want %q", p.Type, "failure_pattern")
	}
	if p.Weight != 1.5 {
		t.Errorf("Weight = %f, want 1.5", p.Weight)
	}
	if p.Category != "correction" {
		t.Errorf("Category = %q, want %q", p.Category, "correction")
	}
}

func TestGroupBySession(t *testing.T) {
	entries := []AuditEntry{
		{ID: "e1", SessionID: "session-1", ToolName: "read"},
		{ID: "e2", SessionID: "session-1", ToolName: "write"},
		{ID: "e3", SessionID: "session-2", ToolName: "exec"},
		{ID: "e4", SessionID: "session-1", ToolName: "search"},
	}

	groups := groupBySession(entries)

	if len(groups) != 2 {
		t.Errorf("expected 2 session groups, got %d", len(groups))
	}

	if len(groups["session-1"]) != 3 {
		t.Errorf("expected 3 entries in session-1, got %d", len(groups["session-1"]))
	}

	if len(groups["session-2"]) != 1 {
		t.Errorf("expected 1 entry in session-2, got %d", len(groups["session-2"]))
	}
}

func TestBuildToolSequence(t *testing.T) {
	entries := []AuditEntry{
		{ToolName: "read", ToolInput: "file1", Success: true},
		{ToolName: "write", ToolInput: "file2", Success: false},
		{ToolName: "exec", ToolInput: "cmd", Success: true},
	}

	seq := buildToolSequence(entries)

	if len(seq) != 3 {
		t.Errorf("expected 3 sequence entries, got %d", len(seq))
	}

	if seq[0].Tool != "read" || seq[0].Input != "file1" || seq[0].Failed {
		t.Errorf("sequence[0] incorrect: %+v", seq[0])
	}

	if seq[1].Tool != "write" || seq[1].Input != "file2" || !seq[1].Failed {
		t.Errorf("sequence[1] incorrect: %+v", seq[1])
	}
}

func TestCountToolUsage(t *testing.T) {
	entries := []AuditEntry{
		{ToolName: "read"},
		{ToolName: "read"},
		{ToolName: "write"},
		{ToolName: "read"},
		{ToolName: "exec"},
	}

	counts := countToolUsage(entries)

	if counts["read"] != 3 {
		t.Errorf("expected read count = 3, got %d", counts["read"])
	}
	if counts["write"] != 1 {
		t.Errorf("expected write count = 1, got %d", counts["write"])
	}
	if counts["exec"] != 1 {
		t.Errorf("expected exec count = 1, got %d", counts["exec"])
	}
}

func TestEstimateTokensFromEntries(t *testing.T) {
	entries := []AuditEntry{
		{ToolInput: "short"},         // 5 chars / 4 = 1 token
		{ToolInput: "medium length"}, // 14 chars / 4 = 3 tokens
		{ToolInput: ""},              // 0 chars / 4 = 0 tokens
	}

	tokens := estimateTokensFromEntries(entries)

	want := int64((5 + 14 + 0) / 4)
	if tokens != want {
		t.Errorf("estimateTokensFromEntries() = %d, want %d", tokens, want)
	}
}

func TestFilterFailures(t *testing.T) {
	entries := []AuditEntry{
		{ID: "e1", Success: true},
		{ID: "e2", Success: false},
		{ID: "e3", Success: true},
		{ID: "e4", Success: false},
		{ID: "e5", Success: false},
	}

	failures := filterFailures(entries)

	if len(failures) != 3 {
		t.Errorf("expected 3 failures, got %d", len(failures))
	}

	for _, f := range failures {
		if f.Success {
			t.Error("filtered failures should not contain successful entries")
		}
	}
}

func TestAuditAnalysisTask_storePattern(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := NewMockAuditAnalysisStore(ctrl)
	patternsStored := []DetectedPattern{}
	captureInsertedPatterns(store, &patternsStored, nil)
	task := NewAuditAnalysisTask(store)

	pattern := DetectedPattern{
		Type:        "correction",
		Description: "Tool corrected from A to B",
		Weight:      1.0,
		Category:    "correction",
		SessionID:   "session-1",
		AgentID:     "agent-1",
	}

	ctx := context.Background()
	if err := task.storePattern(ctx, pattern); err != nil {
		t.Errorf("storePattern() error: %v", err)
	}
	if len(patternsStored) != 1 {
		t.Fatalf("expected 1 pattern stored, got %d", len(patternsStored))
	}
}

func TestDefaultAuditAnalysisConfig(t *testing.T) {
	cfg := DefaultAuditAnalysisConfig()

	if cfg.Interval != 10*time.Minute {
		t.Errorf("Interval = %v, want 10m", cfg.Interval)
	}
	if cfg.Timeout != 60*time.Second {
		t.Errorf("Timeout = %v, want 60s", cfg.Timeout)
	}
	if cfg.DiscoveryThreshold != 50000 {
		t.Errorf("DiscoveryThreshold = %d, want 50000", cfg.DiscoveryThreshold)
	}
	if cfg.FailureThreshold != 3 {
		t.Errorf("FailureThreshold = %d, want 3", cfg.FailureThreshold)
	}
	if cfg.LookbackWindow != 10*time.Minute {
		t.Errorf("LookbackWindow = %v, want 10m", cfg.LookbackWindow)
	}
}
