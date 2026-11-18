package desktop

import (
	"fmt"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/cn-maul/OpenTools/pkg/tool"
)

// storeSidebar lists downloadable and installed tools. Installed tools come
// first, then the catalog order, matching the old rail.
func (s *Shell) storeSidebar(c *ui.Context) {
	t := s.theme
	packages := s.availableStorePackages()
	ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).Gap(3).Padding(4).Children(func() {
			ui.Row(c).Children(func() {
				ui.Text(c, "可获取").FontSize(10).Bold().TextColor(t.TextMuted).Padding(4, 8).Grow(1)
				ui.Text(c, fmt.Sprintf("%02d", len(packages))).FontSize(10).TextColor(t.TextMuted)
			})
			for _, item := range packages {
				s.storeRow(c, item)
			}
			if len(packages) == 0 {
				message := "商店暂无可用工具。"
				if s.storeLoad {
					message = "正在获取商店目录…"
				}
				ui.Text(c, message).FontSize(11).TextColor(t.TextMuted).Padding(8)
			}
		})
	})
}

// availableStorePackages is the sidebar view: tools to install or update.
// Fully installed tools keep their cards in the store grid, where they can
// be opened or uninstalled.
func (s *Shell) availableStorePackages() []tool.Package {
	query := strings.ToLower(strings.TrimSpace(s.search))
	packages := make([]tool.Package, 0, len(s.store.Packages))
	for _, item := range s.store.Packages {
		if item.Installed && !item.UpdateAvailable {
			continue
		}
		if query != "" {
			text := strings.ToLower(item.Definition.Name + " " + item.Definition.Summary + " " + item.Definition.Category + " " + item.Author)
			if !strings.Contains(text, query) {
				continue
			}
		}
		packages = append(packages, item)
	}
	sort.SliceStable(packages, func(i, j int) bool {
		return packages[i].Installed && !packages[j].Installed
	})
	return packages
}

// storePackages is the grid view: every catalog entry, installed first. A
// fully installed tool must stay here, or its uninstall action would be
// unreachable.
func (s *Shell) storePackages() []tool.Package {
	query := strings.ToLower(strings.TrimSpace(s.search))
	packages := make([]tool.Package, 0, len(s.store.Packages))
	for _, item := range s.store.Packages {
		if query != "" {
			text := strings.ToLower(item.Definition.Name + " " + item.Definition.Summary + " " + item.Definition.Category + " " + item.Author)
			if !strings.Contains(text, query) {
				continue
			}
		}
		packages = append(packages, item)
	}
	sort.SliceStable(packages, func(i, j int) bool {
		return packages[i].Installed && !packages[j].Installed
	})
	return packages
}

func (s *Shell) storeRow(c *ui.Context, item tool.Package) {
	t := s.theme
	row := ui.ButtonBase(c).Key("store:"+item.Definition.ID).FillWidth().Padding(6, 8).Gap(8).
		AlignItems(ui.Start).Radius(7).Label(item.Definition.Name)
	if item.Installed && item.Definition.ID == s.selected {
		row.Background(t.Accent.Alpha(0.12))
	} else if item.Installed && row.Hovered() {
		row.Background(t.SurfaceHover)
	}
	row.Children(func() {
		s.toolGlyph(c, item.Definition, 26)
		ui.Column(c).Grow(1).Gap(1).Children(func() {
			ui.Row(c).Gap(5).Children(func() {
				ui.Text(c, item.Definition.Name).FontSize(12.5).Bold().SingleLine().Grow(1)
				label := "可下载"
				colour := t.TextMuted
				if item.Installed {
					label = "已安装"
					colour = t.Success
				}
				ui.Text(c, label).FontSize(9.5).TextColor(colour)
			})
			ui.Text(c, item.Definition.Summary).FontSize(11).TextColor(t.TextMuted).MaxLines(2).Wrap()
		})
	})
	if row.Clicked() && item.Installed {
		s.selectTool(item.Definition.ID)
	}
}

func (s *Shell) storeView(c *ui.Context) {
	t := s.theme
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		ui.Row(c).Height(58).Shrink(0).Padding(0, 16).Children(func() {
			ui.Column(c).Grow(1).Gap(1).Children(func() {
				ui.Text(c, "OpenTools Store").FontSize(10).Bold().TextColor(t.TextMuted)
				ui.Text(c, "工具商店").FontSize(16).Bold()
			})
			if ui.Button(c, "刷新目录").Disabled(s.storeLoad).Clicked() {
				s.loadStore(true)
			}
		})
		ui.Divider(c)
		ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
			ui.Column(c).Padding(14, 16).Gap(10).MaxWidth(1100).Children(func() {
				ui.Text(c, "核心只提供运行框架。工具按需下载、独立更新，也可以随时卸载。").
					FontSize(12).TextColor(t.TextMuted)
				if len(s.store.Warnings) > 0 {
					ui.Column(c).Padding(8, 10).Gap(3).Radius(7).Background(t.Warning.Alpha(0.1)).
						Border(1, t.Warning.Alpha(0.25)).Children(func() {
						ui.Text(c, "部分目录暂不可用").FontSize(11).Bold().TextColor(t.Warning)
						for _, warning := range s.store.Warnings {
							ui.Text(c, warning).FontSize(10.5).TextColor(t.Warning).Wrap()
						}
					})
				}
				packages := s.storePackages()
				if len(packages) == 0 {
					message := "联网刷新目录后选择一个工具下载，安装完成会立即出现在左侧。"
					if s.storeLoad {
						message = "正在获取商店目录…"
					} else if len(s.store.Packages) == 0 {
						message = "当前目录没有可用的工具。"
					}
					ui.Column(c).FillWidth().Padding(24).Center().Gap(6).Radius(10).
						Background(t.Surface).Border(1, t.Border).Children(func() {
						ui.Text(c, "商店暂无可用工具").FontSize(14).Bold()
						ui.Text(c, message).
							FontSize(11.5).TextColor(t.TextMuted).Wrap().MaxWidth(460)
					})
					return
				}
				for _, item := range packages {
					s.storeCard(c, item)
				}
			})
		})
	})
}

func (s *Shell) storeCard(c *ui.Context, item tool.Package) {
	t := s.theme
	ui.Column(c).FillWidth().Padding(12).Gap(8).Radius(9).Background(t.Surface).
		Border(1, t.Border).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Start).Children(func() {
			s.toolGlyph(c, item.Definition, 32)
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Row(c).Gap(6).Children(func() {
					ui.Text(c, item.Definition.Name).FontSize(13).Bold().Grow(1)
					version := item.InstalledVersion
					if version == "" {
						version = item.Definition.Version
					}
					ui.Text(c, "v"+version).FontSize(10).TextColor(t.TextMuted)
				})
				ui.Text(c, item.Definition.Summary).FontSize(11).TextColor(t.TextMuted).Wrap()
				meta := item.Definition.Category + " · " + platformLabel(item.Platform)
				if item.Author != "" {
					meta += " · " + item.Author
				}
				ui.Text(c, meta).FontSize(10).TextColor(t.TextMuted)
			})
		})
		ui.Row(c).Gap(6).Children(func() {
			if item.Installed {
				if item.UpdateAvailable {
					if ui.PrimaryButton(c, busyLabel(item, s.storeBusy, "更新")).Clicked() {
						s.install(item.Definition.ID)
					}
				} else if ui.PrimaryButton(c, "打开工具").Clicked() {
					s.selectTool(item.Definition.ID)
				}
				if ui.Button(c, busyLabel(item, s.storeBusy, "卸载")).Clicked() {
					s.uninstall(item.Definition.ID)
				}
			} else {
				label := "安装"
				if item.UpdateAvailable {
					label = "更新"
				}
				if ui.PrimaryButton(c, busyLabel(item, s.storeBusy, label)).
					Disabled(!item.Compatible).Clicked() {
					s.install(item.Definition.ID)
				}
				if !item.Compatible {
					ui.Text(c, "当前平台或协议版本不兼容").FontSize(10).TextColor(t.TextMuted)
				}
			}
		})
	})
}

func busyLabel(item tool.Package, busy, label string) string {
	if busy != item.Definition.ID {
		return label
	}
	return label + "中…"
}

func platformLabel(platform string) string {
	if platform == "" {
		return "未知平台"
	}
	return platform
}
