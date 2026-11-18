package desktop

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// Appearance modes. "system" follows the desktop; the other two pin the
// shell to one appearance regardless of the system setting.
const (
	themeSystem = "system"
	themeLight  = "light"
	themeDark   = "dark"
)

// themePreference is the small settings file the desktop shell owns. It is
// deliberately next to the jobs database rather than in a toolkit-specific
// store so portable copies keep their appearance too.
type themePreference struct {
	Mode string `json:"mode,omitempty"`
	// Dark is the field of files written before appearance modes existed.
	// It is read, never written.
	Dark *bool `json:"dark,omitempty"`
}

// loadTheme reads the saved appearance mode. A missing or unreadable file
// means the system appearance.
func loadTheme(dataDir string) string {
	if dataDir == "" {
		return themeSystem
	}
	data, err := os.ReadFile(filepath.Join(dataDir, "theme.json"))
	if err != nil {
		return themeSystem
	}
	var saved themePreference
	if err := json.Unmarshal(data, &saved); err != nil {
		return themeSystem
	}
	switch saved.Mode {
	case themeLight, themeDark, themeSystem:
		return saved.Mode
	}
	if saved.Dark != nil {
		if *saved.Dark {
			return themeDark
		}
		return themeLight
	}
	return themeSystem
}

// saveTheme persists the chosen appearance. Failures are non-fatal: the
// switch still applies to the current session.
func saveTheme(dataDir, mode string) error {
	if dataDir == "" {
		return nil
	}
	mode = normalizeThemeMode(mode)
	data, err := json.Marshal(themePreference{Mode: mode})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dataDir, "theme.json"), data, 0o600)
}

func normalizeThemeMode(mode string) string {
	switch mode {
	case themeLight, themeDark, themeSystem:
		return mode
	default:
		return themeSystem
	}
}

// themeSourceFor maps an appearance mode to the MyGo source. A pinned mode
// also tells MyGo which appearance its own dialogs and title bar decorations
// should use.
func themeSourceFor(mode string) mygo.ThemeSource {
	switch normalizeThemeMode(mode) {
	case themeDark:
		return mygo.ThemeDark
	case themeLight:
		return mygo.ThemeLight
	default:
		return mygo.ThemeSystem
	}
}

// shellTheme returns the app's theme with the compact desktop metrics.
func shellTheme(dark bool) *ui.Theme {
	var t *ui.Theme
	if dark {
		t = ui.DarkTheme()
	} else {
		t = ui.LightTheme()
	}
	copy := *t
	copy.Radius = 8
	copy.Spacing = 4
	copy.FontSize = 13
	copy.ScrollbarStyle = ui.ScrollbarStyle{TrackWidth: 12, ThumbWidth: 6}
	return &copy
}
