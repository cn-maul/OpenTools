package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/cn-maul/OpenTools/internal/application"
	"github.com/cn-maul/OpenTools/internal/tasks"
	"github.com/cn-maul/OpenTools/pkg/tool"
)

const (
	shellWidth  = 1240
	shellHeight = 800
	titleHeight = 48
	railWidth   = 248
)

// Shell is the native main window. Its fields are read and changed on the
// UI thread; background work reports changes through win.Update.
type Shell struct {
	app     *application.Service
	version string
	dataDir string

	win *mygo.Window

	tools     []tool.Definition
	jobs      []tasks.Job
	tab       int
	search    string
	values    map[string]any
	selected  string
	jobID     string
	loading   bool
	errText   string
	fieldErr  *tool.FieldError
	store     tool.StoreSnapshot
	storeLoad bool
	storeBusy string
	starting  bool

	dark  bool
	theme *ui.Theme
	mode  string

	searchHandle ui.Handle
	listStates   map[string]*ui.ListState
	arrayDrafts  map[string]string
	arrayErrors  map[string]string
	icons        map[string]ui.ImageSource
	batchState   map[string]*ui.Selection[int]
	rowToggles   map[string]bool
	rowChoices   map[string]string
	toastText    string
	settings     *settingsWindow
	themeWatch   bool

	confirmOpen *bool
	confirmText string
	confirmRun  func()

	closeConfirmed bool
	closeAsking    bool
	sectionTab     int
	sectionJob     string
	logsOpen       bool
}

// NewShell creates the native shell around an application service.
func NewShell(app *application.Service, version, dataDir string) *Shell {
	mode := loadTheme(dataDir)
	return &Shell{
		app:         app,
		version:     version,
		dataDir:     dataDir,
		values:      map[string]any{},
		mode:        mode,
		dark:        mode == themeDark,
		theme:       shellTheme(mode == themeDark),
		listStates:  map[string]*ui.ListState{},
		arrayDrafts: map[string]string{},
		arrayErrors: map[string]string{},
		icons:       map[string]ui.ImageSource{},
		batchState:  map[string]*ui.Selection[int]{},
		rowToggles:  map[string]bool{},
		rowChoices:  map[string]string{},
	}
}

// Open shows the native main window, focusing an existing one.
func (s *Shell) Open() {
	if s.win != nil && !s.win.IsDestroyed() {
		s.win.Show()
		s.win.Focus()
		return
	}
	s.applyAppearance()
	s.watchSystemTheme()
	s.win = mygo.NewWindow(mygo.WindowOptions{
		Title:             "OpenTools",
		Width:             shellWidth,
		Height:            shellHeight,
		MinWidth:          820,
		MinHeight:         560,
		StateKey:          "main",
		TitleBarStyle:     mygo.TitleBarHidden,
		TitleBarHeight:    titleHeight,
		HideWindowButtons: true,
		BackgroundColor:   "light-dark(#f5f7fa, #17181a)",
		Content:           ui.View(s.view),
	})
	s.win.OnClosed(func() { s.win = nil })
	s.win.OnClose(func(e *mygo.CloseEvent) {
		active := s.activeJobs()
		if active == 0 || s.closeConfirmed {
			return
		}
		e.PreventDefault()
		if s.closeAsking {
			return
		}
		s.closeAsking = true
		go s.confirmClose(e.Window, active)
	})
	s.load()
}

// applyAppearance resolves the saved mode against the desktop and updates
// both the shell's palette and MyGo's own dialogs.
func (s *Shell) applyAppearance() {
	s.dark = s.appearanceDark()
	s.theme = shellTheme(s.dark)
	mygo.Theme.SetSource(themeSourceFor(s.mode))
}

func (s *Shell) appearanceDark() bool {
	switch normalizeThemeMode(s.mode) {
	case themeDark:
		return true
	case themeLight:
		return false
	default:
		return mygo.Theme.IsDark()
	}
}

// watchSystemTheme repaints when the desktop flips appearance while the
// user chose to follow it.
func (s *Shell) watchSystemTheme() {
	if s.themeWatch {
		return
	}
	s.themeWatch = true
	mygo.Theme.OnUpdated(func() {
		if normalizeThemeMode(s.mode) != themeSystem {
			return
		}
		dark := mygo.Theme.IsDark()
		if dark == s.dark {
			return
		}
		s.dark = dark
		s.theme = shellTheme(dark)
		if s.win != nil && !s.win.IsDestroyed() {
			s.win.Invalidate()
		}
		if s.settings != nil && s.settings.win != nil && !s.settings.win.IsDestroyed() {
			s.settings.win.Invalidate()
		}
	})
}

func (s *Shell) confirmClose(win *mygo.Window, active int) {
	result, err := mygo.Dialog.Message(mygo.MessageOptions{
		Parent:        win,
		Type:          mygo.MessageWarning,
		Title:         "任务仍在运行",
		Message:       fmt.Sprintf("还有 %d 个任务正在运行。", active),
		Detail:        "退出会终止这些任务，并清理运行记录和任务目录。",
		Buttons:       []string{"取消", "退出"},
		DefaultButton: 0,
		CancelButton:  0,
	})
	if err != nil || result.Button != 1 {
		if win != nil && !win.IsDestroyed() {
			win.Update(func() { s.closeAsking = false })
		}
		return
	}
	if win == nil || win.IsDestroyed() {
		return
	}
	// Both land in the main loop's FIFO: the flag is set before Close asks
	// the OnClose listener again.
	win.Update(func() {
		s.closeAsking = false
		s.closeConfirmed = true
	})
	win.Close()
}

// loadPopulates reads installed tools and recent jobs off the UI thread.
func (s *Shell) load() {
	s.loading = true
	go func() {
		defs := s.app.Tools()
		jobs, _ := s.app.List(context.Background(), 80)
		s.update(func() {
			s.tools = defs
			s.jobs = jobs
			s.loading = false
			if s.selected == "" && len(defs) > 0 {
				s.selectTool(defs[0].ID)
			}
			if len(defs) == 0 {
				s.tab = 1
				s.loadStore(true)
			}
		})
	}()
	go s.events()
}

func (s *Shell) update(fn func()) {
	if s.win == nil || s.win.IsDestroyed() {
		return
	}
	s.win.Update(fn)
}

// events merges job updates into the shell, coalescing bursts so a chatty
// tool does not rebuild the window for every log line.
func (s *Shell) events() {
	events, unsubscribe := s.app.Subscribe()
	defer unsubscribe()
	for event := range events {
		s.update(func() { s.upsertJob(event.Job) })
	}
}

func (s *Shell) upsertJob(job tasks.Job) {
	for i := range s.jobs {
		if s.jobs[i].ID == job.ID {
			s.jobs[i] = job
			sort.SliceStable(s.jobs, func(i, j int) bool {
				return s.jobs[i].CreatedAt.After(s.jobs[j].CreatedAt)
			})
			return
		}
	}
	s.jobs = append([]tasks.Job{job}, s.jobs...)
}

func (s *Shell) selectTool(id string) {
	if s.selected == id && s.tab == 0 && s.jobID != "" {
		return
	}
	s.tab = 0
	s.selected = id
	s.jobID = ""
	s.fieldErr = nil
	s.errText = ""
	s.resetInputState()
	s.sectionTab = 0
	s.sectionJob = ""
	if def, ok := s.toolByID(id); ok {
		s.values = schemaDefaults(def.Input)
	}
	for i := range s.jobs {
		if s.jobs[i].ToolID == id {
			s.jobID = s.jobs[i].ID
			break
		}
	}
}

// resetInputState drops every per-widget draft. Widgets are keyed by field
// path, so a draft left behind by one tool would otherwise be found again
// by the next tool that happens to use the same name.
func (s *Shell) resetInputState() {
	s.arrayDrafts = map[string]string{}
	s.arrayErrors = map[string]string{}
	s.rowToggles = map[string]bool{}
	s.rowChoices = map[string]string{}
	s.listStates = map[string]*ui.ListState{}
	s.batchState = map[string]*ui.Selection[int]{}
	s.confirmOpen = nil
	s.confirmRun = nil
}

func (s *Shell) toolByID(id string) (tool.Definition, bool) {
	for _, def := range s.tools {
		if def.ID == id {
			return def, true
		}
	}
	return tool.Definition{}, false
}

func (s *Shell) selectedTool() (tool.Definition, bool) {
	return s.toolByID(s.selected)
}

func (s *Shell) selectedJob() (tasks.Job, bool) {
	for _, job := range s.jobs {
		if job.ID == s.jobID {
			return job, true
		}
	}
	return tasks.Job{}, false
}

func (s *Shell) runningJob() (tasks.Job, bool) {
	job, ok := s.selectedJob()
	return job, ok && job.Status.Active()
}

func (s *Shell) activeJobs() int {
	n := 0
	for _, job := range s.jobs {
		if job.Status.Active() {
			n++
		}
	}
	return n
}

func (s *Shell) toolActive(id string) bool {
	for _, job := range s.jobs {
		if job.ToolID == id && job.Status.Active() {
			return true
		}
	}
	return false
}

func (s *Shell) run(input map[string]any) {
	def, ok := s.selectedTool()
	if !ok {
		return
	}
	if _, running := s.runningJob(); running {
		return
	}
	if s.starting {
		return
	}
	s.starting = true
	s.errText = ""
	s.fieldErr = nil
	go func() {
		job, err := s.app.Start(context.Background(), def.ID, input)
		s.update(func() {
			s.starting = false
			if err != nil {
				var field *tool.FieldError
				if errors.As(err, &field) {
					s.fieldErr = field
					return
				}
				s.errText = err.Error()
				return
			}
			s.upsertJob(job)
			s.jobID = job.ID
		})
	}()
}

func (s *Shell) runCurrent() {
	input := make(map[string]any, len(s.values))
	for key, value := range s.values {
		input[key] = value
	}
	s.run(input)
}

func (s *Shell) cancel() {
	job, ok := s.runningJob()
	if !ok {
		return
	}
	go func() {
		updated, err := s.app.Cancel(context.Background(), job.ID)
		s.update(func() {
			if err != nil {
				s.errText = err.Error()
				return
			}
			s.upsertJob(updated)
		})
	}()
}

func (s *Shell) loadStore(refresh bool) {
	if s.storeLoad {
		return
	}
	s.storeLoad = true
	s.errText = ""
	go func() {
		var snapshot tool.StoreSnapshot
		var err error
		if refresh {
			snapshot, err = s.app.RefreshPluginStore(context.Background())
		} else {
			snapshot = s.app.PluginStore()
		}
		s.update(func() {
			s.storeLoad = false
			if err != nil {
				s.errText = err.Error()
				return
			}
			s.store = snapshot
		})
	}()
}

func (s *Shell) showStore() {
	s.tab = 1
	s.jobID = ""
	s.errText = ""
	s.fieldErr = nil
	if len(s.store.Packages) == 0 {
		s.loadStore(true)
	}
}

func (s *Shell) install(id string) {
	if s.storeBusy != "" {
		return
	}
	if s.toolActive(id) {
		s.errText = "该工具还有任务在运行，请先停止或等待完成。"
		return
	}
	s.storeBusy = id
	s.errText = ""
	go func() {
		snapshot, err := s.app.InstallPlugin(context.Background(), id)
		s.update(func() {
			s.storeBusy = ""
			if err != nil {
				s.errText = err.Error()
				return
			}
			s.store = snapshot
			if def, ok := s.packageDefinition(id, snapshot); ok {
				s.tools = s.app.Tools()
				s.selectTool(def.ID)
			} else {
				s.reloadTools()
			}
			s.toast("已安装 " + id)
		})
	}()
}

func (s *Shell) uninstall(id string) {
	if s.storeBusy != "" {
		return
	}
	if s.toolActive(id) {
		s.errText = "该工具还有任务在运行，请先停止或等待完成。"
		return
	}
	s.storeBusy = id
	s.errText = ""
	go func() {
		snapshot, err := s.app.UninstallPlugin(context.Background(), id)
		s.update(func() {
			s.storeBusy = ""
			if err != nil {
				s.errText = err.Error()
				return
			}
			s.store = snapshot
			s.reloadTools()
			s.toast("已卸载 " + id)
		})
	}()
}

// packageDefinition finds the installed tool in the fresh store snapshot.
func (s *Shell) packageDefinition(id string, snapshot tool.StoreSnapshot) (tool.Definition, bool) {
	for _, item := range snapshot.Packages {
		if item.Definition.ID == id && item.Installed {
			return item.Definition, true
		}
	}
	return tool.Definition{}, false
}

// reloadTools refreshes the catalog and keeps the selection valid. When the
// selected tool disappeared, the first remaining tool is selected; with no
// tools at all the empty state shows.
func (s *Shell) reloadTools() {
	s.tools = s.app.Tools()
	if _, ok := s.toolByID(s.selected); ok {
		return
	}
	s.selected = ""
	s.jobID = ""
	if len(s.tools) > 0 {
		s.tab = 0
		s.selectTool(s.tools[0].ID)
		return
	}
	s.tab = 1
}

func (s *Shell) toast(text string) {
	s.toastText = text
}

// setAppearance changes the appearance mode and persists it. It is shared by
// the rail button and the settings window.
func (s *Shell) setAppearance(mode string) {
	s.mode = normalizeThemeMode(mode)
	s.applyAppearance()
	_ = saveTheme(s.dataDir, s.mode)
	if s.settings != nil {
		s.settings.appearance = s.mode
	}
	if s.win != nil && !s.win.IsDestroyed() {
		s.win.Invalidate()
	}
	if s.settings != nil && s.settings.win != nil && !s.settings.win.IsDestroyed() {
		s.settings.win.Invalidate()
	}
}

func (s *Shell) cycleAppearance() {
	switch normalizeThemeMode(s.mode) {
	case themeSystem:
		s.setAppearance(themeLight)
	case themeDark:
		s.setAppearance(themeSystem)
	default:
		s.setAppearance(themeDark)
	}
}

// view builds the whole main window.
func (s *Shell) view(c *ui.Context) {
	c.SetTheme(s.theme)
	t := s.theme
	c.Root().Background(t.Background)

	// Resolve the system appearance per frame as well: a saved mode can be
	// changed outside the app, and a window recreated after the first one
	// would otherwise keep the palette it started with.
	if normalizeThemeMode(s.mode) == themeSystem {
		if dark := mygo.Theme.IsDark(); dark != s.dark {
			s.dark = dark
			s.theme = shellTheme(dark)
			c.SetTheme(s.theme)
			t = s.theme
			c.Root().Background(t.Background)
		}
	}

	if s.toastText != "" {
		c.Toast(s.toastText)
		s.toastText = ""
	}
	// Elapsed time comes from the wall clock, not from job events: a long
	// step that only logs occasionally would otherwise freeze its timer.
	if s.activeJobs() > 0 {
		c.After(time.Second)
	}

	ui.Column(c).Fill().Children(func() {
		s.titlebar(c)
		// Grow, not Fill: the row shares the column with the title bar.
		// Fill would size it to the whole window and push the bottom edge
		// out by the title bar's height.
		ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
			s.rail(c)
			ui.Box(c).Width(1).Background(t.Border)
			s.workbench(c)
		})
	})
	s.handleShortcuts(c)
}

func (s *Shell) titlebar(c *ui.Context) {
	t := s.theme
	ui.Row(c).Height(titleHeight).Shrink(0).AlignItems(ui.Center).Padding(0, 16).
		Background(t.Surface).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).DragWindow().Children(func() {
		ui.Row(c).Width(260).Gap(9).AlignItems(ui.Center).Children(func() {
			badge := ui.Text(c, "OT").FontSize(11).Bold().TextColor(t.AccentText).Width(26).Height(22).Center()
			badge.Background(t.Accent).Radius(6)
			ui.Text(c, "OpenTools").FontSize(14).Bold()
			ui.Text(c, "v"+s.version).FontSize(11).TextColor(t.TextMuted)
		})

		search := ui.Row(c).Key("search").Grow(1).MaxWidth(520).Height(30).AlignItems(ui.Center).
			Padding(0, 10).Gap(8).Radius(8).Background(t.Background).Border(1, t.Border).
			Children(func() {
				ui.Text(c, "⌕").FontSize(15).TextColor(t.TextMuted)
				ui.TextInputBase(c, &s.search).Bind(&s.searchHandle).Grow(1).FontSize(13).
					Placeholder("搜索工具、动作或分类").Label("搜索工具").Changed()
				ui.Text(c, "Ctrl K").FontSize(10).TextColor(t.TextMuted)
			})
		_ = search

		ui.Spacer(c)
		if active := s.activeJobs(); active > 0 {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Margin(0, 14, 0, 0).Children(func() {
				ui.Text(c, "●").FontSize(9).TextColor(t.Accent)
				ui.Text(c, fmt.Sprintf("%d 个任务运行中", active)).FontSize(12).TextColor(t.TextMuted)
			})
		}
		ui.Row(c).Gap(2).Margin(0, 0, 0, 10).Children(func() {
			s.windowButton(c, "–", "最小化", func() { s.win.Minimize() })
			s.windowButton(c, "□", "最大化或还原", func() { s.win.ToggleMaximize() })
			s.windowButton(c, "×", "关闭", func() { s.win.Close() })
		})
	})
}

func (s *Shell) windowButton(c *ui.Context, glyph, label string, action func()) {
	t := s.theme
	button := ui.ButtonBase(c).Size(36, 28).Radius(6).Center().Label(label).Tooltip(label)
	if button.Hovered() {
		if label == "关闭" {
			button.Background(t.Danger)
		} else {
			button.Background(t.SurfaceHover)
		}
	}
	button.Children(func() {
		color := t.TextMuted
		if label == "关闭" && button.Hovered() {
			color = t.AccentText
		}
		ui.Text(c, glyph).FontSize(14).TextColor(color)
	})
	if button.Clicked() {
		action()
	}
}

func (s *Shell) rail(c *ui.Context) {
	t := s.theme
	// The rail must take the row's height, not its content height: a
	// content-sized column lets the scrollable list push the footer
	// buttons below the window.
	ui.Column(c).Width(railWidth).MinHeight(0).AlignSelf(ui.Stretch).Background(t.Surface).Children(func() {
		// The list scrolls, so it must be allowed to shrink below its
		// content height; otherwise it pushes the footer buttons out of the
		// window.
		ui.Column(c).Gap(10).Padding(12, 12, 8).Grow(1).MinHeight(0).Children(func() {
			view := s.tab
			if ui.Segmented(c, &view, "工具", "商店").FillEqually().Label("视图").Changed() {
				if view == 1 {
					s.showStore()
				} else {
					s.tab = 0
				}
			}
			s.sidebarList(c)
		})
		ui.Divider(c)
		s.recent(c)
		ui.Divider(c)
		ui.Row(c).Padding(8).Gap(6).Children(func() {
			if ui.Button(c, "设置").Grow(1).Clicked() {
				s.openSettings()
			}
			label := "跟随系统"
			switch normalizeThemeMode(s.mode) {
			case themeDark:
				label = "暗色"
			case themeLight:
				label = "亮色"
			}
			if ui.Button(c, label).Grow(1).Tooltip("切换外观").Clicked() {
				s.cycleAppearance()
			}
		})
	})
}

func (s *Shell) sidebarList(c *ui.Context) {
	t := s.theme
	if s.tab == 1 {
		s.storeSidebar(c)
		return
	}
	query := strings.ToLower(strings.TrimSpace(s.search))
	categories := map[string][]tool.Definition{}
	order := []string{}
	for _, def := range s.tools {
		haystack := strings.ToLower(def.Name + " " + def.Summary + " " + def.Category + " " + strings.Join(def.Keywords, " "))
		if query != "" && !strings.Contains(haystack, query) {
			continue
		}
		if _, ok := categories[def.Category]; !ok {
			order = append(order, def.Category)
		}
		categories[def.Category] = append(categories[def.Category], def)
	}
	sort.Strings(order)
	// Shrink lets the list give up room instead of growing past the
	// window: Grow alone keeps a scroll's content height as its minimum.
	ui.Scroll(c).Grow(1).Shrink(1).MinHeight(0).Children(func() {
		ui.Column(c).Gap(12).Padding(4).Children(func() {
			for _, category := range order {
				items := categories[category]
				ui.Column(c).Gap(3).Children(func() {
					ui.Text(c, category).FontSize(10).Bold().TextColor(t.TextMuted).Padding(4, 8)
					for _, def := range items {
						s.toolRow(c, def)
					}
				})
			}
			if len(order) == 0 {
				ui.Text(c, "没有匹配的工具。").FontSize(12).TextColor(t.TextMuted).Padding(8)
			}
		})
	})
}

func (s *Shell) toolRow(c *ui.Context, def tool.Definition) {
	t := s.theme
	selected := def.ID == s.selected
	row := ui.ButtonBase(c).Key("tool:"+def.ID).FillWidth().Padding(6, 8).Gap(8).AlignItems(ui.Start).
		Radius(7).Label(def.Name)
	if selected {
		row.Background(t.Accent.Alpha(0.12))
	} else if row.Hovered() {
		row.Background(t.SurfaceHover)
	}
	row.Children(func() {
		s.toolGlyph(c, def, 26)
		ui.Column(c).Grow(1).Gap(1).Children(func() {
			ui.Text(c, def.Name).FontSize(12.5).Bold().SingleLine()
			ui.Text(c, def.Summary).FontSize(11).TextColor(t.TextMuted).MaxLines(2).Wrap()
		})
	})
	if row.Clicked() {
		s.selectTool(def.ID)
	}
}

func (s *Shell) toolGlyph(c *ui.Context, def tool.Definition, size float32) {
	t := s.theme
	if svg := toolSVG(def.Icon); svg != nil {
		ui.Row(c).Size(size, size).Radius(6).Center().Background(t.Accent.Alpha(0.1)).Children(func() {
			ui.Icon(c, svg).Size(size*0.62, size*0.62).TextColor(t.Accent)
		})
		return
	}
	initial := "?"
	if runes := []rune(strings.TrimSpace(def.Name)); len(runes) > 0 {
		initial = string(runes[0])
	}
	box := ui.Text(c, initial).FontSize(11).Bold().TextColor(t.Accent).Size(size, size).Center()
	box.Background(t.Accent.Alpha(0.1)).Radius(6)
}

func (s *Shell) recent(c *ui.Context) {
	t := s.theme
	seen := map[string]bool{}
	rows := []tasks.Job{}
	for _, job := range s.jobs {
		if _, installed := s.toolByID(job.ToolID); !installed {
			continue
		}
		if seen[job.ToolID] {
			continue
		}
		seen[job.ToolID] = true
		rows = append(rows, job)
		if len(rows) == 12 {
			break
		}
	}
	ui.Column(c).Shrink(0).Gap(2).Padding(8).Children(func() {
		ui.Row(c).Children(func() {
			ui.Text(c, "最近运行").FontSize(10).Bold().TextColor(t.TextMuted).Grow(1)
			ui.Text(c, fmt.Sprintf("%02d", len(rows))).FontSize(10).TextColor(t.TextMuted)
		})
		for _, job := range rows {
			s.recentRow(c, job)
		}
		if len(rows) == 0 {
			ui.Text(c, "暂无运行记录。").FontSize(11).TextColor(t.TextMuted).Padding(4)
		}
	})
}

func (s *Shell) recentRow(c *ui.Context, job tasks.Job) {
	t := s.theme
	name := job.ToolID
	if def, ok := s.toolByID(job.ToolID); ok {
		name = def.Name
	}
	selected := job.ID == s.jobID
	row := ui.ButtonBase(c).Key("job:"+job.ID).FillWidth().Height(26).Padding(0, 6).Gap(6).Radius(6).Label(name)
	if selected {
		row.Background(t.Accent.Alpha(0.1))
	} else if row.Hovered() {
		row.Background(t.SurfaceHover)
	}
	row.Children(func() {
		ui.Text(c, "●").FontSize(8).TextColor(jobTone(t, job.Status))
		ui.Text(c, name).FontSize(11.5).Grow(1).SingleLine()
		ui.Text(c, job.CreatedAt.Local().Format("15:04")).FontSize(10).TextColor(t.TextMuted)
	})
	if row.Clicked() {
		s.loadJob(job)
	}
}

func (s *Shell) loadJob(job tasks.Job) {
	def, ok := s.toolByID(job.ToolID)
	if !ok {
		return
	}
	s.tab = 0
	s.selected = job.ToolID
	s.jobID = job.ID
	s.errText = ""
	s.fieldErr = nil
	s.values = schemaDefaults(def.Input)
	overlayInput(s.values, job.Input)
	s.resetInputState()
	s.sectionTab = 0
	s.sectionJob = ""
}

// overlayInput copies a stored job input into the form. Redacted secrets are
// skipped: the stored snapshot never holds them, and submitting the
// placeholder would send it to the tool as if it were the real password.
func overlayInput(dst, src map[string]any) {
	for key, value := range src {
		if value == tool.RedactedPlaceholder {
			continue
		}
		if nested, ok := value.(map[string]any); ok {
			current, ok := dst[key].(map[string]any)
			if !ok {
				current = map[string]any{}
				dst[key] = current
			}
			overlayInput(current, nested)
			continue
		}
		dst[key] = value
	}
}

func jobTone(t *ui.Theme, status tasks.Status) ui.Color {
	switch status {
	case tasks.StatusSucceeded:
		return t.Success
	case tasks.StatusFailed:
		return t.Danger
	case tasks.StatusRunning, tasks.StatusQueued, tasks.StatusCanceling:
		return t.Accent
	default:
		return t.TextMuted
	}
}

func (s *Shell) workbench(c *ui.Context) {
	t := s.theme
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).AlignSelf(ui.Stretch).Background(t.Background).Children(func() {
		if s.errText != "" {
			ui.Row(c).Margin(12, 12, 0).Padding(8, 10).Gap(8).Radius(7).AlignItems(ui.Start).
				Background(t.Danger.Alpha(0.1)).Border(1, t.Danger.Alpha(0.3)).Children(func() {
				ui.Text(c, "!").FontSize(12).Bold().TextColor(t.Danger)
				ui.Text(c, s.errText).FontSize(12).TextColor(t.Danger).Grow(1).Wrap()
				if ui.ButtonBase(c).Size(20, 20).Center().Label("关闭提示").Children(func() {
					ui.Text(c, "×").FontSize(12).TextColor(t.Danger)
				}).Clicked() {
					s.errText = ""
				}
			})
		}
		if s.loading {
			s.loadingView(c)
			return
		}
		if s.tab == 1 {
			s.storeView(c)
			return
		}
		if def, ok := s.selectedTool(); ok {
			s.toolView(c, def)
			return
		}
		s.emptyView(c)
	})
}

func (s *Shell) loadingView(c *ui.Context) {
	t := s.theme
	ui.Column(c).Fill().Center().Gap(10).Children(func() {
		ui.Spinner(c)
		ui.Text(c, "正在加载本地工具").FontSize(12).TextColor(t.TextMuted)
	})
}

func (s *Shell) emptyView(c *ui.Context) {
	t := s.theme
	ui.Column(c).Fill().Center().Gap(10).Children(func() {
		ui.Text(c, "还没有安装工具").FontSize(20).Bold()
		ui.Text(c, "OpenTools 核心不内置业务工具。前往工具商店下载后即可使用。").
			FontSize(12).TextColor(t.TextMuted).Wrap().MaxWidth(420)
		if ui.PrimaryButton(c, "打开工具商店").Clicked() {
			s.showStore()
		}
	})
}

func (s *Shell) toolView(c *ui.Context, def tool.Definition) {
	s.syncSections()
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		s.toolHeader(c, def)
		ui.Divider(c)
		ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
			ui.Column(c).Padding(14, 16).Gap(14).MaxWidth(1100).Children(func() {
				s.form(c, def)
				if job, ok := s.selectedJob(); ok {
					s.result(c, def, job)
				} else {
					s.emptyResult(c)
				}
			})
		})
	})
	if s.confirmOpen != nil && *s.confirmOpen {
		switch ui.AlertDialog(c, s.confirmOpen, "确认操作", s.confirmText, "取消", "确认执行") {
		case 1:
			if s.confirmRun != nil {
				s.confirmRun()
			}
			s.confirmRun = nil
			s.confirmOpen = nil
		case ui.AlertDismissed, 0:
			s.confirmRun = nil
			s.confirmOpen = nil
		}
	}
}

func (s *Shell) toolHeader(c *ui.Context, def tool.Definition) {
	t := s.theme
	ui.Row(c).Height(52).Shrink(0).Padding(0, 16).Gap(10).AlignItems(ui.Center).Children(func() {
		s.toolGlyph(c, def, 30)
		ui.Column(c).Grow(1).Gap(0).Children(func() {
			ui.Row(c).Gap(7).Children(func() {
				ui.Text(c, def.Name).FontSize(15).Bold()
				ui.Text(c, "v"+def.Version).FontSize(10.5).TextColor(t.TextMuted)
			})
			ui.Text(c, def.Summary).FontSize(11.5).TextColor(t.TextMuted).SingleLine()
		})
		if _, running := s.runningJob(); running {
			if ui.PrimaryButton(c, "停止任务").Clicked() {
				s.cancel()
			}
		} else if s.starting {
			ui.PrimaryButton(c, "启动中…").Disabled(true)
		} else {
			button := ui.PrimaryButton(c, "运行工具")
			if button.Clicked() {
				s.runCurrent()
			}
			ui.Text(c, "Ctrl ↵").FontSize(10).TextColor(t.TextMuted)
		}
	})
}

func (s *Shell) emptyResult(c *ui.Context) {
	t := s.theme
	ui.Column(c).FillWidth().MinHeight(150).Padding(20).Radius(10).Center().Gap(6).
		Background(t.Surface).Border(1, t.Border).Children(func() {
		ui.Text(c, "结果会显示在这里").FontSize(13).Bold()
		ui.Text(c, "配置好输入后运行工具，输出、日志与可执行操作会集中显示。").
			FontSize(11.5).TextColor(t.TextMuted).Wrap().MaxWidth(520)
		ui.Text(c, "Ctrl + Enter 运行当前工具").FontSize(11).TextColor(t.TextMuted)
	})
}

func (s *Shell) handleShortcuts(c *ui.Context) {
	if c.Shortcut(ui.Cmd, ui.KeyK) {
		s.searchHandle.Focus(c)
	}
	// Ctrl+Enter runs only the visible workbench. Otherwise a confirmation
	// dialog or another view could start a tool the user cannot inspect.
	if s.tab == 0 && !s.loading && s.confirmOpen == nil && c.Shortcut(ui.Cmd, ui.KeyEnter) {
		s.runCurrent()
	}
	if c.Shortcut(0, ui.KeyEscape) && s.search != "" {
		s.search = ""
	}
}

func toolSVG(raw string) *ui.SVG {
	if !strings.HasPrefix(raw, "svg:") {
		return nil
	}
	parts := strings.Split(strings.TrimPrefix(raw, "svg:"), "|")
	var paths strings.Builder
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			paths.WriteString(`<path d="`)
			paths.WriteString(strings.ReplaceAll(part, `"`, "&quot;"))
			paths.WriteString(`"/>`)
		}
	}
	document := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">` + paths.String() + `</svg>`
	svg, err := ui.ParseSVG([]byte(document))
	if err != nil {
		return nil
	}
	return svg
}

func schemaDefaults(schema tool.Schema) map[string]any {
	out := map[string]any{}
	for name, child := range schema.Properties {
		if child == nil {
			continue
		}
		if child.Default != nil {
			out[name] = cloneJSON(child.Default)
			continue
		}
		switch child.Type {
		case "boolean":
			out[name] = false
		case "number", "integer":
			out[name] = 0
		case "object":
			out[name] = schemaDefaults(*child)
		case "array":
			out[name] = []any{}
		default:
			out[name] = ""
		}
	}
	return out
}

func cloneJSON(value any) any {
	data, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var out any
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	if err := decoder.Decode(&out); err != nil {
		return value
	}
	return out
}

func (s *Shell) openSettings() {
	if s.win == nil {
		return
	}
	if s.settings != nil && s.settings.win != nil && !s.settings.win.IsDestroyed() {
		s.settings.win.Show()
		s.settings.win.Focus()
		return
	}
	settings := openSettingsWindow(s.win, s.app, s.dataDir, s.mode, s.setAppearance,
		func(snapshot tool.StoreSnapshot) {
			s.store = snapshot
			s.storeLoad = false
			s.tools = s.app.Tools()
			if _, ok := s.toolByID(s.selected); !ok {
				s.reloadTools()
			}
			if s.win != nil && !s.win.IsDestroyed() {
				s.win.Invalidate()
			}
		})
	s.settings = settings
	settings.win.OnClosed(func() {
		if s.settings == settings {
			s.settings = nil
		}
	})
}
