package tasks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"opentools/pkg/tool"
)

// Manager runs tools independently of any window or HTTP request.
type Manager struct {
	root   string
	store  Store
	logger *slog.Logger
	ctx    context.Context
	stop   context.CancelFunc
	wg     sync.WaitGroup

	mu          sync.RWMutex
	runs        map[string]*runState
	subscribers map[uint64]chan Event
	nextSub     uint64
}

type runState struct {
	mu       sync.Mutex
	job      Job
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
	manager  *Manager
	lastSave time.Time
}

// NewManager recovers interrupted jobs and returns a manager.
func NewManager(ctx context.Context, root string, store Store, logger *slog.Logger) (*Manager, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create job root: %w", err)
	}
	managerCtx, stop := context.WithCancel(ctx)
	m := &Manager{
		root:        root,
		store:       store,
		logger:      logger,
		ctx:         managerCtx,
		stop:        stop,
		runs:        map[string]*runState{},
		subscribers: map[uint64]chan Event{},
	}
	if err := store.MarkInterrupted(ctx, time.Now().UTC().Add(-staleAfter)); err != nil {
		stop()
		return nil, fmt.Errorf("recover interrupted jobs: %w", err)
	}
	m.wg.Add(1)
	go m.maintain()
	return m, nil
}

const (
	heartbeatEvery = 3 * time.Second
	staleAfter     = 15 * time.Second
)

// Start creates and launches a job. The context only covers persistence of
// the initial snapshot; execution is intentionally detached from the caller.
func (m *Manager) Start(ctx context.Context, t tool.Tool, input map[string]any) (Job, error) {
	def := t.Definition()
	now := time.Now().UTC()
	id, err := newID(now)
	if err != nil {
		return Job{}, err
	}
	workDir := filepath.Join(m.root, id)
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		return Job{}, fmt.Errorf("create job directory: %w", err)
	}
	job := Job{
		ID:        id,
		ToolID:    def.ID,
		Revision:  1,
		Status:    StatusQueued,
		Input:     input,
		WorkDir:   workDir,
		CreatedAt: now,
	}
	runCtx, cancel := context.WithCancel(context.Background())
	run := &runState{job: job, ctx: runCtx, cancel: cancel, done: make(chan struct{}), manager: m}

	m.mu.Lock()
	m.runs[id] = run
	m.mu.Unlock()

	if err := m.store.Create(ctx, &job); err != nil {
		cancel()
		m.mu.Lock()
		delete(m.runs, id)
		m.mu.Unlock()
		return Job{}, fmt.Errorf("save job: %w", err)
	}
	m.emit("job.created", job)
	go m.execute(t, run)
	return job, nil
}

func (m *Manager) execute(t tool.Tool, run *runState) {
	run.mu.Lock()
	jobID := run.job.ID
	run.mu.Unlock()
	defer close(run.done)
	defer func() {
		m.mu.Lock()
		delete(m.runs, jobID)
		m.mu.Unlock()
	}()

	run.mu.Lock()
	if run.job.Status == StatusCanceling {
		finished := time.Now().UTC()
		run.job.Status = StatusCanceled
		run.job.FinishedAt = &finished
		run.job.Revision++
		job := cloneJob(run.job)
		run.mu.Unlock()
		m.persistAndEmit("job.finished", &job)
		return
	}
	started := time.Now().UTC()
	run.job.Status = StatusRunning
	run.job.StartedAt = &started
	run.job.Revision++
	job := cloneJob(run.job)
	run.mu.Unlock()

	m.persistAndEmit("job.started", &job)

	var final *tool.Result
	var runErr error
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				runErr = fmt.Errorf("tool panicked: %v", recovered)
				run.report("error", string(debug.Stack()))
			}
		}()
		result, err := t.Run(run.ctx, tool.Input{Values: cloneMap(job.Input)}, tool.Environment{
			WorkDir: job.WorkDir,
			DataDir: filepath.Dir(m.root),
		}, reporter{run: run})
		if err != nil {
			runErr = err
			return
		}
		if err := tool.ValidateResult(result); err != nil {
			runErr = err
			return
		}
		final = &result
	}()

	run.mu.Lock()
	canceled := run.job.Status == StatusCanceling || run.ctx.Err() != nil
	run.mu.Unlock()
	run.cancel()
	finished := time.Now().UTC()
	run.mu.Lock()
	run.job.FinishedAt = &finished
	run.job.Revision++
	updated := cloneJob(run.job)
	switch {
	case canceled || errors.Is(runErr, context.Canceled):
		updated.Status = StatusCanceled
	case runErr != nil:
		updated.Status = StatusFailed
		updated.Error = runErr.Error()
	default:
		updated.Status = StatusSucceeded
		updated.Result = final
		updated.Progress = Progress{Done: max(run.job.Progress.Done, run.job.Progress.Total), Total: max(run.job.Progress.Total, run.job.Progress.Done)}
	}
	if updated.Status == StatusSucceeded {
		if err := normalizeArtifacts(&updated); err != nil {
			updated.Status = StatusFailed
			updated.Error = err.Error()
		}
	}
	run.job = updated
	job = updated
	run.mu.Unlock()

	m.persistAndEmit("job.finished", &job)
}

func normalizeArtifacts(job *Job) error {
	if job.Result == nil {
		return nil
	}
	env := tool.Environment{WorkDir: job.WorkDir}
	for i := range job.Result.Artifacts {
		artifact := &job.Result.Artifacts[i]
		path, size, err := tool.ResolveArtifact(env, artifact.Path)
		if err != nil {
			return fmt.Errorf("artifact %q: %w", artifact.Name, err)
		}
		artifact.Path = path
		if artifact.Name == "" {
			artifact.Name = filepath.Base(path)
		}
		if artifact.MIME == "" {
			artifact.MIME = mimeFor(path)
		}
		_ = size
	}
	return nil
}

func (m *Manager) persistAndEmit(kind string, job *Job) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := m.store.Update(ctx, job); err != nil {
		if errors.Is(err, ErrStaleRevision) {
			return
		}
		m.logger.Error("persist job", "job", job.ID, "status", job.Status, "error", err)
	}
	m.emit(kind, *job)
}

// Get returns the latest snapshot of a job.
func (m *Manager) Get(ctx context.Context, id string) (Job, error) {
	m.mu.RLock()
	run := m.runs[id]
	m.mu.RUnlock()
	if run != nil {
		run.mu.Lock()
		defer run.mu.Unlock()
		return cloneJob(run.job), nil
	}
	job, err := m.store.Get(ctx, id)
	if err != nil {
		return Job{}, err
	}
	return *job, nil
}

// List returns recent jobs, newest first.
func (m *Manager) List(ctx context.Context, limit int) ([]Job, error) {
	jobs, err := m.store.List(ctx, limit)
	if err != nil {
		return nil, err
	}
	latest := make(map[string]Job, len(jobs)+len(m.runs))
	for _, job := range jobs {
		latest[job.ID] = job
	}
	m.mu.RLock()
	for _, run := range m.runs {
		run.mu.Lock()
		latest[run.job.ID] = cloneJob(run.job)
		run.mu.Unlock()
	}
	m.mu.RUnlock()
	jobs = jobs[:0]
	for _, job := range latest {
		jobs = append(jobs, job)
	}
	sort.Slice(jobs, func(i, j int) bool {
		return jobs[i].CreatedAt.After(jobs[j].CreatedAt)
	})
	if limit <= 0 {
		limit = 50
	}
	limit = min(limit, 500)
	if len(jobs) > limit {
		jobs = jobs[:limit]
	}
	return jobs, nil
}

// Cancel requests cancellation and returns immediately.
func (m *Manager) Cancel(ctx context.Context, id string) (Job, error) {
	m.mu.RLock()
	run := m.runs[id]
	m.mu.RUnlock()
	if run == nil {
		job, err := m.store.Get(ctx, id)
		if err != nil {
			return Job{}, err
		}
		if job.Status.Active() {
			return *job, fmt.Errorf("job %s belongs to another OpenTools process and cannot be canceled here", id)
		}
		return *job, fmt.Errorf("job %s is not running", id)
	}

	run.mu.Lock()
	if !run.job.Status.Active() {
		job := cloneJob(run.job)
		run.mu.Unlock()
		return job, fmt.Errorf("job %s has already finished", id)
	}
	run.job.Status = StatusCanceling
	run.job.Revision++
	cancel := run.cancel
	job := cloneJob(run.job)
	run.mu.Unlock()
	m.persistAndEmit("job.canceling", &job)
	cancel()
	return job, nil
}

// Close stops maintenance and gives in-flight tools a bounded chance to
// observe cancellation before the database is closed.
func (m *Manager) Close() error {
	m.stop()
	m.wg.Wait()

	m.mu.RLock()
	runs := make([]*runState, 0, len(m.runs))
	for _, run := range m.runs {
		runs = append(runs, run)
	}
	m.mu.RUnlock()
	for _, run := range runs {
		run.cancel()
	}

	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for _, run := range runs {
		select {
		case <-run.done:
		case <-deadline.C:
			return errors.New("timed out waiting for jobs to stop")
		}
	}
	return m.clearHistory(context.Background())
}

// clearHistory deletes every job record and its work directory. The desktop
// app calls it on exit so results never outlive the process.
func (m *Manager) clearHistory(ctx context.Context) error {
	if err := m.store.Clear(ctx); err != nil {
		return err
	}
	entries, err := os.ReadDir(m.root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var firstErr error
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(m.root, entry.Name())); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (m *Manager) maintain() {
	defer m.wg.Done()
	ticker := time.NewTicker(heartbeatEvery)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
			m.heartbeat(ctx)
			if err := m.store.MarkInterrupted(ctx, time.Now().UTC().Add(-staleAfter)); err != nil && !errors.Is(err, context.Canceled) {
				m.logger.Error("recover interrupted jobs", "error", err)
			}
			cancel()
		}
	}
}

func (m *Manager) heartbeat(ctx context.Context) {
	m.mu.RLock()
	ids := make([]string, 0, len(m.runs))
	for id := range m.runs {
		ids = append(ids, id)
	}
	m.mu.RUnlock()
	if len(ids) == 0 {
		return
	}
	if err := m.store.Heartbeat(ctx, ids, time.Now().UTC()); err != nil && !errors.Is(err, context.Canceled) {
		m.logger.Error("heartbeat jobs", "error", err)
	}
}

// TightenArtifactPermissions removes group and other access after a tool
// writes into its private job directory.
func (m *Manager) TightenArtifactPermissions(jobID string) {
	m.mu.RLock()
	run := m.runs[jobID]
	m.mu.RUnlock()
	if run == nil {
		return
	}
	run.mu.Lock()
	workDir := run.job.WorkDir
	run.mu.Unlock()
	if workDir == "" {
		return
	}
	_ = filepath.WalkDir(workDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		mode := os.FileMode(0o600)
		if entry.IsDir() {
			mode = 0o700
		}
		_ = os.Chmod(path, mode)
		return nil
	})
}

// Subscribe returns a bounded stream of job changes. Events are hints: a
// client that misses one can reload snapshots through Get and List.
func (m *Manager) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 64)
	m.mu.Lock()
	id := m.nextSub
	m.nextSub++
	m.subscribers[id] = ch
	m.mu.Unlock()
	return ch, func() {
		m.mu.Lock()
		if existing, ok := m.subscribers[id]; ok {
			delete(m.subscribers, id)
			close(existing)
		}
		m.mu.Unlock()
	}
}

func (m *Manager) emit(kind string, job Job) {
	event := Event{Kind: kind, Job: cloneJob(job)}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, ch := range m.subscribers {
		select {
		case ch <- event:
		default:
			m.logger.Warn("drop task event for slow subscriber", "job", job.ID, "kind", kind)
		}
	}
}

type reporter struct {
	run *runState
}

func (r reporter) Log(level, message string) {
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}
	line := LogEntry{At: time.Now().UTC(), Level: level, Message: message}
	r.run.mu.Lock()
	if len(r.run.job.Logs) < 2000 {
		r.run.job.Logs = append(r.run.job.Logs, line)
	}
	r.run.job.Revision++
	job := cloneJob(r.run.job)
	r.run.mu.Unlock()
	r.run.manager.persistThrottled(r.run, "job.log", &job)
}

func (r reporter) Progress(done, total int64) {
	r.run.mu.Lock()
	r.run.job.Progress = Progress{Done: done, Total: total}
	r.run.job.Revision++
	job := cloneJob(r.run.job)
	r.run.mu.Unlock()
	r.run.manager.persistThrottled(r.run, "job.progress", &job)
}

func (r reporter) Section(section tool.Section) {
	if tool.ValidateSection(section) != nil {
		return
	}
	r.run.mu.Lock()
	index := -1
	for i := range r.run.job.Sections {
		if r.run.job.Sections[i].ID == section.ID {
			index = i
			break
		}
	}
	if index >= 0 {
		r.run.job.Sections[index] = section
	} else {
		r.run.job.Sections = append(r.run.job.Sections, section)
	}
	r.run.job.Revision++
	job := cloneJob(r.run.job)
	r.run.mu.Unlock()
	r.run.manager.persistThrottled(r.run, "job.section", &job)
}

// persistThrottled keeps a long log stream from forcing a database write per
// line. The final snapshot is always written by execute.
func (m *Manager) persistThrottled(run *runState, kind string, job *Job) {
	now := time.Now()
	run.mu.Lock()
	if now.Sub(run.lastSave) < 200*time.Millisecond {
		run.mu.Unlock()
		m.emit(kind, *job)
		return
	}
	run.lastSave = now
	run.mu.Unlock()
	m.persistAndEmit(kind, job)
}

func (r *runState) report(level, message string) {
	r.mu.Lock()
	if len(r.job.Logs) < 2000 {
		r.job.Logs = append(r.job.Logs, LogEntry{At: time.Now().UTC(), Level: level, Message: message})
	}
	r.job.Revision++
	r.mu.Unlock()
}

func cloneJob(job Job) Job {
	job.Input = cloneMap(job.Input)
	job.Logs = append([]LogEntry(nil), job.Logs...)
	job.Sections = cloneSections(job.Sections)
	return job
}

func cloneSections(sections []tool.Section) []tool.Section {
	out := append([]tool.Section(nil), sections...)
	for i := range out {
		out[i].Blocks = append([]tool.Block(nil), out[i].Blocks...)
	}
	return out
}

func cloneMap(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	data, err := json.Marshal(values)
	if err != nil {
		out := make(map[string]any, len(values))
		for key, value := range values {
			out[key] = value
		}
		return out
	}
	var out map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&out); err != nil {
		out = make(map[string]any, len(values))
		for key, value := range values {
			out[key] = value
		}
	}
	return out
}

func mimeFor(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		return "application/json"
	case ".csv":
		return "text/csv"
	case ".txt", ".log", ".md":
		return "text/plain"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".pdf":
		return "application/pdf"
	default:
		return "application/octet-stream"
	}
}
