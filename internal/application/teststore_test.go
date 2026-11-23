package application

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/cn-maul/OpenTools/internal/tasks"
)

// failingStore fails on write so a test can assert the application layer
// never persists unredacted input even when persistence breaks.
type failingStore struct {
	mu   sync.Mutex
	jobs map[string]tasks.Job
}

func newFailingStore(t interface{ TempDir() string }) *failingStore {
	return &failingStore{jobs: map[string]tasks.Job{}}
}

func (s *failingStore) Create(_ context.Context, job *tasks.Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[job.ID] = *job
	return nil
}

func (s *failingStore) Update(_ context.Context, job *tasks.Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[job.ID] = *job
	return nil
}

func (s *failingStore) Heartbeat(_ context.Context, _ []string, _ time.Time) error { return nil }

func (s *failingStore) Get(_ context.Context, id string) (*tasks.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return &job, nil
}

func (s *failingStore) List(_ context.Context, limit int) ([]tasks.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]tasks.Job, 0, len(s.jobs))
	for _, job := range s.jobs {
		out = append(out, job)
	}
	return out, nil
}

func (s *failingStore) ActiveToolIDs(_ context.Context) ([]string, error) {
	return nil, nil
}

func (s *failingStore) MarkInterrupted(_ context.Context, _ time.Time) error { return nil }
func (s *failingStore) Clear(_ context.Context) error                        { return nil }
