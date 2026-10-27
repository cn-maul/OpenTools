//go:build windows

package systemconfig

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"opentools/pkg/tool"
)

func collect(ctx context.Context, report tool.Reporter) (snapshot, error) {
	if err := ctx.Err(); err != nil {
		return snapshot{}, err
	}
	var value snapshot
	seen := make(map[string]bool, len(inventorySections()))
	err := runPowerShellLines(ctx, systemInventoryScript, func(line string) error {
		line = strings.TrimSpace(line)
		if line == "" {
			return nil
		}
		if strings.Contains(line, "OT_START ") {
			id := strings.TrimSpace(line[strings.Index(line, "OT_START ")+len("OT_START "):])
			if title := sectionTitle(id); title != "" {
				report.Section(tool.Section{ID: id, Title: title, State: tool.SectionRunning})
			}
			return nil
		}
		if strings.Contains(line, "OT_SECTION ") {
			field := strings.TrimSpace(line[strings.Index(line, "OT_SECTION ")+len("OT_SECTION "):])
			id, raw, ok := strings.Cut(field, " ")
			if !ok || raw == "" {
				return fmt.Errorf("系统检测返回了无效的分段：%s", line)
			}
			if sectionTitle(id) == "" {
				return fmt.Errorf("系统检测返回了未知的分段：%s", id)
			}
			var part snapshot
			if err := json.Unmarshal([]byte(raw), &part); err != nil {
				return fmt.Errorf("解析系统检测分段 %s 失败：%w", id, err)
			}
			mergeSnapshot(&value, part)
			if ready, ok := readySection(value, id); ok {
				report.Section(ready)
			}
			seen[id] = true
			report.Progress(int64(len(seen)), int64(len(inventorySections())))
			return nil
		}
		return nil
	})
	if err != nil {
		return snapshot{}, err
	}
	if err := ctx.Err(); err != nil {
		return snapshot{}, err
	}
	for _, section := range inventorySections() {
		if !seen[section.ID] {
			report.Section(tool.Section{
				ID:    section.ID,
				Title: section.Title,
				State: tool.SectionSkipped,
				Text:  "本次检测未返回该项。",
			})
		}
	}
	value.SchemaVersion = 1
	if value.CollectedAt == "" {
		value.CollectedAt = time.Now().Format(time.RFC3339)
	}
	return value, nil
}

const systemInventoryScript = `
$ErrorActionPreference = "SilentlyContinue"

function EmitSection($id, $value) {
    Write-Output ("OT_SECTION " + $id + " " + ($value | ConvertTo-Json -Depth 6 -Compress))
}

function StartSection($id) {
    Write-Output ("OT_START " + $id)
}

function TextOrEmpty($value) {
    if ($null -eq $value) { return "" }
    return ([string]$value).Trim()
}

function DateIso($value) {
    if ($null -eq $value) { return "" }
    try { return ([datetime]$value).ToString("o") } catch { return TextOrEmpty $value }
}

function DiskHealth($value) {
    switch (TextOrEmpty $value) {
        "Healthy" { return "正常" }
        "Warning" { return "警告" }
        "Unhealthy" { return "异常" }
        default { return TextOrEmpty $value }
    }
}

function SecurityText($enabled, $enabledText, $disabledText) {
    if ($enabled -eq $true) { return $enabledText }
    return $disabledText
}

StartSection "device"
$computer = Get-CimInstance Win32_ComputerSystem -ErrorAction SilentlyContinue
$board = Get-CimInstance Win32_BaseBoard -ErrorAction SilentlyContinue
$bios = Get-CimInstance Win32_BIOS -ErrorAction SilentlyContinue
$principal = [Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()
EmitSection "device" ([ordered]@{
    collectedAt = (Get-Date).ToString("o")
    computer = [ordered]@{
        name = TextOrEmpty $computer.Name
        manufacturer = TextOrEmpty $computer.Manufacturer
        model = TextOrEmpty $computer.Model
        systemType = TextOrEmpty $computer.SystemType
        domain = if ($computer.PartOfDomain) { TextOrEmpty $computer.Domain } else { TextOrEmpty $computer.Workgroup }
        currentUser = "$env:USERDOMAIN\$env:USERNAME"
        isAdmin = $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
    }
    board = [ordered]@{
        manufacturer = TextOrEmpty $board.Manufacturer
        product = TextOrEmpty $board.Product
        version = TextOrEmpty $board.Version
    }
    firmware = [ordered]@{
        vendor = TextOrEmpty $bios.Manufacturer
        version = TextOrEmpty $bios.SMBIOSBIOSVersion
        releaseDate = DateIso $bios.ReleaseDate
        bootMode = TextOrEmpty $env:firmware_type
    }
})

StartSection "processor"
$cpu = Get-CimInstance Win32_Processor -ErrorAction SilentlyContinue | Select-Object -First 1
EmitSection "processor" ([ordered]@{
    cpu = [ordered]@{
        name = TextOrEmpty $cpu.Name
        manufacturer = TextOrEmpty $cpu.Manufacturer
        architecture = TextOrEmpty $cpu.AddressWidth
        cores = if ($cpu.NumberOfCores) { [int]$cpu.NumberOfCores } else { 0 }
        logicalProcessors = if ($cpu.NumberOfLogicalProcessors) { [int]$cpu.NumberOfLogicalProcessors } else { 0 }
        maxClockMHz = if ($cpu.MaxClockSpeed) { [int]$cpu.MaxClockSpeed } else { 0 }
        currentClockMHz = if ($cpu.CurrentClockSpeed) { [int]$cpu.CurrentClockSpeed } else { 0 }
        loadPercent = if ($cpu.LoadPercentage) { [int]$cpu.LoadPercentage } else { 0 }
    }
})

$os = Get-CimInstance Win32_OperatingSystem -ErrorAction SilentlyContinue
$installDate = ""
$lastBoot = ""
$uptimeSeconds = 0
if ($os) {
    $installDate = DateIso $os.InstallDate
    $lastBoot = DateIso $os.LastBootUpTime
    if ($os.LastBootUpTime) {
        $uptimeSeconds = [int64][Math]::Max(0, ((Get-Date) - [datetime]$os.LastBootUpTime).TotalSeconds)
    }
}

StartSection "memory"
$memoryModules = @()
foreach ($module in @(Get-CimInstance Win32_PhysicalMemory -ErrorAction SilentlyContinue)) {
    $memoryModules += [ordered]@{
        slot = TextOrEmpty $module.DeviceLocator
        manufacturer = TextOrEmpty $module.Manufacturer
        partNumber = TextOrEmpty $module.PartNumber
        capacityBytes = if ($module.Capacity) { [int64]$module.Capacity } else { 0 }
        speedMHz = if ($module.Speed) { [int]$module.Speed } else { 0 }
        formFactor = if ($module.FormFactor) { [int]$module.FormFactor } else { 0 }
        memoryType = if ($module.SMBIOSMemoryType) { [int]$module.SMBIOSMemoryType } else { 0 }
    }
}
$memoryTotal = if ($computer.TotalPhysicalMemory) { [int64]$computer.TotalPhysicalMemory } else { 0 }
$memoryAvailable = if ($os.FreePhysicalMemory) { [int64]$os.FreePhysicalMemory * 1024 } else { 0 }
EmitSection "memory" ([ordered]@{
    memory = [ordered]@{
        totalBytes = $memoryTotal
        availableBytes = $memoryAvailable
        modules = $memoryModules
    }
})

StartSection "graphics"
$graphicsCandidates = @(Get-CimInstance Win32_VideoController -ErrorAction SilentlyContinue |
    Where-Object { $_.Name -notmatch '(?i)(virtual|remote|indirect|idd|display adapter)' })
if ($graphicsCandidates.Count -eq 0) {
    $graphicsCandidates = @(Get-CimInstance Win32_VideoController -ErrorAction SilentlyContinue)
}
$graphics = @()
foreach ($gpu in $graphicsCandidates) {
    $graphics += [ordered]@{
        name = TextOrEmpty $gpu.Name
        vendor = TextOrEmpty $gpu.AdapterCompatibility
        driverVersion = TextOrEmpty $gpu.DriverVersion
        driverDate = DateIso $gpu.DriverDate
        memoryBytes = if ($gpu.AdapterRAM) { [int64]$gpu.AdapterRAM } else { 0 }
        status = TextOrEmpty $gpu.Status
    }
}
EmitSection "graphics" ([ordered]@{ graphics = $graphics })

StartSection "disk"
$disks = @()
foreach ($disk in @(Get-PhysicalDisk -ErrorAction SilentlyContinue)) {
    $disks += [ordered]@{
        model = TextOrEmpty $disk.FriendlyName
        sizeBytes = if ($disk.Size) { [int64]$disk.Size } else { 0 }
        busType = TextOrEmpty $disk.BusType
        mediaType = TextOrEmpty $disk.MediaType
        health = DiskHealth $disk.HealthStatus
        firmware = TextOrEmpty $disk.FirmwareVersion
    }
}
if ($disks.Count -eq 0) {
    foreach ($disk in @(Get-CimInstance Win32_DiskDrive -ErrorAction SilentlyContinue)) {
        $disks += [ordered]@{
            model = TextOrEmpty $disk.Model
            sizeBytes = if ($disk.Size) { [int64]$disk.Size } else { 0 }
            busType = TextOrEmpty $disk.InterfaceType
            mediaType = TextOrEmpty $disk.MediaType
            health = DiskHealth $disk.Status
            firmware = TextOrEmpty $disk.FirmwareRevision
        }
    }
}
EmitSection "disk" ([ordered]@{ disks = $disks })

StartSection "volume"
$volumes = @()
foreach ($volume in @(Get-CimInstance Win32_LogicalDisk -Filter "DriveType=3" -ErrorAction SilentlyContinue)) {
    $volumes += [ordered]@{
        driveLetter = TextOrEmpty $volume.DeviceID
        label = TextOrEmpty $volume.VolumeName
        fileSystem = TextOrEmpty $volume.FileSystem
        totalBytes = if ($volume.Size) { [int64]$volume.Size } else { 0 }
        freeBytes = if ($volume.FreeSpace) { [int64]$volume.FreeSpace } else { 0 }
    }
}
EmitSection "volume" ([ordered]@{ volumes = $volumes })

StartSection "os"
EmitSection "os" ([ordered]@{
    os = [ordered]@{
        caption = TextOrEmpty $os.Caption
        version = TextOrEmpty $os.Version
        build = TextOrEmpty $os.BuildNumber
        architecture = TextOrEmpty $os.OSArchitecture
        installDate = $installDate
        lastBoot = $lastBoot
        uptimeSeconds = $uptimeSeconds
        timeZone = TextOrEmpty ([TimeZoneInfo]::Local.DisplayName)
        locale = TextOrEmpty ([Globalization.CultureInfo]::CurrentCulture.DisplayName)
    }
})

StartSection "network"
$network = @()
foreach ($adapter in @(Get-NetAdapter -ErrorAction SilentlyContinue | Where-Object { $_.Status -eq "Up" })) {
    $config = Get-NetIPConfiguration -InterfaceIndex $adapter.InterfaceIndex -ErrorAction SilentlyContinue
    $ipv4 = @($config.IPv4Address | ForEach-Object { TextOrEmpty $_.IPAddress } | Where-Object { $_ })
    $gateways = @($config.IPv4DefaultGateway | ForEach-Object { TextOrEmpty $_.NextHop } | Where-Object { $_ })
    $dns = @($config.DNSServer |
        Where-Object { $_.AddressFamily -eq 2 } |
        ForEach-Object { $_.ServerAddresses } |
        Where-Object { $_ })
    $type = TextOrEmpty $adapter.MediaType
    if ($type -eq "802.3") { $type = "以太网" }
    elseif ($type -eq "Native 802.11") { $type = "无线" }
    $network += [ordered]@{
        name = TextOrEmpty $adapter.InterfaceAlias
        description = TextOrEmpty $adapter.InterfaceDescription
        type = $type
        status = TextOrEmpty $adapter.Status
        linkSpeed = TextOrEmpty $adapter.LinkSpeed
        macAddress = TextOrEmpty $adapter.MacAddress
        ipv4 = $ipv4
        gateways = $gateways
        dns = $dns
    }
}
EmitSection "network" ([ordered]@{ network = $network })

StartSection "security"
$secureBoot = "不支持或未知"
try {
    $enabled = Confirm-SecureBootUEFI -ErrorAction Stop
    $secureBoot = SecurityText $enabled "已启用" "未启用"
} catch {
    $secureBoot = "不支持或未知"
}

$tpm = "无法读取"
$tpmVersion = ""
try {
    $tpmState = Get-Tpm -ErrorAction Stop
    if ($tpmState.TpmPresent) {
        $tpm = if ($tpmState.TpmReady) { "已就绪" } else { "已检测到，未就绪" }
        $tpmVersion = TextOrEmpty $tpmState.ManufacturerVersion
    } else {
        $tpm = "未检测到"
    }
} catch {
    $tpm = "无法读取"
}

$defender = "无法读取"
$defenderRealtime = "无法读取"
try {
    $mp = Get-MpComputerStatus -ErrorAction Stop
    $defender = SecurityText $mp.AntivirusEnabled "已启用" "未启用"
    $defenderRealtime = SecurityText $mp.RealTimeProtectionEnabled "已启用" "未启用"
} catch {
    $defender = "无法读取"
    $defenderRealtime = "无法读取"
}

$firewall = "无法读取"
try {
    $profiles = @(Get-NetFirewallProfile -ErrorAction Stop)
    $enabledProfiles = @($profiles | Where-Object { $_.Enabled -eq $true }).Count
    $firewall = "$enabledProfiles/$($profiles.Count) 个配置文件已启用"
} catch {
    $firewall = "无法读取"
}

$bitlocker = "无法读取"
try {
    $systemVolume = Get-BitLockerVolume -MountPoint $env:SystemDrive -ErrorAction Stop
    $bitlocker = TextOrEmpty $systemVolume.ProtectionStatus
    if ($bitlocker -eq "On") { $bitlocker = "系统盘已加密" }
    elseif ($bitlocker -eq "Off") { $bitlocker = "系统盘未加密" }
} catch {
    $bitlocker = "无法读取"
}

$virtualization = if ($computer.HypervisorPresent) { "已启用" } else { "未检测到" }
EmitSection "security" ([ordered]@{
    security = [ordered]@{
        secureBoot = $secureBoot
        tpm = $tpm
        tpmVersion = $tpmVersion
        defender = $defender
        defenderRealtime = $defenderRealtime
        firewall = $firewall
        bitLocker = $bitlocker
        virtualization = $virtualization
    }
})

StartSection "power"
$battery = [ordered]@{
    present = $false
    name = ""
    chargePercent = 0
    status = ""
    runtimeMinutes = 0
}
$batteryState = Get-CimInstance Win32_Battery -ErrorAction SilentlyContinue | Select-Object -First 1
if ($batteryState) {
    $battery.present = $true
    $battery.name = TextOrEmpty $batteryState.Name
    $battery.chargePercent = if ($batteryState.EstimatedChargeRemaining) { [int]$batteryState.EstimatedChargeRemaining } else { 0 }
    $battery.status = TextOrEmpty $batteryState.Status
    $battery.runtimeMinutes = if ($batteryState.EstimatedRunTime) { [int]$batteryState.EstimatedRunTime } else { 0 }
}
EmitSection "power" ([ordered]@{ battery = $battery })
`
