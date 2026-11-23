package application

import (
	"github.com/cn-maul/OpenTools/internal/tasks"
)

// The transport layers (desktop shell and CLI) depend on the application
// boundary only. These aliases re-export the job vocabulary under the
// application package so transports never import internal/tasks directly.
//
// They are aliases, not wrappers: the types stay identical, so no copying or
// conversion is needed anywhere.

// Status is the lifecycle state of a job.
type Status = tasks.Status

const (
	StatusQueued    = tasks.StatusQueued
	StatusRunning   = tasks.StatusRunning
	StatusCanceling = tasks.StatusCanceling
	StatusSucceeded = tasks.StatusSucceeded
	StatusFailed    = tasks.StatusFailed
	StatusCanceled  = tasks.StatusCanceled
)

// Job is the durable view of one tool invocation.
type Job = tasks.Job

// Progress is work completed out of a known total, or an unknown total.
type Progress = tasks.Progress

// LogEntry is one durable line of job output.
type LogEntry = tasks.LogEntry

// Event is a state transition announced to transport adapters.
type Event = tasks.Event
