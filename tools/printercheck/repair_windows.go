//go:build windows

package printercheck

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"opentools/pkg/tool"
)

// repairResult is what the elevated script reports back.
type repairResult struct {
	OK      bool   `json:"ok"`
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
}

// applyRepair performs one repair. Every repair that changes a system
// setting runs in an elevated process, so the host asks for UAC once.
func applyRepair(ctx context.Context, item repair, report tool.Reporter) (string, error) {
	temp, err := os.MkdirTemp("", "opentools-printer-*")
	if err != nil {
		return "", fmt.Errorf("创建临时目录失败：%w", err)
	}
	defer os.RemoveAll(temp)

	inputPath := filepath.Join(temp, "input.json")
	outputPath := filepath.Join(temp, "result.json")
	scriptPath := filepath.Join(temp, "repair-printer.ps1")
	launcherPath := filepath.Join(temp, "launch-elevated.ps1")

	payload := map[string]any{
		"kind":       string(item.Kind),
		"printer":    item.Printer,
		"portName":   item.PortName,
		"host":       item.Host,
		"portNumber": item.PortNumber,
		"startType":  item.StartType,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	// Windows PowerShell 5.1 reads a BOM-less file as ANSI, which corrupts
	// the Chinese text. Every file the script reads or executes therefore
	// carries a UTF-8 BOM.
	if err := os.WriteFile(inputPath, withBOM(data), 0o600); err != nil {
		return "", fmt.Errorf("写入修复参数失败：%w", err)
	}
	if err := os.WriteFile(scriptPath, withBOM([]byte(repairScript)), 0o600); err != nil {
		return "", fmt.Errorf("写入修复脚本失败：%w", err)
	}
	if err := os.WriteFile(launcherPath, withBOM([]byte(repairLauncher)), 0o600); err != nil {
		return "", fmt.Errorf("写入提权启动脚本失败：%w", err)
	}

	powershell, err := windowsPowerShell()
	if err != nil {
		return "", err
	}
	report.Log("info", "正在请求管理员权限执行修复")
	cmd := exec.CommandContext(
		ctx,
		powershell,
		"-NoLogo",
		"-NoProfile",
		"-NonInteractive",
		"-ExecutionPolicy",
		"Bypass",
		"-File",
		launcherPath,
	)
	cmd.Env = append(os.Environ(),
		"OT_PRINTER_INPUT="+inputPath,
		"OT_PRINTER_OUTPUT="+outputPath,
		"OT_PRINTER_SCRIPT="+scriptPath,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	if runErr != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return "", context.Canceled
		}
		// The elevated script writes its own error message, which is far more
		// useful than the exit code of the launcher.
		if result, ok := readRepairResult(outputPath); ok {
			if strings.TrimSpace(result.Error) == "" {
				result.Error = launcherDetail(stdout, stderr, runErr)
			}
			if result.Error == "" {
				result.Error = runErr.Error()
			}
			return "", fmt.Errorf("修复失败：%s", result.Error)
		}
		detail := launcherDetail(stdout, stderr, runErr)
		if detail == "" {
			return "", fmt.Errorf("修复未完成（可能取消了 UAC）：%s", runErr)
		}
		return "", fmt.Errorf("修复未完成（可能取消了 UAC）：%s", detail)
	}
	result, ok := readRepairResult(outputPath)
	if !ok {
		return "", errors.New("修复脚本没有返回结果")
	}
	if !result.OK {
		if strings.TrimSpace(result.Error) == "" {
			result.Error = "未知错误"
		}
		return "", fmt.Errorf("修复失败：%s", result.Error)
	}
	return result.Message, nil
}

// withBOM prefixes a UTF-8 byte sequence with the byte order mark that
// Windows PowerShell 5.1 needs to treat the file as UTF-8.
func withBOM(data []byte) []byte {
	return append([]byte{0xEF, 0xBB, 0xBF}, data...)
}

// launcherDetail collects whatever the launcher printed, so a failed
// elevation is not reported as a bare exit code.
func launcherDetail(stdout, stderr bytes.Buffer, runErr error) string {
	parts := make([]string, 0, 3)
	for _, buffer := range []bytes.Buffer{stdout, stderr} {
		text := strings.TrimSpace(buffer.String())
		if text != "" {
			parts = append(parts, text)
		}
	}
	if len(parts) == 0 {
		if runErr != nil {
			return runErr.Error()
		}
		return ""
	}
	return strings.Join(parts, "；")
}

// readRepairResult reads the elevated script's report, if it wrote one.
func readRepairResult(path string) (repairResult, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return repairResult{}, false
	}
	// Windows PowerShell 5.1 writes UTF-8 with a BOM, which is not valid JSON.
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	var result repairResult
	if err := json.Unmarshal(bytes.TrimSpace(data), &result); err != nil {
		return repairResult{}, false
	}
	return result, true
}

// windowsPowerShell returns the system Windows PowerShell 5.1 executable.
func windowsPowerShell() (string, error) {
	executable := filepath.Join(
		os.Getenv("SystemRoot"),
		"System32",
		"WindowsPowerShell",
		"v1.0",
		"powershell.exe",
	)
	if _, err := os.Stat(executable); err == nil {
		return executable, nil
	}
	found, lookupErr := exec.LookPath("powershell.exe")
	if lookupErr != nil {
		return "", errors.New("未找到 Windows PowerShell 5.1")
	}
	return found, nil
}

// The launcher only forwards paths that the tool itself created, so no user
// input ever reaches a command line.
const repairLauncher = `$ErrorActionPreference = "Stop"
$psi = New-Object System.Diagnostics.ProcessStartInfo
$psi.FileName = Join-Path $env:SystemRoot "System32\WindowsPowerShell\v1.0\powershell.exe"
$psi.UseShellExecute = $true
$psi.Verb = "RunAs"
$psi.WindowStyle = [System.Diagnostics.ProcessWindowStyle]::Hidden
$psi.Arguments = "-NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File " + '"' + $env:OT_PRINTER_SCRIPT + '" ' + '"' + $env:OT_PRINTER_INPUT + '" ' + '"' + $env:OT_PRINTER_OUTPUT + '"'
$p = [System.Diagnostics.Process]::Start($psi)
$p.WaitForExit()
exit $p.ExitCode
`

// The repair script is a fixed program: every argument arrives as JSON, and
// nothing is interpolated into the script source.
const repairScript = `param(
    [Parameter(Mandatory)][string]$InputPath,
    [Parameter(Mandatory)][string]$OutputPath
)
$ErrorActionPreference = "Stop"
$job = Get-Content -Raw -LiteralPath $InputPath | ConvertFrom-Json
$output = [ordered]@{ ok = $false; error = ""; message = "" }

function Set-SpoolerStartType([string]$mode) {
    $service = Get-Service -Name Spooler -ErrorAction Stop
    $target = if ($mode -eq "Automatic") { [System.ServiceProcess.ServiceStartMode]::Automatic } else { $service.StartType }
    if ($service.StartType -ne $target) {
        Set-Service -Name Spooler -StartupType $target -ErrorAction Stop
    }
}

function Start-Spooler {
    $service = Get-Service -Name Spooler -ErrorAction Stop
    if ($service.Status -ne "Running") {
        Start-Service -Name Spooler -ErrorAction Stop
        $service.WaitForStatus([System.ServiceProcess.ServiceControllerStatus]::Running, [TimeSpan]::FromSeconds(20))
    }
}

function Set-PrinterOffline([string]$printer, [bool]$offline) {
    $queue = @(Get-CimInstance Win32_Printer -ErrorAction Stop) |
        Where-Object { [string]$_.Name -eq $printer } |
        Select-Object -First 1
    if (-not $queue) { throw "找不到打印队列：$printer" }
    if ([bool]$queue.WorkOffline -ne $offline) {
        Set-CimInstance -InputObject $queue -Property @{ WorkOffline = $offline } -ErrorAction Stop | Out-Null
    }
}

function Clear-PrintJobs([string]$printer) {
    $jobs = @(Get-PrintJob -PrinterName $printer -ErrorAction Stop)
    $count = $jobs.Count
    foreach ($item in $jobs) {
        Remove-PrintJob -PrinterName $printer -ID $item.ID -ErrorAction SilentlyContinue
    }
    return $count
}

function Set-PrinterDefault([string]$printer) {
    $queue = @(Get-CimInstance Win32_Printer -ErrorAction Stop) |
        Where-Object { [string]$_.Name -eq $printer } |
        Select-Object -First 1
    if (-not $queue) { throw "找不到打印队列：$printer" }
    Invoke-CimMethod -InputObject $queue -MethodName SetDefaultPrinter -ErrorAction Stop | Out-Null
}

function Ensure-TCPPort([string]$portName, [string]$hostName, [int]$portNumber) {
    $existing = Get-PrinterPort -Name $portName -ErrorAction SilentlyContinue
    if ($existing) {
        $same = ([string]$existing.PrinterHostAddress -eq $hostName) -and ([int]$existing.PortNumber -eq $portNumber)
        if ($same) { return }
        Remove-PrinterPort -Name $portName -ErrorAction Stop
    }
    Add-PrinterPort -Name $portName -PrinterHostAddress $hostName -PortNumber $portNumber -ErrorAction Stop
}

function Set-QueuePort([string]$printer, [string]$portName) {
    $queue = Get-Printer -Name $printer -ErrorAction Stop
    Set-Printer -Name $printer -PortName $portName -ErrorAction Stop
    return $queue
}

try {
    switch ([string]$job.kind) {
        "start-spooler" {
            Start-Spooler
            $output.message = "打印服务已启动。"
        }
        "set-spooler-start-type" {
            Set-SpoolerStartType ([string]$job.startType)
            $output.message = "打印服务的启动类型已改为自动。"
        }
        "clear-offline" {
            Set-PrinterOffline ([string]$job.printer) $false
            $output.message = "打印队列已取消脱机状态。"
        }
        "clear-jobs" {
            $count = Clear-PrintJobs ([string]$job.printer)
            $output.message = "已清除 $count 个待处理任务。"
        }
        "set-default" {
            Set-PrinterDefault ([string]$job.printer)
            $output.message = "已设为默认打印机。"
        }
        "rebind-port" {
            $portName = [string]$job.portName
            $hostName = [string]$job.host
            $portNumber = [int]$job.portNumber
            if (-not $hostName) { throw "缺少打印机地址" }
            if ($portNumber -le 0) { $portNumber = 9100 }
            Ensure-TCPPort $portName $hostName $portNumber
            Set-QueuePort ([string]$job.printer) $portName | Out-Null
            $output.message = "打印队列已改用 TCP/IP 端口 $portName（$($hostName):$($portNumber)）。"
        }
        default {
            throw "未知修复操作"
        }
    }
    $output.ok = $true
} catch {
    $output.error = $_.Exception.Message
}
$output | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath $OutputPath -Encoding UTF8
if (-not $output.ok) { exit 1 }
`
