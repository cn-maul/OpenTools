// Package perfcheckup samples live CPU, memory and disk pressure on Windows
// and ranks the heaviest processes. It is read-only.
package perfcheckup

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	"opentools/pkg/tool"
)

const version = "1.0.0"

// Tool reports what is currently slowing the computer down.
type Tool struct{}

// New returns the performance checkup tool.
func New() Tool { return Tool{} }

// Definition implements tool.Tool.
func (Tool) Definition() tool.Definition {
	return tool.Definition{
		ID:          "system.performance-checkup",
		Name:        "卡慢体检",
		Summary:     "采样 CPU 与内存占用，找出拖慢电脑的进程和空间告急的分区。",
		Description: "实时采样约 1.5 秒，统计 CPU 与内存占用最高的进程、整体资源压力和各固定分区的剩余空间，并按阈值给出卡顿原因提示。只读操作，不结束任何进程。",
		Category:    "系统工具",
		Icon:        "gauge",
		Keywords:    []string{"卡", "慢", "CPU", "内存", "占用", "进程", "性能", "体检", "磁盘空间"},
		Version:     version,
		Input:       tool.Object(map[string]*tool.Schema{}),
	}
}

// Run implements tool.Tool.
func (Tool) Run(ctx context.Context, _ tool.Input, _ tool.Environment, report tool.Reporter) (tool.Result, error) {
	for _, section := range []tool.Section{
		{ID: "pressure", Title: "资源压力", State: tool.SectionPending},
		{ID: "processes", Title: "占用排行", State: tool.SectionPending},
		{ID: "disks", Title: "分区空间", State: tool.SectionPending},
	} {
		report.Section(section)
	}
	report.Section(tool.Section{ID: "pressure", Title: "资源压力", State: tool.SectionRunning})
	report.Log("info", "正在采样 CPU 与内存占用（约 2 秒）")

	value, err := collect(ctx)
	if err != nil {
		return tool.Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}

	pressure := pressureSection(value)
	processes := processesSection(value)
	disks := disksSection(value)
	report.Section(pressure)
	report.Progress(1, 3)
	report.Section(processes)
	report.Progress(2, 3)
	report.Section(disks)
	report.Progress(3, 3)
	report.Log("info", "卡慢体检完成")

	blocks := pressure.Blocks
	blocks = append(blocks, processes.Blocks...)
	blocks = append(blocks, disks.Blocks...)
	return tool.Result{
		Summary:  summary(value),
		Blocks:   blocks,
		Warnings: warnings(value),
	}, nil
}

func pressureSection(value snapshot) tool.Section {
	memUsed := value.MemTotalBytes - value.MemFreeBytes
	memPercent := percent(memUsed, value.MemTotalBytes)
	meters := []tool.MeterItem{
		{
			Label:  "CPU 使用率",
			Value:  round1(value.CPULoadPercent),
			Max:    100,
			Unit:   "%",
			Detail: fmt.Sprintf("%d 个逻辑处理器，采样 %.1f 秒", value.LogicalCores, value.SampleSeconds),
			Tone:   loadTone(value.CPULoadPercent),
		},
		{
			Label:  "内存使用率",
			Value:  round1(memPercent),
			Max:    100,
			Unit:   "%",
			Detail: fmt.Sprintf("已用 %s / %s", humanBytes(memUsed), humanBytes(value.MemTotalBytes)),
			Tone:   loadTone(memPercent),
		},
	}
	if system := systemDisk(value.Disks); system != nil {
		used := percent(system.SizeBytes-system.FreeBytes, system.SizeBytes)
		meters = append(meters, tool.MeterItem{
			Label:  "系统盘空间",
			Value:  round1(used),
			Max:    100,
			Unit:   "%",
			Detail: fmt.Sprintf("C: 可用 %s / %s", humanBytes(system.FreeBytes), humanBytes(system.SizeBytes)),
			Tone:   diskTone(system.FreeBytes, system.SizeBytes),
		})
	}
	blocks := []tool.Block{{Kind: tool.BlockMeter, Title: "资源压力", Data: meters}}
	blocks = append(blocks, tool.Block{Kind: tool.BlockFacts, Data: []tool.Fact{
		{Label: "进程总数", Value: fmt.Sprintf("%d 个", value.ProcessCount)},
		{Label: "开机自启项", Value: fmt.Sprintf("%d 项", value.StartupCount)},
	}})
	return tool.Section{ID: "pressure", Title: "资源压力", State: tool.SectionReady, Blocks: blocks}
}

func processesSection(value snapshot) tool.Section {
	blocks := []tool.Block{
		{
			Kind:    tool.BlockTable,
			Title:   "内存占用排行",
			Columns: []string{"进程", "PID", "内存", "累计 CPU 时间"},
			Rows:    processRows(value.TopByMem, false),
		},
		{
			Kind:    tool.BlockTable,
			Title:   "CPU 占用排行",
			Columns: []string{"进程", "PID", "CPU 占用", "累计 CPU 时间"},
			Rows:    processRows(value.TopByCPU, true),
		},
	}
	return tool.Section{ID: "processes", Title: "占用排行", State: tool.SectionReady, Blocks: blocks}
}

func disksSection(value snapshot) tool.Section {
	disks := append([]diskUsage(nil), value.Disks...)
	sort.SliceStable(disks, func(i, j int) bool { return disks[i].Drive < disks[j].Drive })
	rows := make([][]string, 0, len(disks))
	for _, disk := range disks {
		rows = append(rows, []string{
			disk.Drive,
			disk.Label,
			humanBytes(disk.SizeBytes),
			humanBytes(disk.FreeBytes),
			fmt.Sprintf("%.0f%%", percent(disk.SizeBytes-disk.FreeBytes, disk.SizeBytes)),
		})
	}
	blocks := []tool.Block{{
		Kind:    tool.BlockTable,
		Title:   "固定分区空间",
		Columns: []string{"盘符", "卷标", "总容量", "可用空间", "已用"},
		Rows:    rows,
	}}
	return tool.Section{ID: "disks", Title: "分区空间", State: tool.SectionReady, Blocks: blocks}
}

func processRows(list []processInfo, byCPU bool) [][]string {
	rows := make([][]string, 0, len(list))
	for _, process := range list {
		third := humanBytes(process.MemBytes)
		if byCPU {
			third = fmt.Sprintf("%.1f%%", process.CPUPercent)
		}
		rows = append(rows, []string{
			process.Name,
			fmt.Sprintf("%d", process.PID),
			third,
			fmt.Sprintf("%.0f 秒", process.CPUSeconds),
		})
	}
	return rows
}

func warnings(value snapshot) []string {
	out := make([]string, 0, 3)
	memUsed := value.MemTotalBytes - value.MemFreeBytes
	memPercent := percent(memUsed, value.MemTotalBytes)
	if memPercent >= 90 {
		out = append(out, fmt.Sprintf("内存使用率 %.0f%%，已接近满载，卡顿大概率来自内存不足，可关闭内存占用最高的几个程序。", memPercent))
	} else if memPercent >= 80 {
		out = append(out, fmt.Sprintf("内存使用率 %.0f%% 偏高，建议留意常驻程序数量。", memPercent))
	}
	if value.CPULoadPercent >= 70 {
		heaviest := heaviestCPUName(value.TopByCPU)
		if heaviest != "" {
			out = append(out, fmt.Sprintf("采样期间 CPU 占用 %.0f%%，最高的是 %s，可先从它排查。", value.CPULoadPercent, heaviest))
		}
	}
	if system := systemDisk(value.Disks); system != nil && system.SizeBytes > 0 {
		freePercent := percent(system.FreeBytes, system.SizeBytes)
		if freePercent < 10 {
			out = append(out, fmt.Sprintf("系统盘可用空间不足 %.0f%%（剩余 %s），会明显拖慢系统，建议清理。", freePercent, humanBytes(system.FreeBytes)))
		}
	}
	return out
}

func heaviestCPUName(list []processInfo) string {
	if len(list) == 0 {
		return ""
	}
	top := list[0]
	if top.CPUPercent <= 0 {
		return ""
	}
	return fmt.Sprintf("%s（PID %d）", top.Name, top.PID)
}

func summary(value snapshot) string {
	memUsed := value.MemTotalBytes - value.MemFreeBytes
	memPercent := percent(memUsed, value.MemTotalBytes)
	detail := ""
	if system := systemDisk(value.Disks); system != nil {
		detail = fmt.Sprintf("，C: 可用 %s", humanBytes(system.FreeBytes))
	}
	return fmt.Sprintf("CPU %.0f%%，内存 %.0f%%%s", value.CPULoadPercent, memPercent, detail)
}

func systemDisk(disks []diskUsage) *diskUsage {
	for i, disk := range disks {
		if strings.EqualFold(disk.Drive, "C:") {
			return &disks[i]
		}
	}
	return nil
}

func loadTone(value float64) tool.Tone {
	switch {
	case value >= 90:
		return tool.ToneDanger
	case value >= 70:
		return tool.ToneWarning
	default:
		return tool.ToneSuccess
	}
}

func diskTone(free, total int64) tool.Tone {
	freePercent := percent(free, total)
	switch {
	case freePercent < 10:
		return tool.ToneDanger
	case freePercent < 20:
		return tool.ToneWarning
	default:
		return tool.ToneSuccess
	}
}

func percent(part, total int64) float64 {
	if total <= 0 {
		return 0
	}
	return float64(part) * 100 / float64(total)
}

func round1(value float64) float64 {
	return math.Round(value*10) / 10
}

func humanBytes(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	value := float64(size)
	units := []string{"KB", "MB", "GB", "TB", "PB"}
	for _, name := range units {
		value /= unit
		if value < unit {
			return fmt.Sprintf("%.1f %s", value, name)
		}
	}
	return fmt.Sprintf("%.1f EB", value/1024/1024/1024/1024/1024/1024)
}
