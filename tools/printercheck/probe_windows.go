//go:build windows

package printercheck

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
)

func collect(ctx context.Context) (snapshot, error) {
	if err := ctx.Err(); err != nil {
		return snapshot{}, err
	}
	output, err := runPowerShell(ctx, printerInventoryScript)
	if err != nil {
		return snapshot{}, err
	}
	output = bytes.TrimSpace(output)
	if len(output) == 0 {
		return snapshot{}, fmt.Errorf("打印机检测没有返回数据")
	}
	var value snapshot
	if err := json.Unmarshal(output, &value); err != nil {
		return snapshot{}, fmt.Errorf("解析打印机检测结果失败：%w", err)
	}
	if err := ctx.Err(); err != nil {
		return snapshot{}, err
	}
	return value, nil
}

// The script collects each source independently. A stopped or unavailable
// spooler must not hide printers that are still registered for the user.
const printerInventoryScript = `
$ErrorActionPreference = "SilentlyContinue"
$ProgressPreference = "SilentlyContinue"

$result = [ordered]@{
    collectedAt = (Get-Date).ToString("o")
    spooler = [ordered]@{
        found = $false
        status = ""
        startType = ""
    }
    defaultPrinter = ""
    printers = @()
    drivers = @()
    ports = @()
    devices = @()
    neighbors = @()
    warnings = @()
}

$service = Get-Service -Name Spooler -ErrorAction SilentlyContinue | Select-Object -First 1
if ($service) {
    $result.spooler.found = $true
    $result.spooler.status = [string]$service.Status
    $result.spooler.startType = [string]$service.StartType
}

$defaultWmi = Get-CimInstance Win32_Printer -ErrorAction SilentlyContinue |
    Where-Object { $_.Default -eq $true } |
    Select-Object -First 1
if ($defaultWmi) {
    $result.defaultPrinter = [string]$defaultWmi.Name
}

try {
    foreach ($item in @(Get-Printer -ErrorAction Stop)) {
        $result.printers += [ordered]@{
            name = [string]$item.Name
            driverName = [string]$item.DriverName
            portName = [string]$item.PortName
            printerStatus = [string]$item.PrinterStatus
            workOffline = [bool]$item.WorkOffline
            default = ([string]$item.Name -eq [string]$result.defaultPrinter)
            shared = [bool]$item.Shared
            shareName = [string]$item.ShareName
            type = [string]$item.Type
            location = [string]$item.Location
            comment = [string]$item.Comment
            network = ([string]$item.Type -match "Connection|Network")
            local = ([string]$item.Type -match "Local")
            source = "PrintManagement"
            queuedJobs = 0
            jobSummary = ""
        }
    }
} catch {
    # The WMI and registry fallbacks below cover this, so it is not a warning.
}

if ($result.printers.Count -eq 0) {
    try {
        foreach ($item in @(Get-CimInstance Win32_Printer -ErrorAction Stop)) {
            $result.printers += [ordered]@{
                name = [string]$item.Name
                driverName = [string]$item.DriverName
                portName = [string]$item.PortName
                printerStatus = [string]$item.PrinterStatus
                workOffline = [bool]$item.WorkOffline
                default = [bool]$item.Default
                shared = [bool]$item.Shared
                shareName = [string]$item.ShareName
                type = if ($item.Network) { "网络" } elseif ($item.Local) { "本地" } else { "" }
                location = [string]$item.Location
                comment = [string]$item.Comment
                network = [bool]$item.Network
                local = [bool]$item.Local
                source = "Win32_Printer"
                queuedJobs = 0
                jobSummary = ""
            }
        }
    } catch {
        # The registry fallback below covers this.
    }
}

if ($result.printers.Count -eq 0) {
    $seen = @{}
    foreach ($path in @(
        "HKCU:\Software\Microsoft\Windows NT\CurrentVersion\PrinterPorts",
        "HKCU:\Software\Microsoft\Windows NT\CurrentVersion\Devices"
    )) {
        $values = Get-ItemProperty -LiteralPath $path -ErrorAction SilentlyContinue
        if (-not $values) { continue }
        foreach ($property in $values.PSObject.Properties) {
            if ($property.Name -in @("PSPath", "PSParentPath", "PSChildName", "PSDrive", "PSProvider")) { continue }
            if ($seen.ContainsKey($property.Name)) { continue }
            $seen[$property.Name] = $true
            $parts = ([string]$property.Value) -split ","
            $port = if ($parts.Count -ge 2) { $parts[1] } else { "" }
            $result.printers += [ordered]@{
                name = [string]$property.Name
                driverName = ""
                portName = $port
                printerStatus = "未读取（打印服务未运行）"
                workOffline = $false
                default = ([string]$property.Name -eq [string]$result.defaultPrinter)
                shared = $false
                shareName = ""
                type = "本地/注册表"
                location = ""
                comment = ""
                network = $false
                local = $true
                source = "用户注册表"
                queuedJobs = 0
                jobSummary = ""
            }
        }
    }
    if (-not $result.defaultPrinter) {
        $windowsKey = Get-ItemProperty -LiteralPath "HKCU:\Software\Microsoft\Windows NT\CurrentVersion\Windows" -ErrorAction SilentlyContinue
        if ($windowsKey.Device) {
            $result.defaultPrinter = ([string]$windowsKey.Device -split ",")[0]
            foreach ($item in $result.printers) {
                $item.default = ([string]$item.name -eq [string]$result.defaultPrinter)
            }
        }
    }
}

# Pending jobs explain a printer that stays "busy" and are only readable
# while the spooler runs.
try {
    foreach ($item in @(Get-PrintJob -ErrorAction Stop)) {
        $printerName = [string]$item.PrinterName
        $state = [string]$item.JobStatus
        if (-not $state) { $state = [string]$item.Status }
        $summary = if ($state) { $state } else { "排队中" }
        foreach ($printer in $result.printers) {
            if ([string]$printer.name -ne $printerName) { continue }
            $printer.queuedJobs = [int]$printer.queuedJobs + 1
            if ($printer.jobSummary) {
                $printer.jobSummary = $printer.jobSummary + "、" + $summary
            } else {
                $printer.jobSummary = $summary
            }
        }
    }
} catch {
    # A stopped spooler is the usual reason, and that is already reported as
    # the root cause, so only a running spooler turns this into a warning.
    if ($result.spooler.status -eq "Running") {
        $result.warnings += "打印作业队列不可用，未能读取待处理任务。"
    }
}

try {
    foreach ($item in @(Get-PrinterDriver -ErrorAction Stop)) {
        $result.drivers += [ordered]@{
            name = [string]$item.Name
            manufacturer = [string]$item.Manufacturer
            version = [string]$item.DriverVersion
            environment = [string]$item.PrinterEnvironment
            infPath = [string]$item.InfPath
            configFile = [string]$item.ConfigFile
            dataFile = [string]$item.DataFile
            helpFile = [string]$item.HelpFile
            driverDate = ""
            model = ""
            driverFile = ""
            printProcessor = ""
            hardwareId = ""
            source = "PrintManagement"
        }
    }
} catch {
    # The driver registry below still verifies the driver state.
}

# The driver registry is authoritative and readable even while the spooler is
# stopped. It also carries the hardware id that tells a USB printer apart
# from a network one, which the print management API does not expose.
foreach ($environment in @("Windows x64", "Windows ARM64", "Windows NT x86")) {
    foreach ($version in @("Version-3", "Version-4")) {
        $driverRoot = "HKLM:\SYSTEM\CurrentControlSet\Control\Print\Environments\$environment\Drivers\$version"
        Get-ChildItem -LiteralPath $driverRoot -ErrorAction SilentlyContinue | ForEach-Object {
            $values = Get-ItemProperty -LiteralPath $_.PSPath -ErrorAction SilentlyContinue
            if (-not $values) { return }
            $infPath = [string]$values.InfPath
            $model = ""
            if ($infPath -and (Test-Path -LiteralPath $infPath -PathType Leaf -ErrorAction SilentlyContinue)) {
                $manufacturerLine = Get-Content -LiteralPath $infPath -ErrorAction SilentlyContinue |
                    Where-Object { $_ -match "^\s*\[Manufacturer\]\s*$" } |
                    Select-Object -First 1
                if ($manufacturerLine) {
                    $model = ($manufacturerLine -replace "^\s*\[[^]]+\]\s*", "").Trim()
                }
            }
            $registryDriver = [ordered]@{
                name = [string]$_.PSChildName
                manufacturer = [string]$values.Manufacturer
                version = [string]$values.DriverVersion
                driverDate = [string]$values.DriverDate
                environment = $environment + " / " + $version
                model = $model
                infPath = $infPath
                configFile = [string]$values."Configuration File"
                dataFile = [string]$values."Data File"
                helpFile = [string]$values."Help File"
                driverFile = [string]$values.Driver
                printProcessor = [string]$values."Print Processor"
                hardwareId = [string]$values.HardwareID
                source = "注册表"
            }
            $merged = $false
            foreach ($existing in $result.drivers) {
                if ([string]$existing.name -ne [string]$registryDriver.name) { continue }
                $merged = $true
                if (-not $existing.version) { $existing.version = $registryDriver.version }
                if (-not $existing.driverDate) { $existing.driverDate = $registryDriver.driverDate }
                if (-not $existing.driverFile) { $existing.driverFile = $registryDriver.driverFile }
                if (-not $existing.printProcessor) { $existing.printProcessor = $registryDriver.printProcessor }
                if (-not $existing.hardwareId) { $existing.hardwareId = $registryDriver.hardwareId }
                if (-not $existing.environment) { $existing.environment = $registryDriver.environment }
                if (-not $existing.model) { $existing.model = $registryDriver.model }
            }
            if (-not $merged) {
                $result.drivers += $registryDriver
            }
        }
    }
}

try {
    foreach ($item in @(Get-PrinterPort -ErrorAction Stop)) {
        $result.ports += [ordered]@{
            name = [string]$item.Name
            description = [string]$item.Description
            host = [string]$item.PrinterHostAddress
            ipAddress = [string]$item.PrinterHostAddress
            monitor = "打印管理"
            port = if ($item.PortNumber) { [int]$item.PortNumber } else { 0 }
            protocol = [string]$item.Protocol
            snmp = $false
            source = "PrintManagement"
            userRegistry = $false
        }
    }
} catch {
    # The port registry below still lists the printer ports.
}

if ($result.ports.Count -eq 0) {
    $portRoots = @(
        @{ Path = "HKLM:\SYSTEM\CurrentControlSet\Control\Print\Monitors\Standard TCP/IP Port\Ports"; Description = "Standard TCP/IP Port"; Monitor = "Standard TCP/IP Port" },
        @{ Path = "HKLM:\SYSTEM\CurrentControlSet\Control\Print\Monitors\WSD Port\Ports"; Description = "WSD Port"; Monitor = "WSD Port" },
        @{ Path = "HKLM:\SYSTEM\CurrentControlSet\Control\Print\Monitors\Local Port\Ports"; Description = "Local Port"; Monitor = "Local Port" },
        @{ Path = "HKLM:\SYSTEM\CurrentControlSet\Control\Print\Monitors\USB Monitor\Ports"; Description = "USB Monitor"; Monitor = "USB Monitor" }
    )
    foreach ($root in $portRoots) {
        Get-ChildItem -LiteralPath $root.Path -ErrorAction SilentlyContinue | ForEach-Object {
            $values = Get-ItemProperty -LiteralPath $_.PSPath -ErrorAction SilentlyContinue
            $hostName = if ($values.HostName) { [string]$values.HostName } else { "" }
            $ipAddress = if ($values.IPAddress) { [string]$values.IPAddress } else { "" }
            # $host is a PowerShell automatic variable, so the port target
            # must be collected under a different name.
            $portTarget = if ($hostName) { $hostName } elseif ($ipAddress) { $ipAddress } else { "" }
            $result.ports += [ordered]@{
                name = [string]$_.PSChildName
                description = [string]$root.Description
                monitor = [string]$root.Monitor
                host = [string]$portTarget
                ipAddress = [string]$ipAddress
                port = if ($values.PortNumber) { [int]$values.PortNumber } else { 0 }
                protocol = [string]$values.Protocol
                snmp = ([int]$values."SNMP Enabled" -eq 1)
                source = "注册表"
                userRegistry = $false
            }
        }
    }
}

try {
    foreach ($item in @(Get-PnpDevice -ErrorAction Stop | Where-Object { $_.Class -eq "Printer" -or $_.Class -eq "PrintQueue" })) {
        $result.devices += [ordered]@{
            name = [string]$item.FriendlyName
            status = [string]$item.Status
            class = [string]$item.Class
            instanceId = [string]$item.InstanceId
            problem = [string]$item.Problem
            source = "PnP"
        }
    }
} catch {
    try {
        foreach ($item in @(Get-CimInstance Win32_PnPEntity -ErrorAction Stop |
            Where-Object { $_.PNPClass -eq "Printer" -or $_.PNPClass -eq "PrintQueue" })) {
            $result.devices += [ordered]@{
                name = [string]$item.Name
                status = [string]$item.Status
                class = [string]$item.PNPClass
                instanceId = [string]$item.DeviceID
                problem = [string]$item.ConfigManagerErrorCode
                source = "WMI"
            }
        }
    } catch {
        # Without either device API the PnP status stays unknown.
    }
}

# The neighbour table proves whether a printer address is currently present
# on the local link, which separates "host unreachable" from "printer offline".
try {
    foreach ($item in @(Get-NetNeighbor -AddressFamily IPv4 -ErrorAction Stop)) {
        $mac = [string]$item.LinkLayerAddress
        if (-not $mac -or $mac -eq "00-00-00-00-00-00") { continue }
        if ($item.State -eq "Permanent") { continue }
        $result.neighbors += [ordered]@{
            ip = [string]$item.IPAddress
            mac = $mac
            state = [string]$item.State
            interface = [string]$item.InterfaceAlias
        }
    }
} catch {
    $result.warnings += "邻居表不可用，未能核验打印机的网络邻居信息。"
}

$result | ConvertTo-Json -Depth 8 -Compress
`
