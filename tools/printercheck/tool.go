// Package printercheck inspects configured printers, print services, drivers,
// ports and attached hardware without changing any Windows settings.
package printercheck

import (
	"context"
	"fmt"
	"strings"

	"opentools/pkg/tool"
)

const version = "1.1.0"

// Tool reports the local printer configuration and likely health issues.
type Tool struct{}

// New returns the printer inspection tool.
func New() Tool { return Tool{} }

// Definition implements tool.Tool.
func (Tool) Definition() tool.Definition {
	return tool.Definition{
		ID:          "hardware.printers",
		Name:        "打印机检测",
		Summary:     "检测真实打印机、驱动状态，并诊断连不上的具体原因。",
		Description: "只读检查 Windows 打印后台处理程序、实体打印机、打印驱动、端口和 PnP 设备，并逐项给出连接失败的原因与处理建议。虚拟打印机（如 Microsoft Print to PDF）会被忽略。打印机列表、驱动和端口会分别采集，因此即使 Spooler 未运行，也会尽量显示已配置的打印机并完成驱动核验。网络打印机只做短超时 TCP 可达性检查，不会修改任何打印设置。",
		Category:    "系统工具",
		Icon:        "printer",
		Keywords:    []string{"打印机", "打印", "驱动", "Spooler", "端口", "设备", "硬件", "离线", "诊断"},
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
	total := value.physical
	service := serviceLabel(value.snapshot.Spooler)
	if total == 0 {
		return fmt.Sprintf("未发现实体打印机；打印服务：%s。", service)
	}
	parts := []string{
		fmt.Sprintf("检测到 %d 台实体打印机", total),
		fmt.Sprintf("正常 %d 台", value.ready),
		fmt.Sprintf("需检查 %d 台", value.warning),
		fmt.Sprintf("异常 %d 台", value.danger),
	}
	if len(value.ignored) > 0 {
		parts = append(parts, fmt.Sprintf("已忽略 %d 个虚拟打印机", len(value.ignored)))
	}
	return strings.Join(parts, "，") + "；打印服务：" + service + "。"
}
