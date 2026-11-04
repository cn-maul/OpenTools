//go:build windows

package networkconfig

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf16"

	"opentools/pkg/tool"
)

func runPowerShell(ctx context.Context, script string) ([]byte, error) {
	pwsh, err := windowsPowerShell()
	if err != nil {
		return nil, err
	}
	script = "$OutputEncoding = New-Object System.Text.UTF8Encoding $false\n" +
		"[Console]::OutputEncoding = $OutputEncoding\n" +
		script
	// Passing the script as UTF-16LE avoids mojibake in Chinese messages and
	// keeps it independent of the console code page.
	encoded := base64.StdEncoding.EncodeToString(utf16LE(script))
	cmd := exec.CommandContext(ctx, pwsh, "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-EncodedCommand", encoded)
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil, context.Canceled
		}
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return nil, fmt.Errorf("PowerShell 执行失败：%s", detail)
	}
	return stdout.Bytes(), nil
}

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
	found, err := exec.LookPath("powershell.exe")
	if err != nil {
		return "", errors.New("未找到 Windows PowerShell 5.1")
	}
	return found, nil
}

func utf16LE(value string) []byte {
	units := utf16.Encode([]rune(value))
	out := make([]byte, 0, len(units)*2)
	for _, unit := range units {
		out = append(out, byte(unit), byte(unit>>8))
	}
	return out
}

func setDNS(ctx context.Context, index int, guid, mac string, servers []string, report tool.Reporter) error {
	if len(servers) == 0 {
		return errors.New("DNS 地址为空")
	}
	for _, server := range servers {
		if !validDNSAddress(server) {
			return fmt.Errorf("无效 DNS 地址 %q", server)
		}
	}
	if index <= 0 || strings.TrimSpace(guid) == "" {
		return errors.New("主网卡标识不完整")
	}
	report.Log("info", "正在请求管理员权限修改 DNS")
	return runElevatedAction(ctx, map[string]any{
		"action":         "set",
		"interfaceIndex": index,
		"interfaceGuid":  guid,
		"macAddress":     mac,
		"servers":        servers,
	}, report)
}

type elevatedResult struct {
	OK      bool   `json:"ok"`
	Error   string `json:"error"`
	Warning string `json:"warning,omitempty"`
}

// runElevatedAction writes a fixed script and a JSON input file, then uses a
// fixed launcher to request UAC through ProcessStartInfo. No user input is ever
// interpolated into PowerShell source or into the launcher command line.
func runElevatedAction(ctx context.Context, input map[string]any, report tool.Reporter) error {
	temp, err := os.MkdirTemp("", "opentools-dns-*")
	if err != nil {
		return fmt.Errorf("创建临时目录失败：%w", err)
	}
	defer os.RemoveAll(temp)
	inputPath := filepath.Join(temp, "input.json")
	outputPath := filepath.Join(temp, "result.json")
	applyPath := filepath.Join(temp, "apply-network.ps1")
	launcherPath := filepath.Join(temp, "launch-elevated.ps1")

	data, err := json.Marshal(input)
	if err != nil {
		return err
	}
	if err := os.WriteFile(inputPath, data, 0o600); err != nil {
		return fmt.Errorf("写入切换参数失败：%w", err)
	}
	if err := os.WriteFile(applyPath, []byte(applyNetworkScript), 0o600); err != nil {
		return fmt.Errorf("写入网络配置脚本失败：%w", err)
	}
	if err := os.WriteFile(launcherPath, []byte(elevationLauncher), 0o600); err != nil {
		return fmt.Errorf("写入提权启动脚本失败：%w", err)
	}

	powershell, err := windowsPowerShell()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, powershell, "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", launcherPath)
	cmd.Env = append(os.Environ(),
		"OT_DNS_INPUT="+inputPath,
		"OT_DNS_OUTPUT="+outputPath,
		"OT_DNS_SCRIPT="+applyPath,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return context.Canceled
		}
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return fmt.Errorf("管理员操作未完成（可能取消了 UAC）：%s", detail)
	}
	resultData, err := os.ReadFile(outputPath)
	if err != nil {
		return errors.New("管理员脚本没有返回结果")
	}
	var result elevatedResult
	if err := json.Unmarshal(resultData, &result); err != nil {
		return fmt.Errorf("解析管理员操作结果失败：%w", err)
	}
	if !result.OK {
		if strings.TrimSpace(result.Error) == "" {
			result.Error = "未知错误"
		}
		return fmt.Errorf("修改网络配置失败：%s", result.Error)
	}
	if strings.TrimSpace(result.Warning) != "" {
		report.Log("warn", result.Warning)
	}
	return nil
}

const elevationLauncher = `$ErrorActionPreference = "Stop"
$psi = New-Object System.Diagnostics.ProcessStartInfo
$psi.FileName = Join-Path $env:SystemRoot "System32\WindowsPowerShell\v1.0\powershell.exe"
$psi.UseShellExecute = $true
$psi.Verb = "RunAs"
$psi.WindowStyle = [System.Diagnostics.ProcessWindowStyle]::Hidden
$psi.Arguments = "-NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File " + '"' + $env:OT_DNS_SCRIPT + '" ' + '"' + $env:OT_DNS_INPUT + '" ' + '"' + $env:OT_DNS_OUTPUT + '"'
$p = [System.Diagnostics.Process]::Start($psi)
$p.WaitForExit()
exit $p.ExitCode
`

const applyNetworkScript = `param(
    [Parameter(Mandatory)][string]$InputPath,
    [Parameter(Mandatory)][string]$OutputPath
)
$ErrorActionPreference = "Stop"
$job = Get-Content -Raw -LiteralPath $InputPath | ConvertFrom-Json
$output = [ordered]@{ ok = $false; error = "" }

function Assert-Adapter([int]$index, $job) {
    $adapter = Get-NetAdapter -InterfaceIndex $index -ErrorAction Stop
    if ($adapter.InterfaceGuid -ne $job.interfaceGuid) { throw "网卡 GUID 校验失败" }
    $normalize = { param($value) ($value -replace "[-: ]", "").ToLowerInvariant() }
    if ((& $normalize $adapter.MacAddress) -ne (& $normalize $job.macAddress)) { throw "网卡 MAC 校验失败" }
    return $adapter
}

try {
    $index = [int]$job.interfaceIndex
    if ($index -le 0) { throw "接口索引无效" }
    $adapter = Assert-Adapter $index $job
    if ($job.action -eq "set") {
        $servers = @($job.servers)
        if ($servers.Count -eq 0) { throw "DNS 地址为空" }
        foreach ($server in $servers) {
            $parsed = $null
            if (-not [System.Net.IPAddress]::TryParse([string]$server, [ref]$parsed)) { throw "DNS 地址无效：$server" }
        }
        Set-DnsClientServerAddress -InterfaceIndex $index -ServerAddresses ([string[]]$servers) -ErrorAction Stop
    } else {
        throw "未知操作"
    }
    $actual = @(Get-DnsClientServerAddress -InterfaceIndex $index -AddressFamily IPv4 | ForEach-Object { $_.ServerAddresses })
    $output.actual = $actual
    $output.ok = $true
} catch {
    $output.error = $_.Exception.Message
}
$output | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath $OutputPath -Encoding UTF8
if (-not $output.ok) { exit 1 }
`
