//go:build windows

package crashdiag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
)

type crashEvent struct {
	Time    string `json:"time"`
	EventID int    `json:"eventID"`
	Source  string `json:"source"`
	Message string `json:"message"`
}

type minidumpFile struct {
	Name      string `json:"name"`
	Time      string `json:"time"`
	SizeBytes int64  `json:"sizeBytes"`
}

type snapshot struct {
	CollectedAt      string         `json:"collectedAt"`
	LastBoot         string         `json:"lastBoot"`
	UptimeHours      float64        `json:"uptimeHours"`
	BugChecks        []crashEvent   `json:"bugchecks"`
	Unexpected       []crashEvent   `json:"unexpected"`
	Minidumps        []minidumpFile `json:"minidumps"`
	KernelDumpExists bool           `json:"kernelDumpExists"`
	KernelDumpSize   int64          `json:"kernelDumpSize"`
}

func collect(ctx context.Context) (snapshot, error) {
	if err := ctx.Err(); err != nil {
		return snapshot{}, err
	}
	output, err := runPowerShell(ctx, crashDiagScript)
	if err != nil {
		return snapshot{}, err
	}
	output = bytes.TrimSpace(output)
	if len(output) == 0 {
		return snapshot{}, fmt.Errorf("蓝屏诊断没有返回数据")
	}
	var value snapshot
	if err := json.Unmarshal(output, &value); err != nil {
		return snapshot{}, fmt.Errorf("解析蓝屏诊断数据失败：%w", err)
	}
	if err := ctx.Err(); err != nil {
		return snapshot{}, err
	}
	return value, nil
}

const crashDiagScript = `
$ErrorActionPreference = "SilentlyContinue"
$ProgressPreference = "SilentlyContinue"

$result = [ordered]@{
    collectedAt = (Get-Date).ToString("o")
    lastBoot = ""
    uptimeHours = 0
    bugchecks = @()
    unexpected = @()
    minidumps = @()
    kernelDumpExists = $false
    kernelDumpSize = 0
}

$os = Get-CimInstance Win32_OperatingSystem
if ($os -and $os.LastBootUpTime) {
    $boot = [datetime]$os.LastBootUpTime
    $result.lastBoot = $boot.ToString("o")
    $result.uptimeHours = [math]::Round(((Get-Date) - $boot).TotalHours, 1)
}

$since = (Get-Date).AddDays(-90)
$events = @(Get-WinEvent -FilterHashtable @{
    LogName = "System"
    Id = 41, 1001, 6008
    StartTime = $since
} -ErrorAction SilentlyContinue)

foreach ($event in $events) {
    $item = [ordered]@{
        time = $event.TimeCreated.ToString("o")
        eventID = [int]$event.Id
        source = [string]$event.ProviderName
        message = ([string]$event.Message).Trim()
    }
    if ($event.Id -eq 1001) {
        $result.bugchecks += $item
    } else {
        $result.unexpected += $item
    }
}

$minidumpDir = Join-Path $env:SystemRoot "Minidump"
if (Test-Path -LiteralPath $minidumpDir) {
    foreach ($file in @(Get-ChildItem -LiteralPath $minidumpDir -File -ErrorAction SilentlyContinue | Sort-Object LastWriteTime -Descending)) {
        $result.minidumps += [ordered]@{
            name = [string]$file.Name
            time = $file.LastWriteTime.ToString("o")
            sizeBytes = [int64]$file.Length
        }
    }
}
$kernelDump = Join-Path $env:SystemRoot "MEMORY.DMP"
if (Test-Path -LiteralPath $kernelDump) {
    $result.kernelDumpExists = $true
    $result.kernelDumpSize = [int64](Get-Item -LiteralPath $kernelDump).Length
}

$result | ConvertTo-Json -Depth 6 -Compress
`
