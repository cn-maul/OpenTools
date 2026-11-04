// Package uninstaller lists Windows installed programs from the uninstall
// registry and launches their own uninstallers on request.
package uninstaller

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"opentools/pkg/tool"
)

const version = "1.0.6"

// maxRows caps the software table so the result pane does not turn into an
// endless scroll; the name filter narrows a large installed base.
const maxRows = 30

// Tool reports installed software and can run its uninstallers.
type Tool struct{}

// New returns the uninstaller tool.
func New() Tool { return Tool{} }

// Definition implements tool.Tool.
func (Tool) Definition() tool.Definition {
	return tool.Definition{
		ID:          "system.app-uninstaller",
		Name:        "软件卸载",
		Summary:     "列出已安装软件，调用软件自带卸载程序完成卸载。",
		Description: "从 Windows 卸载注册表读取已安装软件清单（含版本、发布者、安装日期和体积），支持按名称过滤；卸载时调用应用自己的卸载程序（优先静默参数），需要管理员权限的操作会弹出 UAC 确认。卸载完成后自动核对注册表项是否已移除。",
		Category:    "系统工具",
		Icon:        "uninstall",
		Keywords:    []string{"卸载", "软件", "程序", "应用", "清理", "安装"},
		Version:     version,
		Effects: []tool.Effect{
			tool.EffectReadFiles,
			tool.EffectDeleteFiles,
			tool.EffectSystemSettings,
		},
		Input: tool.Object(map[string]*tool.Schema{
			"action": tool.Select("操作", "scan 列出软件；uninstall 卸载指定软件。", "scan", "uninstall").
				WithEnumLabels(map[string]string{
					"scan":      "列出软件",
					"uninstall": "卸载",
				}).
				WithWidget("hidden", "").
				WithDefault("scan"),
			"filter": tool.String("名称过滤", "输入软件名或发布者的关键字，只列出匹配的软件。").
				WithDefault(""),
			"item": tool.String("软件标识", "要卸载的软件对应的注册表信息，由表格行内的卸载按钮提供。").
				WithWidget("hidden", "").
				WithDefault(""),
			"items": tool.String("软件列表", "要批量卸载的软件注册表信息，由表格多选操作提供。").
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
	if strings.EqualFold(strings.TrimSpace(p.Action), "uninstall") {
		if strings.TrimSpace(p.Items) != "" {
			return runBatchUninstallAction(ctx, p, report)
		}
		return runUninstallAction(ctx, p, report)
	}
	return runScan(ctx, p.Filter, report)
}

func runBatchUninstallAction(ctx context.Context, p params, report tool.Reporter) (tool.Result, error) {
	targets, err := parseUninstallTargets(p.Items)
	if err != nil {
		return tool.Result{}, fmt.Errorf("解析待卸载软件列表失败：%w", err)
	}
	if len(targets) == 0 {
		return tool.Result{}, fmt.Errorf("没有选择要卸载的软件")
	}

	report.Section(tool.Section{ID: "batch", Title: "批量卸载", State: tool.SectionRunning})
	report.Log("info", fmt.Sprintf("准备卸载 %d 个软件", len(targets)))

	blocks := make([]tool.Block, 0, len(targets))
	succeeded := make([]string, 0, len(targets))
	failed := 0
	for index, target := range targets {
		if err := ctx.Err(); err != nil {
			return tool.Result{}, err
		}
		name := fallbackName(target.Name)
		if strings.TrimSpace(target.Key) == "" {
			failed++
			blocks = append(blocks, tool.Block{
				Kind: tool.BlockNotice,
				Tone: tool.ToneDanger,
				Text: name + "：缺少注册表标识，已跳过。",
			})
			continue
		}
		report.Log("info", fmt.Sprintf("正在卸载 %s（%d/%d）", name, index+1, len(targets)))
		message, err := runUninstall(ctx, appEntry{Key: target.Key, Name: target.Name}, report)
		if err != nil {
			failed++
			blocks = append(blocks, tool.Block{
				Kind: tool.BlockNotice,
				Tone: tool.ToneDanger,
				Text: name + "：" + err.Error(),
			})
			continue
		}
		succeeded = append(succeeded, name)
		blocks = append(blocks, tool.Block{
			Kind: tool.BlockNotice,
			Tone: tool.ToneSuccess,
			Text: message,
		})
		report.Progress(int64(index+1), int64(len(targets)))
	}

	report.Section(tool.Section{
		ID:     "batch",
		Title:  "批量卸载",
		State:  tool.SectionReady,
		Blocks: blocks,
	})
	report.Progress(int64(len(targets)), int64(len(targets)))
	report.Log("info", fmt.Sprintf("批量卸载完成：成功 %d，失败 %d", len(succeeded), failed))

	summary := fmt.Sprintf("批量卸载完成：成功 %d 个，失败 %d 个", len(succeeded), failed)
	tone := tool.ToneSuccess
	if failed > 0 {
		tone = tool.ToneWarning
	}
	return tool.Result{
		Summary: summary,
		Blocks: []tool.Block{{
			Kind: tool.BlockNotice,
			Tone: tone,
			Text: summary,
		}},
		Actions: []tool.Action{{
			Label: "重新扫描软件列表",
			Input: map[string]any{"action": "scan"},
		}},
	}, nil
}

func parseUninstallTargets(raw string) ([]uninstallTarget, error) {
	var entries []json.RawMessage
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &entries); err != nil {
		return nil, err
	}

	targets := make([]uninstallTarget, 0, len(entries))
	for index, entry := range entries {
		payload := entry
		if len(payload) > 0 && payload[0] == '"' {
			var encoded string
			if err := json.Unmarshal(payload, &encoded); err != nil {
				return nil, fmt.Errorf("第 %d 项格式错误：%w", index+1, err)
			}
			payload = []byte(encoded)
		}

		var target uninstallTarget
		if err := json.Unmarshal(payload, &target); err != nil {
			return nil, fmt.Errorf("第 %d 项格式错误：%w", index+1, err)
		}
		targets = append(targets, target)
	}
	return targets, nil
}

func runUninstallAction(ctx context.Context, p params, report tool.Reporter) (tool.Result, error) {
	var target uninstallTarget
	if err := json.Unmarshal([]byte(strings.TrimSpace(p.Item)), &target); err != nil {
		return tool.Result{}, fmt.Errorf("解析软件标识失败：%w", err)
	}
	if strings.TrimSpace(target.Key) == "" {
		return tool.Result{}, fmt.Errorf("缺少要卸载的软件标识")
	}
	message, err := runUninstall(ctx, appEntry{Key: target.Key, Name: target.Name}, report)
	if err != nil {
		return tool.Result{}, err
	}
	blocks := []tool.Block{{
		Kind: tool.BlockNotice,
		Tone: tool.ToneSuccess,
		Text: message,
	}}
	return tool.Result{
		Summary: fmt.Sprintf("已执行卸载：%s", fallbackName(target.Name)),
		Blocks:  blocks,
		Actions: []tool.Action{{
			Label: "重新扫描软件列表",
			Input: map[string]any{"action": "scan"},
		}},
	}, nil
}

func runScan(ctx context.Context, filter string, report tool.Reporter) (tool.Result, error) {
	report.Section(tool.Section{ID: "apps", Title: "已安装软件", State: tool.SectionRunning})
	report.Log("info", "正在读取已安装软件清单")

	value, err := collect(ctx)
	if err != nil {
		return tool.Result{}, err
	}
	apps := filterApps(value.Apps, filter)
	sort.SliceStable(apps, func(i, j int) bool {
		return strings.ToLower(apps[i].Name) < strings.ToLower(apps[j].Name)
	})
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}

	table := appsTable(apps)
	blocks := []tool.Block{table}
	report.Section(tool.Section{
		ID:     "apps",
		Title:  "已安装软件",
		State:  tool.SectionReady,
		Blocks: blocks,
	})
	report.Progress(1, 1)
	report.Log("info", "软件清单读取完成")

	return tool.Result{
		Summary: summary(apps, filter),
		Blocks:  blocks,
	}, nil
}

func appsTable(apps []appEntry) tool.Block {
	rows := make([][]string, 0, len(apps))
	rowIcons := make([]string, 0, len(apps))
	rowActions := make([][]tool.RowAction, 0, len(apps))
	for _, app := range apps[:min(len(apps), maxRows)] {
		rows = append(rows, []string{
			app.Name,
			app.Version,
			app.Publisher,
			humanBytes(app.SizeBytes),
			formatDate(app.InstallDate),
		})
		rowIcons = append(rowIcons, app.Icon)
		target, err := json.Marshal(uninstallTarget{Key: app.Key, Name: app.Name})
		if err != nil {
			rowActions = append(rowActions, nil)
			continue
		}
		rowActions = append(rowActions, []tool.RowAction{{
			Label:   "卸载",
			Tone:    tool.ToneDanger,
			Confirm: uninstallConfirm(app),
			Input: map[string]any{
				"action": "uninstall",
				"item":   string(target),
			},
		}})
	}
	return tool.Block{
		Kind:         tool.BlockTable,
		Title:        "已安装软件明细",
		Columns:      []string{"名称", "版本", "发布者", "大小", "安装日期"},
		ColumnWidths: []string{"31%", "13%", "20%", "11%", "13%"},
		Rows:         rows,
		RowIcons:     rowIcons,
		RowActions:   rowActions,
		BatchAction: &tool.BatchAction{
			Label:    "卸载选中项",
			Tone:     tool.ToneDanger,
			Confirm:  "将依次调用所选软件自带的卸载程序，共 {count} 个。该操作不可撤销，继续吗？",
			Input:    map[string]any{"action": "uninstall"},
			ItemKey:  "item",
			ItemsKey: "items",
		},
	}
}

func uninstallConfirm(app appEntry) string {
	name := fallbackName(app.Name)
	if app.QuietString != "" || app.WindowsInstaller {
		return fmt.Sprintf("将尝试静默卸载 %s，卸载过程不需要手动操作。该操作不可撤销，继续吗？", name)
	}
	return fmt.Sprintf("将运行 %s 自带的卸载程序。部分卸载需要管理员权限并会弹出 UAC 确认，继续吗？", name)
}

func summary(apps []appEntry, filter string) string {
	if filter != "" {
		return fmt.Sprintf("匹配 %q 的软件共 %d 个", strings.TrimSpace(filter), len(apps))
	}
	return fmt.Sprintf("已安装软件 %d 个", len(apps))
}

func fallbackName(name string) string {
	if strings.TrimSpace(name) == "" {
		return "所选软件"
	}
	return name
}

func formatDate(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) == 8 {
		return fmt.Sprintf("%s-%s-%s", raw[0:4], raw[4:6], raw[6:8])
	}
	return raw
}

func humanBytes(size int64) string {
	if size <= 0 {
		return "—"
	}
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
