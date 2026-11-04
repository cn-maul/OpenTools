// Package crashdiag reports Windows blue screens and unexpected reboots from
// the system event log and dump files. It is read-only.
package crashdiag

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"opentools/pkg/tool"
)

const version = "1.0.0"

// maxEventRows and maxDumpRows cap the tables so a machine with a long
// failure history still fits one screen; the newest records win.
const maxEventRows = 20
const maxDumpRows = 20

const detailLimit = 60

// Tool reports bug checks, unexpected shutdowns and dump files.
type Tool struct{}

// New returns the crash diagnostics tool.
func New() Tool { return Tool{} }

// Definition implements tool.Tool.
func (Tool) Definition() tool.Definition {
	return tool.Definition{
		ID:          "system.crash-diagnostics",
		Name:        "蓝屏与重启诊断",
		Summary:     "汇总蓝屏停机码、异常关机事件和转储文件，定位死机重启原因。",
		Description: "读取最近 90 天的系统事件日志：1001（蓝屏检查）、41（内核断电/异常重启）、6008（意外关机），解析停机码并给出常见含义；同时列出 Minidump 和内核转储文件。只读操作，不修改任何设置。",
		Category:    "系统工具",
		Icon:        "crash",
		Keywords:    []string{"蓝屏", "BSOD", "死机", "重启", "断电", "停机码", "Minidump", "转储", "事件日志"},
		Version:     version,
		Input:       tool.Object(map[string]*tool.Schema{}),
	}
}

// Run implements tool.Tool.
func (Tool) Run(ctx context.Context, _ tool.Input, _ tool.Environment, report tool.Reporter) (tool.Result, error) {
	for _, section := range []tool.Section{
		{ID: "overview", Title: "诊断概览", State: tool.SectionPending},
		{ID: "bugchecks", Title: "蓝屏记录", State: tool.SectionPending},
		{ID: "unexpected", Title: "异常关机与断电", State: tool.SectionPending},
		{ID: "dumps", Title: "转储文件", State: tool.SectionPending},
	} {
		report.Section(section)
	}
	report.Section(tool.Section{ID: "overview", Title: "诊断概览", State: tool.SectionRunning})
	report.Log("info", "正在读取系统事件日志与转储文件")

	value, err := collect(ctx)
	if err != nil {
		return tool.Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}

	overview := overviewSection(value)
	bugchecks := bugchecksSection(value.BugChecks)
	unexpected := unexpectedSection(value.Unexpected)
	dumps := dumpsSection(value)
	report.Section(overview)
	report.Progress(1, 4)
	report.Section(bugchecks)
	report.Progress(2, 4)
	report.Section(unexpected)
	report.Progress(3, 4)
	report.Section(dumps)
	report.Progress(4, 4)
	report.Log("info", "蓝屏诊断完成")

	blocks := overview.Blocks
	blocks = append(blocks, bugchecks.Blocks...)
	blocks = append(blocks, unexpected.Blocks...)
	blocks = append(blocks, dumps.Blocks...)
	return tool.Result{
		Summary:  summary(value),
		Blocks:   blocks,
		Warnings: warnings(value),
	}, nil
}

var stopCodePattern = regexp.MustCompile(`0x[0-9a-fA-F]{8}`)

// stopCodeNames maps the bug check codes that cover the overwhelming
// majority of consumer blue screens. Unlisted codes keep their raw value.
var stopCodeNames = map[string]string{
	"0x0000000a": "IRQL_NOT_LESS_OR_EQUAL（通常是驱动问题）",
	"0x0000001a": "MEMORY_MANAGEMENT（常见于内存故障）",
	"0x0000003b": "SYSTEM_SERVICE_EXCEPTION（驱动或系统服务异常）",
	"0x00000050": "PAGE_FAULT_IN_NONPAGED_AREA（内存或驱动问题）",
	"0x0000007e": "SYSTEM_THREAD_EXCEPTION_NOT_HANDLED（驱动问题）",
	"0x0000007f": "UNEXPECTED_KERNEL_MODE_TRAP（硬件或驱动问题）",
	"0x000000d1": "DRIVER_IRQL_NOT_LESS_OR_EQUAL（驱动问题）",
	"0x000000ef": "CRITICAL_PROCESS_DIED（关键系统进程崩溃）",
	"0x000000f4": "CRITICAL_OBJECT_TERMINATION（进程异常终止）",
	"0x00000101": "CLOCK_WATCHDOG_TIMEOUT（处理器响应超时）",
	"0x00000116": "VIDEO_TDR_FAILURE（显卡驱动重置失败）",
	"0x00000117": "VIDEO_TDR_TIMEOUT_DETECTED（显卡驱动超时）",
	"0x00000124": "WHEA_UNCORRECTABLE_ERROR（硬件错误）",
	"0x00000133": "DPC_WATCHDOG_VIOLATION（驱动或固态固件问题）",
	"0x00000139": "KERNEL_SECURITY_CHECK_FAILURE（内核一致性检查失败）",
	"0x0000015f": "CONNECTED_STANDBY_WATCHDOG_TIMEOUT（现代待机超时）",
	"0xc000021a": "WINLOGON_FATAL_ERROR（系统进程崩溃）",
	"0xc0000221": "BAD_IMAGE_CHECKSUM（系统文件损坏）",
}

func stopCodeMeaning(message string) (string, string) {
	match := stopCodePattern.FindString(strings.ToLower(message))
	if match == "" {
		return "", ""
	}
	name, ok := stopCodeNames[match]
	if !ok {
		name = "少见停机码，可联网查询"
	}
	return strings.ToUpper(match), name
}

func overviewSection(value snapshot) tool.Section {
	uptime := fmt.Sprintf("%.1f 小时", value.UptimeHours)
	if value.UptimeHours >= 24 {
		uptime = fmt.Sprintf("%.1f 天", value.UptimeHours/24)
	}
	lastBoot := "未知"
	if value.LastBoot != "" {
		if parsed, err := time.Parse(time.RFC3339, value.LastBoot); err == nil {
			lastBoot = parsed.Local().Format("2006-01-02 15:04:05")
		}
	}
	facts := []tool.Fact{
		{Label: "本次开机", Value: lastBoot + "（已运行 " + uptime + "）"},
		{Label: "90 天内蓝屏", Value: fmt.Sprintf("%d 次", len(value.BugChecks))},
		{Label: "90 天内异常关机/断电", Value: fmt.Sprintf("%d 次", len(value.Unexpected))},
		{Label: "转储文件", Value: fmt.Sprintf("Minidump %d 个", len(value.Minidumps))},
	}
	blocks := []tool.Block{{Kind: tool.BlockFacts, Data: facts}}
	if len(value.BugChecks) == 0 && len(value.Unexpected) == 0 {
		blocks = append(blocks, tool.Block{
			Kind: tool.BlockNotice,
			Tone: tool.ToneSuccess,
			Text: "最近 90 天没有发现蓝屏或异常关机记录。",
		})
	}
	return tool.Section{ID: "overview", Title: "诊断概览", State: tool.SectionReady, Blocks: blocks}
}

func bugchecksSection(events []crashEvent) tool.Section {
	if len(events) == 0 {
		return tool.Section{
			ID:     "bugchecks",
			Title:  "蓝屏记录",
			State:  tool.SectionReady,
			Blocks: []tool.Block{{Kind: tool.BlockNotice, Tone: tool.ToneSuccess, Text: "没有蓝屏（BugCheck 1001）记录。"}},
		}
	}
	rows := make([][]string, 0, len(events))
	for _, event := range events {
		code, meaning := stopCodeMeaning(event.Message)
		rows = append(rows, []string{localTime(event.Time), code, meaning})
	}
	return tool.Section{ID: "bugchecks", Title: "蓝屏记录", State: tool.SectionReady, Blocks: []tool.Block{{
		Kind:    tool.BlockTable,
		Title:   "蓝屏停机码",
		Columns: []string{"时间", "停机码", "含义"},
		Rows:    rows,
	}}}
}

func unexpectedSection(events []crashEvent) tool.Section {
	if len(events) == 0 {
		return tool.Section{
			ID:     "unexpected",
			Title:  "异常关机与断电",
			State:  tool.SectionReady,
			Blocks: []tool.Block{{Kind: tool.BlockNotice, Tone: tool.ToneSuccess, Text: "没有异常关机或断电记录。"}},
		}
	}
	rows := make([][]string, 0, len(events))
	for _, event := range events[:min(len(events), maxEventRows)] {
		kind := "异常重启（内核断电）"
		detail := "系统在未正常关机的情况下重新启动，常见于断电、长按电源或死机硬重启。"
		if event.EventID == 6008 {
			kind = "意外关机"
			detail = firstLine(event.Message)
		}
		rows = append(rows, []string{localTime(event.Time), kind, truncate(detail, detailLimit)})
	}
	blocks := []tool.Block{{
		Kind:    tool.BlockTable,
		Title:   "异常事件明细",
		Columns: []string{"时间", "类型", "详情"},
		Rows:    rows,
	}}
	if len(events) > maxEventRows {
		blocks = append(blocks, tool.Block{
			Kind: tool.BlockNotice,
			Tone: tool.ToneNeutral,
			Text: fmt.Sprintf("仅显示最近 %d 条（共 %d 条），更早的记录可在事件查看器中查看。", maxEventRows, len(events)),
		})
	}
	return tool.Section{ID: "unexpected", Title: "异常关机与断电", State: tool.SectionReady, Blocks: blocks}
}

func dumpsSection(value snapshot) tool.Section {
	blocks := make([]tool.Block, 0, 2)
	rows := make([][]string, 0, len(value.Minidumps))
	dumps := value.Minidumps
	if len(dumps) > maxDumpRows {
		dumps = dumps[:maxDumpRows]
	}
	for _, dump := range dumps {
		rows = append(rows, []string{dump.Name, localTime(dump.Time), humanBytes(dump.SizeBytes)})
	}
	if len(rows) > 0 {
		blocks = append(blocks, tool.Block{
			Kind:    tool.BlockTable,
			Title:   "Minidump 文件",
			Columns: []string{"文件名", "生成时间", "大小"},
			Rows:    rows,
		})
	}
	if value.KernelDumpExists {
		blocks = append(blocks, tool.Block{
			Kind: tool.BlockNotice,
			Tone: tool.ToneNeutral,
			Text: fmt.Sprintf("存在内核转储 MEMORY.DMP（%s），包含完整内存信息，可交给技术人员用 WinDbg 分析。", humanBytes(value.KernelDumpSize)),
		})
	}
	if len(blocks) == 0 {
		blocks = append(blocks, tool.Block{
			Kind: tool.BlockNotice,
			Tone: tool.ToneNeutral,
			Text: "没有找到转储文件（系统未配置保存转储或从未蓝屏）。",
		})
	}
	return tool.Section{ID: "dumps", Title: "转储文件", State: tool.SectionReady, Blocks: blocks}
}

func warnings(value snapshot) []string {
	out := make([]string, 0, 2)
	counts := map[string]int{}
	for _, event := range value.BugChecks {
		if code, _ := stopCodeMeaning(event.Message); code != "" {
			counts[code]++
		}
	}
	for code, count := range counts {
		if count >= 2 {
			out = append(out, fmt.Sprintf("停机码 %s 在 90 天内出现 %d 次，指向同一类问题，建议针对性排查驱动或硬件。", code, count))
		}
	}
	if len(value.BugChecks) == 0 && len(value.Unexpected) >= 3 {
		out = append(out, "多次异常断电且没有蓝屏记录，多为电源、供电或死机硬重启，请检查电源适配器、电池和散热。")
	}
	return out
}

func summary(value snapshot) string {
	total := len(value.BugChecks) + len(value.Unexpected)
	if total == 0 {
		return "最近 90 天无蓝屏或异常关机记录"
	}
	parts := []string{}
	if len(value.BugChecks) > 0 {
		parts = append(parts, fmt.Sprintf("蓝屏 %d 次", len(value.BugChecks)))
	}
	if len(value.Unexpected) > 0 {
		parts = append(parts, fmt.Sprintf("异常关机/断电 %d 次", len(value.Unexpected)))
	}
	return fmt.Sprintf("最近 90 天：%s", strings.Join(parts, "，"))
}

func firstLine(text string) string {
	if index := strings.IndexAny(text, "\r\n"); index >= 0 {
		return strings.TrimSpace(text[:index])
	}
	return text
}

func truncate(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}

func localTime(iso string) string {
	if parsed, err := time.Parse(time.RFC3339, iso); err == nil {
		return parsed.Local().Format("2006-01-02 15:04:05")
	}
	return iso
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
