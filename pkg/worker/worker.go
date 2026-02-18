package worker

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/sipeed/picoclaw/pkg/memory/sqlc"
	"github.com/sipeed/picoclaw/pkg/pcerrors"
)

// HandlerFunc processes a single claimed job. Returning nil marks it succeeded.
type HandlerFunc func(ctx context.Context, q *sqlc.Queries, job sqlc.Job) error

// Options configures the worker loop.
type Options struct {
	Handlers map[string]HandlerFunc
	LockedBy string
	Sleep    time.Duration
	Backoff  func(attempt int) time.Duration
	Now      func() time.Time
	Logger   *zerolog.Logger
}

func (o *Options) lockedBy() string {
	if o != nil && o.LockedBy != "" {
		return o.LockedBy
	}
	hn, err := os.Hostname()
	if err == nil && hn != "" {
		return hn
	}
	return "worker"
}

func (o *Options) now() time.Time {
	if o != nil && o.Now != nil {
		return o.Now()
	}
	return time.Now().UTC()
}

func (o *Options) sleep() time.Duration {
	if o != nil && o.Sleep > 0 {
		return o.Sleep
	}
	return 2 * time.Second
}

func (o *Options) backoff(attempt int) time.Duration {
	if o != nil && o.Backoff != nil {
		return o.Backoff(attempt)
	}
	d := time.Duration(1<<min(attempt-1, 5)) * time.Second
	if d > 5*time.Minute {
		return 5 * time.Minute
	}
	return d
}

func (o *Options) logger() zerolog.Logger {
	if o != nil && o.Logger != nil {
		return *o.Logger
	}
	return zerolog.Nop()
}

// RunOnce claims and executes a single job if available.
func RunOnce(ctx context.Context, q *sqlc.Queries, opts *Options) error {
	if q == nil {
		return pcerrors.New(pcerrors.CodeFailedPrecondition, "queries is nil")
	}

	l := opts.logger()
	lockedBy := opts.lockedBy()
	handlers := map[string]HandlerFunc{}
	if opts != nil && opts.Handlers != nil {
		handlers = opts.Handlers
	}

	jobID, err := q.FindNextRunnableJob(ctx)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return err
	}

	job, err := q.ClaimJobByID(ctx, sqlc.ClaimJobByIDParams{
		ID:       jobID,
		LockedBy: &lockedBy,
	})
	if err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return err
	}

	handler := handlers[job.Kind]
	if handler == nil {
		msg := fmt.Sprintf("no handler for kind=%s", job.Kind)
		l.Warn().Str("job_id", job.ID.String()).Msg(msg)
		_, _ = q.MarkJobFailed(ctx, sqlc.MarkJobFailedParams{
			LastError: &msg,
			ID:        job.ID,
		})
		return nil
	}

	l.Info().
		Str("job_id", job.ID.String()).
		Str("kind", job.Kind).
		Int64("attempt", job.Attempts).
		Msg("running job")

	start := time.Now()
	runErr := handler(ctx, q, job)
	elapsed := time.Since(start)

	if runErr == nil {
		l.Info().
			Str("job_id", job.ID.String()).
			Str("kind", job.Kind).
			Dur("duration_ms", elapsed).
			Msg("job succeeded")
		_, err = q.MarkJobSucceeded(ctx, sqlc.MarkJobSucceededParams{
			ID: job.ID,
		})
		return err
	}

	l.Warn().
		Str("job_id", job.ID.String()).
		Str("kind", job.Kind).
		Int64("attempt", job.Attempts).
		Dur("duration_ms", elapsed).
		Err(runErr).
		Msg("job failed")

	if job.Attempts >= job.MaxAttempts {
		msg := runErr.Error()
		_, _ = q.MarkJobFailed(ctx, sqlc.MarkJobFailedParams{
			LastError: &msg,
			ID:        job.ID,
		})
		return nil
	}

	backoff := opts.backoff(int(job.Attempts))
	nextRun := opts.now().Add(backoff)
	msg := runErr.Error()
	_, err = q.RequeueJob(ctx, sqlc.RequeueJobParams{
		ID:        job.ID,
		RunAt:     nextRun,
		LastError: &msg,
	})
	return err
}

// RunLoop polls for work until ctx is canceled.
func RunLoop(ctx context.Context, q *sqlc.Queries, opts *Options) error {
	if opts == nil {
		opts = &Options{}
	}
	l := opts.logger()
	sleep := opts.sleep()

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if err := RunOnce(ctx, q, opts); err != nil {
			l.Error().Err(err).Msg("worker run once failed")
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(sleep):
			}
			continue
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(sleep):
		}
	}
}
