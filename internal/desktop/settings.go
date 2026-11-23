package desktop

import (
	"context"
	"os"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/cn-maul/OpenTools/internal/application"
	"github.com/cn-maul/OpenTools/sdk/registry"
)

// giteeStoreURL is the catalog used by the one-click preset.
const giteeStoreURL = "https://gitee.com/dogge2333/opentools-store/raw/master/catalog.json"

// settingsWindow is the native settings UI.
//
// Settings are a handful of controls, so the window is drawn entirely with
// MyGo widgets and opens as a modal child of the main window. All fields below
// are read on the main thread by view; background work reports back through
// Window.Update.
type settingsWindow struct {
	app     *application.Service
	dataDir string

	win *mygo.Window
	// onSaved receives the fresh store snapshot after a successful save.
	onSaved func(registry.StoreSnapshot)
	// value is the draft the user is editing, not the saved catalog list.
	value   string
	saved   []string
	saving  bool
	errText string
	// toast is shown once by the next frame, since c.Toast belongs to a build.
	toast string

	appearance   string
	onAppearance func(string)
}

// openSettingsWindow creates the native settings sheet as a modal child of the
// main window.
func openSettingsWindow(parent *mygo.Window, app *application.Service, dataDir, appearance string, onAppearance func(string), onSaved func(registry.StoreSnapshot)) *settingsWindow {
	settings := &settingsWindow{
		app:          app,
		dataDir:      dataDir,
		value:        app.PluginStoreURL(),
		saved:        app.PluginStore().Sources,
		onSaved:      onSaved,
		appearance:   appearance,
		onAppearance: onAppearance,
	}
	settings.win = mygo.NewWindow(mygo.WindowOptions{
		Title:     "设置",
		Width:     560,
		Height:    520,
		MinWidth:  460,
		MinHeight: 460,
		Parent:    parent,
		Modal:     parent != nil,
		Content:   ui.View(settings.view),
	})
	return settings
}

// view draws the settings window from the current state.
func (s *settingsWindow) view(c *ui.Context) {
	dark := s.appearance == themeDark
	if normalizeThemeMode(s.appearance) == themeSystem {
		dark = mygo.Theme.IsDark()
	}
	c.SetTheme(shellTheme(dark))
	t := c.Theme()

	if s.toast != "" {
		c.Toast(s.toast)
		s.toast = ""
	}

	// Enter in the field and the Save button both land here. Reading the
	// field's Submitted keeps the key local to it, so typing a path never
	// needs a global shortcut.
	saveRequested := false
	closeRequested := false
	appearance := s.appearance
	index := 0
	switch appearance {
	case themeLight:
		index = 1
	case themeDark:
		index = 2
	}

	ui.Column(c).Fill().Padding(24).Gap(16).Children(func() {
		ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
			ui.Column(c).Gap(14).Padding(0, 0, 12, 0).Children(func() {
				ui.Text(c, "外观").FontSize(13).Bold()
				if ui.Segmented(c, &index, "跟随系统", "亮色", "暗色").FillWidth().
					Label("外观").Changed() {
					appearance = []string{themeSystem, themeLight, themeDark}[index]
				}

				ui.Divider(c)

				ui.Text(c, "数据目录").FontSize(13).Bold()
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, s.dataDir).FontSize(12).TextColor(t.TextMuted).Grow(1).SingleLine().
						Tooltip(s.dataDir).Selectable()
					if ui.Button(c, "打开").Clicked() {
						if err := mygo.Shell.OpenPath(s.dataDir); err != nil {
							s.errText = err.Error()
						}
					}
					if ui.Button(c, "复制").Clicked() {
						mygo.Clipboard.WriteText(s.dataDir)
						s.toast = "已复制数据目录"
					}
				})
				if _, err := os.Stat(s.dataDir); err != nil {
					ui.Text(c, err.Error()).FontSize(11).TextColor(t.Danger).Wrap()
				}

				ui.Divider(c)

				ui.Text(c, "工具商店").FontSize(13).Bold()

				ui.Column(c).Gap(8).Children(func() {
					ui.Text(c, "目录地址").FontSize(12).TextColor(t.TextMuted)
					field := ui.TextInput(c, &s.value).
						Placeholder("https://example.com/opentools/catalog.json").
						MaxLength(2048)
					if field.Submitted() && !s.saving {
						saveRequested = true
					}
				})

				ui.Text(c,
					"留空时使用程序自带目录，联网环境自动回退到 Gitee 源。多个地址用英文分号分隔，也支持本地文件路径。",
				).FontSize(12).TextColor(t.TextMuted).Wrap()

				ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "Gitee 源").FontSize(12).TextColor(t.TextMuted)
					ui.Text(c, giteeStoreURL).FontSize(12).TextColor(t.TextMuted).Grow(1).SingleLine()
					if ui.Button(c, "填入").Disabled(s.saving).Clicked() {
						s.value = giteeStoreURL
					}
				})

				ui.Divider(c)

				ui.Column(c).Gap(4).Children(func() {
					ui.Text(c, "当前来源").FontSize(12).Bold()
					if len(s.saved) == 0 {
						ui.Text(c, "程序自带目录").FontSize(12).TextColor(t.TextMuted)
					}
					for _, source := range s.saved {
						ui.Text(c, source).FontSize(12).TextColor(t.TextMuted).Selectable()
					}
				})

				if s.errText != "" {
					ui.Text(c, s.errText).FontSize(12).TextColor(t.Danger).Wrap()
				}
			})
		})

		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			// Appearance applies as soon as it is picked, so closing the
			// window is a finish, not a revert.
			if ui.Button(c, "关闭").Disabled(s.saving).Clicked() {
				closeRequested = true
			}
			if ui.PrimaryButton(c, saveLabel(s.saving)).Disabled(s.saving).Clicked() {
				saveRequested = true
			}
		})
	})

	if appearance != s.appearance {
		s.appearance = appearance
		if s.onAppearance != nil {
			s.onAppearance(appearance)
		}
	}

	// Read Escape only after the controls above applied their input: a
	// Shortcut call before the field is built would consume the pending
	// Enter and leave the field's Submitted false.
	if closeRequested || (c.Shortcut(0, ui.KeyEscape) && !s.saving) {
		s.win.Close()
		return
	}
	if saveRequested && !s.saving {
		s.save()
	}
}

func saveLabel(saving bool) string {
	if saving {
		return "保存中…"
	}
	return "保存"
}

// save persists the draft off the main thread, then repaints with the result.
func (s *settingsWindow) save() {
	value := strings.TrimSpace(s.value)
	s.saving = true
	s.errText = ""

	go func() {
		snapshot, err := s.app.SavePluginStoreURL(context.Background(), value)
		s.win.Update(func() {
			s.saving = false
			if err != nil {
				s.errText = err.Error()
				return
			}
			s.value = value
			s.saved = snapshot.Sources
			s.toast = "设置已保存"
			if s.onSaved != nil {
				s.onSaved(snapshot)
			}
		})
	}()
}
