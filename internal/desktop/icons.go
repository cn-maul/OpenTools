package desktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

// glyph builds a 24x24 stroke icon from path data. Icons inherit the
// surrounding text colour, so one definition works in both appearances.
func glyph(paths ...string) *ui.SVG {
	var builder strings.Builder
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		builder.WriteString(`<path d="`)
		builder.WriteString(strings.ReplaceAll(path, `"`, "&quot;"))
		builder.WriteString(`"/>`)
	}
	document := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">` + builder.String() + `</svg>`
	svg, err := ui.ParseSVG([]byte(document))
	if err != nil {
		return nil
	}
	return svg
}

// Shared interface glyphs. The layout guides through icons first; text only
// names what the icon already shows.
var (
	glyphStore    = glyph("M6 8h12l1.1 12H4.9L6 8z", "M9 8V6.5a3 3 0 0 1 6 0V8")
	glyphDownload = glyph("M12 4v11", "M7 11l5 5 5-5", "M5 20h14")
	glyphPlay     = glyph("M9 6.5v11l9-5.5z")
	glyphSparkles = glyph(
		"M12 4l1.7 4.3L18 10l-4.3 1.7L12 16l-1.7-4.3L6 10l4.3-1.7L12 4z",
		"M18.5 15l.8 1.7 1.7.8-1.7.8-.8 1.7-.8-1.7-1.7-.8 1.7-.8.8-1.7z",
	)
	glyphDoc    = glyph("M7 3h7l4 4v14H7z", "M14 3v4h4", "M10 12h5", "M10 16h5")
	glyphArrow  = glyph("M5 12h13", "M13 7l5 5-5 5")
	glyphSearch = glyph("M10.5 4a6.5 6.5 0 1 1 0 13 6.5 6.5 0 0 1 0-13z", "M15.5 15.5 20 20")
)

// guideStep is one icon-led step in a guide row.
type guideStep struct {
	icon  *ui.SVG
	label string
}

// guideRow draws steps as icon chips joined by arrows. The flow is readable
// from the icons alone, so no explanatory paragraph is needed.
func guideRow(c *ui.Context, t *ui.Theme, steps ...guideStep) {
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		for i, step := range steps {
			if i > 0 && glyphArrow != nil {
				ui.Icon(c, glyphArrow).Size(14, 14).TextColor(t.TextMuted)
			}
			ui.Column(c).Gap(5).Center().Children(func() {
				chipBox := ui.Row(c).Size(40, 40).Radius(11).Center().
					Background(t.Accent.Alpha(0.1))
				if step.icon != nil {
					chipBox.Children(func() {
						ui.Icon(c, step.icon).Size(21, 21).TextColor(t.Accent)
					})
				}
				ui.Text(c, step.label).FontSize(10.5).TextColor(t.TextMuted)
			})
		}
	})
}

// keyCap renders a keyboard key as a bordered chip, so a shortcut hint reads
// as interface rather than fine print.
func keyCap(c *ui.Context, t *ui.Theme, label string) {
	ui.Text(c, label).FontSize(10).Bold().TextColor(t.Text).
		Padding(1, 6).Radius(5).Background(t.Background).Border(1, t.Border)
}

// chip renders a compact rounded metadata badge. Metadata becomes scannable
// geometry instead of a dotted sentence of small text.
func chip(c *ui.Context, t *ui.Theme, text string) {
	ui.Text(c, text).FontSize(9.5).TextColor(t.TextMuted).
		Padding(2, 7).Radius(999).Background(t.SurfaceHover)
}
