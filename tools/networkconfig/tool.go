// Package networkconfig manages the IPv4 and DNS configuration of the active
// Windows network adapter. Probe and backup operations are read-only.
package networkconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"opentools/pkg/tool"
)

const version = "1.3.0"

// Tool detects the primary adapter, backs up its network settings and
// benchmarks DNS providers.
type Tool struct{}

// New returns the network configuration tool.
func New() Tool { return Tool{} }

// Definition implements tool.Tool.
func (Tool) Definition() tool.Definition {
	return tool.Definition{
		ID:          "network.dns-switcher",
		Name:        "DNS 测速",
		Summary:     "测速常用国内 DNS，结果按延迟排序，一行一个切换按钮。",
		Description: "自动识别主网卡并备份其配置，对每个 DNS 服务并发发送多轮解析查询。测速结果按成功率与中位延迟排序，可用的服务行内提供一键切换按钮；切换前自动备份，只改 DNS，不动 IP 与网关。",
		Category:    "网络工具",
		Icon:        "network",
		Keywords:    []string{"DNS", "测速", "切换", "网卡", "延迟", "解析"},
		Version:     version,
		Effects: []tool.Effect{
			tool.EffectReadFiles,
			tool.EffectWriteFiles,
			tool.EffectNetwork,
			tool.EffectSystemSettings,
		},
		Input: tool.Object(map[string]*tool.Schema{
			"rounds": tool.Number("测速轮次", "对每个 DNS 服务重复查询的次数，轮次越多结果越稳定，耗时也越长。").
				WithDefault(3).
				WithMin(1).
				WithMax(8),
		}),
	}
}

type params struct {
	Rounds   float64 `json:"rounds"`
	Provider string  `json:"provider"`
}

type backup struct {
	SchemaVersion    int             `json:"schemaVersion"`
	CreatedAt        string          `json:"createdAt"`
	ComputerName     string          `json:"computerName"`
	InterfaceGUID    string          `json:"interfaceGuid"`
	InterfaceIndex   int             `json:"interfaceIndex"`
	InterfaceAlias   string          `json:"interfaceAlias"`
	InterfaceDesc    string          `json:"interfaceDescription"`
	MACAddress       string          `json:"macAddress"`
	DHCPEnabled      bool            `json:"dhcpEnabled"`
	IPv4             []ipAddress     `json:"ipv4"`
	Gateways         []string        `json:"gateways"`
	AutomaticMetric  bool            `json:"automaticMetric"`
	InterfaceMetric  int             `json:"interfaceMetric"`
	DefaultRoutes    []routeConfig   `json:"defaultRoutes,omitempty"`
	DNSAutomatic     bool            `json:"dnsAutomatic"`
	DNS              []string        `json:"dns"`
	OriginalRegistry *registryBackup `json:"originalRegistry,omitempty"`
}

type registryBackup struct {
	NameServer     string `json:"nameServer"`
	DhcpNameServer string `json:"dhcpNameServer"`
}

type ipAddress struct {
	Address      string `json:"address"`
	PrefixLength int    `json:"prefixLength"`
	Origin       string `json:"origin"`
}

type routeConfig struct {
	DestinationPrefix string `json:"destinationPrefix"`
	NextHop           string `json:"nextHop"`
	RouteMetric       int    `json:"routeMetric"`
	Protocol          string `json:"protocol,omitempty"`
}

type snapshot struct {
	ComputerName     string    `json:"computerName"`
	IsAdmin          bool      `json:"isAdmin"`
	Adapters         []adapter `json:"adapters"`
	PrimaryIndex     int       `json:"primaryIndex"`
	PrimaryReason    string    `json:"primaryReason"`
	Ambiguous        bool      `json:"ambiguous"`
	CandidateIndexes []int     `json:"candidateIndexes"`
	DetectedAt       string    `json:"detectedAt"`
}

type adapter struct {
	Name               string        `json:"name"`
	InterfaceIndex     int           `json:"interfaceIndex"`
	InterfaceGUID      string        `json:"interfaceGuid"`
	Description        string        `json:"description"`
	MACAddress         string        `json:"macAddress"`
	LinkSpeed          string        `json:"linkSpeed"`
	MediaType          string        `json:"mediaType"`
	Status             string        `json:"status"`
	Virtual            bool          `json:"virtual"`
	HardwareInterface  bool          `json:"hardwareInterface"`
	ConnectorPresent   bool          `json:"connectorPresent"`
	DHCPEnabled        bool          `json:"dhcpEnabled"`
	AutomaticMetric    bool          `json:"automaticMetric"`
	InterfaceMetric    int           `json:"interfaceMetric"`
	DNSAutomatic       bool          `json:"dnsAutomatic"`
	HasDefaultRoute    bool          `json:"hasDefaultRoute"`
	RouteMetric        int           `json:"routeMetric"`
	Gateways           []string      `json:"gateways"`
	DefaultRoutes      []routeConfig `json:"defaultRoutes"`
	IPv4               []ipAddress   `json:"ipv4"`
	DNS                []string      `json:"dns"`
	ProfileName        string        `json:"profileName"`
	IPv4Connectivity   string        `json:"ipv4Connectivity"`
	Internet           bool          `json:"internet"`
	RoutePriority      int           `json:"routePriority"`
	EligibleForPrimary bool          `json:"eligibleForPrimary"`
	ExclusionReason    string        `json:"exclusionReason,omitempty"`
}

type probeResult struct {
	Provider   provider
	Attempts   int
	Successes  int
	MedianMS   float64
	JitterMS   float64
	LastError  string
	Successful bool
}

type provider struct {
	Key      string
	Name     string
	Servers  []string
	Region   string
	Note     string
	Fallback bool
}

// Run implements tool.Tool.
func (Tool) Run(ctx context.Context, input tool.Input, env tool.Environment, report tool.Reporter) (tool.Result, error) {
	var p params
	if err := input.Decode(&p); err != nil {
		return tool.Result{}, err
	}
	if runtime.GOOS != "windows" {
		return tool.Result{}, errors.New("DNS 测速工具当前仅支持 Windows")
	}
	state, err := readSnapshot(ctx)
	if err != nil {
		return tool.Result{}, err
	}

	// Row switches invoke apply with a provider; plain runs benchmark.
	if strings.TrimSpace(p.Provider) != "" {
		return applyResult(ctx, state, p.Provider, env, report)
	}
	item, err := primaryAdapter(state)
	if err != nil {
		return tool.Result{}, err
	}
	rounds := int(p.Rounds)
	if rounds <= 0 {
		rounds = 3
	}
	return benchmarkResult(ctx, item, rounds, report)
}

func itemIPv4(item adapter) []string {
	out := make([]string, 0, len(item.IPv4))
	for _, ip := range item.IPv4 {
		out = append(out, fmt.Sprintf("%s/%d", ip.Address, ip.PrefixLength))
	}
	return out
}

func primaryAdapter(state snapshot) (adapter, error) {
	if state.Ambiguous {
		return adapter{}, fmt.Errorf("存在多个同等优先级的主网卡候选（接口 %v），无法安全自动选择", state.CandidateIndexes)
	}
	if state.PrimaryIndex == 0 {
		return adapter{}, errors.New("没有找到可用的主网卡")
	}
	for _, item := range state.Adapters {
		if item.InterfaceIndex == state.PrimaryIndex {
			return item, nil
		}
	}
	return adapter{}, fmt.Errorf("主网卡接口 %d 已不存在", state.PrimaryIndex)
}

func captureBackup(ctx context.Context, item adapter) (backup, error) {
	if err := ctx.Err(); err != nil {
		return backup{}, err
	}
	original, err := readRegistryDNS(ctx, item.InterfaceGUID)
	if err != nil {
		return backup{}, err
	}
	return backup{
		SchemaVersion:    2,
		CreatedAt:        time.Now().UTC().Format(time.RFC3339),
		ComputerName:     computerName(),
		InterfaceGUID:    item.InterfaceGUID,
		InterfaceIndex:   item.InterfaceIndex,
		InterfaceAlias:   item.Name,
		InterfaceDesc:    item.Description,
		MACAddress:       item.MACAddress,
		DHCPEnabled:      item.DHCPEnabled,
		IPv4:             append([]ipAddress(nil), item.IPv4...),
		Gateways:         append([]string(nil), item.Gateways...),
		AutomaticMetric:  item.AutomaticMetric,
		InterfaceMetric:  item.InterfaceMetric,
		DefaultRoutes:    append([]routeConfig(nil), item.DefaultRoutes...),
		DNSAutomatic:     original == nil || strings.TrimSpace(original.NameServer) == "",
		DNS:              append([]string(nil), item.DNS...),
		OriginalRegistry: original,
	}, nil
}

func backupDir(env tool.Environment) string {
	root := env.DataDir
	if strings.TrimSpace(root) == "" {
		root = env.WorkDir
	}
	return filepath.Join(root, "network", "backups")
}

func persistBackup(env tool.Environment, name string, data []byte) (string, error) {
	dir := backupDir(env)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func benchmarkResult(ctx context.Context, item adapter, rounds int, report tool.Reporter) (tool.Result, error) {
	report.Log("info", fmt.Sprintf("正在对 %d 个 DNS 服务测速（每个 %d 轮查询）", len(availableProviders()), rounds))
	providers := availableProviders()
	total := int64(0)
	for _, p := range providers {
		total += int64(min(2, len(p.Servers)) * len(probeDomains) * rounds)
	}
	results := make([]probeResult, 0, len(providers))
	var done int64
	for _, p := range providers {
		if err := ctx.Err(); err != nil {
			return tool.Result{}, err
		}
		result := probeProvider(ctx, p, rounds, func() {
			done++
			report.Progress(done, total)
		})
		results = append(results, result)
		report.Log("info", fmt.Sprintf("%s：%d/%d 次成功", p.Name, result.Successes, result.Attempts))
	}
	sort.SliceStable(results, func(i, j int) bool {
		left, right := results[i], results[j]
		if left.Successes != right.Successes {
			return left.Successes > right.Successes
		}
		if left.Successes == 0 {
			return left.Provider.Name < right.Provider.Name
		}
		if left.MedianMS != right.MedianMS {
			return left.MedianMS < right.MedianMS
		}
		return left.JitterMS < right.JitterMS
	})
	rows := make([][]string, 0, len(results))
	rowActions := make([][]tool.RowAction, 0, len(results))
	for i, result := range results {
		median, jitter := "-", "-"
		if result.Successes > 0 {
			median = fmt.Sprintf("%.1f", result.MedianMS)
			jitter = fmt.Sprintf("%.1f", result.JitterMS)
		}
		rows = append(rows, []string{
			result.Provider.Name,
			strings.Join(result.Provider.Servers, " / "),
			median,
			jitter,
			fmt.Sprintf("%d/%d", result.Successes, result.Attempts),
		})
		if result.Successes > 0 {
			rowActions = append(rowActions, []tool.RowAction{{
				Label: "切换",
				Tone:  actionTone(i),
				Confirm: fmt.Sprintf(
					"将网卡「%s」（接口 %d）的 DNS 改为 %s（%s）。切换前会自动备份当前配置，IP 与网关保持不变。",
					item.Name, item.InterfaceIndex, result.Provider.Name, strings.Join(result.Provider.Servers, " / "),
				),
				Input: map[string]any{"provider": result.Provider.Key},
			}})
		} else {
			rowActions = append(rowActions, nil)
		}
	}
	best := "没有可用的 DNS 服务"
	if len(results) > 0 && results[0].Successes > 0 {
		best = fmt.Sprintf("最快 %s（中位 %.1f ms，抖动 %.1f ms）", results[0].Provider.Name, results[0].MedianMS, results[0].JitterMS)
	}
	return tool.Result{
		Summary: fmt.Sprintf("DNS 测速完成：%s；网卡 %s（接口 %d）当前 DNS %s", best, item.Name, item.InterfaceIndex, strings.Join(item.DNS, " / ")),
		Blocks: []tool.Block{
			{
				Kind:         tool.BlockTable,
				Title:        "测速结果（按延迟排序）",
				Columns:      []string{"DNS 服务", "地址", "中位延迟 ms", "抖动 ms", "成功"},
				ColumnWidths: []string{"22%", "30%", "14%", "14%", "12%"},
				Rows:         rows,
				RowActions:   rowActions,
			},
		},
	}, nil
}

func actionTone(index int) tool.Tone {
	if index == 0 {
		return tool.ToneSuccess
	}
	return tool.ToneNeutral
}

func applyResult(ctx context.Context, state snapshot, key string, env tool.Environment, report tool.Reporter) (tool.Result, error) {
	p, ok := providerByKey(key)
	if !ok {
		return tool.Result{}, fmt.Errorf("未知 DNS 服务 %q", key)
	}
	item, err := primaryAdapter(state)
	if err != nil {
		return tool.Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}
	b, err := captureBackup(ctx, item)
	if err != nil {
		return tool.Result{}, err
	}
	backupName := fmt.Sprintf("network-backup-before-apply-%s.json", time.Now().Format("20060102-150405-000"))
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return tool.Result{}, fmt.Errorf("编码切换前备份失败：%w", err)
	}
	data = append(data, '\n')
	if _, err := persistBackup(env, backupName, data); err != nil {
		return tool.Result{}, fmt.Errorf("写入切换前备份失败：%w", err)
	}
	report.Log("info", "切换前配置已备份")
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}
	if err := setDNS(ctx, item.InterfaceIndex, item.InterfaceGUID, item.MACAddress, p.Servers, report); err != nil {
		return tool.Result{}, err
	}
	after, err := readSnapshot(ctx)
	if err != nil {
		return tool.Result{}, fmt.Errorf("DNS 已提交，但重新读取配置失败：%w", err)
	}
	updated, err := primaryAdapter(after)
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{
		Summary: fmt.Sprintf("已将 %s 的 DNS 切换为 %s", item.Name, p.Name),
		Blocks: []tool.Block{
			{Kind: tool.BlockNotice, Tone: tool.ToneSuccess, Title: "切换完成", Text: fmt.Sprintf("DNS：%s\nIP 与网关未修改；切换前备份已保存在产物中。", strings.Join(updated.DNS, " / "))},
			{Kind: tool.BlockTable, Title: "当前配置", Columns: []string{"项目", "值"}, Rows: [][]string{
				{"网卡", updated.Name},
				{"IPv4", strings.Join(itemIPv4(updated), " / ")},
				{"网关", strings.Join(updated.Gateways, ", ")},
				{"DNS", strings.Join(updated.DNS, ", ")},
			}},
		},
		Actions: []tool.Action{
			{
				Label: "重新测速",
				Tone:  tool.ToneSuccess,
				Input: map[string]any{},
			},
		},
	}, nil
}

func validDNSAddress(value string) bool {
	addr, err := netip.ParseAddr(strings.TrimSpace(value))
	return err == nil && addr.IsValid()
}

func providerByKey(key string) (provider, bool) {
	for _, p := range providerList() {
		if p.Key == key {
			return p, true
		}
	}
	return provider{}, false
}

func providerList() []provider {
	return []provider{
		{Key: "dnspod", Name: "腾讯 DNSPod", Servers: []string{"119.29.29.29", "119.28.28.28"}, Region: "中国大陆", Note: "腾讯云公共解析服务。"},
		{Key: "alidns", Name: "阿里云 DNS", Servers: []string{"223.5.5.5", "223.6.6.6"}, Region: "中国大陆", Note: "国内解析快，支持 IPv4/IPv6。"},
		{Key: "baidu", Name: "百度 DNS", Servers: []string{"180.76.76.76"}, Region: "中国大陆", Note: "国内访问稳定。"},
		{Key: "bytedance", Name: "字节跳动 DNS", Servers: []string{"180.184.1.1", "180.184.2.2"}, Region: "中国大陆", Note: "火山引擎公共解析服务。"},
		{Key: "360unicom", Name: "360 联通 DNS", Servers: []string{"123.125.81.6"}, Region: "中国大陆", Note: "360 与联通合作节点，联通线路延迟低。"},
		{Key: "hentelecom", Name: "河南电信 DNS", Servers: []string{"222.88.88.88", "222.85.85.85"}, Region: "中国大陆", Note: "河南电信运营商节点，电信线路延迟低。"},
	}
}

func availableProviders() []provider {
	return providerList()
}

func computerName() string {
	name, _ := os.Hostname()
	return name
}

// readSnapshot invokes a fixed PowerShell script. No user input is interpolated
// into the script text.
func readSnapshot(ctx context.Context) (snapshot, error) {
	script := `
$ErrorActionPreference = "Stop"
$adapters = @()
foreach ($a in @(Get-NetAdapter -IncludeHidden)) {
    $index = [int]$a.InterfaceIndex
    $ip = Get-NetIPInterface -InterfaceIndex $index -AddressFamily IPv4 -ErrorAction SilentlyContinue | Select-Object -First 1
    $addresses = @(Get-NetIPAddress -InterfaceIndex $index -AddressFamily IPv4 -ErrorAction SilentlyContinue)
    $routes = @(Get-NetRoute -InterfaceIndex $index -AddressFamily IPv4 -DestinationPrefix "0.0.0.0/0" -ErrorAction SilentlyContinue)
    $dns = @(Get-DnsClientServerAddress -InterfaceIndex $index -AddressFamily IPv4 -ErrorAction SilentlyContinue | ForEach-Object { $_.ServerAddresses })
    $profile = Get-NetConnectionProfile -InterfaceIndex $index -ErrorAction SilentlyContinue | Select-Object -First 1
    $guid = [string]$a.InterfaceGuid
    $registry = Get-ItemProperty -Path "HKLM:\SYSTEM\CurrentControlSet\Services\Tcpip\Parameters\Interfaces\$guid" -ErrorAction SilentlyContinue
    $virtual = ($a.Virtual -eq $true) -or ($a.InterfaceDescription -match '(?i)(virtual|vmware|hyper-v|vethernet|tap|tun|loopback|wsl|docker|bluetooth)')
    $eligible = ($a.Status -eq "Up") -and ($a.HardwareInterface -eq $true) -and (-not $virtual) -and ($a.MediaType -eq "802.3" -or $a.MediaType -eq "Native 802.11")
    $nonAPIPA = @($addresses | Where-Object { $_.IPAddress -notlike "169.254.*" })
    $route = $routes | Sort-Object { [int]$_.RouteMetric + [int]$_.InterfaceMetric } | Select-Object -First 1
    if ($nonAPIPA.Count -eq 0) { $eligible = $false }
    if (-not $route) { $eligible = $false }
    if (-not $ip) { $eligible = $false }
    $priority = 2147483647
    if ($route) { $priority = [int]$route.RouteMetric + [int]$route.InterfaceMetric }
    $adapters += [ordered]@{
        name = [string]$a.InterfaceAlias
        interfaceIndex = $index
        interfaceGuid = [string]$a.InterfaceGuid
        description = [string]$a.InterfaceDescription
        macAddress = [string]$a.MacAddress
        linkSpeed = [string]$a.LinkSpeed
        mediaType = [string]$a.MediaType
        status = [string]$a.Status
        virtual = [bool]$virtual
        hardwareInterface = [bool]$a.HardwareInterface
        connectorPresent = [bool]$a.ConnectorPresent
        dhcpEnabled = [bool]($ip.Dhcp -eq "Enabled")
        automaticMetric = [bool]($ip.AutomaticMetric -eq "Enabled")
        interfaceMetric = if ($ip) { [int]$ip.InterfaceMetric } else { 0 }
        dnsAutomatic = [bool](-not $registry -or [string]::IsNullOrWhiteSpace([string]$registry.NameServer))
        hasDefaultRoute = [bool]($route -ne $null)
        routeMetric = if ($route) { [int]$route.RouteMetric } else { 0 }
        gateways = @($routes | ForEach-Object { [string]$_.NextHop })
        defaultRoutes = @($routes | ForEach-Object { [ordered]@{ destinationPrefix=[string]$_.DestinationPrefix; nextHop=[string]$_.NextHop; routeMetric=[int]$_.RouteMetric; protocol=[string]$_.Protocol } })
        ipv4 = @($nonAPIPA | ForEach-Object { [ordered]@{ address=[string]$_.IPAddress; prefixLength=[int]$_.PrefixLength; origin=[string]$_.PrefixOrigin } })
        dns = @($dns | Where-Object { $_ })
        profileName = if ($profile) { [string]$profile.Name } else { "" }
        ipv4Connectivity = if ($profile) { [string]$profile.IPv4Connectivity } else { "" }
        internet = [bool]($profile -and $profile.IPv4Connectivity -eq "Internet")
        routePriority = $priority
        eligibleForPrimary = [bool]$eligible
        exclusionReason = if ($eligible) { "" } elseif ($a.Status -ne "Up") { "接口未连接" } elseif ($virtual) { "虚拟或非物理网卡" } elseif ($nonAPIPA.Count -eq 0) { "没有可用 IPv4" } elseif (-not $route) { "没有 IPv4 默认路由" } else { "非以太网或无线链路" }
    }
}
$candidates = @($adapters | Where-Object { $_.eligibleForPrimary } | Sort-Object @{ Expression = { if ($_.internet) { 0 } else { 1 } } }, routePriority)
$primary = $candidates | Select-Object -First 1
$ambiguous = $false
$candidateIndexes = @()
if ($candidates.Count -gt 1) {
    $bestInternet = [bool]$candidates[0].internet
    $best = [int]$candidates[0].routePriority
    $first = @($candidates | Where-Object { [bool]$_.internet -eq $bestInternet -and [int]$_.routePriority -eq $best })
    if ($first.Count -gt 1) { $ambiguous = $true }
    $candidateIndexes = @($first | ForEach-Object { [int]$_.interfaceIndex })
}
if ($ambiguous) { $primary = $null }
$reason = "按物理链路、非 APIPA IPv4、IPv4 默认路由、Internet 连接和路由优先级综合判定。"
if ($primary) { $reason += " 当前主网卡路由优先级为 $($primary.routePriority)。" } elseif ($ambiguous) { $reason += " 存在多个同等优先级候选，未自动选择。" } else { $reason += " 未找到满足条件的候选。" }
[ordered]@{
    computerName = [Environment]::MachineName
    isAdmin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
    adapters = $adapters
    primaryIndex = if ($primary) { [int]$primary.interfaceIndex } else { 0 }
    primaryReason = $reason
    ambiguous = $ambiguous
    candidateIndexes = $candidateIndexes
    detectedAt = (Get-Date).ToUniversalTime().ToString("o")
} | ConvertTo-Json -Depth 8 -Compress
`
	output, err := runPowerShell(ctx, script)
	if err != nil {
		return snapshot{}, err
	}
	var state snapshot
	if err := json.Unmarshal(bytes.TrimSpace(output), &state); err != nil {
		return snapshot{}, fmt.Errorf("解析网卡信息失败：%w", err)
	}
	return state, nil
}

func readRegistryDNS(ctx context.Context, guid string) (*registryBackup, error) {
	if strings.TrimSpace(guid) == "" {
		return nil, errors.New("网卡缺少 GUID")
	}
	serialized, _ := json.Marshal(guid)
	script := fmt.Sprintf(`
$ErrorActionPreference = "Stop"
$guid = %s
$path = "HKLM:\SYSTEM\CurrentControlSet\Services\Tcpip\Parameters\Interfaces\$guid"
$item = Get-ItemProperty -Path $path -ErrorAction SilentlyContinue
if ($null -eq $item) { "{}"; exit 0 }
[ordered]@{
    nameServer = [string]$item.NameServer
    dhcpNameServer = [string]$item.DhcpNameServer
} | ConvertTo-Json -Compress
`, string(serialized))
	output, err := runPowerShell(ctx, script)
	if err != nil {
		return nil, err
	}
	var out registryBackup
	if err := json.Unmarshal(bytes.TrimSpace(output), &out); err != nil {
		return nil, fmt.Errorf("解析 DNS 注册表配置失败：%w", err)
	}
	if out.NameServer == "" && out.DhcpNameServer == "" {
		return nil, nil
	}
	return &out, nil
}
