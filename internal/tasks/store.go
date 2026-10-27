package tasks

import (
	"context"
	"errors"
	"time"
)

// ErrStaleRevision means a newer snapshot was stored before this one.
// Callers may safely discard the older snapshot.
var ErrStaleRevision = errors.New("stale job revision")

// Store persists jobs. Implementations must be safe for concurrent use.
type Store interface {
	Create(context.Context, *Job) error
	Update(context.Context, *Job) error
	// Heartbeat refreshes liveness without changing snapshots or revisions.
	Heartbeat(context.Context, []string, time.Time) error
	Get(context.Context, string) (*Job, error)
	List(context.Context, int) ([]Job, error)
	// MarkInterrupted fails active snapshots older than the given heartbeat.
	MarkInterrupted(context.Context, time.Time) error
	// Clear deletes every stored job snapshot.
	Clear(context.Context) error
}
