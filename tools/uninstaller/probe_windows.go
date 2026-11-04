//go:build windows

package uninstaller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

func collect(ctx context.Context) (snapshot, error) {
	if err := ctx.Err(); err != nil {
		return snapshot{}, err
	}
	output, err := runPowerShell(ctx, uninstallScanScript)
	if err != nil {
		return snapshot{}, err
	}
	output = bytes.TrimSpace(output)
	if len(output) == 0 {
		return snapshot{}, fmt.Errorf("软件清单没有返回数据")
	}
	var value snapshot
	if err := json.Unmarshal(output, &value); err != nil {
		return snapshot{}, fmt.Errorf("解析软件清单失败：%w", err)
	}
	if err := ctx.Err(); err != nil {
		return snapshot{}, err
	}
	return value, nil
}

const uninstallScanScript = `
$ErrorActionPreference = "SilentlyContinue"
$ProgressPreference = "SilentlyContinue"

$principal = [Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()
$result = [ordered]@{
    collectedAt = (Get-Date).ToString("o")
    isAdmin = $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
    apps = @()
}

Add-Type -AssemblyName System.Drawing

function Get-AppIconData([string]$displayIcon) {
    $raw = $displayIcon.Trim()
    if ($raw -eq "") { return "" }
    $path = $raw
    $index = 0
    if ($raw.StartsWith('"')) {
        $end = $raw.IndexOf('"', 1)
        if ($end -gt 0) {
            $path = $raw.Substring(1, $end - 1)
            $rest = $raw.Substring($end + 1).TrimStart(' ', ',')
            if ($rest -match '^-?\d+$') { $index = [int]$rest }
        }
    } elseif ($raw -match '^(.*),\s*(-?\d+)$') {
        $path = $Matches[1].Trim().Trim('"')
        $index = [int]$Matches[2]
    }
    $path = [Environment]::ExpandEnvironmentVariables($path)
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { return "" }
    $icon = $null
    $bitmap = $null
    $small = $null
    $graphics = $null
    $stream = $null
    try {
        if ([IO.Path]::GetExtension($path).ToLowerInvariant() -eq ".ico") {
            $icon = New-Object System.Drawing.Icon($path)
        } else {
            $icon = [System.Drawing.Icon]::ExtractAssociatedIcon($path)
        }
        if ($null -eq $icon) { return "" }
        $bitmap = $icon.ToBitmap()
        $small = New-Object System.Drawing.Bitmap 28,28
        $graphics = [System.Drawing.Graphics]::FromImage($small)
        $graphics.InterpolationMode = [System.Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
        $graphics.DrawImage($bitmap, 0, 0, 28, 28)
        $stream = New-Object IO.MemoryStream
        $small.Save($stream, [System.Drawing.Imaging.ImageFormat]::Png)
        return "data:image/png;base64," + [Convert]::ToBase64String($stream.ToArray())
    } catch {
        return ""
    } finally {
        if ($stream) { $stream.Dispose() }
        if ($graphics) { $graphics.Dispose() }
        if ($small) { $small.Dispose() }
        if ($bitmap) { $bitmap.Dispose() }
        if ($icon) { $icon.Dispose() }
    }
}

$roots = @(
    @{ hive = "HKLM"; path = "HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall" },
    @{ hive = "HKLM32"; path = "HKLM:\SOFTWARE\Wow6432Node\Microsoft\Windows\CurrentVersion\Uninstall" },
    @{ hive = "HKCU"; path = "HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall" }
)

foreach ($root in $roots) {
    if (-not (Test-Path -LiteralPath $root.path)) { continue }
    foreach ($entry in @(Get-ChildItem -LiteralPath $root.path -ErrorAction SilentlyContinue)) {
        $values = Get-ItemProperty -LiteralPath $entry.PSPath -ErrorAction SilentlyContinue
        if (-not $values) { continue }
        $name = ([string]$values.DisplayName).Trim()
        if ($name -eq "") { continue }
        if ([int]$values.SystemComponent -eq 1) { continue }
        if ($name -match "^KB[0-9]{4,}") { continue }
        $uninstall = ([string]$values.UninstallString).Trim()
        if ($uninstall -eq "") { continue }
        $size = 0
        try { $size = [int64]$values.EstimatedSize * 1024 } catch { $size = 0 }
        $result.apps += [ordered]@{
            key = "Registry::" + [string]$entry.PSPath.Replace("Microsoft.PowerShell.Core\Registry::", "")
            hive = [string]$root.hive
            name = $name
            version = [string]$values.DisplayVersion
            publisher = [string]$values.Publisher
            installDate = [string]$values.InstallDate
            sizeBytes = $size
            icon = Get-AppIconData ([string]$values.DisplayIcon)
            uninstallString = $uninstall
            quietString = ([string]$values.QuietUninstallString).Trim()
            windowsInstaller = ([int]$values.WindowsInstaller -eq 1)
        }
    }
}

$result | ConvertTo-Json -Depth 6 -Compress
`

// The uninstall script reads the registry entry itself, so the only inputs
// are a registry path and a display name chosen from scan results.
const uninstallActionScript = `param(
    [Parameter(Mandatory)][string]$InputPath,
    [Parameter(Mandatory)][string]$OutputPath
)
$ErrorActionPreference = "Stop"
$job = Get-Content -Raw -LiteralPath $InputPath | ConvertFrom-Json
$output = [ordered]@{ ok = $false; error = ""; message = ""; stillInstalled = $false }

function Split-CommandLine([string]$line) {
    $line = $line.Trim()
    if ($line.StartsWith('"')) {
        $end = $line.IndexOf('"', 1)
        if ($end -lt 0) { return @($line.Trim('"'), "") }
        return @($line.Substring(1, $end - 1), $line.Substring($end + 1).Trim())
    }
    for ($end = $line.Length - 1; $end -gt 0; $end--) {
        if ($line[$end] -ne ' ') { continue }
        $candidate = $line.Substring(0, $end).Trim()
        if (Test-Path -LiteralPath $candidate -PathType Leaf) {
            return @($candidate, $line.Substring($end + 1).Trim())
        }
    }
    return @($line, "")
}

try {
    $key = [string]$job.key
    if (-not $key.StartsWith("Registry::") -and
        -not $key.StartsWith("Microsoft.PowerShell.Core\Registry::")) {
        if ($key.StartsWith("HKLM:")) {
            $key = "Registry::HKEY_LOCAL_MACHINE" + $key.Substring(5)
        } elseif ($key.StartsWith("HKCU:")) {
            $key = "Registry::HKEY_CURRENT_USER" + $key.Substring(5)
        } else {
            $key = "Registry::" + $key
        }
    }
    if (-not (Test-Path -LiteralPath $key)) { throw "注册表项不存在：$key" }
    $values = Get-ItemProperty -LiteralPath $key
    $quiet = ([string]$values.QuietUninstallString).Trim()
    $normal = ([string]$values.UninstallString).Trim()
    $commandLine = ""
    $mode = "quiet"
    if ($quiet) {
        $commandLine = $quiet
    } elseif ($normal -match "(?i)msiexec") {
        $code = [regex]::Match($normal, "\{[0-9A-Fa-f\-]{36}\}")
        if (-not $code.Success) { throw "无法从卸载命令解析产品代码" }
        $switch = "/qb"
        if ($normal -match "(?i)/qn|/quiet") { $switch = "/qn" }
        $commandLine = "msiexec.exe /x " + $code.Value + " " + $switch
    } elseif ($normal) {
        $commandLine = $normal
        $mode = "interactive"
    } else {
        throw "该应用没有提供卸载命令"
    }
    $parts = Split-CommandLine $commandLine
    $process = if ($parts[1]) {
        Start-Process -FilePath $parts[0] -ArgumentList $parts[1] -Wait -PassThru
    } else {
        Start-Process -FilePath $parts[0] -Wait -PassThru
    }
    Start-Sleep -Milliseconds 800
    $still = Test-Path -LiteralPath $key
    $output.stillInstalled = [bool]$still
    if ($still) {
        $output.message = "卸载程序已退出（代码 " + $process.ExitCode + "），注册表项仍然存在，可能安装了其他组件，稍后可重新检测。"
    } else {
        $output.message = "已卸载 " + [string]$job.name
    }
    $output.ok = $true
} catch {
    $output.error = $_.Exception.Message
}
$output | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath $OutputPath -Encoding UTF8
if (-not $output.ok) { exit 1 }
`

// The launcher only forwards paths that the tool itself created.
const uninstallLauncher = `$ErrorActionPreference = "Stop"
$psi = New-Object System.Diagnostics.ProcessStartInfo
$psi.FileName = Join-Path $env:SystemRoot "System32\WindowsPowerShell\v1.0\powershell.exe"
$psi.UseShellExecute = $true
$psi.Verb = "RunAs"
$psi.WindowStyle = [System.Diagnostics.ProcessWindowStyle]::Hidden
$psi.Arguments = "-NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File " + '"' + $env:OT_UNINSTALL_SCRIPT + '" ' + '"' + $env:OT_UNINSTALL_INPUT + '" ' + '"' + $env:OT_UNINSTALL_OUTPUT + '"'
$p = [System.Diagnostics.Process]::Start($psi)
$p.WaitForExit()
exit $p.ExitCode
`

func trimJSON(data []byte) []byte {
	return bytes.TrimPrefix(bytes.TrimSpace(data), []byte{0xEF, 0xBB, 0xBF})
}

// filterApps narrows the scan result by a case-insensitive substring on the
// display name or publisher, mirroring the input filter field.
func filterApps(apps []appEntry, filter string) []appEntry {
	filter = strings.ToLower(strings.TrimSpace(filter))
	if filter == "" {
		return apps
	}
	out := make([]appEntry, 0, len(apps))
	for _, app := range apps {
		if strings.Contains(strings.ToLower(app.Name), filter) ||
			strings.Contains(strings.ToLower(app.Publisher), filter) {
			out = append(out, app)
		}
	}
	return out
}
