package cortex

import (
	"context"
	"time"
)

// Task is a periodic background job managed by the Cortex scheduler.
type Task interface {
	Name() string
	Interval() time.Duration
	Timeout() time.Duration
	Execute(ctx context.Context) error
}
