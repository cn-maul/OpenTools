package tasks

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/cn-maul/OpenTools/sdk/tool"
)

// fakeTool is a controllable tool for lifecycle tests.
type fakeTool struct {
	def tool.Definition
	run func(ctx context.Context, input tool.Input, env tool.Environment, report tool.Reporter) (tool.Result, error)
}

func (f fakeTool) Definition() tool.Definition { return f.def }

func (f fakeTool) Run(ctx context.Context, input tool.Input, env tool.Environment, report tool.Reporter) (tool.Result, error) {
	return f.run(ctx, input, env, report)
}

func testToolDefinition(id string) tool.Definition {
	return tool.Definition{
		ID:       id,
		Name:     "测试",
		Summary:  "测试",
		Category: "测试",
		Version:  "1.0.0",
		Input:    tool.Object(map[string]*tool.Schema{}),
	}
}

// memoryStore is a minimal in-memory Store implementation.
type memoryStore struct {
	mu    sync.Mutex
	jobs  map[string]Job
	order []string
}

func newMemoryStore() *memoryStore {
	return &memoryStore{jobs: map[string]Job{}}
}

func (s *memoryStore) Create(_ context.Context, job *Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[job.ID] = *job
	s.order = append(s.order, job.ID)
	return nil
}

func (s *memoryStore) Update(_ context.Context, job *Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[job.ID] = *job
	return nil
}

func (s *memoryStore) Heartbeat(_ context.Context, ids []string, _ time.Time) error {
	return nil
}

func (s *memoryStore) Get(_ context.Context, id string) (*Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return &job, nil
}

func (s *memoryStore) List(_ context.Context, limit int) ([]Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Job, 0, len(s.order))
	for _, id := range s.order {
		out = append(out, s.jobs[id])
	}
	return out, nil
}

func (s *memoryStore) ActiveToolIDs(_ context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ids []string
	for _, job := range s.jobs {
		if job.Status.Active() {
			ids = append(ids, job.ToolID)
		}
	}
	return ids, nil
}

func (s *memoryStore) MarkInterrupted(_ context.Context, _ time.Time) error { return nil }
func (s *memoryStore) Clear(_ context.Context) error                        { return nil }

func newTestManager(t *testing.T) (*Manager, *memoryStore) {
	t.Helper()
	store := newMemoryStore()
	manager, err := NewManager(context.Background(), t.TempDir(), t.TempDir(), store, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	return manager, store
}

func TestStartAndComplete(t *testing.T) {
	manager, store := newTestManager(t)
	impl := fakeTool{def: testToolDefinition("test.ok")}
	impl.run = func(ctx context.Context, input tool.Input, env tool.Environment, report tool.Reporter) (tool.Result, error) {
		report.Log("info", "工作中")
		report.Progress(1, 1)
		return tool.Result{Summary: "完成"}, nil
	}

	job, err := manager.Start(context.Background(), impl, map[string]any{}, nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		snapshot, err := manager.Get(context.Background(), job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !snapshot.Status.Active() {
			if snapshot.Status != StatusSucceeded {
				t.Fatalf("status = %q, want succeeded (error: %s)", snapshot.Status, snapshot.Error)
			}
			if snapshot.Result == nil || snapshot.Result.Summary != "完成" {
				t.Fatalf("unexpected result: %+v", snapshot.Result)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for job to finish")
		}
		time.Sleep(10 * time.Millisecond)
	}

	persisted, err := store.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != StatusSucceeded {
		t.Fatalf("persisted status = %q", persisted.Status)
	}
}

func TestCancelRunningJob(t *testing.T) {
	manager, _ := newTestManager(t)
	started := make(chan struct{})
	impl := fakeTool{def: testToolDefinition("test.slow")}
	impl.run = func(ctx context.Context, input tool.Input, env tool.Environment, report tool.Reporter) (tool.Result, error) {
		close(started)
		<-ctx.Done()
		return tool.Result{}, ctx.Err()
	}

	job, err := manager.Start(context.Background(), impl, map[string]any{}, nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	<-started

	updated, err := manager.Cancel(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if updated.Status != StatusCanceling {
		t.Fatalf("status after cancel = %q, want canceling", updated.Status)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		snapshot, err := manager.Get(context.Background(), job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.Status == StatusCanceled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job did not reach canceled state, status = %q", snapshot.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestFailedJobRecordsError(t *testing.T) {
	manager, _ := newTestManager(t)
	impl := fakeTool{def: testToolDefinition("test.fail")}
	impl.run = func(ctx context.Context, input tool.Input, env tool.Environment, report tool.Reporter) (tool.Result, error) {
		return tool.Result{}, errors.New("工具出错了")
	}

	job, err := manager.Start(context.Background(), impl, map[string]any{}, nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		snapshot, err := manager.Get(context.Background(), job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !snapshot.Status.Active() {
			if snapshot.Status != StatusFailed {
				t.Fatalf("status = %q, want failed", snapshot.Status)
			}
			if snapshot.Error != "工具出错了" {
				t.Fatalf("error = %q", snapshot.Error)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestToolDataDirIsPerTool(t *testing.T) {
	manager, _ := newTestManager(t)
	dataRoot := t.TempDir()
	manager.dataDir = dataRoot

	var seen string
	impl := fakeTool{def: testToolDefinition("test.datadir")}
	impl.run = func(ctx context.Context, input tool.Input, env tool.Environment, report tool.Reporter) (tool.Result, error) {
		seen = env.DataDir
		return tool.Result{Summary: "ok"}, nil
	}
	job, err := manager.Start(context.Background(), impl, map[string]any{}, nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		snapshot, err := manager.Get(context.Background(), job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !snapshot.Status.Active() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
	want := filepath.Join(dataRoot, "tools", "test.datadir", "data")
	if seen != want {
		t.Fatalf("DataDir = %q, want %q", seen, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("data dir was not created: %v", err)
	}
}

func TestSecretInputIsRedacted(t *testing.T) {
	// Redaction is the application layer's job: it passes the tool the real
	// input and hands the manager a redacted copy for persistence and
	// display. The manager must keep the two apart.
	manager, store := newTestManager(t)
	def := testToolDefinition("test.secret")

	var toolSaw string
	impl := fakeTool{def: def}
	impl.run = func(ctx context.Context, input tool.Input, env tool.Environment, report tool.Reporter) (tool.Result, error) {
		toolSaw, _ = input.Values["token"].(string)
		return tool.Result{Summary: "ok"}, nil
	}

	realInput := map[string]any{"token": "super-secret"}
	redacted := map[string]any{"token": tool.RedactedPlaceholder}
	job, err := manager.Start(context.Background(), impl, realInput, redacted)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if job.Input["token"] != tool.RedactedPlaceholder {
		t.Fatalf("in-memory job input not redacted: %v", job.Input)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		snapshot, err := manager.Get(context.Background(), job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !snapshot.Status.Active() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if toolSaw != "super-secret" {
		t.Fatalf("tool saw %q, want the real value", toolSaw)
	}
	persisted, err := store.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Input["token"] != tool.RedactedPlaceholder {
		t.Fatalf("persisted input not redacted: %v", persisted.Input)
	}
}

func TestHasActive(t *testing.T) {
	manager, _ := newTestManager(t)
	started := make(chan struct{})
	release := make(chan struct{})
	impl := fakeTool{def: testToolDefinition("test.active")}
	impl.run = func(ctx context.Context, input tool.Input, env tool.Environment, report tool.Reporter) (tool.Result, error) {
		close(started)
		<-release
		return tool.Result{Summary: "ok"}, nil
	}

	job, err := manager.Start(context.Background(), impl, map[string]any{}, nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	<-started

	active, err := manager.HasActive(context.Background(), "test.active")
	if err != nil {
		t.Fatal(err)
	}
	if !active {
		t.Fatal("expected active job")
	}
	close(release)

	deadline := time.Now().Add(5 * time.Second)
	for {
		snapshot, err := manager.Get(context.Background(), job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !snapshot.Status.Active() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
	active, err = manager.HasActive(context.Background(), "test.active")
	if err != nil {
		t.Fatal(err)
	}
	if active {
		t.Fatal("expected no active job after completion")
	}
}
