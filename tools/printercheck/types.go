package printercheck

type snapshot struct {
	CollectedAt    string        `json:"collectedAt"`
	Spooler        spoolerInfo   `json:"spooler"`
	DefaultPrinter string        `json:"defaultPrinter"`
	Printers       []printerInfo `json:"printers"`
	Drivers        []driverInfo  `json:"drivers"`
	Ports          []portInfo    `json:"ports"`
	Devices        []deviceInfo  `json:"devices"`
	Warnings       []string      `json:"warnings,omitempty"`
}

type spoolerInfo struct {
	Found     bool   `json:"found"`
	Status    string `json:"status"`
	StartType string `json:"startType"`
}

type printerInfo struct {
	Name          string `json:"name"`
	DriverName    string `json:"driverName"`
	PortName      string `json:"portName"`
	PrinterStatus string `json:"printerStatus"`
	WorkOffline   bool   `json:"workOffline"`
	Default       bool   `json:"default"`
	Shared        bool   `json:"shared"`
	ShareName     string `json:"shareName"`
	Type          string `json:"type"`
	Location      string `json:"location"`
	Comment       string `json:"comment"`
	Network       bool   `json:"network"`
	Local         bool   `json:"local"`
	Source        string `json:"source"`
}

type driverInfo struct {
	Name         string `json:"name"`
	Manufacturer string `json:"manufacturer"`
	Version      string `json:"version"`
	Environment  string `json:"environment"`
	InfPath      string `json:"infPath"`
	ConfigFile   string `json:"configFile"`
	DataFile     string `json:"dataFile"`
	HelpFile     string `json:"helpFile"`
	FilesChecked bool   `json:"filesChecked"`
	FilesOK      bool   `json:"filesOK"`
	Source       string `json:"source"`
}

type portInfo struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	Host         string `json:"host"`
	Port         int    `json:"port"`
	Protocol     string `json:"protocol"`
	Source       string `json:"source"`
	UserRegistry bool   `json:"userRegistry,omitempty"`
}

type deviceInfo struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Class      string `json:"class"`
	InstanceID string `json:"instanceId"`
	Problem    string `json:"problem"`
	Source     string `json:"source"`
}
