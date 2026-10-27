package printercheck

import "opentools/pkg/tool"

type snapshot struct {
	CollectedAt    string         `json:"collectedAt"`
	Spooler        spoolerInfo    `json:"spooler"`
	DefaultPrinter string         `json:"defaultPrinter"`
	Printers       []printerInfo  `json:"printers"`
	Drivers        []driverInfo   `json:"drivers"`
	Ports          []portInfo     `json:"ports"`
	Devices        []deviceInfo   `json:"devices"`
	Neighbors      []neighborInfo `json:"neighbors,omitempty"`
	Warnings       []string       `json:"warnings,omitempty"`
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
	QueuedJobs    int    `json:"queuedJobs"`
	JobSummary    string `json:"jobSummary,omitempty"`
}

type driverInfo struct {
	Name           string `json:"name"`
	Manufacturer   string `json:"manufacturer"`
	Version        string `json:"version"`
	DriverDate     string `json:"driverDate"`
	Environment    string `json:"environment"`
	Model          string `json:"model"`
	InfPath        string `json:"infPath"`
	ConfigFile     string `json:"configFile"`
	DataFile       string `json:"dataFile"`
	HelpFile       string `json:"helpFile"`
	DriverFile     string `json:"driverFile"`
	PrintProcessor string `json:"printProcessor"`
	HardwareID     string `json:"hardwareId"`
	Source         string `json:"source"`
}

type portInfo struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	Monitor      string `json:"monitor"`
	Host         string `json:"host"`
	IPAddress    string `json:"ipAddress"`
	Port         int    `json:"port"`
	Protocol     string `json:"protocol"`
	SNMP         bool   `json:"snmp"`
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

type neighborInfo struct {
	IP        string `json:"ip"`
	MAC       string `json:"mac"`
	State     string `json:"state"`
	Interface string `json:"interface"`
}

// repairKind names one repair the tool knows how to perform.
type repairKind string

const (
	// repairStartSpooler starts the Print Spooler service.
	repairStartSpooler repairKind = "start-spooler"
	// repairSetSpoolerStartType makes the spooler start with Windows.
	repairSetSpoolerStartType repairKind = "set-spooler-start-type"
	// repairRebindPort points a queue at a real TCP/IP printer port.
	repairRebindPort repairKind = "rebind-port"
	// repairClearOffline takes a queue out of the offline state.
	repairClearOffline repairKind = "clear-offline"
	// repairClearJobs removes stuck jobs from a queue.
	repairClearJobs repairKind = "clear-jobs"
	// repairSetDefault makes a queue the default printer.
	repairSetDefault repairKind = "set-default"
)

// repair is one actionable fix, derived from the diagnosis.
type repair struct {
	Kind repairKind `json:"kind"`
	// Label is the button text.
	Label string `json:"label"`
	// Tone marks the urgency of the repair.
	Tone tool.Tone `json:"tone"`
	// Confirm is shown before the repair runs.
	Confirm string `json:"confirm,omitempty"`
	// Printer is the queue the repair targets, empty for system wide repairs.
	Printer string `json:"printer,omitempty"`
	// PortName is the target TCP/IP port for a rebind.
	PortName string `json:"portName,omitempty"`
	// Host is the printer address for a rebind.
	Host string `json:"host,omitempty"`
	// PortNumber is the printer port for a rebind.
	PortNumber int `json:"portNumber,omitempty"`
	// StartType is the service start type for a spooler repair.
	StartType string `json:"startType,omitempty"`
}
