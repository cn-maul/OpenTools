package systemconfig

type snapshot struct {
	SchemaVersion int             `json:"schemaVersion"`
	Profile       string          `json:"profile,omitempty"`
	CollectedAt   string          `json:"collectedAt"`
	Computer      computerInfo    `json:"computer"`
	OS            operatingSystem `json:"os"`
	Board         boardInfo       `json:"board"`
	Firmware      firmwareInfo    `json:"firmware"`
	CPU           cpuInfo         `json:"cpu"`
	Memory        memoryInfo      `json:"memory"`
	Graphics      []graphicsInfo  `json:"graphics"`
	Disks         []diskInfo      `json:"disks"`
	Volumes       []volumeInfo    `json:"volumes"`
	Network       []networkInfo   `json:"network"`
	Security      securityInfo    `json:"security"`
	Battery       batteryInfo     `json:"battery"`
}

type computerInfo struct {
	Name         string `json:"name"`
	Manufacturer string `json:"manufacturer"`
	Model        string `json:"model"`
	SystemType   string `json:"systemType"`
	Domain       string `json:"domain"`
	CurrentUser  string `json:"currentUser"`
	IsAdmin      bool   `json:"isAdmin"`
}

type operatingSystem struct {
	Caption       string `json:"caption"`
	Version       string `json:"version"`
	Build         string `json:"build"`
	Architecture  string `json:"architecture"`
	InstallDate   string `json:"installDate"`
	LastBoot      string `json:"lastBoot"`
	UptimeSeconds int64  `json:"uptimeSeconds"`
	TimeZone      string `json:"timeZone"`
	Locale        string `json:"locale"`
}

type boardInfo struct {
	Manufacturer string `json:"manufacturer"`
	Product      string `json:"product"`
	Version      string `json:"version"`
}

type firmwareInfo struct {
	Vendor      string `json:"vendor"`
	Version     string `json:"version"`
	ReleaseDate string `json:"releaseDate"`
	BootMode    string `json:"bootMode"`
}

type cpuInfo struct {
	Name              string `json:"name"`
	Manufacturer      string `json:"manufacturer"`
	Architecture      string `json:"architecture"`
	Cores             int    `json:"cores"`
	LogicalProcessors int    `json:"logicalProcessors"`
	MaxClockMHz       int    `json:"maxClockMHz"`
	CurrentClockMHz   int    `json:"currentClockMHz"`
	LoadPercent       int    `json:"loadPercent"`
}

type memoryInfo struct {
	TotalBytes     int64          `json:"totalBytes"`
	AvailableBytes int64          `json:"availableBytes"`
	Modules        []memoryModule `json:"modules"`
}

type memoryModule struct {
	Slot         string `json:"slot"`
	Manufacturer string `json:"manufacturer"`
	PartNumber   string `json:"partNumber"`
	Capacity     int64  `json:"capacityBytes"`
	SpeedMHz     int    `json:"speedMHz"`
	FormFactor   int    `json:"formFactor"`
	MemoryType   int    `json:"memoryType"`
}

type graphicsInfo struct {
	Name          string `json:"name"`
	Vendor        string `json:"vendor"`
	DriverVersion string `json:"driverVersion"`
	DriverDate    string `json:"driverDate"`
	MemoryBytes   int64  `json:"memoryBytes"`
	Status        string `json:"status"`
}

type diskInfo struct {
	Model     string `json:"model"`
	SizeBytes int64  `json:"sizeBytes"`
	BusType   string `json:"busType"`
	MediaType string `json:"mediaType"`
	Health    string `json:"health"`
	Firmware  string `json:"firmware"`
}

type volumeInfo struct {
	DriveLetter string `json:"driveLetter"`
	Label       string `json:"label"`
	FileSystem  string `json:"fileSystem"`
	TotalBytes  int64  `json:"totalBytes"`
	FreeBytes   int64  `json:"freeBytes"`
}

type networkInfo struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Type        string   `json:"type"`
	Status      string   `json:"status"`
	LinkSpeed   string   `json:"linkSpeed"`
	MACAddress  string   `json:"macAddress"`
	IPv4        []string `json:"ipv4"`
	Gateways    []string `json:"gateways"`
	DNS         []string `json:"dns"`
}

type securityInfo struct {
	SecureBoot       string `json:"secureBoot"`
	TPM              string `json:"tpm"`
	TPMVersion       string `json:"tpmVersion"`
	Defender         string `json:"defender"`
	DefenderRealtime string `json:"defenderRealtime"`
	Firewall         string `json:"firewall"`
	BitLocker        string `json:"bitLocker"`
	Virtualization   string `json:"virtualization"`
}

type batteryInfo struct {
	Present        bool   `json:"present"`
	Name           string `json:"name"`
	ChargePercent  int    `json:"chargePercent"`
	Status         string `json:"status"`
	RuntimeMinutes int    `json:"runtimeMinutes"`
}

// mergeSnapshot merges one streamed section into the report aggregate.
func mergeSnapshot(dst *snapshot, src snapshot) error {
	if src.CollectedAt != "" {
		dst.CollectedAt = src.CollectedAt
	}
	if !isZeroComputer(src.Computer) {
		dst.Computer = src.Computer
	}
	if src.OS.Caption != "" || src.OS.Version != "" || src.OS.Build != "" {
		dst.OS = src.OS
	}
	if src.Board.Manufacturer != "" || src.Board.Product != "" {
		dst.Board = src.Board
	}
	if src.Firmware.Vendor != "" || src.Firmware.Version != "" || src.Firmware.BootMode != "" {
		dst.Firmware = src.Firmware
	}
	if src.CPU.Name != "" || src.CPU.LogicalProcessors != 0 {
		dst.CPU = src.CPU
	}
	if src.Memory.TotalBytes != 0 || src.Memory.AvailableBytes != 0 || src.Memory.Modules != nil {
		dst.Memory = src.Memory
	}
	if src.Graphics != nil {
		dst.Graphics = src.Graphics
	}
	if src.Disks != nil {
		dst.Disks = src.Disks
	}
	if src.Volumes != nil {
		dst.Volumes = src.Volumes
	}
	if src.Network != nil {
		dst.Network = src.Network
	}
	if src.Security.SecureBoot != "" || src.Security.TPM != "" ||
		src.Security.Defender != "" || src.Security.Firewall != "" ||
		src.Security.BitLocker != "" || src.Security.Virtualization != "" {
		dst.Security = src.Security
	}
	if src.Battery.Present || src.Battery.Name != "" || src.Battery.Status != "" ||
		src.Battery.ChargePercent != 0 || src.Battery.RuntimeMinutes != 0 {
		dst.Battery = src.Battery
	}
	return nil
}

func isZeroComputer(value computerInfo) bool {
	return value == computerInfo{}
}
