// Package sqlite is the durable job store for OpenTools.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"opentools/internal/tasks"
)

// Store keeps jobs in a SQLite database with WAL enabled, so the desktop app
// and CLI can safely use the same history.
type Store struct {
	db *sql.DB
}

// Open creates or opens the database at path.
func Open(ctx context.Context, path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	// One connection per process still allows the other process to access the
	// database; SQLite serializes writers and WAL allows concurrent readers.
	db.SetMaxOpenConns(1)
	store := &Store{db: db}
	if err := store.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func dsn(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		absolute = path
	}
	slash := filepath.ToSlash(absolute)
	if !strings.HasPrefix(slash, "/") {
		slash = "/" + slash
	}
	u := &url.URL{Scheme: "file", Path: slash}
	q := u.Query()
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(NORMAL)")
	u.RawQuery = q.Encode()
	return u.String()
}

func (s *Store) migrate(ctx context.Context) error {
	const schema = `
CREATE TABLE IF NOT EXISTS jobs (
	id          TEXT PRIMARY KEY,
	tool_id     TEXT NOT NULL,
	status      TEXT NOT NULL,
	created_at  TEXT NOT NULL,
	updated_at  TEXT NOT NULL,
	revision    INTEGER NOT NULL,
	snapshot    BLOB NOT NULL
);
CREATE INDEX IF NOT EXISTS jobs_created_at ON jobs(created_at DESC);
CREATE INDEX IF NOT EXISTS jobs_tool_id ON jobs(tool_id, created_at DESC);
CREATE INDEX IF NOT EXISTS jobs_status ON jobs(status, created_at DESC);
PRAGMA user_version = 1;
`
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}
	return nil
}

// Close closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}

// Clear deletes every job snapshot.
func (s *Store) Clear(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM jobs`); err != nil {
		return fmt.Errorf("clear jobs: %w", err)
	}
	return nil
}

// Create inserts a job snapshot.
func (s *Store) Create(ctx context.Context, job *tasks.Job) error {
	if job.Revision == 0 {
		job.Revision = 1
	}
	data, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("encode job: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO jobs (id, tool_id, status, created_at, updated_at, revision, snapshot)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		job.ID, job.ToolID, string(job.Status), formatTime(job.CreatedAt),
		formatTime(time.Now().UTC()), job.Revision, data)
	if err != nil {
		return fmt.Errorf("insert job: %w", err)
	}
	return nil
}

// Update writes a newer job snapshot. An older revision is rejected so
// progress events cannot overwrite completion.
func (s *Store) Update(ctx context.Context, job *tasks.Job) error {
	if job.Revision <= 0 {
		return errors.New("update job: revision must be positive")
	}
	data, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("encode job: %w", err)
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE jobs SET status = ?, updated_at = ?, revision = ?, snapshot = ?
WHERE id = ? AND revision < ?`,
		string(job.Status), formatTime(time.Now().UTC()), job.Revision, data, job.ID, job.Revision)
	if err != nil {
		return fmt.Errorf("update job: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("update job: %w", err)
	}
	if affected == 0 {
		return tasks.ErrStaleRevision
	}
	return nil
}

// Heartbeat marks active jobs as still owned by a live OpenTools process
// without changing their snapshots or revisions.
func (s *Store) Heartbeat(ctx context.Context, ids []string, at time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids)+2)
	args = append(args, formatTime(at), formatTime(at))
	for _, id := range ids {
		args = append(args, id)
	}
	_, err := s.db.ExecContext(ctx, `
UPDATE jobs SET updated_at = ?
WHERE status IN ('queued', 'running', 'canceling') AND updated_at < ? AND id IN (`+placeholders+`)`,
		args...)
	if err != nil {
		return fmt.Errorf("heartbeat jobs: %w", err)
	}
	return nil
}

// Get loads one job.
func (s *Store) Get(ctx context.Context, id string) (*tasks.Job, error) {
	var data []byte
	var revision int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT snapshot, revision FROM jobs WHERE id = ?`, id,
	).Scan(&data, &revision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("job %q not found", id)
		}
		return nil, fmt.Errorf("load job: %w", err)
	}
	job, err := decodeJob(data, revision)
	if err != nil {
		return nil, err
	}
	return job, nil
}

// List returns recent jobs, newest first.
func (s *Store) List(ctx context.Context, limit int) ([]tasks.Job, error) {
	if limit <= 0 {
		limit = 50
	}
	limit = min(limit, 500)
	rows, err := s.db.QueryContext(ctx,
		`SELECT snapshot, revision FROM jobs ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	defer rows.Close()
	var jobs []tasks.Job
	for rows.Next() {
		var data []byte
		var revision int64
		if err := rows.Scan(&data, &revision); err != nil {
			return nil, fmt.Errorf("scan job: %w", err)
		}
		job, err := decodeJob(data, revision)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, *job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	return jobs, nil
}

// MarkInterrupted fails active jobs whose heartbeat is older than before.
// A job owned by another live process has a fresh heartbeat and is untouched.
// These jobs cannot be resumed safely without a durable execution log.
func (s *Store) MarkInterrupted(ctx context.Context, before time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("recover interrupted jobs: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `
SELECT snapshot, revision FROM jobs
WHERE status IN ('queued', 'running', 'canceling') AND updated_at < ?`,
		formatTime(before))
	if err != nil {
		return fmt.Errorf("find interrupted jobs: %w", err)
	}
	var interrupted []*tasks.Job
	for rows.Next() {
		var data []byte
		var revision int64
		if err := rows.Scan(&data, &revision); err != nil {
			rows.Close()
			return fmt.Errorf("scan interrupted job: %w", err)
		}
		job, err := decodeJob(data, revision)
		if err != nil {
			rows.Close()
			return err
		}
		interrupted = append(interrupted, job)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("scan interrupted jobs: %w", err)
	}
	rows.Close()

	for _, job := range interrupted {
		now := time.Now().UTC()
		job.Status = tasks.StatusFailed
		job.Error = "OpenTools stopped before the job finished"
		job.FinishedAt = &now
		job.Revision++
		data, err := json.Marshal(job)
		if err != nil {
			return fmt.Errorf("encode interrupted job: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE jobs SET status = ?, updated_at = ?, revision = ?, snapshot = ?
WHERE id = ? AND revision = ? AND updated_at < ?`,
			string(job.Status), formatTime(now), job.Revision, data,
			job.ID, job.Revision-1, formatTime(before)); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit interrupted jobs: %w", err)
	}
	return nil
}

func decodeJob(data []byte, revision int64) (*tasks.Job, error) {
	var job tasks.Job
	if err := json.Unmarshal(data, &job); err != nil {
		return nil, fmt.Errorf("decode job: %w", err)
	}
	job.Revision = revision
	return &job, nil
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}
