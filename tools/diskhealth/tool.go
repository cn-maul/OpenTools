// Package diskhealth reports Windows storage device health from storage
// reliability counters and volume state. It is read-only.
package diskhealth

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"opentools/pkg/tool"
)

const version = "1.0.0"

// Tool reports disk health, wear and temperature without changing anything.
type Tool struct{}

// New returns the disk health tool.
func New() Tool { return Tool{} }

// Definition implements tool.Tool.
func (Tool) Definition() tool.Definition {
	return tool.Definition{
		ID:          "hardware.disk-health",
		Name:        "磁盘健康",
		Summary:     "检测磁盘健康状态、寿命损耗、温度和分区可用空间。",
		Description: "通过存储可靠性计数器读取每块物理盘的健康状态、SSD 磨损度、温度、通电小时数和读写错误计数，并列出所有卷的容量与剩余空间。只读操作，不写入任何数据。部分机型（尤其非管理员权限时）个别计数器可能为空。",
		Category:    "系统工具",
		Icon:        "disk",
		Keywords:    []string{"磁盘", "硬盘", "SSD", "固态", "SMART", "健康", "寿命", "温度", "坏道"},
		Version:     version,
		Input:       tool.Object(map[string]*tool.Schema{}),
	}
}

// Run implements tool.Tool.
func (Tool) Run(ctx context.Context, _ tool.Input, _ tool.Environment, report tool.Reporter) (tool.Result, error) {
	for _, section := range []tool.Section{
		{ID: "disks", Title: "物理磁盘", State: tool.SectionPending},
		{ID: "volumes", Title: "分区与剩余空间", State: tool.SectionPending},
	} {
		report.Section(section)
	}
	report.Section(tool.Section{ID: "disks", Title: "物理磁盘", State: tool.SectionRunning})
	report.Log("info", "正在读取磁盘健康数据")

	value, err := collect(ctx)
	if err != nil {
		return tool.Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}

	disks := disksSection(value)
	volumes := volumesSection(value)
	report.Section(disks)
	report.Progress(1, 2)
	report.Section(volumes)
	report.Progress(2, 2)
	report.Log("info", "磁盘健康检测完成")

	return tool.Result{
		Summary:  summary(value),
		Blocks:   append(disks.Blocks, volumes.Blocks...),
		Warnings: healthWarnings(value.Disks),
	}, nil
}

func disksSection(value snapshot) tool.Section {
	blocks := make([]tool.Block, 0, 3)
	blocks = append(blocks, diskFacts(value))
	if meters := diskMeters(value.Disks); len(meters) > 0 {
		blocks = append(blocks, tool.Block{Kind: tool.BlockMeter, Title: "SSD 剩余寿命", Data: meters})
	}
	blocks = append(blocks, tool.Block{
		Kind:    tool.BlockTable,
		Title:   "物理磁盘明细",
		Columns: []string{"型号", "介质", "容量", "健康状态", "温度", "通电", "剩余寿命"},
		Rows:    diskRows(value.Disks),
	})
	return tool.Section{ID: "disks", Title: "物理磁盘", State: tool.SectionReady, Blocks: blocks}
}

func volumesSection(value snapshot) tool.Section {
	rows := make([][]string, 0, len(value.Volumes))
	for _, volume := range value.Volumes {
		rows = append(rows, []string{
			volume.DriveLetter + ":",
			volume.Label,
			volume.Filesystem,
			humanBytes(volume.SizeBytes),
			humanBytes(volume.FreeBytes),
			healthText(volume.Health),
		})
	}
	blocks := []tool.Block{{
		Kind:    tool.BlockTable,
		Title:   "分区与剩余空间",
		Columns: []string{"盘符", "卷标", "文件系统", "总容量", "可用空间", "卷状态"},
		Rows:    rows,
	}}
	return tool.Section{ID: "volumes", Title: "分区与剩余空间", State: tool.SectionReady, Blocks: blocks}
}

func diskFacts(value snapshot) tool.Block {
	ssd, hdd := 0, 0
	for _, disk := range value.Disks {
		switch disk.MediaType {
		case "SSD":
			ssd++
		case "HDD":
			hdd++
		}
	}
	permission := "否（部分计数器可能缺失）"
	if value.IsAdmin {
		permission = "是"
	}
	return tool.Block{Kind: tool.BlockFacts, Data: []tool.Fact{
		{Label: "物理磁盘", Value: fmt.Sprintf("%d 块（SSD %d，HDD %d）", len(value.Disks), ssd, hdd)},
		{Label: "管理员权限", Value: permission},
	}}
}

func diskMeters(disks []physicalDisk) []tool.MeterItem {
	meters := make([]tool.MeterItem, 0, len(disks))
	for _, disk := range disks {
		if disk.MediaType != "SSD" || disk.Wear <= 0 {
			continue
		}
		remaining := 100 - disk.Wear
		tone := tool.ToneSuccess
		switch {
		case remaining < 10:
			tone = tool.ToneDanger
		case remaining < 20:
			tone = tool.ToneWarning
		}
		meters = append(meters, tool.MeterItem{
			Label:  diskLabel(disk),
			Value:  float64(remaining),
			Max:    100,
			Unit:   "%",
			Detail: fmt.Sprintf("已写入寿命 %.0f%%", float64(disk.Wear)),
			Tone:   tone,
		})
	}
	return meters
}

func diskRows(disks []physicalDisk) [][]string {
	rows := make([][]string, 0, len(disks))
	for _, disk := range disks {
		life := "—"
		if disk.MediaType == "SSD" && disk.Wear > 0 {
			life = fmt.Sprintf("%d%%", 100-disk.Wear)
		}
		temperature := "未上报"
		if disk.Temperature > 0 {
			temperature = fmt.Sprintf("%d °C", disk.Temperature)
		}
		powerOn := "未上报"
		if disk.PowerOnHours > 0 {
			powerOn = fmt.Sprintf("%d 小时", disk.PowerOnHours)
		}
		rows = append(rows, []string{
			diskLabel(disk),
			mediaText(disk),
			humanBytes(disk.SizeBytes),
			healthText(disk.HealthStatus),
			temperature,
			powerOn,
			life,
		})
	}
	return rows
}

func diskLabel(disk physicalDisk) string {
	name := disk.FriendlyName
	if name == "" {
		name = fmt.Sprintf("磁盘 %d", disk.DiskNumber)
	}
	if disk.DriveLetters != "" {
		name += fmt.Sprintf("（%s）", disk.DriveLetters)
	}
	return name
}

func mediaText(disk physicalDisk) string {
	var media string
	switch disk.MediaType {
	case "SSD":
		media = "固态"
	case "HDD":
		media = "机械"
	default:
		media = "未知介质"
	}
	if disk.BusType != "" && disk.BusType != "Unspecified" {
		return media + " · " + disk.BusType
	}
	return media
}

func healthText(status string) string {
	switch status {
	case "Healthy":
		return "正常"
	case "Warning":
		return "警告"
	case "Unhealthy":
		return "异常"
	default:
		return status
	}
}

func healthWarnings(disks []physicalDisk) []string {
	warnings := make([]string, 0, 2)
	for _, disk := range disks {
		switch disk.HealthStatus {
		case "Warning":
			warnings = append(warnings, fmt.Sprintf("%s 报告警告状态，建议尽快备份重要数据。", diskLabel(disk)))
		case "Unhealthy":
			warnings = append(warnings, fmt.Sprintf("%s 健康状态异常，请立即备份数据并考虑更换磁盘。", diskLabel(disk)))
		}
		if disk.Temperature >= 60 {
			warnings = append(warnings, fmt.Sprintf("%s 温度 %d °C 偏高，请检查散热。", diskLabel(disk), disk.Temperature))
		}
		if disk.ReadErrorsUncorrected > 0 || disk.WriteErrorsUncorrected > 0 {
			warnings = append(warnings, fmt.Sprintf(
				"%s 有未纠正错误（读 %d / 写 %d），建议立即备份重要数据。",
				diskLabel(disk), disk.ReadErrorsUncorrected, disk.WriteErrorsUncorrected,
			))
		}
	}
	return warnings
}

func summary(value snapshot) string {
	sort.SliceStable(value.Volumes, func(i, j int) bool {
		return value.Volumes[i].DriveLetter < value.Volumes[j].DriveLetter
	})
	attention := len(healthWarnings(value.Disks))
	headline := "全部健康"
	if attention > 0 {
		headline = fmt.Sprintf("%d 项需要注意", attention)
	}
	detail := ""
	if system := systemVolume(value); system != nil && system.SizeBytes > 0 {
		detail = fmt.Sprintf("，系统卷可用 %s / %s", humanBytes(system.FreeBytes), humanBytes(system.SizeBytes))
	}
	return fmt.Sprintf("%d 块磁盘：%s%s", len(value.Disks), headline, detail)
}

func systemVolume(value snapshot) *logicalVolume {
	for i, volume := range value.Volumes {
		if strings.EqualFold(volume.DriveLetter, "C") {
			return &value.Volumes[i]
		}
	}
	return nil
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
