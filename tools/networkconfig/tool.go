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

const version = "1.2.0"

// Tool detects the primary adapter, backs up its network settings and
// benchmarks DNS providers.
type Tool struct{}

// New returns the network configuration tool.
func New() Tool { return Tool{} }

// Definition implements tool.Tool.
func (Tool) Definition() tool.Definition {
	return tool.Definition{
		ID:          "network.dns-switcher",
		Name:        "网络与 DNS",
		Summary:     "识别主网卡，完整备份 IPv4 与 DNS，测速并切换 DNS。",
		Description: "自动排除虚拟网卡和 APIPA 地址，依据默认路由与接口跃点识别主网卡。备份包含 DHCP、IPv4、网关、接口跃点、默认路由和 DNS；切换前自动备份，恢复时核对网卡 GUID 与 MAC，失败会尝试回滚。",
		Category:    "系统工具",
		Icon:        "network",
		Keywords:    []string{"网络", "网卡", "DNS", "测速", "备份", "恢复", "IP", "网关"},
		Version:     version,
		Effects: []tool.Effect{
			tool.EffectReadFiles,
			tool.EffectWriteFiles,
			tool.EffectNetwork,
			tool.EffectSystemSettings,
		},
		Input: tool.Object(map[string]*tool.Schema{
			"action": tool.Select("操作", "检测是只读操作；切换 DNS 和恢复配置需要管理员权限。", "detect", "backup", "benchmark", "apply", "restore").
				WithEnumLabels(map[string]string{
					"detect":    "检测主网卡",
					"backup":    "备份当前配置",
					"benchmark": "DNS 测速",
					"apply":     "切换 DNS",
					"restore":   "恢复备份",
				}).
				WithDefault("detect"),
			"provider": tool.Select("DNS 服务", "DNS 测速结果中会提供每个服务的一键切换操作。", providerKeys()...).
				WithEnumLabels(providerLabels()).
				WithDefault("alidns").
				WithVisibleWhen("action", "apply"),
			"backupFile": tool.String("备份文件", "填写手动备份的 JSON 路径；留空时恢复最新备份。").
				WithWidget("file", "选择 OpenTools 生成的网络配置备份").
				WithAccept("json").
				WithVisibleWhen("action", "restore"),
			"probeRounds": tool.Number("测速轮次", "每个 DNS 服务重复查询的次数，默认 3 轮。").
				WithDefault(3).
				WithMin(1).
				WithMax(8).
				WithVisibleWhen("action", "benchmark"),
		}, "action"),
	}
}

type params struct {
	Action      string  `json:"action"`
	Provider    string  `json:"provider"`
	BackupFile  string  `json:"backupFile"`
	ProbeRounds float64 `json:"probeRounds"`
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
		return tool.Result{}, errors.New("网络与 DNS 工具当前仅支持 Windows")
	}
	if p.ProbeRounds == 0 {
		p.ProbeRounds = 3
	}
	state, err := readSnapshot(ctx)
	if err != nil {
		return tool.Result{}, err
	}

	switch strings.TrimSpace(p.Action) {
	case "detect":
		return detectResult(state), nil
	case "backup":
		report.Log("info", "正在读取主网卡配置")
		item, err := primaryAdapter(state)
		if err != nil {
			return tool.Result{}, err
		}
		b, err := captureBackup(ctx, item)
		if err != nil {
			return tool.Result{}, err
		}
		return saveBackupResult(ctx, env, b, report)
	case "benchmark":
		item, err := primaryAdapter(state)
		if err != nil {
			return tool.Result{}, err
		}
		return benchmarkResult(ctx, item, int(p.ProbeRounds), report)
	case "apply":
		return applyResult(ctx, state, p.Provider, env, report)
	case "restore":
		return restoreResult(ctx, p.BackupFile, env, report)
	default:
		return tool.Result{}, fmt.Errorf("未知操作 %q", p.Action)
	}
}

func detectResult(state snapshot) tool.Result {
	rows := make([][]string, 0, len(state.Adapters))
	excluded := 0
	for _, item := range state.Adapters {
		stateText := "候选"
		switch {
		case item.InterfaceIndex == state.PrimaryIndex:
			stateText = "主网卡"
		case !item.EligibleForPrimary:
			excluded++
			continue
		}
		dnsMode := "静态"
		if item.DNSAutomatic {
			dnsMode = "自动"
		}
		rows = append(rows, []string{
			stateText,
			item.Name,
			fmt.Sprint(item.InterfaceIndex),
			item.Status,
			strings.Join(itemIPv4(item), " / "),
			strings.Join(item.DNS, ", "),
			dnsMode,
			fmt.Sprint(item.RoutePriority),
		})
	}
	blocks := []tool.Block{
		{Kind: tool.BlockTable, Title: "网卡识别", Columns: []string{"判定", "网卡", "索引", "状态", "IPv4", "DNS", "地址模式", "路由优先级"}, Rows: rows},
	}
	warnings := []string{}
	if excluded > 0 {
		warnings = append(warnings, fmt.Sprintf("另有 %d 个虚拟、未连接或不适用的网卡未列出。", excluded))
	}
	if state.Ambiguous {
		warnings = append(warnings, "检测到多个同等优先级的主网卡候选，请先明确要操作的网卡。")
	}
	if !state.IsAdmin {
		warnings = append(warnings, "当前进程不是管理员：检测、备份和测速可用；切换 DNS 与恢复配置时会请求 UAC。")
	}
	if state.PrimaryIndex > 0 {
		for _, item := range state.Adapters {
			if item.InterfaceIndex == state.PrimaryIndex {
				warnings = append(warnings, fmt.Sprintf(
					"当前主网卡：%s（接口 %d，%s）；IPv4 %s；网关 %s；DNS %s。",
					item.Name,
					item.InterfaceIndex,
					map[bool]string{true: "DHCP", false: "静态"}[item.DHCPEnabled],
					strings.Join(itemIPv4(item), " / "),
					strings.Join(item.Gateways, " / "),
					strings.Join(item.DNS, " / "),
				))
				break
			}
		}
	}
	blocks = append(blocks, tool.Block{
		Kind:  tool.BlockNotice,
		Title: "判定依据",
		Text:  state.PrimaryReason,
		Tone:  tool.ToneNeutral,
	})
	if state.PrimaryIndex > 0 {
		blocks = append(blocks, tool.Block{
			Kind:  tool.BlockNotice,
			Tone:  tool.ToneSuccess,
			Title: "当前主网卡",
			Text:  fmt.Sprintf("接口 %d 具备物理链路、可用 IPv4、默认路由和 Internet 连接，优先于其他候选。", state.PrimaryIndex),
		})
	}
	summary := fmt.Sprintf("检测到 %d 个网卡，主网卡为接口 %d", len(state.Adapters), state.PrimaryIndex)
	if state.Ambiguous {
		summary = fmt.Sprintf("检测到 %d 个网卡，主网卡存在多个同等优先级候选", len(state.Adapters))
	} else if state.PrimaryIndex == 0 {
		summary = fmt.Sprintf("检测到 %d 个网卡，但没有可用的主网卡", len(state.Adapters))
	}
	return tool.Result{
		Summary:  summary,
		Blocks:   blocks,
		Warnings: warnings,
		Actions: []tool.Action{
			{
				Label: "备份当前网络配置",
				Input: map[string]any{"action": "backup"},
			},
			{
				Label: "测试 DNS 速度",
				Tone:  tool.ToneSuccess,
				Input: map[string]any{"action": "benchmark"},
			},
		},
	}
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

func saveBackupResult(ctx context.Context, env tool.Environment, b backup, report tool.Reporter) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}
	name := fmt.Sprintf("network-backup-%s.json", time.Now().Format("20060102-150405-000"))
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return tool.Result{}, fmt.Errorf("编码网络备份失败：%w", err)
	}
	data = append(data, '\n')
	path, err := persistBackup(env, name, data)
	if err != nil {
		return tool.Result{}, fmt.Errorf("写入网络备份失败：%w", err)
	}
	report.Log("info", "网络配置备份已保存")
	rows := backingRows(b)
	return tool.Result{
		Summary: fmt.Sprintf("已备份主网卡 %s 的网络配置", b.InterfaceAlias),
		Blocks: []tool.Block{
			{Kind: tool.BlockNotice, Tone: tool.ToneSuccess, Title: "备份完成", Text: "文件包含网卡标识、DHCP 状态、IPv4、默认网关、自动/固定接口跃点、默认路由跃点和 DNS 配置。"},
			{Kind: tool.BlockTable, Title: "备份内容", Columns: []string{"项目", "值"}, Rows: rows},
		},
		Actions: []tool.Action{
			{
				Label:   "恢复此备份",
				Input:   map[string]any{"action": "restore", "backupFile": path},
				Confirm: restoreConfirm(b, b.InterfaceAlias),
			},
			{
				Label: "测试 DNS 速度",
				Tone:  tool.ToneSuccess,
				Input: map[string]any{"action": "benchmark"},
			},
		},
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

func backingRows(b backup) [][]string {
	rows := [][]string{
		{"网卡", fmt.Sprintf("%s（接口 %d）", b.InterfaceAlias, b.InterfaceIndex)},
		{"GUID", b.InterfaceGUID},
		{"MAC", b.MACAddress},
		{"DHCP", map[bool]string{true: "启用", false: "静态配置"}[b.DHCPEnabled]},
		{"IPv4", strings.Join(backupIPv4(b.IPv4), " / ")},
		{"网关", strings.Join(b.Gateways, ", ")},
		{"自动跃点", map[bool]string{true: "启用", false: "关闭"}[b.AutomaticMetric]},
		{"接口跃点", fmt.Sprint(b.InterfaceMetric)},
		{"DNS", strings.Join(b.DNS, ", ")},
	}
	if b.OriginalRegistry != nil {
		rows = append(rows, []string{"注册表 DNS", b.OriginalRegistry.NameServer})
	}
	return rows
}

func backupIPv4(items []ipAddress) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, fmt.Sprintf("%s/%d", item.Address, item.PrefixLength))
	}
	return out
}

func benchmarkResult(ctx context.Context, item adapter, rounds int, report tool.Reporter) (tool.Result, error) {
	report.Log("info", "开始直连 DNS 测速（不经过系统 DNS）")
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
	actions := make([]tool.Action, 0, len(results))
	for i, result := range results {
		status := "失败"
		median, jitter := "-", "-"
		if result.Successes > 0 {
			status = "可用"
			median = fmt.Sprintf("%.1f ms", result.MedianMS)
			jitter = fmt.Sprintf("%.1f ms", result.JitterMS)
		}
		rank := "-"
		if result.Successes > 0 {
			rank = fmt.Sprint(i + 1)
		}
		rows = append(rows, []string{
			rank,
			result.Provider.Name,
			strings.Join(result.Provider.Servers, " / "),
			status,
			fmt.Sprintf("%d/%d", result.Successes, result.Attempts),
			median,
			jitter,
			result.Provider.Region,
		})
		if result.Successes > 0 {
			actions = append(actions, tool.Action{
				Label: "切换至 " + result.Provider.Name,
				Tone:  actionTone(i),
				Input: map[string]any{"action": "apply", "provider": result.Provider.Key},
				Confirm: fmt.Sprintf(
					"将网卡「%s」（接口 %d）的 DNS 改为 %s（%s）。切换前会自动备份当前配置，IP 与网关保持不变。",
					item.Name, item.InterfaceIndex, result.Provider.Name, strings.Join(result.Provider.Servers, " / "),
				),
			})
		}
	}
	best := "没有可用的 DNS 服务"
	if len(results) > 0 && results[0].Successes > 0 {
		best = fmt.Sprintf("推荐 %s（中位 %.1f ms，抖动 %.1f ms）", results[0].Provider.Name, results[0].MedianMS, results[0].JitterMS)
	}
	return tool.Result{
		Summary: "DNS 测速完成：" + best,
		Blocks: []tool.Block{
			{Kind: tool.BlockNotice, Title: "测速方法", Text: fmt.Sprintf("对每个服务发送 %d 轮 A/AAAA 查询，排序优先成功率，其次中位 RTT 和抖动。测速不经过当前系统 DNS。", rounds)},
			{Kind: tool.BlockTable, Title: "测速结果", Columns: []string{"排名", "服务", "DNS 地址", "状态", "成功", "中位 RTT", "抖动", "区域"}, Rows: rows},
		},
		Actions: actions,
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
	backupPath, err := persistBackup(env, backupName, data)
	if err != nil {
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
				Label: "测试 DNS 速度",
				Tone:  tool.ToneSuccess,
				Input: map[string]any{"action": "benchmark"},
			},
			{
				Label:   "恢复切换前配置",
				Input:   map[string]any{"action": "restore", "backupFile": backupPath},
				Confirm: restoreConfirm(b, item.Name),
			},
		},
	}, nil
}

func restoreResult(ctx context.Context, backupFile string, env tool.Environment, report tool.Reporter) (tool.Result, error) {
	b, path, err := loadBackup(env, backupFile)
	if err != nil {
		return tool.Result{}, err
	}
	state, err := readSnapshot(ctx)
	if err != nil {
		return tool.Result{}, err
	}
	item, err := backupTargetAdapter(state, b)
	if err != nil {
		return tool.Result{}, err
	}
	if err := verifyBackupTarget(b, item); err != nil {
		return tool.Result{}, err
	}
	b = normalizeBackup(b)
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}
	if err := restoreNetwork(ctx, item.InterfaceIndex, item.InterfaceGUID, item.MACAddress, b, report); err != nil {
		return tool.Result{}, err
	}
	after, err := readSnapshot(ctx)
	if err != nil {
		return tool.Result{}, fmt.Errorf("恢复已提交，但重新读取配置失败：%w；请稍后重新运行检测确认连接", err)
	}
	updated, err := backupTargetAdapter(after, b)
	if err != nil {
		return tool.Result{}, fmt.Errorf("恢复已提交，但无法重新读取该网卡：%w", err)
	}
	return tool.Result{
		Summary: fmt.Sprintf("已恢复 %s 的 IPv4 与 DNS 配置", item.Name),
		Blocks: []tool.Block{
			{Kind: tool.BlockNotice, Tone: tool.ToneSuccess, Title: "恢复完成", Text: "DHCP 状态、IPv4、默认网关、接口跃点和 DNS 已按备份恢复。"},
			{Kind: tool.BlockTable, Title: "恢复目标", Columns: []string{"项目", "值"}, Rows: [][]string{
				{"备份", filepath.Base(path)},
				{"网卡", item.Name},
				{"GUID", b.InterfaceGUID},
				{"地址模式", map[bool]string{true: "DHCP", false: "静态"}[updated.DHCPEnabled]},
				{"IPv4", strings.Join(itemIPv4(updated), " / ")},
				{"网关", strings.Join(updated.Gateways, ", ")},
				{"DNS", strings.Join(updated.DNS, ", ")},
			}},
		},
	}, nil
}

func backupTargetAdapter(state snapshot, b backup) (adapter, error) {
	for _, item := range state.Adapters {
		if strings.EqualFold(strings.TrimSpace(item.InterfaceGUID), strings.TrimSpace(b.InterfaceGUID)) &&
			sameMAC(item.MACAddress, b.MACAddress) {
			return item, nil
		}
	}
	return adapter{}, fmt.Errorf("备份对应的网卡 %s 当前不可用", b.InterfaceGUID)
}

func loadBackup(env tool.Environment, requested string) (backup, string, error) {
	requested = strings.TrimSpace(requested)
	var paths []string
	if requested != "" {
		path, err := filepath.Abs(requested)
		if err != nil {
			return backup{}, "", fmt.Errorf("解析备份路径失败：%w", err)
		}
		paths = []string{path}
	} else {
		entries, err := filepath.Glob(filepath.Join(backupDir(env), "network-backup-*.json"))
		if err != nil {
			return backup{}, "", fmt.Errorf("查找备份失败：%w", err)
		}
		sort.Sort(sort.Reverse(sort.StringSlice(entries)))
		paths = entries
	}
	if len(paths) == 0 {
		return backup{}, "", errors.New("当前任务没有网络备份；请先备份，或选择一个 JSON 备份文件")
	}
	path := paths[0]
	data, err := os.ReadFile(path)
	if err != nil {
		return backup{}, "", fmt.Errorf("读取备份失败：%w", err)
	}
	var b backup
	if err := json.Unmarshal(data, &b); err != nil {
		return backup{}, "", fmt.Errorf("备份文件无效：%w", err)
	}
	if err := validateBackup(b); err != nil {
		return backup{}, "", err
	}
	return b, path, nil
}

func validateBackup(b backup) error {
	if b.SchemaVersion != 1 && b.SchemaVersion != 2 {
		return fmt.Errorf("不支持的备份版本 %d", b.SchemaVersion)
	}
	if strings.TrimSpace(b.InterfaceGUID) == "" || strings.TrimSpace(b.MACAddress) == "" {
		return errors.New("备份缺少网卡 GUID 或 MAC")
	}
	for _, server := range b.DNS {
		if !validDNSAddress(server) {
			return fmt.Errorf("备份包含无效 DNS 地址 %q", server)
		}
	}
	for _, address := range b.IPv4 {
		if addr, err := netip.ParseAddr(strings.TrimSpace(address.Address)); err != nil || !addr.Is4() {
			return fmt.Errorf("备份包含无效 IPv4 地址 %q", address.Address)
		}
		if address.PrefixLength < 0 || address.PrefixLength > 32 {
			return fmt.Errorf("备份包含无效 IPv4 前缀长度 %d", address.PrefixLength)
		}
	}
	for _, route := range b.DefaultRoutes {
		if strings.TrimSpace(route.DestinationPrefix) != "0.0.0.0/0" {
			return fmt.Errorf("备份包含不支持的默认路由 %q", route.DestinationPrefix)
		}
		if addr, err := netip.ParseAddr(strings.TrimSpace(route.NextHop)); err != nil || !addr.Is4() {
			return fmt.Errorf("备份包含无效网关地址 %q", route.NextHop)
		}
	}
	return nil
}

func normalizeBackup(b backup) backup {
	if b.SchemaVersion == 1 && len(b.DefaultRoutes) == 0 {
		for _, gateway := range b.Gateways {
			b.DefaultRoutes = append(b.DefaultRoutes, routeConfig{
				DestinationPrefix: "0.0.0.0/0",
				NextHop:           gateway,
			})
		}
	}
	return b
}

func restoreConfirm(b backup, alias string) string {
	mode := "静态配置"
	if b.DHCPEnabled {
		mode = "DHCP"
	}
	return fmt.Sprintf("将把网卡「%s」恢复为 %s；IPv4 %s，网关 %s，DNS %s。管理员脚本会先保留当前配置，恢复失败时尝试自动回滚。",
		alias,
		mode,
		strings.Join(backupIPv4(b.IPv4), " / "),
		strings.Join(b.Gateways, " / "),
		strings.Join(b.DNS, " / "),
	)
}

func verifyBackupTarget(b backup, item adapter) error {
	if !strings.EqualFold(strings.TrimSpace(b.InterfaceGUID), strings.TrimSpace(item.InterfaceGUID)) {
		return fmt.Errorf("备份属于网卡 GUID %s，当前主网卡为 %s", b.InterfaceGUID, item.InterfaceGUID)
	}
	if !sameMAC(b.MACAddress, item.MACAddress) {
		return fmt.Errorf("备份 MAC %s 与当前主网卡 MAC %s 不一致", b.MACAddress, item.MACAddress)
	}
	return nil
}

func sameMAC(left, right string) bool {
	normalize := func(value string) string {
		return strings.ToLower(strings.NewReplacer("-", "", ":", "", " ", "").Replace(value))
	}
	return normalize(left) != "" && normalize(left) == normalize(right)
}

func validDNSAddress(value string) bool {
	addr, err := netip.ParseAddr(strings.TrimSpace(value))
	return err == nil && addr.IsValid()
}

func providerKeys() []string {
	providers := providerList()
	keys := make([]string, 0, len(providers))
	for _, p := range providers {
		keys = append(keys, p.Key)
	}
	return keys
}

func providerLabels() map[string]string {
	labels := map[string]string{}
	for _, p := range providerList() {
		labels[p.Key] = p.Name
	}
	return labels
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
		{Key: "alidns", Name: "阿里公共 DNS", Servers: []string{"223.5.5.5", "223.6.6.6"}, Region: "中国大陆", Note: "国内解析快，支持 IPv4/IPv6。"},
		{Key: "dnspod", Name: "DNSPod 公共 DNS", Servers: []string{"119.29.29.29", "182.254.116.116"}, Region: "中国大陆", Note: "腾讯云公共解析服务。"},
		{Key: "114dns", Name: "114 DNS", Servers: []string{"114.114.114.114", "114.114.115.115"}, Region: "中国大陆", Note: "运营商覆盖广，适合国内网站。"},
		{Key: "baidu", Name: "百度公共 DNS", Servers: []string{"180.76.76.76"}, Region: "中国大陆", Note: "国内访问稳定。"},
		{Key: "cnnic", Name: "CNNIC DNS", Servers: []string{"1.2.4.8", "210.2.4.8"}, Region: "中国大陆", Note: "公共 DNS 服务，部分地区延迟较低。"},
		{Key: "cloudflare", Name: "Cloudflare DNS", Servers: []string{"1.1.1.1", "1.0.0.1"}, Region: "全球", Note: "隐私友好，国际站点体验好。"},
		{Key: "google", Name: "Google Public DNS", Servers: []string{"8.8.8.8", "8.8.4.4"}, Region: "全球", Note: "全球覆盖广，国内延迟可能较高。"},
		{Key: "quad9", Name: "Quad9", Servers: []string{"9.9.9.9", "149.112.112.112"}, Region: "全球", Note: "带恶意域名拦截。"},
		{Key: "opendns", Name: "OpenDNS", Servers: []string{"208.67.222.222", "208.67.220.220"}, Region: "全球", Note: "老牌公共 DNS，支持家庭防护。"},
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
