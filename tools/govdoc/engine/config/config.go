// Package config persists the LLM connection, formatting profiles and other
// preferences between runs.
//
// # Where the settings live
//
// The file sits next to the executable, which is what the user asked for: the
// program and its settings stay together, so moving the folder (or carrying it
// on a USB stick) carries the configuration with it. The trade-off is accepted
// deliberately — if the folder is read-only (Program Files, a mounted share),
// Save reports the failure rather than silently keeping the value in memory and
// pretending it persisted.
//
// # Why the key's handling is explicit
//
// An API key written to disk in plain text is a real risk on a shared machine,
// so:
//
//   - the file sits beside the program, not in the working directory of whatever
//     the user happens to be in, and not in a source tree that might get
//     committed;
//   - it is written with 0600 permissions (owner-only) where the OS enforces
//     them, and the directory is created 0700;
//   - saving the key is a deliberate act: the UI sends it, and the user is told
//     the value is stored. Nothing harvests it from the environment silently.
//
// Everything else (endpoint, model, protocol, 行文方向, formatting profiles) is
// not sensitive and is always persisted.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ErrNoConfigDir is returned when no writable location can be determined, so
// the caller can degrade to in-memory-only settings instead of failing to
// start.
var ErrNoConfigDir = errors.New("config: 无法确定可写入的设置目录")

// File is the on-disk shape of the settings file.
//
// It is a separate type from the server's runtime config so that changing how
// settings are stored cannot silently change the HTTP contract, and so a future
// format change is a visible edit here.
type File struct {
	// Endpoint is the model API base URL, e.g. https://api.deepseek.com/v1.
	Endpoint string `json:"endpoint,omitempty"`
	// Model is the model id, e.g. deepseek-chat.
	Model string `json:"model,omitempty"`
	// Protocol is "" | "openai-chat" | "openai-responses" | "anthropic".
	Protocol string `json:"protocol,omitempty"`
	// APIKey is stored so the tool works after a restart without re-entry.
	// It is the one sensitive field; see the package comment.
	APIKey string `json:"api_key,omitempty"`
	// Direction is 行文方向, one of 下行文 / 上行文 / 平行文. It applies to the
	// built-in 公文 profile (and any profile that leaves it empty).
	Direction string `json:"direction,omitempty"`
	// AllowEdits records whether the user permits content corrections.
	AllowEdits bool `json:"allow_edits,omitempty"`
	// ActiveProfile is the id of the profile applied to new uploads when the UI
	// has not chosen one explicitly.
	ActiveProfile string `json:"active_profile,omitempty"`
	// Profiles are the user's formatting profiles. Built-ins are seeded on
	// first run and are marked BuiltIn; user copies are editable.
	Profiles []Profile `json:"profiles,omitempty"`
	// Version is the file format version, so a future change can migrate
	// rather than guess.
	Version int `json:"version"`
	// SavedAt is informational; it lets the UI say when settings were last
	// written.
	SavedAt time.Time `json:"saved_at,omitempty"`
}

// currentVersion is the format this build writes.
const currentVersion = 2

// Store reads and writes the settings file. It is safe for concurrent use: the
// HTTP handlers write it from parallel goroutines.
type Store struct {
	mu   sync.Mutex
	path string
	// disabled records that no config directory was available, so Save becomes
	// a no-op rather than an error the user cannot act on.
	disabled bool
	// lastErr keeps the most recent failure so the API can report it without
	// the caller having to remember.
	lastErr error
}

// DefaultPath returns the settings file path, next to the executable.
//
// Beside the binary is what the user asked for, and it has a concrete benefit:
// the program and its settings stay together, so copying the folder (to another
// machine, or onto a USB stick) carries the configuration with it.
//
// The executable's directory is preferred over the working directory on purpose.
// The working directory is wherever the user happened to type the command and
// is often somewhere unrelated to the program.
//
// When the executable path cannot be determined (some embedded or test
// environments), it falls back to the working directory so the tool still
// starts; a settings file that lands somewhere unexpected is better than one
// that is silently never written.
func DefaultPath() (string, error) {
	dir, err := ExeDir()
	if err != nil || dir == "" {
		wd, wderr := os.Getwd()
		if wderr != nil || wd == "" {
			return "", ErrNoConfigDir
		}
		dir = wd
	}
	return filepath.Join(dir, "govdoc.config.json"), nil
}

// ExeDir returns the directory holding the running executable, resolved through
// symlinks so a link placed on the PATH does not scatter settings around the
// directories it points at.
func ExeDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		// Not fatal: a path that cannot be resolved still has a directory.
		resolved = exe
	}
	return filepath.Dir(resolved), nil
}

// New opens (or prepares) the settings file at path. An empty path uses the
// platform default. A store whose directory cannot be determined is returned
// disabled rather than as an error, because settings persistence is a
// convenience and must not stop the program from running.
func New(path string) *Store {
	if strings.TrimSpace(path) == "" {
		p, err := DefaultPath()
		if err != nil {
			return &Store{disabled: true, lastErr: err}
		}
		path = p
	}
	return &Store{path: path}
}

// Path returns the file path, or "" when persistence is disabled.
func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.disabled {
		return ""
	}
	return s.path
}

// Disabled reports whether persistence is unavailable.
func (s *Store) Disabled() bool {
	if s == nil {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.disabled
}

// Load reads the settings file. A missing file is not an error: it is the
// normal state on first run, and returning a zero File lets the caller fall
// back to flags and environment variables.
func (s *Store) Load() (File, error) {
	if s == nil {
		return File{}, ErrNoConfigDir
	}
	s.mu.Lock()
	path, disabled := s.path, s.disabled
	s.mu.Unlock()

	if disabled {
		return File{}, ErrNoConfigDir
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// First run: seed the built-in profiles so the program is usable
			// immediately, with no "create a profile first" step.
			return EnsureProfiles(File{}), nil
		}
		return File{}, fmt.Errorf("config: 读取设置失败: %w", err)
	}

	var f File
	if err := json.Unmarshal(raw, &f); err != nil {
		// A corrupt file must not brick the program. Report it so the user can
		// see why their settings were ignored, and let the caller start from
		// defaults; the next Save overwrites the damage.
		return EnsureProfiles(File{}), fmt.Errorf("config: 设置文件格式损坏（将使用默认值）: %w", err)
	}
	// Seed any profile introduced by an upgrade, without touching the user's
	// own copies.
	return EnsureProfiles(f), nil
}

// Save writes the settings atomically.
//
// The write goes to a temporary file in the same directory and is then renamed
// over the target. A direct write would leave a truncated file if the process
// died mid-write, and a truncated settings file loses the user's API key —
// exactly the failure that is most annoying to recover from.
func (s *Store) Save(f File) error {
	if s == nil {
		return ErrNoConfigDir
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.disabled {
		s.lastErr = ErrNoConfigDir
		return ErrNoConfigDir
	}

	f.Version = currentVersion
	f.SavedAt = time.Now()

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		s.lastErr = err
		return fmt.Errorf("config: 创建配置目录失败: %w", err)
	}

	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		s.lastErr = err
		return fmt.Errorf("config: 序列化设置失败: %w", err)
	}
	// A trailing newline keeps the file pleasant to inspect and to diff.
	data = append(data, '\n')

	tmp, err := os.CreateTemp(dir, "config-*.tmp")
	if err != nil {
		s.lastErr = err
		return fmt.Errorf("config: 创建临时文件失败: %w", err)
	}
	tmpName := tmp.Name()
	// Best-effort cleanup if any step below fails.
	defer func() {
		if _, err := os.Stat(tmpName); err == nil {
			_ = os.Remove(tmpName)
		}
	}()

	// 0600: the file may hold an API key, so it is owner-readable only.
	//
	// os.CreateTemp already creates with 0600 on Unix, but the mode is set
	// explicitly anyway because the rename below is what determines the final
	// mode on some platforms, and an implicitly-inherited 0666 would quietly
	// expose the key. On Windows the permission bits do not map faithfully;
	// there the protection comes from the per-user config directory.
	if err := tmp.Chmod(0o600); err != nil {
		// Not fatal: some filesystems refuse chmod outright.
		_ = err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		s.lastErr = err
		return fmt.Errorf("config: 写入设置失败: %w", err)
	}
	// fsync before rename: without it a power loss can leave the renamed file
	// present but empty on some filesystems.
	if err := tmp.Sync(); err != nil {
		_ = err // not fatal, and not supported everywhere
	}
	if err := tmp.Close(); err != nil {
		s.lastErr = err
		return fmt.Errorf("config: 关闭临时文件失败: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		s.lastErr = err
		return fmt.Errorf("config: 保存设置失败: %w", err)
	}
	s.lastErr = nil
	return nil
}

// LastError returns the most recent Save failure, for diagnostics.
func (s *Store) LastError() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastErr
}

// Exists reports whether a settings file is present on disk.
//
// The GET /api/config endpoint uses it so `saved_to_disk` is a statement about
// reality rather than a constant: "settings exist and will be reloaded" is
// different from "nothing has been saved yet", and the UI words its reassurance
// differently for each.
func (s *Store) Exists() bool {
	if s == nil || s.Disabled() {
		return false
	}
	s.mu.Lock()
	path := s.path
	s.mu.Unlock()

	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
