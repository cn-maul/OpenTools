// Package appdata resolves the same data directory for the desktop app and
// the CLI, independent of either transport.
package appdata

import (
	"os"
	"path/filepath"
	"runtime"
)

const appName = "OpenTools"

// Dir returns the OpenTools data directory: a "data" folder next to the
// executable for portable use, falling back to the per-user config directory
// when that is not writable. OPENTOOLS_DATA_DIR overrides both.
func Dir() (string, error) {
	if override := os.Getenv("OPENTOOLS_DATA_DIR"); override != "" {
		return filepath.Abs(override)
	}
	// 便携模式：数据放在 exe 同级的 data 目录，整个文件夹拷走即可迁移。
	if executable, err := os.Executable(); err == nil {
		portable := filepath.Join(filepath.Dir(executable), "data")
		if err := os.MkdirAll(portable, 0o700); err == nil {
			if probe := filepath.Join(portable, ".writable"); os.WriteFile(probe, nil, 0o600) == nil {
				_ = os.Remove(probe)
				return filepath.Abs(portable)
			}
		}
	}
	// exe 目录不可写（如 Program Files）时退回用户目录。
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "linux" {
		if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
			base = xdg
		}
	}
	return filepath.Join(base, appName), nil
}
