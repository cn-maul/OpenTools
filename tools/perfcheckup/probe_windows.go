//go:build windows

package perfcheckup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
)

type processInfo struct {
	Name       string  `json:"name"`
	PID        int     `json:"pid"`
	CPUPercent float64 `json:"cpuPercent"`
	MemBytes   int64   `json:"memBytes"`
	CPUSeconds float64 `json:"cpuSeconds"`
}

type diskUsage struct {
	Drive     string `json:"drive"`
	Label     string `json:"label"`
	SizeBytes int64  `json:"sizeBytes"`
	FreeBytes int64  `json:"freeBytes"`
}

type snapshot struct {
	CollectedAt    string        `json:"collectedAt"`
	LogicalCores   int           `json:"logicalCores"`
	MemTotalBytes  int64         `json:"memTotalBytes"`
	MemFreeBytes   int64         `json:"memFreeBytes"`
	CPULoadPercent float64       `json:"cpuLoadPercent"`
	ProcessCount   int           `json:"processCount"`
	StartupCount   int           `json:"startupCount"`
	SampleSeconds  float64       `json:"sampleSeconds"`
	TopByMem       []processInfo `json:"topByMem"`
	TopByCPU       []processInfo `json:"topByCpu"`
	Disks          []diskUsage   `json:"disks"`
}

func collect(ctx context.Context) (snapshot, error) {
	if err := ctx.Err(); err != nil {
		return snapshot{}, err
	}
	output, err := runPowerShell(ctx, perfCheckupScript)
	if err != nil {
		return snapshot{}, err
	}
	output = bytes.TrimSpace(output)
	if len(output) == 0 {
		return snapshot{}, fmt.Errorf("卡慢体检没有返回数据")
	}
	var value snapshot
	if err := json.Unmarshal(output, &value); err != nil {
		return snapshot{}, fmt.Errorf("解析卡慢体检数据失败：%w", err)
	}
	if err := ctx.Err(); err != nil {
		return snapshot{}, err
	}
	return value, nil
}

// The script samples per-process CPU twice around a short sleep, so the CPU
// rankings reflect real usage during the run instead of lifetime totals.
const perfCheckupScript = `
$ErrorActionPreference = "SilentlyContinue"
$ProgressPreference = "SilentlyContinue"

$result = [ordered]@{
    collectedAt = (Get-Date).ToString("o")
    logicalCores = 1
    memTotalBytes = 0
    memFreeBytes = 0
    cpuLoadPercent = 0
    processCount = 0
    startupCount = 0
    sampleSeconds = 0
    topByMem = @()
    topByCpu = @()
    disks = @()
}

$system = Get-CimInstance Win32_ComputerSystem
$os = Get-CimInstance Win32_OperatingSystem
if ($system) { $result.logicalCores = [int]$system.NumberOfLogicalProcessors }
if ($result.logicalCores -lt 1) { $result.logicalCores = 1 }
if ($os) {
    $result.memTotalBytes = [int64]$os.TotalVisibleMemorySize * 1024
    $result.memFreeBytes = [int64]$os.FreePhysicalMemory * 1024
}

$before = @{}
foreach ($process in Get-Process) {
    $before[[int]$process.Id] = $process.TotalProcessorTime.TotalSeconds
}
$stopwatch = [System.Diagnostics.Stopwatch]::StartNew()
Start-Sleep -Milliseconds 1500
$stopwatch.Stop()
$elapsed = $stopwatch.Elapsed.TotalSeconds
$result.sampleSeconds = [math]::Round($elapsed, 2)

$samples = @()
$totalDelta = 0.0
foreach ($process in Get-Process) {
    $id = [int]$process.Id
    if ($id -eq 0) { continue }
    $previous = $before[$id]
    if ($null -eq $previous) { continue }
    $delta = $process.TotalProcessorTime.TotalSeconds - $previous
    if ($delta -lt 0) { $delta = 0 }
    $totalDelta += $delta
    $samples += [pscustomobject]@{
        name = [string]$process.ProcessName
        pid = $id
        cpuPercent = [math]::Round($delta * 100 / ($elapsed * $result.logicalCores), 1)
        memBytes = [int64]$process.WorkingSet64
        cpuSeconds = [math]::Round($process.TotalProcessorTime.TotalSeconds, 1)
    }
}
$result.processCount = $samples.Count
$result.cpuLoadPercent = [math]::Round($totalDelta * 100 / ($elapsed * $result.logicalCores), 1)
$result.topByCpu = @($samples | Sort-Object cpuPercent -Descending | Select-Object -First 10)
$result.topByMem = @($samples | Sort-Object memBytes -Descending | Select-Object -First 10)

foreach ($disk in @(Get-CimInstance Win32_LogicalDisk -Filter "DriveType=3")) {
    $result.disks += [ordered]@{
        drive = [string]$disk.DeviceID
        label = [string]$disk.VolumeName
        sizeBytes = [int64]$disk.Size
        freeBytes = [int64]$disk.FreeSpace
    }
}

$runKeys = @(
    "HKCU:\Software\Microsoft\Windows\CurrentVersion\Run",
    "HKLM:\Software\Microsoft\Windows\CurrentVersion\Run"
)
foreach ($path in $runKeys) {
    $values = Get-ItemProperty -LiteralPath $path -ErrorAction SilentlyContinue
    if (-not $values) { continue }
    foreach ($property in $values.PSObject.Properties) {
        if ($property.Name -in @("PSPath", "PSParentPath", "PSChildName", "PSDrive", "PSProvider")) { continue }
        $result.startupCount++
    }
}
$result.startupCount += @(Get-ChildItem ([Environment]::GetFolderPath("Startup")) -File -ErrorAction SilentlyContinue).Count
$result.startupCount += @(Get-ChildItem ([Environment]::GetFolderPath("CommonStartup")) -File -ErrorAction SilentlyContinue).Count

$result | ConvertTo-Json -Depth 6 -Compress
`
