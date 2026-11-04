package startupmgr

// startupItem is one auto-start entry from the registry, startup folders,
// scheduled tasks or services.
type startupItem struct {
	Kind         string `json:"kind"`
	Name         string `json:"name"`
	Location     string `json:"location"`
	Command      string `json:"command"`
	Enabled      bool   `json:"enabled"`
	Scope        string `json:"scope"`
	Note         string `json:"note"`
	ApprovedPath string `json:"approvedPath"`
	TaskPath     string `json:"taskPath"`
	StartType    string `json:"startType"`
	RunState     string `json:"runState"`
}

type snapshot struct {
	CollectedAt string        `json:"collectedAt"`
	IsAdmin     bool          `json:"isAdmin"`
	Items       []startupItem `json:"items"`
}

type params struct {
	Action string `json:"action"`
	Item   string `json:"item"`
}

// toggleResult is what the elevated script reports back.
type toggleResult struct {
	OK      bool   `json:"ok"`
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
}

// toggleJob is the payload for one startup state change.
type toggleJob struct {
	Kind          string `json:"kind"`
	Name          string `json:"name"`
	ApprovedPath  string `json:"approvedPath"`
	TaskPath      string `json:"taskPath"`
	Enable        bool   `json:"enable"`
	StartType     string `json:"startType"`
	StartTypeText string `json:"startTypeText"`
}
