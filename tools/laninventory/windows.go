//go:build windows

package laninventory

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
)

func readWindowsNetwork(ctx context.Context) (networkState, error) {
	output, err := runPowerShell(ctx, networkStateScript)
	if err != nil {
		return networkState{}, err
	}
	var state networkState
	if err := json.Unmarshal(bytes.TrimSpace(output), &state); err != nil {
		return networkState{}, fmt.Errorf("解析局域网信息失败：%w", err)
	}
	return state, nil
}

func neighbors(ctx context.Context) map[string]string {
	output, err := runPowerShell(ctx, neighborScript)
	if err != nil {
		return nil
	}
	var rows []struct {
		Address string `json:"address"`
		MAC     string `json:"mac"`
	}
	if json.Unmarshal(bytes.TrimSpace(output), &rows) != nil {
		return nil
	}
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		if row.Address != "" && row.MAC != "" {
			out[row.Address] = row.MAC
		}
	}
	return out
}

func runPowerShell(ctx context.Context, script string) ([]byte, error) {
	executable := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	if _, err := os.Stat(executable); err != nil {
		found, lookupErr := exec.LookPath("powershell.exe")
		if lookupErr != nil {
			return nil, errors.New("未找到 Windows PowerShell 5.1")
		}
		executable = found
	}
	script = "$OutputEncoding = New-Object System.Text.UTF8Encoding $false\n" +
		"[Console]::OutputEncoding = $OutputEncoding\n" +
		script
	encoded := base64.StdEncoding.EncodeToString(utf16LE(script))
	cmd := exec.CommandContext(ctx, executable, "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-EncodedCommand", encoded)
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
		return nil, fmt.Errorf("Windows PowerShell 执行失败：%s", detail)
	}
	return stdout.Bytes(), nil
}

func utf16LE(value string) []byte {
	units := utf16.Encode([]rune(value))
	out := make([]byte, 0, len(units)*2)
	for _, unit := range units {
		out = append(out, byte(unit), byte(unit>>8))
	}
	return out
}

const networkStateScript = `$ErrorActionPreference = "Stop"
function IsVirtual([string]$text) {
    return $text -match '(?i)(virtual|vmware|hyper-v|vethernet|tap|tun|loopback|wsl|docker|bluetooth)'
}
$adapters = @()
foreach ($item in @(Get-NetAdapter -Physical -ErrorAction SilentlyContinue | Where-Object { $_.Status -eq "Up" })) {
    $ipv4 = @(Get-NetIPAddress -InterfaceIndex $item.InterfaceIndex -AddressFamily IPv4 -ErrorAction SilentlyContinue |
        Where-Object { $_.IPAddress -notlike "169.254.*" -and $_.AddressState -ne "Tentative" } |
        ForEach-Object { [ordered]@{ address = [string]$_.IPAddress } })
    if ($ipv4.Count -eq 0 -or (IsVirtual $item.InterfaceDescription)) { continue }
    $source = Get-NetIPAddress -InterfaceIndex $item.InterfaceIndex -AddressFamily IPv4 -ErrorAction SilentlyContinue |
        Where-Object { $_.IPAddress -notlike "169.254.*" } | Select-Object -First 1
    $gateways = @(Get-NetRoute -InterfaceIndex $item.InterfaceIndex -AddressFamily IPv4 -DestinationPrefix "0.0.0.0/0" -ErrorAction SilentlyContinue |
        ForEach-Object { [string]$_.NextHop })
    $adapters += [ordered]@{
        name = [string]$item.InterfaceAlias
        macAddress = [string]$item.MacAddress
        ipv4 = $ipv4
        prefixLength = if ($source) { [int]$source.PrefixLength } else { 24 }
        gateways = $gateways
    }
}
$primary = $adapters | Where-Object { @($_.gateways).Count -gt 0 } | Select-Object -First 1
if (-not $primary) { $primary = $adapters | Select-Object -First 1 }
[ordered]@{ primary = $primary; adapters = $adapters } | ConvertTo-Json -Depth 6 -Compress
`

const neighborScript = `$ErrorActionPreference = "SilentlyContinue"
$rows = @(Get-NetNeighbor -AddressFamily IPv4 |
    Where-Object { $_.State -in @("Reachable", "Stale", "Delay", "Probe") -and $_.LinkLayerAddress -and $_.LinkLayerAddress -ne "00-00-00-00-00-00" } |
    ForEach-Object { [ordered]@{ address = [string]$_.IPAddress; mac = [string]$_.LinkLayerAddress } })
if ($rows.Count -eq 0) { "[]" } else { $rows | ConvertTo-Json -Depth 3 -Compress }
`
