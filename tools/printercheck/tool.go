// Package printercheck inspects configured printers, print services, drivers,
// ports and attached hardware, and can repair common Windows print problems.
package printercheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"opentools/pkg/tool"
)

const version = "1.2.0"

type params struct {
	Action string `json:"action"`
	Repair string `json:"repair"`
}

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
		Description: "检查 Windows 打印后台处理程序、实体打印机、打印驱动、端口和 PnP 设备，逐项诊断连接失败的原因，并支持启动打印服务、设置开机自启、取消脱机、清理卡住任务和修正 TCP/IP 端口。修复操作需要管理员权限并会弹出 UAC 确认。虚拟打印机（如 Microsoft Print to PDF）会被忽略。打印机列表、驱动和端口会分别采集，因此即使 Spooler 未运行，也会尽量显示已配置的打印机并完成驱动核验。网络打印机只做短超时 TCP 可达性检查。",
		Category:    "系统工具",
		Icon:        "printer",
		Keywords:    []string{"打印机", "打印", "驱动", "Spooler", "端口", "设备", "硬件", "离线", "诊断"},
		Version:     version,
		Effects: []tool.Effect{
			tool.EffectReadFiles,
			tool.EffectNetwork,
			tool.EffectSystemSettings,
		},
		Input: tool.Object(map[string]*tool.Schema{
			"action": tool.Select("操作", "检测是只读操作；修复会修改系统打印设置并请求管理员权限。", "detect", "repair").
				WithEnumLabels(map[string]string{
					"detect": "检测",
					"repair": "执行修复",
				}).
				WithWidget("hidden", "").
				WithDefault("detect"),
			"repair": tool.String("修复项", "要执行的修复编号，由检测结果中的修复按钮提供。").
				WithWidget("hidden", "").
				WithDefault(""),
		}, "action"),
	}
}

// Run implements tool.Tool.
func (Tool) Run(ctx context.Context, input tool.Input, _ tool.Environment, report tool.Reporter) (tool.Result, error) {
	var p params
	if err := input.Decode(&p); err != nil {
		return tool.Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}
	if strings.EqualFold(strings.TrimSpace(p.Action), "repair") {
		return runRepair(ctx, p.Repair, report)
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

	fixes := repairs(state)
	if len(fixes) > 0 {
		report.Log("info", fmt.Sprintf("发现 %d 个可自动修复的问题", len(fixes)))
	}
	return tool.Result{
		Summary:  summary(state),
		Blocks:   append(append(overview.Blocks, printers.Blocks...), drivers.Blocks...),
		Warnings: resultWarnings(state),
		Actions:  repairActions(fixes),
	}, nil
}

// repairActions turns the available fixes into buttons. Each action carries
// its parameters in the input, so the host never has to know what a
// particular repair means.
func repairActions(fixes []repair) []tool.Action {
	out := make([]tool.Action, 0, len(fixes))
	for _, item := range fixes {
		payload, err := json.Marshal(item)
		if err != nil {
			continue
		}
		out = append(out, tool.Action{
			Label:   item.Label,
			Tone:    actionTone(item.Tone),
			Confirm: item.Confirm,
			Input: map[string]any{
				"action": "repair",
				"repair": string(payload),
			},
		})
	}
	return out
}

func actionTone(tone tool.Tone) tool.Tone {
	switch tone {
	case tool.ToneSuccess, tool.ToneWarning, tool.ToneDanger:
		return tone
	default:
		return tool.ToneNeutral
	}
}

// runRepair applies one repair and then re-checks the machine, so the result
// shows the state after the fix rather than a promise.
func runRepair(ctx context.Context, payload string, report tool.Reporter) (tool.Result, error) {
	var item repair
	if err := json.Unmarshal([]byte(strings.TrimSpace(payload)), &item); err != nil {
		return tool.Result{}, fmt.Errorf("修复参数无效：%w", err)
	}
	if strings.TrimSpace(string(item.Kind)) == "" {
		return tool.Result{}, errors.New("修复参数缺少类型")
	}
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}

	// A repair button is a snapshot of an earlier diagnosis. Re-check it
	// against the current machine so a stale button cannot apply a fix that
	// is no longer needed.
	value, err := collect(ctx)
	if err != nil {
		return tool.Result{}, err
	}
	sortPrinters(value.Printers)
	state := analyze(ctx, value)
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}
	if !repairStillApplies(state, item) {
		return tool.Result{}, fmt.Errorf("该问题已经不存在，无需修复：%s", item.Label)
	}

	report.Log("info", "正在执行修复："+item.Label)
	message, err := applyRepair(ctx, item, report)
	if err != nil {
		return tool.Result{}, err
	}
	report.Log("success", message)

	// Re-read the machine so the result reflects the repair.
	after, err := collect(ctx)
	if err != nil {
		return tool.Result{}, err
	}
	sortPrinters(after.Printers)
	newState := analyze(ctx, after)
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}

	overview := tool.Section{
		ID:     "overview",
		Title:  "修复结果",
		State:  tool.SectionReady,
		Blocks: overviewBlocks(newState),
	}
	printers := tool.Section{
		ID:     "printers",
		Title:  "已配置打印机",
		State:  tool.SectionReady,
		Blocks: printersBlocks(newState),
	}
	drivers := tool.Section{
		ID:     "drivers",
		Title:  "驱动与端口",
		State:  tool.SectionReady,
		Blocks: driversBlocks(newState),
	}
	report.Section(overview)
	report.Section(printers)
	report.Section(drivers)

	blocks := []tool.Block{{
		Kind:  tool.BlockNotice,
		Tone:  tool.ToneSuccess,
		Title: item.Label,
		Text:  message,
	}}
	blocks = append(blocks, overview.Blocks...)
	blocks = append(blocks, printers.Blocks...)
	blocks = append(blocks, drivers.Blocks...)

	return tool.Result{
		Summary:  message,
		Blocks:   blocks,
		Warnings: resultWarnings(newState),
		Actions:  repairActions(repairs(newState)),
	}, nil
}

// repairStillApplies reports whether the diagnosed problem is still present.
func repairStillApplies(state analysis, item repair) bool {
	// Only accept the repair exactly as the current diagnosis defines it.
	// A hidden form field or CLI argument is not a trusted repair command.
	canonical := false
	for _, candidate := range repairs(state) {
		if sameRepair(candidate, item) {
			canonical = true
			break
		}
	}
	if !canonical {
		return false
	}
	switch item.Kind {
	case repairStartSpooler:
		return state.snapshot.Spooler.Found &&
			!strings.EqualFold(state.snapshot.Spooler.Status, "Running")
	case repairSetSpoolerStartType:
		return state.snapshot.Spooler.Found &&
			!strings.EqualFold(state.snapshot.Spooler.StartType, "Automatic")
	case repairClearOffline:
		return findPrinter(state, item.Printer) != nil &&
			findPrinter(state, item.Printer).Info.WorkOffline
	case repairClearJobs:
		found := findPrinter(state, item.Printer)
		return found != nil && found.Info.QueuedJobs > 0
	case repairRebindPort:
		found := findPrinter(state, item.Printer)
		return found != nil && found.AltPort != nil && found.NetworkIP != ""
	}
	return false
}

func sameRepair(a, b repair) bool {
	return a.Kind == b.Kind &&
		a.Printer == b.Printer &&
		a.PortName == b.PortName &&
		a.Host == b.Host &&
		a.PortNumber == b.PortNumber &&
		a.StartType == b.StartType
}

func findPrinter(state analysis, name string) *printerAnalysis {
	for i := range state.printers {
		if state.printers[i].Info.Name == name {
			return &state.printers[i]
		}
	}
	return nil
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
