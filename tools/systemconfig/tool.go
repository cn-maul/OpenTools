// Package systemconfig collects a read-only inventory of the local computer.
package systemconfig

import (
	"context"
	"fmt"
	"strings"
	"time"

	"opentools/pkg/tool"
)

const version = "1.3.0"

// Tool reports hardware, operating system, network and security state.
type Tool struct{}

// New returns the system inventory tool.
func New() Tool { return Tool{} }

// Definition implements tool.Tool.
func (Tool) Definition() tool.Definition {
	return tool.Definition{
		ID:          "system.inventory",
		Name:        "系统配置",
		Summary:     "检测本机硬件、系统、磁盘、网络与安全状态。",
		Description: "只读采集本机配置，不修改任何设置。覆盖处理器、内存、显卡、磁盘、操作系统、网络适配器、启动模式、TPM、Defender、防火墙与 BitLocker；同时生成可保存的 JSON 报告。",
		Category:    "系统工具",
		Icon:        "system",
		Keywords:    []string{"系统", "配置", "硬件", "电脑", "CPU", "内存", "显卡", "磁盘", "主板", "BIOS", "安全"},
		Version:     version,
		Input:       tool.Object(map[string]*tool.Schema{}),
	}
}

// Run implements tool.Tool.
func (Tool) Run(ctx context.Context, input tool.Input, env tool.Environment, report tool.Reporter) (tool.Result, error) {
	const scope = "full"
	for _, section := range inventorySections() {
		report.Section(section)
	}
	report.Log("info", "正在读取本机系统配置")
	state, err := collect(ctx, report)
	if err != nil {
		return tool.Result{}, err
	}
	state.Profile = scope
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}

	blocks := resultBlocks(state)
	report.Log("info", "系统配置检测完成")

	return tool.Result{
		Summary: fmt.Sprintf(
			"%s：%s · %s · %s 内存 · %d 块磁盘",
			scopeLabel(scope),
			fallback(state.Computer.Model, state.Computer.Name, "未知设备"),
			fallback(state.OS.Caption, "未知系统"),
			formatBytes(state.Memory.TotalBytes),
			len(state.Disks),
		),
		Blocks:   blocks,
		Warnings: resultWarnings(state),
	}, nil
}

const (
	sectionDevice    = "device"
	sectionProcessor = "processor"
	sectionMemory    = "memory"
	sectionGraphics  = "graphics"
	sectionDisk      = "disk"
	sectionVolume    = "volume"
	sectionOS        = "os"
	sectionNetwork   = "network"
	sectionSecurity  = "security"
	sectionPower     = "power"
)

func inventorySections() []tool.Section {
	return []tool.Section{
		{ID: sectionDevice, Title: "设备概览", State: tool.SectionPending},
		{ID: sectionProcessor, Title: "处理器", State: tool.SectionPending},
		{ID: sectionMemory, Title: "内存", State: tool.SectionPending},
		{ID: sectionGraphics, Title: "显卡", State: tool.SectionPending},
		{ID: sectionDisk, Title: "物理磁盘", State: tool.SectionPending},
		{ID: sectionVolume, Title: "磁盘卷", State: tool.SectionPending},
		{ID: sectionOS, Title: "操作系统", State: tool.SectionPending},
		{ID: sectionNetwork, Title: "已连接网络", State: tool.SectionPending},
		{ID: sectionSecurity, Title: "安全与启动", State: tool.SectionPending},
		{ID: sectionPower, Title: "电源", State: tool.SectionPending},
	}
}

func sectionTitle(id string) string {
	for _, section := range inventorySections() {
		if section.ID == id {
			return section.Title
		}
	}
	return ""
}

func readySection(state snapshot, id string) (tool.Section, bool) {
	title := sectionTitle(id)
	if title == "" {
		return tool.Section{}, false
	}
	blocks := append(hardwareBlocks(state), systemBlocks(state)...)
	for _, block := range blocks {
		if sectionID(block.Title) == id {
			return tool.Section{
				ID:     id,
				Title:  title,
				State:  tool.SectionReady,
				Blocks: []tool.Block{block},
			}, true
		}
	}
	return tool.Section{}, false
}

func sectionID(title string) string {
	switch title {
	case "设备概览":
		return sectionDevice
	case "处理器":
		return sectionProcessor
	case "内存":
		return sectionMemory
	case "显卡":
		return sectionGraphics
	case "物理磁盘":
		return sectionDisk
	case "磁盘卷":
		return sectionVolume
	case "操作系统":
		return sectionOS
	case "已连接网络":
		return sectionNetwork
	case "安全与启动":
		return sectionSecurity
	case "电源", "电池":
		return sectionPower
	default:
		return ""
	}
}

func resultBlocks(state snapshot) []tool.Block {
	return append(hardwareBlocks(state), systemBlocks(state)...)
}

func hardwareBlocks(state snapshot) []tool.Block {
	computer := state.Computer
	board := state.Board
	blocks := []tool.Block{
		factsBlock("设备概览", []tool.Fact{
			{Label: "设备名称", Value: fallback(computer.Name, "未知")},
			{Label: "制造商 / 型号", Value: joinNonEmpty(" / ", computer.Manufacturer, computer.Model)},
			{Label: "主板", Value: joinNonEmpty(" / ", board.Manufacturer, board.Product)},
			{Label: "系统类型", Value: fallback(computer.SystemType, "未知")},
			{Label: "固件", Value: joinNonEmpty(" / ", state.Firmware.Vendor, state.Firmware.Version)},
			{Label: "启动模式", Value: fallback(state.Firmware.BootMode, "未知")},
		}),
		processorBlock(state),
		memoryBlock(state.Memory),
		graphicsBlock(state.Graphics),
		diskBlock(state.Disks),
		volumeBlock(state.Volumes),
	}
	return blocks
}

func systemBlocks(state snapshot) []tool.Block {
	osInfo := state.OS
	blocks := []tool.Block{
		factsBlock("操作系统", []tool.Fact{
			{Label: "版本", Value: fallback(osInfo.Caption, "未知")},
			{Label: "系统版本", Value: fallback(osInfo.Version, "未知")},
			{Label: "Build", Value: fallback(osInfo.Build, "未知")},
			{Label: "架构", Value: fallback(osInfo.Architecture, "未知")},
			{Label: "安装时间", Value: formatDate(osInfo.InstallDate)},
			{Label: "上次启动", Value: formatDate(osInfo.LastBoot)},
			{Label: "已运行", Value: formatDuration(time.Duration(osInfo.UptimeSeconds) * time.Second)},
			{Label: "时区", Value: fallback(osInfo.TimeZone, "未知")},
			{Label: "当前用户", Value: fallback(state.Computer.CurrentUser, "未知")},
		}),
		networkBlock(state.Network),
		securityBlock(state.Security),
		batteryBlock(state.Battery),
	}
	return blocks
}

func factsBlock(title string, facts []tool.Fact) tool.Block {
	return tool.Block{
		Kind:  tool.BlockFacts,
		Title: title,
		Data:  map[string]any{"items": facts},
	}
}

// percentTone colours a load or usage meter by pressure, not by pass/fail.
func percentTone(value float64) tool.Tone {
	switch {
	case value >= 90:
		return tool.ToneDanger
	case value >= 70:
		return tool.ToneWarning
	default:
		return tool.ToneSuccess
	}
}

func processorBlock(state snapshot) tool.Block {
	load := float64(state.CPU.LoadPercent)
	return tool.Block{
		Kind:  tool.BlockMeter,
		Title: "处理器",
		Data: map[string]any{"items": []tool.MeterItem{
			{
				Label:   "当前负载",
				Value:   load,
				Max:     100,
				Unit:    "%",
				Detail:  fmt.Sprintf("%d 核 / %d 线程", state.CPU.Cores, state.CPU.LogicalProcessors),
				Tone:    percentTone(load),
				Variant: "ring",
			},
		}},
	}
}

func memoryBlock(info memoryInfo) tool.Block {
	used := info.TotalBytes - info.AvailableBytes
	if info.TotalBytes <= 0 {
		used = 0
	}
	percent := float64(0)
	if info.TotalBytes > 0 {
		percent = float64(used) * 100 / float64(info.TotalBytes)
	}
	items := []tool.MeterItem{
		{
			Label:   "内存占用",
			Value:   percent,
			Max:     100,
			Unit:    "%",
			Detail:  fmt.Sprintf("%s / %s", formatBytes(used), formatBytes(info.TotalBytes)),
			Tone:    percentTone(percent),
			Variant: "ring",
		},
	}
	facts := make([]tool.Fact, 0, len(info.Modules))
	for _, module := range info.Modules {
		facts = append(facts, tool.Fact{
			Label: fmt.Sprintf("内存条 %s", fallback(module.Slot, "未标注插槽")),
			Value: fmt.Sprintf("%s · %s · %d MHz", fallback(module.Manufacturer, "未知厂商"), fallback(module.PartNumber, "未知型号"), module.SpeedMHz),
		})
	}
	return tool.Block{
		Kind:  tool.BlockMeter,
		Title: "内存",
		Data:  map[string]any{"items": items, "facts": facts},
	}
}

func graphicsBlock(items []graphicsInfo) tool.Block {
	facts := make([]tool.Fact, 0, len(items))
	for _, item := range items {
		memory := formatBytes(item.MemoryBytes)
		if item.MemoryBytes == 0 {
			memory = "共享/未知"
		}
		facts = append(facts, tool.Fact{
			Label: fallback(item.Name, "未知显卡"),
			Value: fmt.Sprintf("%s · %s · 驱动 %s · %s", fallback(item.Vendor, "未知厂商"), memory, fallback(item.DriverVersion, "未知"), fallback(item.Status, "未知")),
		})
	}
	if len(facts) == 0 {
		facts = append(facts, tool.Fact{Label: "显卡", Value: "未检测到显卡信息"})
	}
	return factsBlock("显卡", facts)
}

func diskBlock(items []diskInfo) tool.Block {
	facts := make([]tool.Fact, 0, len(items))
	for _, item := range items {
		facts = append(facts, tool.Fact{
			Label: fallback(item.Model, "未知磁盘"),
			Value: fmt.Sprintf("%s · %s · %s · %s", formatBytes(item.SizeBytes), fallback(item.BusType, "未知总线"), fallback(item.MediaType, "未知介质"), fallback(item.Health, "未知健康")),
		})
	}
	if len(facts) == 0 {
		facts = append(facts, tool.Fact{Label: "磁盘", Value: "未检测到物理磁盘"})
	}
	return factsBlock("物理磁盘", facts)
}

func volumeBlock(items []volumeInfo) tool.Block {
	metrics := make([]tool.MeterItem, 0, len(items))
	for _, item := range items {
		used := item.TotalBytes - item.FreeBytes
		percent := float64(0)
		if item.TotalBytes > 0 {
			percent = float64(used) * 100 / float64(item.TotalBytes)
		}
		metrics = append(metrics, tool.MeterItem{
			Label: fmt.Sprintf("%s %s", fallback(item.DriveLetter, "?"), fallback(item.Label, "本地磁盘")),
			Value: percent,
			Max:   100,
			Unit:  "%",
			Detail: fmt.Sprintf("%s / %s · 可用 %s · %s",
				formatBytes(used), formatBytes(item.TotalBytes), formatBytes(item.FreeBytes), fallback(item.FileSystem, "未知")),
			Tone:    percentTone(percent),
			Variant: "bar",
		})
	}
	if len(metrics) == 0 {
		return factsBlock("磁盘卷", []tool.Fact{{Label: "磁盘卷", Value: "未检测到本地卷"}})
	}
	return tool.Block{
		Kind:  tool.BlockMeter,
		Title: "磁盘卷",
		Data:  map[string]any{"items": metrics},
	}
}

func networkBlock(items []networkInfo) tool.Block {
	facts := make([]tool.Fact, 0, len(items)*2)
	for _, item := range items {
		name := fallback(item.Name, "未命名")
		facts = append(facts,
			tool.Fact{
				Label: name,
				Group: fallback(item.Type, "网卡"),
				Value: fmt.Sprintf("%s · IPv4 %s", fallback(item.LinkSpeed, "未知速率"), fallback(strings.Join(item.IPv4, " / "), "无")),
			},
			tool.Fact{
				Label: "DNS",
				Group: fallback(item.Type, "网卡"),
				Value: fallback(strings.Join(item.DNS, " / "), "无"),
			},
		)
	}
	if len(facts) == 0 {
		facts = append(facts, tool.Fact{Label: "网络", Value: "没有已连接的网卡"})
	}
	return factsBlock("已连接网络", facts)
}

func securityBlock(info securityInfo) tool.Block {
	return factsBlock("安全与启动", []tool.Fact{
		{Label: "安全启动", Value: fallback(info.SecureBoot, "未知")},
		{Label: "TPM", Value: joinNonEmpty(" · ", info.TPM, info.TPMVersion)},
		{Label: "Defender", Value: fallback(info.Defender, "未知")},
		{Label: "实时保护", Value: fallback(info.DefenderRealtime, "未知")},
		{Label: "防火墙", Value: fallback(info.Firewall, "未知")},
		{Label: "BitLocker", Value: fallback(info.BitLocker, "未知")},
		{Label: "硬件虚拟化", Value: fallback(info.Virtualization, "未知")},
	})
}

func batteryBlock(info batteryInfo) tool.Block {
	if !info.Present {
		return tool.Block{
			Kind:  tool.BlockNotice,
			Tone:  tool.ToneNeutral,
			Title: "电源",
			Text:  "未检测到电池，当前设备按台式机或固定电源供电。",
		}
	}
	return tool.Block{
		Kind:  tool.BlockNotice,
		Tone:  tool.ToneNeutral,
		Title: "电池",
		Text:  fmt.Sprintf("%s · 电量 %d%% · 状态代码 %s", fallback(info.Name, "电池"), info.ChargePercent, fallback(info.Status, "未知")),
	}
}

func resultWarnings(state snapshot) []string {
	var warnings []string
	if state.Security.SecureBoot == "不支持或未知" {
		warnings = append(warnings, "安全启动状态无法读取；虚拟机、旧版 BIOS 或权限限制都可能导致该结果。")
	}
	if state.Security.TPM == "无法读取" {
		warnings = append(warnings, "TPM 状态无法读取；这不代表设备一定没有 TPM。")
	}
	return warnings
}

func scopeLabel(_ string) string {
	return "完整系统检测"
}

func fallback(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func joinNonEmpty(separator string, values ...string) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			parts = append(parts, strings.TrimSpace(value))
		}
	}
	if len(parts) == 0 {
		return "未知"
	}
	return strings.Join(parts, separator)
}

func formatBytes(value int64) string {
	if value <= 0 {
		return "未知"
	}
	const (
		kib = 1024
		mib = 1024 * kib
		gib = 1024 * mib
		tib = 1024 * gib
	)
	switch {
	case value >= tib:
		return fmt.Sprintf("%.2f TB", float64(value)/tib)
	case value >= gib:
		return fmt.Sprintf("%.1f GB", float64(value)/gib)
	case value >= mib:
		return fmt.Sprintf("%.0f MB", float64(value)/mib)
	default:
		return fmt.Sprintf("%.0f KB", float64(value)/kib)
	}
}

func formatMHz(value int) string {
	if value <= 0 {
		return "未知"
	}
	return fmt.Sprintf("%.2f GHz", float64(value)/1000)
}

func formatDate(value string) string {
	if strings.TrimSpace(value) == "" {
		return "未知"
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.Local().Format("2006-01-02 15:04")
		}
	}
	return value
}

func formatDuration(value time.Duration) string {
	if value <= 0 {
		return "未知"
	}
	days := int(value / (24 * time.Hour))
	hours := int(value%(24*time.Hour)) / int(time.Hour)
	minutes := int(value%time.Hour) / int(time.Minute)
	if days > 0 {
		return fmt.Sprintf("%d 天 %d 小时", days, hours)
	}
	return fmt.Sprintf("%d 小时 %d 分钟", hours, minutes)
}
