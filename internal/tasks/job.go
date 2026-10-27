// Package tasks owns job lifecycle, cancellation and persisted snapshots.
package tasks

import (
	"time"

	"opentools/pkg/tool"
)

// Status is the lifecycle state of a job.
type Status string

const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusCanceling Status = "canceling"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusCanceled  Status = "canceled"
)

// Active reports whether work is still in flight.
func (s Status) Active() bool {
	return s == StatusQueued || s == StatusRunning || s == StatusCanceling
}

// Progress is work completed out of a known total, or an unknown total.
type Progress struct {
	Done  int64 `json:"done"`
	Total int64 `json:"total"`
}

// LogEntry is one durable line of job output.
type LogEntry struct {
	At      time.Time `json:"at"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
}

// Job is the durable view of one tool invocation.
type Job struct {
	ID         string         `json:"id"`
	ToolID     string         `json:"toolId"`
	Revision   int64          `json:"revision"`
	Status     Status         `json:"status"`
	Input      map[string]any `json:"input"`
	Progress   Progress       `json:"progress"`
	Logs       []LogEntry     `json:"logs,omitempty"`
	Sections   []tool.Section `json:"sections,omitempty"`
	Result     *tool.Result   `json:"result,omitempty"`
	Error      string         `json:"error,omitempty"`
	WorkDir    string         `json:"-"`
	CreatedAt  time.Time      `json:"createdAt"`
	StartedAt  *time.Time     `json:"startedAt,omitempty"`
	FinishedAt *time.Time     `json:"finishedAt,omitempty"`
}

// Event is a state transition announced to transport adapters.
type Event struct {
	Kind string `json:"kind"`
	Job  Job    `json:"job"`
}
