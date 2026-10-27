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
        }
    }
} catch {
    $result.warnings += "打印管理接口不可用：" + $_.Exception.Message
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
            }
        }
    } catch {
        $result.warnings += "WMI 打印机接口不可用：" + $_.Exception.Message
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

try {
    foreach ($item in @(Get-PrinterDriver -ErrorAction Stop)) {
        $files = @()
        foreach ($path in @($item.InfPath, $item.ConfigFile, $item.DataFile, $item.HelpFile)) {
            if ($path) { $files += [string]$path }
        }
        $checked = $files.Count -gt 0
        $ok = $true
        foreach ($path in $files) {
            if (-not (Test-Path -LiteralPath $path -PathType Leaf -ErrorAction SilentlyContinue)) {
                $ok = $false
            }
        }
        $result.drivers += [ordered]@{
            name = [string]$item.Name
            manufacturer = [string]$item.Manufacturer
            version = [string]$item.DriverVersion
            environment = [string]$item.PrinterEnvironment
            infPath = [string]$item.InfPath
            configFile = [string]$item.ConfigFile
            dataFile = [string]$item.DataFile
            helpFile = [string]$item.HelpFile
            filesChecked = [bool]$checked
            filesOK = [bool]($checked -and $ok)
            source = "PrintManagement"
        }
    }
} catch {
    $result.warnings += "打印驱动接口不可用：" + $_.Exception.Message
}

if ($result.drivers.Count -eq 0) {
    $architecture = if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "Windows ARM64" } else { "Windows x64" }
    foreach ($version in @("Version-3", "Version-4")) {
        $driverRoot = "HKLM:\SYSTEM\CurrentControlSet\Control\Print\Environments\$architecture\Drivers\$version"
        Get-ChildItem -LiteralPath $driverRoot -ErrorAction SilentlyContinue | ForEach-Object {
            $result.drivers += [ordered]@{
                name = [string]$_.PSChildName
                manufacturer = ""
                version = ""
                environment = $architecture + " / " + $version
                infPath = ""
                configFile = ""
                dataFile = ""
                helpFile = ""
                filesChecked = $false
                filesOK = $false
                source = "注册表"
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
            port = if ($item.PortNumber) { [int]$item.PortNumber } else { 0 }
            protocol = [string]$item.Protocol
            source = "PrintManagement"
            userRegistry = $false
        }
    }
} catch {
    $result.warnings += "打印端口接口不可用：" + $_.Exception.Message
}

if ($result.ports.Count -eq 0) {
    $portRoots = @(
        @{ Path = "HKLM:\SYSTEM\CurrentControlSet\Control\Print\Monitors\Standard TCP/IP Port\Ports"; Description = "Standard TCP/IP Port" },
        @{ Path = "HKLM:\SYSTEM\CurrentControlSet\Control\Print\Monitors\WSD Port\Ports"; Description = "WSD Port" },
        @{ Path = "HKLM:\SYSTEM\CurrentControlSet\Control\Print\Monitors\Local Port\Ports"; Description = "Local Port" }
    )
    foreach ($root in $portRoots) {
        Get-ChildItem -LiteralPath $root.Path -ErrorAction SilentlyContinue | ForEach-Object {
            $values = Get-ItemProperty -LiteralPath $_.PSPath -ErrorAction SilentlyContinue
            $host = if ($values.HostName) { $values.HostName } elseif ($values.IPAddress) { $values.IPAddress } else { "" }
            $result.ports += [ordered]@{
                name = [string]$_.PSChildName
                description = [string]$root.Description
                host = [string]$host
                port = if ($values.PortNumber) { [int]$values.PortNumber } else { 0 }
                protocol = [string]$values.Protocol
                source = "注册表"
                userRegistry = $false
            }
        }
    }
}

try {
    foreach ($item in @(Get-PnpDevice -Class Printer -ErrorAction Stop)) {
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
            Where-Object { $_.PNPClass -eq "Printer" -or $_.Name -match "(?i)printer|打印" })) {
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
        $result.warnings += "打印机硬件接口不可用：" + $_.Exception.Message
    }
}

$result | ConvertTo-Json -Depth 8 -Compress
`
