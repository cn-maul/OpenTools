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

func restoreNetwork(ctx context.Context, index int, guid, mac string, b backup, report tool.Reporter) error {
	if index <= 0 || strings.TrimSpace(guid) == "" {
		return errors.New("主网卡标识不完整")
	}
	report.Log("info", "正在请求管理员权限恢复完整网络配置")
	return runElevatedAction(ctx, map[string]any{
		"action":         "restore",
		"interfaceIndex": index,
		"interfaceGuid":  guid,
		"macAddress":     mac,
		"backup":         b,
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

function Get-NetworkState([int]$index) {
    $ip = Get-NetIPInterface -InterfaceIndex $index -AddressFamily IPv4 -ErrorAction Stop | Select-Object -First 1
    $addresses = @(Get-NetIPAddress -InterfaceIndex $index -AddressFamily IPv4 -ErrorAction SilentlyContinue |
        Where-Object { $_.PrefixOrigin -ne "WellKnown" } |
        ForEach-Object { [ordered]@{ address = [string]$_.IPAddress; prefixLength = [int]$_.PrefixLength; origin = [string]$_.PrefixOrigin } })
    $routes = @(Get-NetRoute -InterfaceIndex $index -AddressFamily IPv4 -DestinationPrefix "0.0.0.0/0" -ErrorAction SilentlyContinue |
        ForEach-Object { [ordered]@{ destinationPrefix = [string]$_.DestinationPrefix; nextHop = [string]$_.NextHop; routeMetric = [int]$_.RouteMetric; protocol = [string]$_.Protocol } })
    $dns = @(Get-DnsClientServerAddress -InterfaceIndex $index -AddressFamily IPv4 -ErrorAction SilentlyContinue | ForEach-Object { @($_.ServerAddresses) })
    $adapter = Get-NetAdapter -InterfaceIndex $index -ErrorAction Stop
    $registry = Get-ItemProperty -Path "HKLM:\SYSTEM\CurrentControlSet\Services\Tcpip\Parameters\Interfaces\$($adapter.InterfaceGuid)" -ErrorAction SilentlyContinue
    return [ordered]@{
        dhcpEnabled = [bool]($ip.Dhcp -eq "Enabled")
        ipv4 = $addresses
        gateways = @($routes | ForEach-Object { $_.nextHop })
        automaticMetric = [bool]($ip.AutomaticMetric -eq "Enabled")
        interfaceMetric = [int]$ip.InterfaceMetric
        defaultRoutes = $routes
        dnsAutomatic = [bool](-not $registry -or [string]::IsNullOrWhiteSpace([string]$registry.NameServer))
        dns = @($dns | Where-Object { $_ })
    }
}

function Set-DnsMode([int]$index, $state) {
    if ([bool]$state.dnsAutomatic) {
        Set-DnsClientServerAddress -InterfaceIndex $index -ResetServerAddresses -ErrorAction Stop
        return
    }
    $servers = @($state.dns)
    foreach ($server in $servers) {
        $parsed = $null
        if (-not [System.Net.IPAddress]::TryParse([string]$server, [ref]$parsed)) { throw "DNS 地址无效：$server" }
    }
    if ($servers.Count -eq 0) {
        Set-DnsClientServerAddress -InterfaceIndex $index -ResetServerAddresses -ErrorAction Stop
    } else {
        Set-DnsClientServerAddress -InterfaceIndex $index -ServerAddresses ([string[]]$servers) -ErrorAction Stop
    }
}

function Set-InterfaceAddress([int]$index, $state) {
    $target = @($state.ipv4)
    foreach ($item in $target) {
        if ([int]$item.prefixLength -lt 0 -or [int]$item.prefixLength -gt 32) { throw "IPv4 前缀长度无效" }
        $parsed = $null
        if (-not [System.Net.IPAddress]::TryParse([string]$item.address, [ref]$parsed)) { throw "IPv4 地址无效：$($item.address)" }
    }
    $gateway = @($state.gateways) | Select-Object -First 1
    $current = @(Get-NetIPAddress -InterfaceIndex $index -AddressFamily IPv4 -ErrorAction SilentlyContinue |
        Where-Object { $_.PrefixOrigin -ne "WellKnown" -and $_.AddressState -ne "Tentative" })
    if ([bool]$state.dhcpEnabled) {
        Set-NetIPInterface -InterfaceIndex $index -AddressFamily IPv4 -Dhcp Enabled -ErrorAction Stop
        return
    }
    Set-NetIPInterface -InterfaceIndex $index -AddressFamily IPv4 -Dhcp Disabled -ErrorAction Stop
    foreach ($item in $current) {
        Remove-NetIPAddress -InterfaceIndex $index -IPAddress $item.IPAddress -PrefixLength $item.PrefixLength -Confirm:$false -ErrorAction Stop
    }
    if ($target.Count -eq 0) { throw "静态 IPv4 备份为空" }
    $primary = $target[0]
    $addressArgs = @{
        IPAddress = [string]$primary.address
        PrefixLength = [byte]$primary.prefixLength
        InterfaceIndex = $index
        AddressFamily = "IPv4"
        Confirm = $false
        ErrorAction = "Stop"
    }
    New-NetIPAddress @addressArgs | Out-Null
    for ($i = 1; $i -lt $target.Count; $i++) {
        New-NetIPAddress -IPAddress ([string]$target[$i].address) -PrefixLength ([byte]$target[$i].prefixLength) -InterfaceIndex $index -AddressFamily IPv4 -Confirm:$false -ErrorAction Stop | Out-Null
    }
    foreach ($route in @(Get-NetRoute -InterfaceIndex $index -AddressFamily IPv4 -DestinationPrefix "0.0.0.0/0" -ErrorAction SilentlyContinue)) {
        Remove-NetRoute -InterfaceIndex $index -AddressFamily IPv4 -DestinationPrefix "0.0.0.0/0" -NextHop $route.NextHop -Confirm:$false -ErrorAction Stop
    }
    foreach ($route in @($state.defaultRoutes)) {
        New-NetRoute -DestinationPrefix "0.0.0.0/0" -InterfaceIndex $index -NextHop ([string]$route.nextHop) -RouteMetric ([uint16][Math]::Max(0, [int]$route.routeMetric)) -AddressFamily IPv4 -Confirm:$false -ErrorAction Stop | Out-Null
    }
}

function Set-InterfaceMetrics([int]$index, $state) {
    $automatic = if ([bool]$state.automaticMetric) { "Enabled" } else { "Disabled" }
    $metricArgs = @{
        InterfaceIndex = $index
        AddressFamily = "IPv4"
        AutomaticMetric = $automatic
        ErrorAction = "Stop"
    }
    if (-not [bool]$state.automaticMetric) {
        $metricArgs.InterfaceMetric = [uint32][Math]::Max(1, [int]$state.interfaceMetric)
    }
    Set-NetIPInterface @metricArgs
    foreach ($route in @($state.defaultRoutes)) {
        $metric = [uint16][Math]::Max(0, [int]$route.routeMetric)
        Set-NetRoute -InterfaceIndex $index -AddressFamily IPv4 -DestinationPrefix "0.0.0.0/0" -NextHop ([string]$route.nextHop) -RouteMetric $metric -Confirm:$false -ErrorAction Stop
    }
}

function Apply-NetworkState([int]$index, $state) {
    Set-InterfaceAddress $index $state
    Set-InterfaceMetrics $index $state
    Set-DnsMode $index $state
}

function Renew-DhcpLease([int]$index, $state) {
    if (-not [bool]$state.dhcpEnabled) { return }
    $adapter = Get-NetAdapter -InterfaceIndex $index -ErrorAction Stop
    $alias = [string]$adapter.InterfaceAlias
    $ipconfig = Join-Path $env:SystemRoot "System32\ipconfig.exe"
    & $ipconfig /renew $alias | Out-Null
    if ($LASTEXITCODE -ne 0) {
        throw "DHCP 续租失败（ipconfig 退出码 $LASTEXITCODE）"
    }
}

function Assert-NetworkState([int]$index, $desired, [switch]$AllowPendingDhcp) {
    $errors = @()
    for ($attempt = 0; $attempt -lt 30; $attempt++) {
        $actual = Get-NetworkState $index
        $errors = @()
        if ([bool]$actual.dhcpEnabled -ne [bool]$desired.dhcpEnabled) {
            $errors += "DHCP 状态不一致"
        }
        if (-not $AllowPendingDhcp -and [bool]$desired.dhcpEnabled -and @($actual.ipv4).Count -eq 0) {
            $errors += "DHCP 尚未取得 IPv4 地址"
        } elseif (-not [bool]$desired.dhcpEnabled) {
            $actualAddresses = @($actual.ipv4 | ForEach-Object { "$($_.address)/$($_.prefixLength)" })
            foreach ($item in @($desired.ipv4)) {
                $expected = "$($item.address)/$($item.prefixLength)"
                if ($actualAddresses -notcontains $expected) { $errors += "缺少 IPv4 $expected" }
            }
        }
        if (@($desired.gateways).Count -gt 0) {
            $actualGateways = @($actual.gateways)
            foreach ($gateway in @($desired.gateways)) {
                if ($actualGateways -notcontains [string]$gateway) { $errors += "缺少默认网关 $gateway" }
            }
        }
        if (-not [bool]$desired.automaticMetric -and [int]$actual.interfaceMetric -ne [int]$desired.interfaceMetric) {
            $errors += "接口跃点不一致"
        }
        if ([bool]$desired.dnsAutomatic) {
            if (-not [bool]$actual.dnsAutomatic) { $errors += "DNS 未恢复为自动获取" }
        } elseif (@($actual.dns) -join "," -ne (@($desired.dns) -join ",")) {
            $errors += "DNS 地址不一致"
        }
        if ($errors.Count -eq 0) { return }
        if ($attempt -lt 29) { Start-Sleep -Milliseconds 500 }
    }
    throw "恢复结果校验失败：$($errors -join "；")"
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
    } elseif ($job.action -eq "reset") {
        Set-DnsClientServerAddress -InterfaceIndex $index -ResetServerAddresses -ErrorAction Stop
    } elseif ($job.action -eq "restore") {
        $target = $job.backup
        if ($null -eq $target) { throw "备份内容为空" }
        $rollback = Get-NetworkState $index
        $rollback | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath ($OutputPath + ".rollback.json") -Encoding UTF8
        try {
            Apply-NetworkState $index $target
            try {
                Renew-DhcpLease $index $target
                Assert-NetworkState $index $target
            } catch {
                if ([bool]$target.dhcpEnabled -and $_.Exception.Message -like "恢复结果校验失败：DHCP 尚未取得 IPv4 地址") {
                    $output.warning = "配置已恢复，但 DHCP 尚未取得 IPv4；请稍后检查连接或在系统中执行 ipconfig /renew"
                } else {
                    throw
                }
            }
        } catch {
            $applyError = $_.Exception.Message
            try {
                Apply-NetworkState $index $rollback
                throw "恢复失败，已回滚到操作前配置：$applyError"
            } catch {
                if ($_.Exception.Message -like "恢复失败，已回滚*") { throw }
                throw "恢复失败且自动回滚失败：$applyError；回滚错误：$($_.Exception.Message)"
            }
        }
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
