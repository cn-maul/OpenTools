package uninstaller

// appEntry is one installed program from the Windows uninstall registry.
type appEntry struct {
	Key              string `json:"key"`
	Hive             string `json:"hive"`
	Name             string `json:"name"`
	Version          string `json:"version"`
	Publisher        string `json:"publisher"`
	InstallDate      string `json:"installDate"`
	SizeBytes        int64  `json:"sizeBytes"`
	Icon             string `json:"icon"`
	UninstallString  string `json:"uninstallString"`
	QuietString      string `json:"quietString"`
	WindowsInstaller bool   `json:"windowsInstaller"`
}

type snapshot struct {
	CollectedAt string     `json:"collectedAt"`
	IsAdmin     bool       `json:"isAdmin"`
	Apps        []appEntry `json:"apps"`
}

type params struct {
	Action string `json:"action"`
	Item   string `json:"item"`
	Items  string `json:"items"`
	Filter string `json:"filter"`
}

// uninstallTarget is the per-row payload embedded in row actions; the
// elevated script re-reads the registry entry itself.
type uninstallTarget struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

// uninstallResult is what the elevated script reports back.
type uninstallResult struct {
	OK             bool   `json:"ok"`
	Error          string `json:"error"`
	Message        string `json:"message,omitempty"`
	StillInstalled bool   `json:"stillInstalled"`
}
