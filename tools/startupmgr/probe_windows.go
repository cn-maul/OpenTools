//go:build windows

package startupmgr

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
	output, err := runPowerShell(ctx, startupScanScript)
	if err != nil {
		return snapshot{}, err
	}
	output = bytes.TrimSpace(output)
	if len(output) == 0 {
		return snapshot{}, fmt.Errorf("启动项扫描没有返回数据")
	}
	var value snapshot
	if err := json.Unmarshal(output, &value); err != nil {
		return snapshot{}, fmt.Errorf("解析启动项数据失败：%w", err)
	}
	if err := ctx.Err(); err != nil {
		return snapshot{}, err
	}
	return value, nil
}

const startupScanScript = `
$ErrorActionPreference = "SilentlyContinue"
$ProgressPreference = "SilentlyContinue"

$principal = [Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()
$result = [ordered]@{
    collectedAt = (Get-Date).ToString("o")
    isAdmin = $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
    items = @()
}

function ApprovedState([string]$approvedPath, [string]$name) {
    if (-not $approvedPath) { return $true }
    $holder = Get-ItemProperty -LiteralPath $approvedPath -ErrorAction SilentlyContinue
    if (-not $holder) { return $true }
    $data = $holder.$name
    if ($null -eq $data) { return $true }
    return -not ($data[0] -eq 3)
}

function Add-RunEntries([string]$runPath, [string]$approvedPath, [string]$scope, [string]$scopeNote) {
    $values = Get-ItemProperty -LiteralPath $runPath -ErrorAction SilentlyContinue
    if (-not $values) { return }
    foreach ($property in $values.PSObject.Properties) {
        if ($property.Name -in @("PSPath", "PSParentPath", "PSChildName", "PSDrive", "PSProvider")) { continue }
        $result.items += [ordered]@{
            kind = "run"
            name = [string]$property.Name
            location = $runPath
            command = [string]$property.Value
            enabled = (ApprovedState $approvedPath ([string]$property.Name))
            scope = $scope
            note = $scopeNote
            approvedPath = $approvedPath
            taskPath = ""
            startType = ""
            runState = ""
        }
    }
}

$runKey = "Software\Microsoft\Windows\CurrentVersion\Run"
$approvedKey = "Software\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\Run"
Add-RunEntries ("HKCU:\" + $runKey) ("HKCU:\" + $approvedKey) "user" "当前用户注册表"
Add-RunEntries ("HKLM:\" + $runKey) ("HKLM:\" + $approvedKey) "machine" "全机注册表"
Add-RunEntries ("HKLM:\Software\Wow6432Node\" + $runKey) ("HKLM:\Software\Wow6432Node\" + $approvedKey) "machine" "全机注册表（32 位）"

$folders = @(
    @{ path = [Environment]::GetFolderPath("Startup"); scope = "user"; note = "启动文件夹（当前用户）"; approved = "HKCU:\Software\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\StartupFolder" },
    @{ path = [Environment]::GetFolderPath("CommonStartup"); scope = "machine"; note = "启动文件夹（所有用户）"; approved = "HKLM:\Software\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\StartupFolder" }
)
foreach ($folder in $folders) {
    if (-not (Test-Path -LiteralPath $folder.path)) { continue }
    foreach ($file in @(Get-ChildItem -LiteralPath $folder.path -File -ErrorAction SilentlyContinue)) {
        $result.items += [ordered]@{
            kind = "folder"
            name = [string]$file.Name
            location = [string]$folder.path
            command = [string]$file.FullName
            enabled = (ApprovedState $folder.approved ([string]$file.Name))
            scope = [string]$folder.scope
            note = [string]$folder.note
            approvedPath = [string]$folder.approved
            taskPath = ""
            startType = ""
            runState = ""
        }
    }
}

foreach ($task in @(Get-ScheduledTask)) {
    if ([string]$task.TaskPath -like "\Microsoft\*") { continue }
    $triggers = @($task.Triggers | Where-Object { $_.CimClass.CimClassName -in @("MSFT_TaskLogonTrigger", "MSFT_TaskBootTrigger") })
    if ($triggers.Count -eq 0) { continue }
    $action = $task.Actions | Select-Object -First 1
    $command = ([string]$action.Execute + " " + [string]$action.Arguments).Trim()
    $result.items += [ordered]@{
        kind = "task"
        name = [string]$task.TaskName
        location = [string]$task.TaskPath
        command = $command
        enabled = ([string]$task.State -ne "Disabled")
        scope = "machine"
        note = "登录 / 开机触发"
        approvedPath = ""
        taskPath = [string]$task.TaskPath
        startType = ""
        runState = ""
    }
}

foreach ($service in @(Get-CimInstance Win32_Service)) {
    $path = ([string]$service.PathName).Trim()
    if ($path -eq "") { continue }
    if ($path -like "*\Windows\*") { continue }
    $startMode = [string]$service.StartMode
    if ($startMode -notin @("Auto", "Manual", "Disabled")) { continue }
    $delayed = (Get-ItemProperty -LiteralPath ("HKLM:\SYSTEM\CurrentControlSet\Services\" + $service.Name) -ErrorAction SilentlyContinue).DelayedAutostart
    $startType = $startMode
    if ($startMode -eq "Auto") {
        $startType = "Automatic"
        if ($delayed -eq 1) {
            $startType = "AutomaticDelayed"
        }
    }
    $result.items += [ordered]@{
        kind = "service"
        name = [string]$service.Name
        location = [string]$service.DisplayName
        command = $path
        enabled = ($startMode -ne "Disabled")
        scope = "machine"
        note = [string]$service.DisplayName
        approvedPath = ""
        taskPath = ""
        startType = $startType
        runState = [string]$service.State
    }
}

$result | ConvertTo-Json -Depth 6 -Compress
`

const startupToggleScript = `param(
    [Parameter(Mandatory)][string]$InputPath,
    [Parameter(Mandatory)][string]$OutputPath
)
$ErrorActionPreference = "Stop"
$job = Get-Content -Raw -LiteralPath $InputPath | ConvertFrom-Json
$output = [ordered]@{ ok = $false; error = ""; message = "" }

try {
    switch ([string]$job.kind) {
        "approved" {
            $approved = [string]$job.approvedPath
            $name = [string]$job.name
            if ([bool]$job.enable) {
                Remove-ItemProperty -LiteralPath $approved -Name $name -ErrorAction SilentlyContinue
                $output.message = "已启用开机启动项：$name"
            } else {
                if (-not (Test-Path -LiteralPath $approved)) {
                    New-Item -Path $approved -Force | Out-Null
                }
                $bytes = [byte[]](3, 0, 0, 0) + [BitConverter]::GetBytes([DateTime]::UtcNow.ToFileTime())
                New-ItemProperty -LiteralPath $approved -Name $name -PropertyType Binary -Value $bytes -Force | Out-Null
                $output.message = "已禁用开机启动项：$name"
            }
            $holder = Get-ItemProperty -LiteralPath $approved -ErrorAction SilentlyContinue
            $data = $holder.$name
            $disabled = ($null -ne $data) -and ($data[0] -eq 3)
            if ([bool]$job.enable -eq $disabled) {
                throw "启动项状态未按预期改变，请重新扫描确认"
            }
        }
        "task" {
            $path = [string]$job.taskPath
            $name = [string]$job.name
            if ([bool]$job.enable) {
                Enable-ScheduledTask -TaskPath $path -TaskName $name -ErrorAction Stop | Out-Null
                $output.message = "已启用计划任务：$name"
            } else {
                Disable-ScheduledTask -TaskPath $path -TaskName $name -ErrorAction Stop | Out-Null
                $output.message = "已禁用计划任务：$name"
            }
        }
        "service" {
            $name = [string]$job.name
            $startType = [string]$job.startType
            $serviceKey = "HKLM:\SYSTEM\CurrentControlSet\Services\" + $name
            switch ($startType) {
                "AutomaticDelayed" {
                    Set-Service -Name $name -StartupType Automatic -ErrorAction Stop
                    New-ItemProperty -LiteralPath $serviceKey -Name DelayedAutostart -PropertyType DWord -Value 1 -Force | Out-Null
                }
                "Automatic" {
                    Set-Service -Name $name -StartupType Automatic -ErrorAction Stop
                    New-ItemProperty -LiteralPath $serviceKey -Name DelayedAutostart -PropertyType DWord -Value 0 -Force | Out-Null
                }
                "Manual" {
                    Set-Service -Name $name -StartupType Manual -ErrorAction Stop
                    Remove-ItemProperty -LiteralPath $serviceKey -Name DelayedAutostart -ErrorAction SilentlyContinue
                }
                "Disabled" {
                    Set-Service -Name $name -StartupType Disabled -ErrorAction Stop
                    Remove-ItemProperty -LiteralPath $serviceKey -Name DelayedAutostart -ErrorAction SilentlyContinue
                }
                default {
                    throw "未知服务启动类型：$startType"
                }
            }
            $output.message = "服务 $name 的启动类型已改为 " + [string]$job.startTypeText
        }
        default {
            throw "未知启动项类型"
        }
    }
    $output.ok = $true
} catch {
    $output.error = $_.Exception.Message
}
$output | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath $OutputPath -Encoding UTF8
if (-not $output.ok) { exit 1 }
`

// The launcher only forwards paths that the tool itself created.
const startupLauncher = `$ErrorActionPreference = "Stop"
$psi = New-Object System.Diagnostics.ProcessStartInfo
$psi.FileName = Join-Path $env:SystemRoot "System32\WindowsPowerShell\v1.0\powershell.exe"
$psi.UseShellExecute = $true
$psi.Verb = "RunAs"
$psi.WindowStyle = [System.Diagnostics.ProcessWindowStyle]::Hidden
$psi.Arguments = "-NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File " + '"' + $env:OT_STARTUP_SCRIPT + '" ' + '"' + $env:OT_STARTUP_INPUT + '" ' + '"' + $env:OT_STARTUP_OUTPUT + '"'
$p = [System.Diagnostics.Process]::Start($psi)
$p.WaitForExit()
exit $p.ExitCode
`
