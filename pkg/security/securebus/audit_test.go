package securebus

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuditLogAppendAndRetrieve(t *testing.T) {
	t.Parallel()
	al := NewAuditLog()

	event := AuditEvent{
		RequestID:   "req-1",
		SessionKey:  "sess-A",
		ToolName:    "read_file",
		CommandType: "tool_exec",
		At:          time.Now(),
		DurationMS:  50,
	}

	require.NoError(t, al.Append(event))
	assert.Empty(t, cmp.Diff(1, al.Len()))

	events := al.Events()
	require.Len(t, events, 1)
	assert.Empty(t, cmp.Diff("req-1", events[0].RequestID))
	assert.Empty(t, cmp.Diff("read_file", events[0].ToolName))
}

func TestAuditLogConcurrentAppend(t *testing.T) {
	t.Parallel()
	al := NewAuditLog()
	n := 100

	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			_ = al.Append(AuditEvent{
				RequestID: fmt.Sprintf("req-%d", idx),
				At:        time.Now(),
			})
		}(i)
	}
	wg.Wait()

	assert.Empty(t, cmp.Diff(n, al.Len()))
}

func TestAuditLogFilterBySession(t *testing.T) {
	t.Parallel()
	al := NewAuditLog()

	_ = al.Append(AuditEvent{RequestID: "r1", SessionKey: "sess-A"})
	_ = al.Append(AuditEvent{RequestID: "r2", SessionKey: "sess-B"})
	_ = al.Append(AuditEvent{RequestID: "r3", SessionKey: "sess-A"})

	sessA := al.FilterBySession("sess-A")
	assert.Len(t, sessA, 2)

	sessB := al.FilterBySession("sess-B")
	assert.Len(t, sessB, 1)

	sessC := al.FilterBySession("sess-C")
	assert.Empty(t, sessC)
}

func TestAuditLogLeakEvents(t *testing.T) {
	t.Parallel()
	al := NewAuditLog()

	_ = al.Append(AuditEvent{RequestID: "r1", LeakDetected: false})
	_ = al.Append(AuditEvent{RequestID: "r2", LeakDetected: true, RedactedKeys: []string{"api_key"}})
	_ = al.Append(AuditEvent{RequestID: "r3", LeakDetected: true, RedactedKeys: []string{"token"}})

	leaks := al.LeakEvents()
	assert.Len(t, leaks, 2)
	assert.Empty(t, cmp.Diff("r2", leaks[0].RequestID))
	assert.Empty(t, cmp.Diff("r3", leaks[1].RequestID))
}

type mockSink struct {
	mu     sync.Mutex
	events []AuditEvent
	failAt int
}

func (ms *mockSink) Write(event AuditEvent) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	if ms.failAt > 0 && len(ms.events) >= ms.failAt {
		return fmt.Errorf("sink full")
	}
	ms.events = append(ms.events, event)
	return nil
}

func TestAuditLogSinkIntegration(t *testing.T) {
	t.Parallel()
	sink := &mockSink{}
	al := NewAuditLog(sink)

	_ = al.Append(AuditEvent{RequestID: "r1"})
	_ = al.Append(AuditEvent{RequestID: "r2"})

	assert.Empty(t, cmp.Diff(2, al.Len()))
	assert.Len(t, sink.events, 2)
}

func TestAuditLogSinkError(t *testing.T) {
	t.Parallel()
	sink := &mockSink{failAt: 1}
	al := NewAuditLog(sink)

	assert.NoError(t, al.Append(AuditEvent{RequestID: "r1"}))

	err := al.Append(AuditEvent{RequestID: "r2"})
	assert.Error(t, err)

	assert.Empty(t, cmp.Diff(2, al.Len()), "in-memory log should always append")
}

func TestAuditLogEventsImmutable(t *testing.T) {
	t.Parallel()
	al := NewAuditLog()
	_ = al.Append(AuditEvent{RequestID: "r1"})

	events := al.Events()
	events[0].RequestID = "mutated"

	original := al.Events()
	assert.Empty(t, cmp.Diff("r1", original[0].RequestID), "original should be unaffected")
}
