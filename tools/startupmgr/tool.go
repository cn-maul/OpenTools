// Package startupmgr reports and toggles Windows auto-start entries from the
// registry, startup folders, scheduled tasks and services.
package startupmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"opentools/pkg/tool"
)

const version = "1.0.1"

// maxRows caps each section table so the result pane stays on one screen.
const maxRows = 30

const commandLimit = 46

// Tool reports startup entries and can enable or disable them.
type Tool struct{}

// New returns the startup manager tool.
func New() Tool { return Tool{} }

// Definition implements tool.Tool.
func (Tool) Definition() tool.Definition {
	return tool.Definition{
		ID:          "system.startup-manager",
		Name:        "启动项管理",
		Summary:     "查看注册表、启动文件夹、计划任务和服务里的启动项，并调整启用状态或服务启动类型。",
		Description: "汇总当前用户与全机的 Run 注册表项、启动文件夹、登录/开机触发的计划任务和系统外服务，显示命令与服务运行状态；注册表项通过 StartupApproved 启用/禁用，计划任务直接切换状态，服务可设为自动、自动（延迟）、手动或禁用。修改操作需要管理员权限并会弹出 UAC 确认。",
		Category:    "系统工具",
		Icon:        "startup",
		Keywords:    []string{"启动项", "开机", "自启", "自启动", "计划任务", "服务", "禁用", "加速"},
		Version:     version,
		Effects: []tool.Effect{
			tool.EffectReadFiles,
			tool.EffectSystemSettings,
		},
		Input: tool.Object(map[string]*tool.Schema{
			"action": tool.Select("操作", "scan 列出启动项；toggle 修改指定启动项。", "scan", "toggle").
				WithEnumLabels(map[string]string{
					"scan":   "列出启动项",
					"toggle": "启用/禁用",
				}).
				WithWidget("hidden", "").
				WithDefault("scan"),
			"item": tool.String("启动项", "要修改的启动项描述，由表格状态操作提供。").
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
	if strings.EqualFold(strings.TrimSpace(p.Action), "toggle") {
		return runToggleAction(ctx, p.Item, report)
	}
	return runScan(ctx, report)
}

func runToggleAction(ctx context.Context, raw string, report tool.Reporter) (tool.Result, error) {
	var job toggleJob
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &job); err != nil {
		return tool.Result{}, fmt.Errorf("解析启动项参数失败：%w", err)
	}
	message, err := runToggle(ctx, job, report)
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{
		Summary: message,
		Blocks: []tool.Block{{
			Kind: tool.BlockNotice,
			Tone: tool.ToneSuccess,
			Text: message,
		}},
		Actions: []tool.Action{{
			Label: "重新扫描启动项",
			Input: map[string]any{"action": "scan"},
		}},
	}, nil
}

func runScan(ctx context.Context, report tool.Reporter) (tool.Result, error) {
	for _, section := range []tool.Section{
		{ID: "overview", Title: "概览", State: tool.SectionPending},
		{ID: "registry", Title: "注册表与启动文件夹", State: tool.SectionPending},
		{ID: "tasks", Title: "计划任务", State: tool.SectionPending},
		{ID: "services", Title: "服务", State: tool.SectionPending},
	} {
		report.Section(section)
	}
	report.Section(tool.Section{ID: "overview", Title: "概览", State: tool.SectionRunning})
	report.Log("info", "正在扫描开机启动项")

	value, err := collect(ctx)
	if err != nil {
		return tool.Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}
	sortItems(value.Items)

	overview := overviewSection(value)
	registry := registrySection(value.Items)
	tasks := tasksSection(value.Items)
	services := servicesSection(value.Items)
	report.Section(overview)
	report.Progress(1, 4)
	report.Section(registry)
	report.Progress(2, 4)
	report.Section(tasks)
	report.Progress(3, 4)
	report.Section(services)
	report.Progress(4, 4)
	report.Log("info", "启动项扫描完成")

	blocks := overview.Blocks
	blocks = append(blocks, registry.Blocks...)
	blocks = append(blocks, tasks.Blocks...)
	blocks = append(blocks, services.Blocks...)
	return tool.Result{
		Summary:  summary(value.Items),
		Blocks:   blocks,
		Warnings: scanWarnings(value.Items),
	}, nil
}

var kindOrder = map[string]int{"run": 0, "folder": 1, "task": 2, "service": 3}

func sortItems(items []startupItem) {
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i], items[j]
		if kindOrder[left.Kind] != kindOrder[right.Kind] {
			return kindOrder[left.Kind] < kindOrder[right.Kind]
		}
		return strings.ToLower(left.Name) < strings.ToLower(right.Name)
	})
}

func overviewSection(value snapshot) tool.Section {
	enabled, disabled := 0, 0
	for _, item := range value.Items {
		if item.Enabled {
			enabled++
		} else {
			disabled++
		}
	}
	permission := "否（修改系统级启动项时会弹出 UAC 确认）"
	if value.IsAdmin {
		permission = "是"
	}
	blocks := []tool.Block{{Kind: tool.BlockFacts, Data: []tool.Fact{
		{Label: "启动项总数", Value: fmt.Sprintf("%d 项（启用 %d，禁用 %d）", len(value.Items), enabled, disabled)},
		{Label: "管理员权限", Value: permission},
	}}}
	return tool.Section{ID: "overview", Title: "概览", State: tool.SectionReady, Blocks: blocks}
}

func registrySection(items []startupItem) tool.Section {
	return listSection(
		"registry",
		"注册表与启动文件夹",
		"没有发现注册表或启动文件夹自启项。",
		[]string{"名称", "来源", "命令"},
		[]string{"24%", "18%", "44%"},
		items,
		"run",
		"folder",
	)
}

func tasksSection(items []startupItem) tool.Section {
	return listSection(
		"tasks",
		"计划任务",
		"没有发现登录或开机触发的第三方计划任务。",
		[]string{"名称", "来源", "命令"},
		[]string{"24%", "18%", "44%"},
		items,
		"task",
	)
}

func servicesSection(items []startupItem) tool.Section {
	section := listSection(
		"services",
		"服务",
		"没有发现系统外服务。",
		[]string{"名称", "运行状态", "命令"},
		[]string{"24%", "14%", "46%"},
		items,
		"service",
	)
	if len(section.Blocks) > 0 && section.Blocks[0].Kind == tool.BlockTable {
		section.Blocks = append([]tool.Block{{
			Kind: tool.BlockNotice,
			Tone: tool.ToneNeutral,
			Text: "“状态”下拉框修改的是服务启动类型，不会立即启动或停止当前服务。",
		}}, section.Blocks...)
	}
	return section
}

func listSection(
	id, title, emptyText string,
	columns, widths []string,
	items []startupItem,
	kinds ...string,
) tool.Section {
	blocks := make([]tool.Block, 0, 1)
	rows := make([][]string, 0, len(items))
	rowActions := make([][]tool.RowAction, 0, len(items))
	truncated := 0
	for _, item := range items {
		if !contains(kinds, item.Kind) {
			continue
		}
		if len(rows) >= maxRows {
			truncated++
			continue
		}
		command := item.Command
		if runes := []rune(command); len(runes) > commandLimit {
			command = string(runes[:commandLimit]) + "…"
		}
		action, ok := rowAction(item)
		if !ok {
			continue
		}
		if item.Kind == "service" {
			rows = append(rows, []string{
				item.Location,
				runStateText(item.RunState),
				command,
			})
		} else {
			rows = append(rows, []string{item.Name, item.Note, command})
		}
		rowActions = append(rowActions, []tool.RowAction{action})
	}
	if len(rows) == 0 {
		blocks = append(blocks, tool.Block{Kind: tool.BlockNotice, Tone: tool.ToneSuccess, Text: emptyText})
	} else {
		blocks = append(blocks, tool.Block{
			Kind:         tool.BlockTable,
			Title:        title,
			Columns:      columns,
			ColumnWidths: widths,
			Rows:         rows,
			RowActions:   rowActions,
		})
		if truncated > 0 {
			blocks = append(blocks, tool.Block{
				Kind: tool.BlockNotice,
				Tone: tool.ToneNeutral,
				Text: fmt.Sprintf("仅显示前 %d 项（另有 %d 项未显示）。", maxRows, truncated),
			})
		}
	}
	return tool.Section{ID: id, Title: title, State: tool.SectionReady, Blocks: blocks}
}

func rowAction(item startupItem) (tool.RowAction, bool) {
	if item.Kind == "service" {
		options := make([]tool.RowActionOption, 0, 4)
		for _, option := range []struct {
			value string
			label string
		}{
			{value: "Automatic", label: "自动"},
			{value: "AutomaticDelayed", label: "自动（延迟）"},
			{value: "Manual", label: "手动"},
			{value: "Disabled", label: "禁用"},
		} {
			job := toggleJob{
				Kind:          "service",
				Name:          item.Name,
				StartType:     option.value,
				StartTypeText: option.label,
			}
			payload, err := json.Marshal(job)
			if err != nil {
				return tool.RowAction{}, false
			}
			options = append(options, tool.RowActionOption{
				Label:   option.label,
				Value:   option.value,
				Confirm: serviceConfirm(item, option.value, option.label),
				Input: map[string]any{
					"action": "toggle",
					"item":   string(payload),
				},
			})
		}
		return tool.RowAction{
			Label:   "修改 " + serviceDisplayName(item) + " 的启动类型",
			Variant: "select",
			Value:   item.StartType,
			Options: options,
		}, true
	}

	job := itemJob(item)
	payload, err := json.Marshal(job)
	if err != nil {
		return tool.RowAction{}, false
	}
	return tool.RowAction{
		Label:   "切换 " + item.Name + " 的启动状态",
		Confirm: actionConfirm(job),
		Variant: "switch",
		On:      switchOn(item),
		Input: map[string]any{
			"action": "toggle",
			"item":   string(payload),
		},
	}, true
}

// itemJob describes the state change the status switch performs for one
// registry or scheduled-task entry.
func itemJob(item startupItem) toggleJob {
	switch item.Kind {
	case "run", "folder":
		return toggleJob{
			Kind:         "approved",
			Name:         item.Name,
			ApprovedPath: item.ApprovedPath,
			Enable:       !item.Enabled,
		}
	case "task":
		return toggleJob{
			Kind:     "task",
			Name:     item.Name,
			TaskPath: item.TaskPath,
			Enable:   !item.Enabled,
		}
	default:
		return toggleJob{}
	}
}

// switchOn reports the visual state of the status switch.
func switchOn(item startupItem) bool {
	return item.Enabled
}

func actionConfirm(job toggleJob) string {
	switch job.Kind {
	case "approved":
		if job.Enable {
			return fmt.Sprintf("将恢复 %s 的开机自启（与任务管理器一致），继续吗？", job.Name)
		}
		return fmt.Sprintf("将禁用 %s 的开机自启（可随时再启用），继续吗？", job.Name)
	case "task":
		if job.Enable {
			return fmt.Sprintf("将启用计划任务 %s，登录或开机时会自动运行，继续吗？", job.Name)
		}
		return fmt.Sprintf("将禁用计划任务 %s，可随时再启用，继续吗？", job.Name)
	case "service":
		return fmt.Sprintf("将服务 %s 的启动类型改为 %s，已停止的服务不会立即启动，继续吗？", job.Name, job.StartTypeText)
	default:
		return "继续吗？"
	}
}

func serviceConfirm(item startupItem, startType, startTypeText string) string {
	return fmt.Sprintf(
		"将服务「%s」的启动类型改为 %s。当前运行状态为 %s，此操作不会立即启动或停止服务。继续吗？",
		serviceDisplayName(item),
		startTypeText,
		runStateText(item.RunState),
	)
}

func scanWarnings(items []startupItem) []string {
	out := make([]string, 0, 2)
	machineServices := 0
	for _, item := range items {
		if item.Kind == "service" && item.Scope == "machine" {
			machineServices++
		}
	}
	if machineServices > 0 {
		out = append(out, "服务的启动类型改动会影响系统功能，不确定用途的服务保持现状更安全。")
	}
	return out
}

func summary(items []startupItem) string {
	enabled := 0
	for _, item := range items {
		if item.Enabled {
			enabled++
		}
	}
	return fmt.Sprintf("启动项 %d 个：启用 %d，禁用 %d", len(items), enabled, len(items)-enabled)
}

func runStateText(state string) string {
	switch state {
	case "Running":
		return "正在运行"
	case "Stopped":
		return "已停止"
	case "StartPending":
		return "正在启动"
	case "StopPending":
		return "正在停止"
	case "ContinuePending":
		return "正在继续"
	case "PausePending":
		return "正在暂停"
	case "Paused":
		return "已暂停"
	default:
		if strings.TrimSpace(state) == "" {
			return "未知"
		}
		return state
	}
}

func serviceDisplayName(item startupItem) string {
	if strings.TrimSpace(item.Location) != "" {
		return item.Location
	}
	return item.Name
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}
