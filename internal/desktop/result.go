package desktop

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/cn-maul/OpenTools/internal/application"
	"github.com/cn-maul/OpenTools/sdk/tool"
)

func (s *Shell) result(c *ui.Context, def tool.Definition, job application.Job) {
	t := s.theme
	ui.Column(c).FillWidth().Gap(10).Children(func() {
		s.resultStatus(c, job)
		if job.Status.Active() {
			value := -1.0
			if job.Progress.Total > 0 {
				value = float64(job.Progress.Done) / float64(job.Progress.Total)
			}
			ui.Progress(c, value).FillWidth().Label("任务进度")
		}
		if job.Result != nil && job.Result.Summary != "" {
			ui.Text(c, job.Result.Summary).FontSize(12).TextColor(t.TextMuted).Wrap()
		}

		if len(job.Sections) > 0 {
			s.sectionsResult(c, def, job)
		} else if job.Result != nil {
			s.blocksResult(c, job, job.Result.Blocks)
		} else if job.Status.Active() {
			ui.Column(c).Padding(18).Center().Gap(8).Background(t.Surface).Radius(10).
				Border(1, t.Border).Children(func() {
				ui.Spinner(c)
				ui.Text(c, "正在准备结果…").FontSize(12.5).Bold()
			})
		}

		if job.Result != nil {
			s.resultExtras(c, job)
		}
		if job.Error != "" {
			ui.Row(c).Padding(10).Gap(8).Radius(8).AlignItems(ui.Start).
				Background(t.Danger.Alpha(0.1)).Border(1, t.Danger.Alpha(0.3)).Children(func() {
				ui.Text(c, "执行失败").FontSize(12).Bold().TextColor(t.Danger)
				ui.Text(c, job.Error).FontSize(11.5).TextColor(t.Danger).Grow(1).Wrap()
			})
		}
		if len(job.Logs) > 0 {
			s.logs(c, job)
		}
	})
}

func (s *Shell) resultStatus(c *ui.Context, job application.Job) {
	t := s.theme
	ui.Row(c).Children(func() {
		ui.Text(c, statusLabel(job.Status)).FontSize(11).Bold().TextColor(jobTone(t, job.Status))
		ui.Text(c, job.ID).FontSize(10.5).TextColor(t.TextMuted).Margin(0, 0, 0, 10).SingleLine()
		ui.Spacer(c)
		if elapsed := jobElapsed(job); elapsed != "" {
			ui.Text(c, elapsed).FontSize(10.5).TextColor(t.TextMuted)
		}
	})
}

func statusLabel(status application.Status) string {
	switch status {
	case application.StatusQueued:
		return "排队中"
	case application.StatusRunning:
		return "运行中"
	case application.StatusCanceling:
		return "正在停止"
	case application.StatusSucceeded:
		return "已完成"
	case application.StatusFailed:
		return "失败"
	case application.StatusCanceled:
		return "已取消"
	default:
		return string(status)
	}
}

func sectionStateLabel(state tool.SectionState) string {
	switch state {
	case tool.SectionPending:
		return "等待检测"
	case tool.SectionRunning:
		return "检测中"
	case tool.SectionReady:
		return "已完成"
	case tool.SectionSkipped:
		return "已跳过"
	default:
		return string(state)
	}
}

func jobElapsed(job application.Job) string {
	if job.StartedAt == nil {
		return ""
	}
	end := time.Now()
	if job.FinishedAt != nil {
		end = *job.FinishedAt
	}
	elapsed := end.Sub(*job.StartedAt)
	if elapsed < time.Second {
		return fmt.Sprintf("%d ms", max(0, elapsed.Milliseconds()))
	}
	return fmt.Sprintf("%.1f s", elapsed.Seconds())
}

func (s *Shell) sectionsResult(c *ui.Context, def tool.Definition, job application.Job) {
	t := s.theme
	tabbed := def.Layout == "tabs" && len(job.Sections) > 1
	shown := job.Sections
	if tabbed {
		if s.sectionTab < 0 || s.sectionTab >= len(job.Sections) {
			s.sectionTab = 0
		}
		labels := make([]string, 0, len(job.Sections))
		for _, section := range job.Sections {
			labels = append(labels, section.Title)
		}
		if ui.Segmented(c, &s.sectionTab, labels...).FillWidth().Label("结果分类").Changed() {
			// Segmented writes the choice into sectionTab.
		}
		shown = job.Sections[s.sectionTab : s.sectionTab+1]
	}
	for index, section := range shown {
		number := index + 1
		if tabbed {
			number = s.sectionTab + 1
		}
		ui.Column(c).FillWidth().Gap(8).Padding(12).Radius(10).
			Background(t.Surface).Border(1, t.Border).Children(func() {
			ui.Row(c).Children(func() {
				ui.Text(c, fmt.Sprintf("%02d", number)).FontSize(10).TextColor(t.TextMuted)
				ui.Text(c, section.Title).FontSize(12.5).Bold().Margin(0, 0, 0, 8)
				ui.Spacer(c)
				ui.Text(c, sectionStateLabel(section.State)).FontSize(10.5).
					TextColor(sectionTone(t, section.State))
			})
			if section.State == tool.SectionPending {
				ui.Text(c, "等待前一步完成。").FontSize(11).TextColor(t.TextMuted)
				return
			}
			s.blocksResult(c, job, section.Blocks)
		})
	}
}

func sectionTone(t *ui.Theme, state tool.SectionState) ui.Color {
	switch state {
	case tool.SectionRunning:
		return t.Accent
	case tool.SectionReady:
		return t.Success
	case tool.SectionSkipped:
		return t.TextMuted
	default:
		return t.TextMuted
	}
}

func (s *Shell) blocksResult(c *ui.Context, job application.Job, blocks []tool.Block) {
	for index, block := range blocks {
		if block.Title != "" {
			ui.Text(c, block.Title).FontSize(11).Bold().TextColor(s.theme.TextMuted).Margin(4, 0, 0, 0)
		}
		s.block(c, job, block, index)
	}
}

func (s *Shell) block(c *ui.Context, job application.Job, block tool.Block, index int) {
	t := s.theme
	switch block.Kind {
	case tool.BlockNotice:
		colour := toneColor(t, block.Tone)
		ui.Row(c).Padding(8, 10).Gap(8).Radius(7).AlignItems(ui.Start).
			Background(colour.Alpha(0.1)).Border(1, colour.Alpha(0.25)).Children(func() {
			ui.Text(c, "●").FontSize(9).TextColor(colour)
			ui.Text(c, block.Text).FontSize(11.5).TextColor(colour).Grow(1).Wrap()
		})
	case tool.BlockText:
		ui.Text(c, block.Text).FontSize(11.5).Wrap().Selectable()
	case tool.BlockCode, tool.BlockJSON:
		text := block.Text
		if block.Kind == tool.BlockJSON {
			data, _ := json.MarshalIndent(block.Data, "", "  ")
			text = string(data)
		}
		ui.Column(c).Gap(4).Children(func() {
			ui.Row(c).Children(func() {
				ui.Spacer(c)
				if ui.ButtonBase(c).Padding(2, 6).Radius(4).Label("复制代码").Children(func() {
					ui.Text(c, "复制").FontSize(10).TextColor(t.TextMuted)
				}).Clicked() {
					mygo.Clipboard.WriteText(text)
					s.toast("已复制")
				}
			})
			ui.Text(c, text).Font("monospace").FontSize(11).Selectable().Wrap().
				Padding(10).Radius(7).Background(t.Background).Border(1, t.Border)
		})
	case tool.BlockTable:
		s.tableBlock(c, job, block, index)
	case tool.BlockGrid:
		s.statusGrid(c, block)
	case tool.BlockMeter, tool.BlockFacts:
		s.metricBlock(c, block)
	}
}

func toneColor(t *ui.Theme, tone tool.Tone) ui.Color {
	switch tone {
	case tool.ToneSuccess:
		return t.Success
	case tool.ToneWarning:
		return t.Warning
	case tool.ToneDanger:
		return t.Danger
	case tool.ToneNeutral:
		return t.TextMuted
	default:
		return t.Accent
	}
}

func (s *Shell) statusGrid(c *ui.Context, block tool.Block) {
	t := s.theme
	var data struct {
		Cells   []tool.StatusCell       `json:"cells"`
		Legend  []tool.StatusLegendItem `json:"legend"`
		Labeled bool                    `json:"labeled"`
	}
	raw, _ := json.Marshal(block.Data)
	_ = json.Unmarshal(raw, &data)
	ui.Column(c).Gap(8).Children(func() {
		if len(data.Legend) > 0 {
			ui.Row(c).Gap(12).Wrap().Children(func() {
				for _, item := range data.Legend {
					ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
						colour := toneColor(t, item.Tone)
						if item.Tone == "" {
							colour = t.TextMuted
						}
						ui.Text(c, "●").FontSize(8).TextColor(colour)
						ui.Text(c, item.Label).FontSize(10.5).TextColor(t.TextMuted)
					})
				}
			})
		}
		ui.Row(c).Gap(3).Wrap().Children(func() {
			for _, cell := range data.Cells {
				colour := toneColor(t, cell.Tone)
				if cell.Tone == "" {
					colour = t.TextMuted
				}
				box := ui.Column(c).Size(18, 18).Center().Radius(4).Background(colour.Alpha(0.85))
				if data.Labeled {
					box.Size(72, 34).Padding(3, 5).Gap(0).AlignItems(ui.Start)
					box.Children(func() {
						ui.Text(c, cell.Label).FontSize(9.5).Bold().SingleLine()
						if cell.Detail != "" {
							ui.Text(c, cell.Detail).FontSize(9).TextColor(t.Text).SingleLine()
						}
					})
				} else {
					box.Label(cell.Label)
					box.Tooltip(strings.TrimSpace(cell.Label + " " + cell.Detail))
				}
			}
		})
	})
}

func (s *Shell) metricBlock(c *ui.Context, block tool.Block) {
	t := s.theme
	if block.Kind == tool.BlockFacts {
		var facts []tool.Fact
		raw, _ := json.Marshal(block.Data)
		if err := json.Unmarshal(raw, &facts); err != nil {
			var envelope struct {
				Items []tool.Fact `json:"items"`
			}
			_ = json.Unmarshal(raw, &envelope)
			facts = envelope.Items
		}
		s.facts(c, facts)
		return
	}
	var data struct {
		Items []tool.MeterItem `json:"items"`
		Facts []tool.Fact      `json:"facts"`
	}
	raw, _ := json.Marshal(block.Data)
	if err := json.Unmarshal(raw, &data); err != nil {
		var items []tool.MeterItem
		_ = json.Unmarshal(raw, &items)
		data.Items = items
	}
	ui.Column(c).Gap(10).Children(func() {
		for _, meter := range data.Items {
			maximum := meter.Max
			if maximum <= 0 {
				maximum = 100
			}
			ui.Column(c).Gap(4).Children(func() {
				ui.Row(c).Children(func() {
					ui.Text(c, meter.Label).FontSize(11.5).Bold().Grow(1)
					ui.Text(c, meterValue(meter)).FontSize(11).TextColor(t.TextMuted)
				})
				levels := &ui.MeterLevels{NormalColor: toneColor(t, meter.Tone)}
				if meter.Tone == "" {
					levels.NormalColor = t.Accent
				}
				ui.Meter(c, meter.Value, 0, maximum, levels).FillWidth().Label(meter.Label)
				if meter.Detail != "" {
					ui.Text(c, meter.Detail).FontSize(10.5).TextColor(t.TextMuted)
				}
				if len(meter.Segments) > 0 {
					ui.Row(c).Gap(3).Wrap().Children(func() {
						for _, segment := range meter.Segments {
							ui.Row(c).Gap(3).AlignItems(ui.Center).Children(func() {
								ui.Text(c, "■").FontSize(8).TextColor(toneColor(t, segment.Tone))
								ui.Text(c, fmt.Sprintf("%s %s", segment.Label, meterNumber(segment.Value, ""))).
									FontSize(10).TextColor(t.TextMuted)
							})
						}
					})
				}
			})
		}
		s.facts(c, data.Facts)
	})
}

func (s *Shell) facts(c *ui.Context, facts []tool.Fact) {
	t := s.theme
	if len(facts) == 0 {
		return
	}
	group := ""
	for _, fact := range facts {
		if fact.Group != "" && fact.Group != group {
			group = fact.Group
			ui.Text(c, group).FontSize(10.5).Bold().TextColor(t.TextMuted).Margin(6, 0, 0, 0)
		}
		ui.Row(c).Gap(10).Children(func() {
			ui.Text(c, fact.Label).FontSize(11).TextColor(t.TextMuted).Width(150).SingleLine()
			ui.Text(c, fact.Value).FontSize(11.5).Grow(1).Wrap().Selectable()
		})
	}
}

func meterValue(item tool.MeterItem) string {
	unit := item.Unit
	if unit == "" && item.Max == 100 {
		unit = "%"
	}
	return meterNumber(item.Value, unit)
}

func meterNumber(value float64, unit string) string {
	if value == float64(int64(value)) {
		return fmt.Sprintf("%d%s", int64(value), unit)
	}
	return fmt.Sprintf("%.1f%s", value, unit)
}

func (s *Shell) tableBlock(c *ui.Context, job application.Job, block tool.Block, index int) {
	t := s.theme
	key := fmt.Sprintf("table:%s:%d", job.ID, index)
	list := s.listState(key)
	list.Key = func(row int) any { return row }
	list.Label = func(row int) string {
		if row >= 0 && row < len(block.Rows) && len(block.Rows[row]) > 0 {
			return block.Rows[row][0]
		}
		return ""
	}
	selection := (*ui.Selection[int])(nil)
	if block.BatchAction != nil {
		selection = s.batchSelection(key)
		list.Selection = selection
	}

	columns := make([]ui.TableColumn, 0, len(block.Columns)+2)
	if block.BatchAction != nil {
		columns = append(columns, ui.TableColumn{Title: "", Width: 30, Fixed: true})
	}
	for i, title := range block.Columns {
		width := float32(0)
		if i < len(block.ColumnWidths) {
			width = cssWidth(block.ColumnWidths[i])
		}
		columns = append(columns, ui.TableColumn{
			Title: title, Width: width, MinWidth: 72, RowHeader: i == 0,
		})
	}
	hasActions := len(block.RowActions) > 0 && block.BatchAction == nil
	if hasActions {
		columns = append(columns, ui.TableColumn{Title: "操作", Width: 160, MinWidth: 100})
	}

	height := float32(len(block.Rows))*30 + 42
	height = max(96, min(height, 360))
	table := ui.Table(c, list, columns, len(block.Rows), func(row, col int) {
		if row < 0 || row >= len(block.Rows) {
			return
		}
		offset := 0
		if block.BatchAction != nil {
			if col == 0 {
				checked := selection.Has(row)
				if ui.Checkbox(c, &checked, "").Label("选择此行").Changed() {
					if checked {
						selection.Add(row)
					} else {
						selection.Remove(row)
					}
				}
				return
			}
			offset = 1
		}
		dataCol := col - offset
		if dataCol >= 0 && dataCol < len(block.Columns) {
			if dataCol == 0 && len(block.RowIcons) > row && block.RowIcons[row] != "" {
				s.rowIcon(c, block.RowIcons[row], block.Rows[row][0], func() {
					text := ""
					if dataCol < len(block.Rows[row]) {
						text = block.Rows[row][dataCol]
					}
					ui.Text(c, text).FontSize(11).SingleLine().Tooltip(text)
				})
				return
			}
			text := ""
			if dataCol < len(block.Rows[row]) {
				text = block.Rows[row][dataCol]
			}
			ui.Text(c, text).FontSize(11).SingleLine().Tooltip(text)
			return
		}
		if hasActions && dataCol == len(block.Columns) {
			if row < len(block.RowActions) {
				s.rowActions(c, job, key, row, block.RowActions[row])
			}
		}
	})
	table.FillWidth().Height(height).Border(1, t.Border).Radius(8)

	if block.BatchAction != nil {
		count := selection.Len()
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, fmt.Sprintf("已选择 %d / %d 项", count, len(block.Rows))).
				FontSize(10.5).TextColor(t.TextMuted).Grow(1)
			if ui.PrimaryButton(c, block.BatchAction.Label).Disabled(count == 0).Clicked() {
				s.runBatch(job, block, selection)
			}
		})
	}
}

func (s *Shell) batchSelection(key string) *ui.Selection[int] {
	selection, ok := s.batchState[key]
	if !ok {
		selection = &ui.Selection[int]{}
		s.batchState[key] = selection
	}
	return selection
}

// rowIcon renders the optional per-row icon next to the first column. Icons
// arrive as data: URIs and are decoded once per job, since a table is rebuilt
// on every frame and decoding a PNG per row per frame is wasteful.
func (s *Shell) rowIcon(c *ui.Context, source, label string, text func()) {
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		if image, ok := s.icon(source); ok {
			ui.Image(c, image).Size(16, 16)
		} else if runes := []rune(strings.TrimSpace(label)); len(runes) > 0 {
			ui.Text(c, string(runes[0])).FontSize(10).Bold().TextColor(s.theme.Accent).Size(16, 16).Center()
		}
		text()
	})
}

func (s *Shell) icon(source string) (ui.ImageSource, bool) {
	if image, ok := s.icons[source]; ok {
		return image, image != nil
	}
	data, ok := dataURIBytes(source)
	if !ok {
		s.icons[source] = nil
		return nil, false
	}
	image, err := ui.DecodeImage(data)
	if err != nil {
		s.icons[source] = nil
		return nil, false
	}
	s.icons[source] = image
	return image, true
}

func dataURIBytes(source string) ([]byte, bool) {
	source = strings.TrimSpace(source)
	if !strings.HasPrefix(source, "data:") {
		return nil, false
	}
	comma := strings.IndexByte(source, ',')
	if comma < 0 {
		return nil, false
	}
	meta, payload := source[len("data:"):comma], source[comma+1:]
	if strings.HasSuffix(meta, ";base64") {
		decoded, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return nil, false
		}
		return decoded, true
	}
	unescaped, err := url.PathUnescape(payload)
	if err != nil {
		return nil, false
	}
	return []byte(unescaped), true
}

func (s *Shell) listState(key string) *ui.ListState {
	state, ok := s.listStates[key]
	if !ok {
		state = &ui.ListState{}
		s.listStates[key] = state
	}
	return state
}

func (s *Shell) rowActions(c *ui.Context, job application.Job, key string, row int, actions []tool.RowAction) {
	ui.Row(c).Gap(4).Children(func() {
		for actionIndex, action := range actions {
			actionKey := fmt.Sprintf("%s:%d:%d", key, row, actionIndex)
			switch {
			case len(action.Options) > 0:
				current, ok := s.rowChoices[actionKey]
				if !ok {
					current = action.Value
				}
				labels := make([]string, 0, len(action.Options))
				for _, option := range action.Options {
					labels = append(labels, option.Label)
				}
				index := 0
				for i, option := range action.Options {
					if option.Value == current {
						index = i
					}
				}
				choice := current
				base := ui.SelectBase(c.Key("rowselect:"+actionKey), &choice)
				base.Trigger.Gap(4).Padding(2, 7).Radius(5).Background(s.theme.Surface).
					Border(1, s.theme.Border).Label(action.Label).Children(func() {
					label := ""
					if index < len(action.Options) {
						label = action.Options[index].Label
					}
					ui.Text(c, label).FontSize(10.5).SingleLine()
					ui.Text(c, "⌄").FontSize(9).TextColor(s.theme.TextMuted)
				})
				base.Popup(func(panel ui.Element) {
					panel.Padding(4).Radius(7).Background(s.theme.Background).Border(1, s.theme.Border)
					for _, option := range action.Options {
						item := base.Item(option.Value).Padding(4, 8).Radius(4)
						if item.Highlighted() {
							item.Background(s.theme.Accent).TextColor(s.theme.AccentText)
						}
						item.Children(func() { ui.Text(c, option.Label).FontSize(10.5) })
					}
				})
				if base.Trigger.Changed() {
					s.rowChoices[actionKey] = choice
					for _, option := range action.Options {
						if option.Value == choice && choice != action.Value {
							confirm := option.Confirm
							if confirm == "" {
								confirm = action.Confirm
							}
							s.triggerAction(job, tool.Action{
								Label: action.Label, Input: option.Input, Confirm: confirm,
							})
							break
						}
					}
				}
			default:
				if ui.Button(c, action.Label).Clicked() {
					s.triggerAction(job, tool.Action{
						Label: action.Label, Input: action.Input, Confirm: action.Confirm,
					})
				}
			}
		}
	})
}

func (s *Shell) runBatch(job application.Job, block tool.Block, selection *ui.Selection[int]) {
	if block.BatchAction == nil {
		return
	}
	items := []any{}
	rows := []int{}
	for row := range selection.All() {
		rows = append(rows, row)
	}
	sortInts(rows)
	for _, row := range rows {
		if row < len(block.RowActions) && len(block.RowActions[row]) > 0 {
			key := block.BatchAction.ItemKey
			if key == "" {
				key = "item"
			}
			if value, ok := block.RowActions[row][0].Input[key]; ok {
				items = append(items, value)
			}
		}
	}
	if len(items) == 0 {
		return
	}
	payload, _ := json.Marshal(items)
	input := map[string]any{}
	for key, value := range block.BatchAction.Input {
		input[key] = value
	}
	itemsKey := block.BatchAction.ItemsKey
	if itemsKey == "" {
		itemsKey = "items"
	}
	input[itemsKey] = string(payload)
	confirm := strings.ReplaceAll(block.BatchAction.Confirm, "{count}", strconv.Itoa(len(items)))
	s.triggerInput(job, tool.Action{
		Label: block.BatchAction.Label,
		Input: input, Confirm: confirm,
	})
}

func sortInts(values []int) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

func (s *Shell) triggerAction(job application.Job, action tool.Action) {
	s.triggerInput(job, action)
}

func (s *Shell) triggerInput(job application.Job, action tool.Action) {
	if action.Confirm != "" {
		open := true
		s.confirmOpen = &open
		s.confirmText = action.Confirm
		s.confirmRun = func() { s.runFollowUp(job, action) }
		return
	}
	s.runFollowUp(job, action)
}

func (s *Shell) runFollowUp(job application.Job, action tool.Action) {
	def, ok := s.toolByID(job.ToolID)
	if !ok {
		return
	}
	// A follow-up belongs to the job whose result shows the button. Start
	// from that job's stored input, skipping redacted secrets, and never
	// fold in the current form: it may belong to another tool by now.
	input := schemaDefaults(def.Input)
	overlayInput(input, job.Input)
	for key, value := range action.Input {
		input[key] = value
	}
	s.tab = 0
	s.selected = def.ID
	s.jobID = job.ID
	s.values = schemaDefaults(def.Input)
	overlayInput(s.values, input)
	s.run(input)
}

func (s *Shell) resultExtras(c *ui.Context, job application.Job) {
	t := s.theme
	result := job.Result
	if len(result.Artifacts) > 0 {
		ui.Column(c).Gap(4).Children(func() {
			ui.Text(c, "产出文件").FontSize(10.5).Bold().TextColor(t.TextMuted)
			for _, artifact := range result.Artifacts {
				name := artifact.Name
				ui.Row(c).Gap(8).Padding(6, 8).Radius(7).Background(t.Surface).Children(func() {
					ui.Text(c, name).FontSize(11).Grow(1).SingleLine()
					ui.Text(c, artifactKind(artifact.MIME)).FontSize(10).TextColor(t.TextMuted)
					if ui.Button(c, "打开").Clicked() {
						if path, err := s.app.Artifact(context.Background(), job.ID, name); err == nil {
							mygo.Shell.ShowItemInFolder(path)
						}
					}
					if ui.Button(c, "复制路径").Clicked() {
						if path, err := s.app.Artifact(context.Background(), job.ID, name); err == nil {
							mygo.Clipboard.WriteText(path)
							s.toast("已复制路径")
						}
					}
				})
			}
		})
	}
	if len(result.Actions) > 0 {
		ui.Column(c).Gap(6).Children(func() {
			ui.Text(c, "下一步").FontSize(10.5).Bold().TextColor(t.TextMuted)
			ui.Row(c).Gap(6).Wrap().Children(func() {
				for _, action := range result.Actions {
					if ui.Button(c, action.Label).Clicked() {
						s.triggerAction(job, action)
					}
				}
			})
		})
	}
	ui.Row(c).Gap(6).Children(func() {
		if ui.Button(c, "复制全部结果").Clicked() {
			mygo.Clipboard.WriteText(resultText(result, job.Sections))
			s.toast("已复制全部结果")
		}
		if ui.Button(c, "导出结果").Clicked() {
			s.exportResult(job)
		}
	})
}

func (s *Shell) exportResult(job application.Job) {
	if job.Result == nil {
		return
	}
	path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
		Parent: s.win, Title: "导出结果", ButtonLabel: "保存",
		DefaultPath: job.ToolID + "-" + job.ID + ".txt",
		Filters:     []mygo.FileFilter{{Name: "文本文件", Extensions: []string{"txt"}}},
	})
	if err != nil || path == "" {
		return
	}
	if err := os.WriteFile(path, []byte(resultText(job.Result, job.Sections)), 0o600); err != nil {
		s.toast(err.Error())
		return
	}
	s.toast("已导出到 " + filepath.Base(path))
}

func artifactKind(mime string) string {
	switch {
	case strings.Contains(mime, "wordprocessingml"):
		return "Word"
	case strings.Contains(mime, "spreadsheetml"):
		return "Excel"
	case strings.Contains(mime, "presentationml"):
		return "演示"
	case mime == "text/csv":
		return "CSV"
	case strings.HasPrefix(mime, "image/"):
		return "图片"
	case mime == "application/pdf":
		return "PDF"
	default:
		return "文件"
	}
}

// resultText renders a job as plain text for the clipboard and file export.
// Sections come first because a sectioned tool keeps its blocks there, not in
// Result.Blocks.
func resultText(result *tool.Result, sections []tool.Section) string {
	parts := []string{result.Summary}
	for _, section := range sections {
		parts = append(parts, "", "## "+section.Title)
		parts = appendBlockText(parts, section.Blocks)
	}
	parts = appendBlockText(parts, result.Blocks)
	return strings.Join(parts, "\n")
}

func appendBlockText(parts []string, blocks []tool.Block) []string {
	for _, block := range blocks {
		if block.Title != "" {
			parts = append(parts, "", block.Title)
		}
		switch block.Kind {
		case tool.BlockJSON:
			data, _ := json.MarshalIndent(block.Data, "", "  ")
			parts = append(parts, string(data))
		case tool.BlockTable:
			if len(block.Columns) > 0 {
				parts = append(parts, strings.Join(block.Columns, "\t"))
			}
			for _, row := range block.Rows {
				parts = append(parts, strings.Join(row, "\t"))
			}
		case tool.BlockFacts:
			parts = append(parts, factsText(block.Data)...)
		case tool.BlockMeter:
			parts = append(parts, meterText(block.Data)...)
		case tool.BlockGrid:
			parts = append(parts, gridText(block.Data)...)
		}
		if block.Text != "" {
			parts = append(parts, block.Text)
		}
	}
	return parts
}

func factsText(data any) []string {
	raw, _ := json.Marshal(data)
	var facts []tool.Fact
	if err := json.Unmarshal(raw, &facts); err != nil {
		var envelope struct {
			Items []tool.Fact `json:"items"`
		}
		if json.Unmarshal(raw, &envelope) != nil {
			return nil
		}
		facts = envelope.Items
	}
	lines := make([]string, 0, len(facts))
	for _, fact := range facts {
		lines = append(lines, fact.Label+": "+fact.Value)
	}
	return lines
}

func meterText(data any) []string {
	raw, _ := json.Marshal(data)
	var envelope struct {
		Items []tool.MeterItem `json:"items"`
		Facts []tool.Fact      `json:"facts"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		var items []tool.MeterItem
		if json.Unmarshal(raw, &items) != nil {
			return nil
		}
		envelope.Items = items
	}
	lines := make([]string, 0, len(envelope.Items))
	for _, item := range envelope.Items {
		lines = append(lines, item.Label+": "+meterValue(item))
	}
	return append(lines, factsText(envelope.Facts)...)
}

func gridText(data any) []string {
	raw, _ := json.Marshal(data)
	var grid struct {
		Cells []tool.StatusCell `json:"cells"`
	}
	if json.Unmarshal(raw, &grid) != nil {
		return nil
	}
	lines := make([]string, 0, len(grid.Cells))
	for _, cell := range grid.Cells {
		line := cell.Label
		if cell.Detail != "" {
			line += " " + cell.Detail
		}
		lines = append(lines, line)
	}
	return lines
}

func (s *Shell) logs(c *ui.Context, job application.Job) {
	t := s.theme
	ui.Column(c).Gap(4).Children(func() {
		if ui.ButtonBase(c).FillWidth().Padding(6, 4).Radius(5).Label("活动记录").Children(func() {
			ui.Text(c, fmt.Sprintf("活动记录  %d 行", len(job.Logs))).FontSize(10.5).
				TextColor(t.TextMuted).Grow(1)
			ui.Text(c, map[bool]string{true: "⌃", false: "⌄"}[s.logsOpen]).FontSize(10).
				TextColor(t.TextMuted)
		}).Clicked() {
			s.logsOpen = !s.logsOpen
		}
		if s.logsOpen {
			lines := make([]string, 0, len(job.Logs))
			for _, entry := range job.Logs {
				lines = append(lines, entry.At.Local().Format("15:04:05")+"  "+entry.Message)
			}
			ui.Scroll(c).FillWidth().Height(180).Padding(8).Radius(7).
				Background(t.Background).Border(1, t.Border).Children(func() {
				ui.Text(c, strings.Join(lines, "\n")).Font("monospace").FontSize(10.5).
					TextColor(t.TextMuted).Selectable()
			})
		}
	})
}

// syncSections resets the selected tab when the workbench switches to
// another job, so the tab index never points past a shorter section list.
func (s *Shell) syncSections() {
	job, ok := s.selectedJob()
	if !ok {
		return
	}
	if job.ID != s.sectionJob {
		s.sectionJob = job.ID
		s.sectionTab = 0
	}
	if s.sectionTab >= len(job.Sections) {
		s.sectionTab = 0
	}
}

func cssWidth(value string) float32 {
	value = strings.TrimSpace(value)
	value = strings.TrimSpace(strings.TrimSuffix(strings.ToLower(value), "px"))
	if value == "" {
		return 0
	}
	if number, err := strconv.ParseFloat(value, 32); err == nil && number > 0 {
		return float32(number)
	}
	return 0
}
