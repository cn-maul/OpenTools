// Package printercheck inspects configured printers, print services, drivers,
// ports and attached hardware without changing any Windows settings.
package printercheck

import (
	"context"
	"fmt"
	"strings"

	"opentools/pkg/tool"
)

const version = "1.0.0"

// Tool reports the local printer configuration and likely health issues.
type Tool struct{}

// New returns the printer inspection tool.
func New() Tool { return Tool{} }

// Definition implements tool.Tool.
func (Tool) Definition() tool.Definition {
	return tool.Definition{
		ID:          "hardware.printers",
		Name:        "打印机检测",
		Summary:     "检测打印服务、已连接打印机、驱动和端口状态。",
		Description: "只读检查 Windows 打印后台处理程序、打印机、打印驱动、端口和 PnP 设备。打印机列表、驱动和端口会分别采集，因此即使 Spooler 未运行，也会尽量显示已配置的打印机。网络打印机只做短超时 TCP 可达性检查，不会修改任何打印设置。",
		Category:    "系统工具",
		Icon:        "printer",
		Keywords:    []string{"打印机", "打印", "驱动", "Spooler", "端口", "设备", "硬件"},
		Version:     version,
		Effects:     []tool.Effect{tool.EffectReadFiles, tool.EffectNetwork},
		Input:       tool.Object(map[string]*tool.Schema{}),
	}
}

// Run implements tool.Tool.
func (Tool) Run(ctx context.Context, input tool.Input, _ tool.Environment, report tool.Reporter) (tool.Result, error) {
	var params struct{}
	if err := input.Decode(&params); err != nil {
		return tool.Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}

	sections := []tool.Section{
		{ID: "overview", Title: "检测概览", State: tool.SectionPending},
		{ID: "printers", Title: "已配置打印机", State: tool.SectionPending},
		{ID: "drivers", Title: "驱动与端口", State: tool.SectionPending},
	}
	for _, section := range sections {
		report.Section(section)
	}
	report.Section(tool.Section{ID: "overview", Title: "检测概览", State: tool.SectionRunning})
	report.Log("info", "正在读取打印服务、打印机、驱动和端口")

	value, err := collect(ctx)
	if err != nil {
		return tool.Result{}, err
	}
	sortPrinters(value.Printers)
	state := analyze(ctx, value)
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}

	overview := tool.Section{
		ID:     "overview",
		Title:  "检测概览",
		State:  tool.SectionReady,
		Blocks: overviewBlocks(state),
	}
	report.Section(overview)
	report.Progress(1, 3)

	printers := tool.Section{
		ID:     "printers",
		Title:  "已配置打印机",
		State:  tool.SectionReady,
		Blocks: printersBlocks(state),
	}
	report.Section(printers)
	report.Progress(2, 3)

	drivers := tool.Section{
		ID:     "drivers",
		Title:  "驱动与端口",
		State:  tool.SectionReady,
		Blocks: driversBlocks(state),
	}
	report.Section(drivers)
	report.Progress(3, 3)
	report.Log("info", "打印机检测完成")

	return tool.Result{
		Summary:  summary(state),
		Blocks:   append(append(overview.Blocks, printers.Blocks...), drivers.Blocks...),
		Warnings: resultWarnings(state),
	}, nil
}

func summary(value analysis) string {
	total := len(value.printers)
	service := serviceLabel(value.snapshot.Spooler)
	if total == 0 {
		return fmt.Sprintf("未发现已配置打印机；打印服务：%s。", service)
	}
	parts := []string{
		fmt.Sprintf("检测到 %d 台已配置打印机", total),
		fmt.Sprintf("物理打印机 %d 台", value.physical),
		fmt.Sprintf("物理正常 %d 台", value.ready),
		fmt.Sprintf("物理需检查 %d 台", value.warning),
		fmt.Sprintf("物理异常 %d 台", value.danger),
	}
	if value.virtual > 0 {
		parts = append(parts, fmt.Sprintf("虚拟 %d 台", value.virtual))
	}
	return strings.Join(parts, "，") + "；打印服务：" + service + "。"
}
