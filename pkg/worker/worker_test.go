package worker_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory/delegate"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory/sqlc"
	"github.com/ZanzyTHEbar/dragonscale/pkg/worker"
)

func newTestDB(t *testing.T) *sqlc.Queries {
	t.Helper()
	d, err := delegate.NewLibSQLInMemory()
	require.NoError(t, err)
	require.NoError(t, d.Init(t.Context()))
	t.Cleanup(func() { _ = d.Close() })
	return d.Queries()
}

func enqueueJob(t *testing.T, q *sqlc.Queries, kind, dedupeKey string, maxAttempts int64) sqlc.Job {
	t.Helper()
	past := time.Now().UTC().Add(-time.Second)
	job, err := q.EnqueueJob(t.Context(), sqlc.EnqueueJobParams{
		ID:          ids.New(),
		Kind:        kind,
		DedupeKey:   &dedupeKey,
		MaxAttempts: maxAttempts,
		RunAt:       past,
		PayloadJson: []byte(`{}`),
	})
	require.NoError(t, err, "EnqueueJob")
	return job
}

func TestWorker_RunOnce_NoJobs(t *testing.T) {
	t.Parallel()
	q := newTestDB(t)
	err := worker.RunOnce(t.Context(), q, nil)
	assert.NoError(t, err, "empty queue should not error")
}

func TestWorker_RunOnce_HandlerCalled(t *testing.T) {
	t.Parallel()
	q := newTestDB(t)
	ctx := t.Context()

	enqueueJob(t, q, "greet", "greet-1", 3)

	var called atomic.Int32
	opts := &worker.Options{
		LockedBy: "test-worker",
		Handlers: map[string]worker.HandlerFunc{
			"greet": func(_ context.Context, _ *sqlc.Queries, _ sqlc.Job) error {
				called.Add(1)
				return nil
			},
		},
	}

	err := worker.RunOnce(ctx, q, opts)
	require.NoError(t, err)
	assert.Equal(t, int32(1), called.Load(), "handler must be called once")
}

func TestWorker_RunOnce_JobMarkedSucceeded(t *testing.T) {
	t.Parallel()
	q := newTestDB(t)
	ctx := t.Context()

	job := enqueueJob(t, q, "ping", "ping-1", 3)

	opts := &worker.Options{
		LockedBy: "test-worker",
		Handlers: map[string]worker.HandlerFunc{
			"ping": func(_ context.Context, _ *sqlc.Queries, _ sqlc.Job) error {
				return nil
			},
		},
	}

	require.NoError(t, worker.RunOnce(ctx, q, opts))

	done, err := q.GetJob(ctx, sqlc.GetJobParams{ID: job.ID})
	require.NoError(t, err)
	assert.Equal(t, "succeeded", done.Status)
}

func TestWorker_RunOnce_HandlerError_Requeued(t *testing.T) {
	t.Parallel()
	q := newTestDB(t)
	ctx := t.Context()

	job := enqueueJob(t, q, "fail", "fail-1", 3)

	opts := &worker.Options{
		LockedBy: "test-worker",
		Now:      func() time.Time { return time.Now().UTC() },
		Handlers: map[string]worker.HandlerFunc{
			"fail": func(_ context.Context, _ *sqlc.Queries, _ sqlc.Job) error {
				return errors.New("transient error")
			},
		},
	}

	require.NoError(t, worker.RunOnce(ctx, q, opts))

	requeued, err := q.GetJob(ctx, sqlc.GetJobParams{ID: job.ID})
	require.NoError(t, err)
	assert.Equal(t, "queued", requeued.Status, "job should be requeued after transient failure")
	assert.Equal(t, int64(1), requeued.Attempts, "attempt count must increment")
	assert.NotNil(t, requeued.LastError)
}

func TestWorker_RunOnce_MaxAttemptsExhausted_MarkedFailed(t *testing.T) {
	t.Parallel()
	q := newTestDB(t)
	ctx := t.Context()

	enqueueJob(t, q, "exhaust", "exhaust-1", 1)

	opts := &worker.Options{
		LockedBy: "test-worker",
		Now:      func() time.Time { return time.Now().UTC() },
		Handlers: map[string]worker.HandlerFunc{
			"exhaust": func(_ context.Context, _ *sqlc.Queries, _ sqlc.Job) error {
				return errors.New("always fails")
			},
		},
	}

	require.NoError(t, worker.RunOnce(ctx, q, opts))

	all, err := q.ListJobs(ctx, sqlc.ListJobsParams{Off: 0, Lim: 100})
	require.NoError(t, err)
	failed := 0
	for _, j := range all {
		if j.Status == "failed" {
			failed++
		}
	}
	assert.Equal(t, 1, failed, "job should be permanently failed after exhausting max attempts")
}

func TestWorker_RunOnce_UnknownKind_MarkedFailed(t *testing.T) {
	t.Parallel()
	q := newTestDB(t)
	ctx := t.Context()

	enqueueJob(t, q, "unknown-kind", "unk-1", 3)

	opts := &worker.Options{
		LockedBy: "test-worker",
		Handlers: map[string]worker.HandlerFunc{},
	}

	require.NoError(t, worker.RunOnce(ctx, q, opts))

	all, err := q.ListJobs(ctx, sqlc.ListJobsParams{Off: 0, Lim: 100})
	require.NoError(t, err)
	failed := 0
	for _, j := range all {
		if j.Status == "failed" {
			failed++
		}
	}
	assert.Equal(t, 1, failed, "unknown kind should permanently fail the job")
}

func TestWorker_RunOnce_NilQueries(t *testing.T) {
	t.Parallel()
	err := worker.RunOnce(t.Context(), nil, nil)
	assert.Error(t, err, "nil queries should return an error")
}

func TestWorker_RunLoop_StopsOnCancel(t *testing.T) {
	t.Parallel()
	q := newTestDB(t)
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	opts := &worker.Options{
		Sleep:    50 * time.Millisecond,
		LockedBy: "loop-worker",
		Handlers: map[string]worker.HandlerFunc{},
	}

	err := worker.RunLoop(ctx, q, opts)
	assert.ErrorIs(t, err, context.DeadlineExceeded, "RunLoop must respect context cancellation")
}

func TestWorker_RunLoop_ProcessesJobs(t *testing.T) {
	t.Parallel()
	q := newTestDB(t)

	var processed atomic.Int32
	total := 5
	for i := range total {
		enqueueJob(t, q, "batch", "batch-"+string(rune('A'+i)), 3)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	opts := &worker.Options{
		Sleep:    10 * time.Millisecond,
		LockedBy: "loop-worker",
		Handlers: map[string]worker.HandlerFunc{
			"batch": func(_ context.Context, _ *sqlc.Queries, _ sqlc.Job) error {
				processed.Add(1)
				if int(processed.Load()) >= total {
					cancel()
				}
				return nil
			},
		},
	}

	_ = worker.RunLoop(ctx, q, opts)
	assert.Equal(t, int32(total), processed.Load(), "all jobs must be processed")
}
