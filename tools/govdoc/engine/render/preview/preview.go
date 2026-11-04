// Package preview renders a 公文 as self-contained HTML for the browser.
//
// It exists to answer one question honestly: "what will the printed page look
// like?" It therefore simulates pagination (sheets of the standard's size with
// the standard's margins) and reads every measurement from gbstandard.Spec —
// the same table the DOCX writer uses — so screen and file cannot drift apart.
//
// # Where the numbers come from
//
// This package carries no typographic magnitude of its own. Page size, margins,
// line grid, 字号, colours and rules are all read out of the Spec and emitted as
// CSS custom properties (--gd-page-w, --gd-body-size, ...), which is also what
// makes the stylesheet inspectable: a reviewer can see the standard reflected in
// the variables rather than buried in rule bodies. The only constants defined
// here are unit definitions (mm→pt) and the heuristic line box of a block whose
// style asks for automatic line spacing.
//
// # Pagination
//
// Browsers do not paginate a fragment, so the renderer does it: each block's
// height is measured in units of the standard's fixed line grid, and blocks are
// distributed over sheets of spec.Page.LinesPerPage lines, honouring
// 版头/版记 placement and KeepWithNext. The result is an approximation of Word's
// pagination — close enough to review a layout, not a substitute for the file.
//
// No CSS framework is used anywhere in this project; this package emits plain
// CSS derived from the spec.
package preview

import (
	"bytes"
	"errors"
	"fmt"
	"html"
	"math"
	"net/url"
	"sort"
	"strings"

	"opentools/tools/govdoc/engine/docmodel"
	"opentools/tools/govdoc/engine/gbstandard"
)

// ErrNotImplemented is retained only for compatibility with the earlier
// placeholder skeleton; Render no longer returns it.
//
// Deprecated: the renderer is implemented.
var ErrNotImplemented = errors.New("preview: 渲染尚未实现")

// ptPerMM converts millimetres to PostScript points. This is a unit definition
// rather than a format magnitude: everything the renderer prints still comes
// from gbstandard.Spec.
const ptPerMM = 72.0 / 25.4

// autoLineFactor approximates the line box of a block whose style asks for
// automatic line spacing and gives no explicit value. A block that does carry a
// spacing (红色分隔线 does) uses it directly.
const autoLineFactor = 1.15

// Render produces an HTML fragment (no <html> wrapper) containing the paginated
// preview plus a <style> block generated from spec.
//
// Contract:
//   - All measurements derive from spec; they are emitted as CSS custom
//     properties on a root element so the stylesheet stays inspectable.
//   - Each page is a .gd-page element sized spec.Page.WidthMM/HeightMM with
//     the spec's margins.
//   - Fonts use gbstandard.FontChain so a machine missing 仿宋_GB2312 still
//     shows a faithful substitute.
//   - Text is HTML-escaped; the source text is displayed verbatim.
//   - 版头 elements are grouped on the first sheet; 版记 is pinned to the foot
//     of the last sheet; 发文机关标志 and 红色分隔线 are red.
func Render(d *docmodel.Doc, spec gbstandard.Spec) ([]byte, error) {
	if d == nil {
		return nil, errors.New("preview: 文档为空，无法生成预览")
	}

	l := newLayout(d, spec)

	var b bytes.Buffer
	b.WriteString("<style>\n")
	b.WriteString(l.stylesheet())
	b.WriteString("</style>\n")

	fmt.Fprintf(&b, "<div class=\"gd-root\" data-direction=\"%s\" data-page-count=\"%d\">\n",
		escape(string(spec.Direction)), len(l.pages))
	for i, items := range l.pages {
		writePage(&b, l, i, items)
	}
	b.WriteString("</div>\n")
	return b.Bytes(), nil
}

// ---------------------------------------------------------------- layout model

// item is one rendered block together with the geometry the paginator needs.
type item struct {
	blk   docmodel.OutBlock
	style gbstandard.ParaStyle

	// lines is the number of grid lines the block's text occupies.
	lines float64
	// linePT is this block's own line box, in points.
	linePT float64
	// before and after are 空N行 expressed in lines.
	before, after float64
	// height is before + lines + after, converted to points.
	height float64
}

// layout is the whole pagination decision: which items go on which sheet.
type layout struct {
	spec gbstandard.Spec
	// linePT is the grid line of the 版心 (spec.Page.LineSpacingPT).
	linePT float64
	// capacity is how many grid lines fit on one sheet.
	capacity float64

	head []item // 版头: first sheet, above 主体
	foot []item // 版记: foot of the last sheet
	// pages holds the 主体 blocks, one slice per sheet.
	pages [][]item
}

func newLayout(d *docmodel.Doc, spec gbstandard.Spec) *layout {
	l := &layout{spec: spec}

	l.linePT = spec.Page.LineSpacingPT()
	if l.linePT <= 0 {
		if st, ok := spec.Style(docmodel.RoleBody); ok {
			l.linePT = st.Size.Points()
		}
	}
	if l.linePT <= 0 {
		l.linePT = 1
	}
	l.capacity = float64(spec.Page.LinesPerPage)
	if l.capacity <= 0 {
		l.capacity = 1
	}

	items := make([]item, 0, len(d.Blocks))
	for _, blk := range d.Blocks {
		items = append(items, l.measure(blk, spec.MustStyle(blk.Role)))
	}

	// 版头 is a leading run, 版记 a trailing run; everything between flows.
	start := 0
	for start < len(items) && isHeadRole(items[start].blk.Role) {
		start++
	}
	end := len(items)
	for end > start && isFootRole(items[end-1].blk.Role) {
		end--
	}
	l.head = items[:start]
	l.foot = items[end:]
	l.paginate(items[start:end])
	return l
}

// measure computes a block's grid height from its style and its text.
func (l *layout) measure(blk docmodel.OutBlock, st gbstandard.ParaStyle) item {
	linePT := st.LineSpacingPT
	if linePT <= 0 {
		linePT = st.Size.Points() * autoLineFactor
	}
	if linePT <= 0 {
		linePT = l.linePT
	}

	it := item{
		blk:    blk,
		style:  st,
		linePT: linePT,
		before: st.SpaceBeforeLines / 100,
		after:  st.SpaceAfterLines / 100,
	}
	it.lines = l.lineCount(blk, st)
	it.height = (it.before+it.after)*l.linePT + it.lines*linePT
	return it
}

// lineCount estimates how many grid lines a block's text occupies. It counts
// full-width glyphs as one 字 and half-width glyphs as half a 字, which is what
// "每行N字" in the standard means for mixed Chinese/Latin text.
func (l *layout) lineCount(blk docmodel.OutBlock, st gbstandard.ParaStyle) float64 {
	if len(blk.Rows) > 0 {
		total := 0.0
		for _, row := range blk.Rows {
			units := 0.0
			for i, cell := range row {
				if i > 0 {
					units++ // cell separator
				}
				units += widthUnits(cell)
			}
			total += math.Max(1, math.Ceil(units/l.gridChars(st)))
		}
		return math.Max(1, total)
	}
	if blk.Image != nil {
		return 1
	}

	text := blk.Text
	if blk.AutoNumber != "" {
		text = blk.AutoNumber + text
	}
	if text == "" {
		return 1 // rules and blank separators still occupy a line box
	}

	full := l.gridChars(st)
	// text-indent applies to the first formatted line only, so the first
	// paragraph gets a different capacity from the rest.
	//
	// The indent used here is the *effective* one, i.e. after subtracting
	// whatever the text already indents itself with (a paragraph that opens
	// with two 全角空格 and a style that asks for two would otherwise be
	// counted as consuming two characters of the first line twice).
	first := full - gbstandard.EffectiveFirstLineIndent(st, blk.Text) - st.HangingIndentChars
	if first < 1 {
		first = 1
	}

	total := 0.0
	for i, para := range strings.Split(text, "\n") {
		units := widthUnits(para)
		if i == 0 && units <= first {
			total++
			continue
		}
		if i == 0 {
			total++
			units -= first
		}
		total += math.Max(1, math.Ceil(units/full))
	}
	return math.Max(1, total)
}

// gridChars is the number of 字 that fit on one line of a block, after its
// left/right indents.
func (l *layout) gridChars(st gbstandard.ParaStyle) float64 {
	n := float64(l.spec.Page.CharsPerLine)
	if n <= 0 {
		n = 1
	}
	n -= st.LeftIndentChars + st.RightIndentChars
	if n < 1 {
		n = 1
	}
	return n
}

// paginate distributes the 主体 blocks over sheets.
func (l *layout) paginate(items []item) {
	var pages [][]item
	var cur []item
	used := l.headLines()

	for _, it := range items {
		need := it.height / l.linePT
		if it.style.PageBreakBefore || it.blk.PageBreakBefore {
			if len(cur) > 0 {
				pages = append(pages, cur)
				cur, used = nil, 0
			}
		}
		if len(cur) > 0 && used+need > l.capacity {
			pages = append(pages, cur)
			cur, used = nil, 0
		}
		cur = append(cur, it)
		used += need
	}
	if len(cur) > 0 || len(pages) == 0 {
		pages = append(pages, cur)
	}

	// KeepWithNext: never leave a heading alone at the foot of a sheet when
	// the block it introduces starts the next one.
	for p := 0; p+1 < len(pages); p++ {
		for len(pages[p]) > 0 {
			last := pages[p][len(pages[p])-1]
			if !last.style.KeepWithNext {
				break
			}
			if l.used(p, pages[p+1])+last.height/l.linePT > l.capacity {
				break
			}
			pages[p] = pages[p][:len(pages[p])-1]
			pages[p+1] = append([]item{last}, pages[p+1]...)
		}
	}

	// 版记 belongs at the foot of the last sheet; if it does not fit there,
	// it gets its own sheet rather than being clipped.
	if n := len(l.foot); n > 0 {
		need := 0.0
		for _, it := range l.foot {
			need += it.height / l.linePT
		}
		last := len(pages) - 1
		if len(pages[last]) > 0 && l.used(last, pages[last])+need > l.capacity {
			pages = append(pages, nil)
		}
	}
	l.pages = pages
}

// used reports how many grid lines are already consumed on a sheet.
func (l *layout) used(pageIdx int, items []item) float64 {
	used := 0.0
	if pageIdx == 0 {
		used = l.headLines()
	}
	for _, it := range items {
		used += it.height / l.linePT
	}
	return used
}

// headLines is how many grid lines the 版头 group occupies on the first sheet.
func (l *layout) headLines() float64 {
	return l.headHeightPT() / l.linePT
}

// headHeightPT is the 版头 group's height: whichever is taller of the elements
// above 发文机关标志 and the distance the standard reserves for it, plus the
// 发文机关标志 group itself.
func (l *layout) headHeightPT() float64 {
	reserved := l.spec.Direction.MastheadTopMM() * ptPerMM
	top := 0.0
	main := 0.0
	for _, it := range l.head {
		if isHeadTopRole(it.blk.Role) {
			top += it.height
		} else {
			main += it.height
		}
	}
	if top > reserved {
		reserved = top
	}
	return reserved + main
}

// headTopHeightPT is the height of the 份号/密级/紧急程度 stack, which the
// stylesheet needs in order to place 发文机关标志 at the standard distance
// below the top of the 版心 (标准 §4.3.2, §7.2.4).
func (l *layout) headTopHeightPT() float64 {
	h := 0.0
	for _, it := range l.head {
		if isHeadTopRole(it.blk.Role) {
			h += it.height
		}
	}
	return h
}

// headReservePT is the whitespace between the 版心左上角 block and the top edge
// of 发文机关标志: the direction's normative distance minus whatever the
// 份号/密级/紧急程度 stack already occupies, never negative.
func (l *layout) headReservePT() float64 {
	r := l.spec.Direction.MastheadTopMM()*ptPerMM - l.headTopHeightPT()
	if r < 0 {
		return 0
	}
	return r
}

// ---------------------------------------------------------------- role groups

// isHeadRole reports whether a role is part of 版头 (标准 §7.2), which is
// grouped at the top of the first sheet.
func isHeadRole(r docmodel.Role) bool {
	switch r {
	case docmodel.RoleCopyNumber, docmodel.RoleSecurity, docmodel.RoleUrgency,
		docmodel.RoleMasthead, docmodel.RoleDocNumber, docmodel.RoleSignatory,
		docmodel.RoleRedLine:
		return true
	}
	return false
}

// isHeadTopRole reports whether a role belongs to the 版心左上角 block that sits
// above 发文机关标志: 份号, 密级和保密期限, 紧急程度 (标准 §7.2.1–7.2.3).
func isHeadTopRole(r docmodel.Role) bool {
	switch r {
	case docmodel.RoleCopyNumber, docmodel.RoleSecurity, docmodel.RoleUrgency:
		return true
	}
	return false
}

// isFootRole reports whether a role is part of 版记 (标准 §7.5), which is
// pinned to the foot of the last sheet.
func isFootRole(r docmodel.Role) bool {
	return r == docmodel.RoleCC || r == docmodel.RolePrinter
}

// ---------------------------------------------------------------- HTML output

// writePage emits one sheet: 版头 on the first, 主体, 版记 on the last, 页码.
func writePage(b *bytes.Buffer, l *layout, pageIdx int, items []item) {
	fmt.Fprintf(b, "<div class=\"gd-page\" data-page=\"%d\">\n", pageIdx+1)

	if pageIdx == 0 && len(l.head) > 0 {
		b.WriteString("<div class=\"gd-head\">\n")
		b.WriteString("<div class=\"gd-head-top\">\n")
		for _, it := range l.head {
			if isHeadTopRole(it.blk.Role) {
				writeBlock(b, it, l.spec)
			}
		}
		b.WriteString("</div>\n")
		b.WriteString("<div class=\"gd-head-gap\"></div>\n")
		b.WriteString("<div class=\"gd-head-main\">\n")
		for _, it := range l.head {
			if !isHeadTopRole(it.blk.Role) {
				writeBlock(b, it, l.spec)
			}
		}
		b.WriteString("</div>\n")
		b.WriteString("</div>\n")
	}

	b.WriteString("<div class=\"gd-body\">\n")
	for _, it := range items {
		writeBlock(b, it, l.spec)
	}
	b.WriteString("</div>\n")

	if pageIdx == len(l.pages)-1 && len(l.foot) > 0 {
		b.WriteString("<div class=\"gd-foot\">\n")
		for _, it := range l.foot {
			writeBlock(b, it, l.spec)
		}
		b.WriteString("</div>\n")
	}

	writePageNumber(b, l, pageIdx)
	b.WriteString("</div>\n")
}

// writePageNumber emits 页码: the spec's font and size, one 一字线 on each
// side, on the outer edge (odd sheets right, even sheets left).
func writePageNumber(b *bytes.Buffer, l *layout, pageIdx int) {
	side := "right"
	if pageIdx%2 == 1 {
		side = "left"
	}
	fmt.Fprintf(b, "<div class=\"gd-pageno gd-pageno--%s\">", side)
	b.WriteString("<span class=\"gd-pageno-dash\">")
	b.WriteString(escape(l.spec.PageNumberDash))
	b.WriteString("</span>")
	fmt.Fprintf(b, "<span class=\"gd-pageno-num\">%d</span>", pageIdx+1)
	b.WriteString("<span class=\"gd-pageno-dash\">")
	b.WriteString(escape(l.spec.PageNumberDash))
	b.WriteString("</span>")
	b.WriteString("</div>\n")
}

// writeBlock emits one block: text (or a table, or an image) prefixed by its
// 层次序数 and carrying its role class.
func writeBlock(b *bytes.Buffer, it item, spec gbstandard.Spec) {
	blk := it.blk
	class := "gd-block gd-role-" + cssIdent(string(blk.Role))
	empty := blk.Text == "" && len(blk.Rows) == 0 && blk.Image == nil
	if empty {
		// 「只有一条横线」的块（红色分隔线、版记线）。类名刻意与
		// web/src/styles 的应用壳类名区隔，纸张内部的规则不外泄。
		class += " gd-rule-block"
	}

	if blk.NodeID != "" {
		fmt.Fprintf(b, "<div class=\"%s\" data-node-id=\"%s\"", class, escape(blk.NodeID))
	} else {
		fmt.Fprintf(b, "<div class=\"%s\"", class)
	}
	if blk.Level > 0 {
		fmt.Fprintf(b, " data-level=\"%d\"", blk.Level)
	}

	// 已声明的改动：标注出来，供人工复核。
	//
	// 这一块里的文字与原文不同，而不同之处是**被声明的**（每条都在
	// plan.edits 里，带 before/after/reason）。预览必须让它一眼可见，
	// 否则用户看到的仍是一份「看不出被改过」的稿子，"可审计"就落空了。
	//
	// 标记只影响绘制（背景与左侧色条），绝不改变字号/行距/页边距 ——
	// 纸张度量只能来自 gbstandard.Spec，屏幕上的高亮不得反过来影响分页。
	if blk.Edited {
		b.WriteString(` data-edited="1"`)
		if len(blk.EditKinds) > 0 {
			fmt.Fprintf(b, ` data-edit-kinds="%s"`, escape(strings.Join(blk.EditKinds, " ")))
		}
		b.WriteString(` title="此段已按声明的改动修改"`)
	}

	// 首行缩进按块修正。
	//
	// 中文作者习惯在段首敲两个全角空格来做缩进，解析器逐字保留它们是正确
	// 的（那是文件内容的一部分）。若样式再叠加「首行缩进2字」，这一段就会
	// 缩进 4 字。这里把样式要求的缩进量减去文本自带的缩进量，得到的才是
	// 作者真正想要的两字。
	//
	// 只在二者不同时才输出内联覆盖，这样规则里仍是 spec 的值，覆盖处一眼
	// 可见，也不会给绝大多数段落平白增加一个属性。
	if st, ok := spec.Style(blk.Role); ok {
		want := st.FirstLineIndentChars
		got := gbstandard.EffectiveFirstLineIndent(st, blk.Text)
		if want != got {
			fmt.Fprintf(b, " style=\"text-indent:%s\"", pt(got*spec.Page.CharPitchPT()))
			// 文本自带缩进时，行容量也要相应变化，否则分页估算会把这段
			// 多算一行。
		}
	}
	b.WriteString(">")

	if blk.AutoNumber != "" {
		// 层次序数 is assembler-computed scaffolding; the node's own text
		// follows it untouched.
		b.WriteString("<span class=\"gd-autonum\">")
		b.WriteString(escape(blk.AutoNumber))
		b.WriteString("</span>")
	}

	switch {
	case len(blk.Rows) > 0:
		writeTable(b, blk.Rows)
	case blk.Image != nil:
		writeImage(b, blk)
	case blk.Text != "":
		writeText(b, blk)
	}
	b.WriteString("</div>\n")
}

// writeText writes the block's text, preserving inline bold/italic when the
// runs reproduce the text exactly. Anything else falls back to the whole
// verbatim string so no character can be lost in translation.
func writeText(b *bytes.Buffer, blk docmodel.OutBlock) {
	if len(blk.Runs) > 0 && runsMatchText(blk.Runs, blk.Text) {
		for _, r := range blk.Runs {
			if r.Text == "" {
				continue
			}
			var styles []string
			if r.Bold {
				styles = append(styles, "font-weight:700")
			}
			if r.Italic {
				styles = append(styles, "font-style:italic")
			}
			if len(styles) == 0 {
				b.WriteString(escape(r.Text))
				continue
			}
			b.WriteString("<span style=\"")
			b.WriteString(strings.Join(styles, ";"))
			b.WriteString("\">")
			b.WriteString(escape(r.Text))
			b.WriteString("</span>")
		}
		return
	}
	b.WriteString(escape(blk.Text))
}

func runsMatchText(runs []docmodel.Run, text string) bool {
	var joined strings.Builder
	for _, r := range runs {
		joined.WriteString(r.Text)
	}
	return joined.String() == text
}

// writeTable emits a source table. Header emphasis is not claimed because the
// render model does not carry it; the text itself is verbatim.
func writeTable(b *bytes.Buffer, rows [][]string) {
	b.WriteString("<table class=\"gd-paper-table\">\n")
	for _, row := range rows {
		b.WriteString("<tr>")
		for _, cell := range row {
			b.WriteString("<td>")
			b.WriteString(escape(cell))
			b.WriteString("</td>")
		}
		b.WriteString("</tr>\n")
	}
	b.WriteString("</table>\n")
}

// writeImage references an embedded picture through the sibling images
// endpoint. The URL is relative on purpose: the preview is served from
// /api/documents/{id}/preview, so "images/{node}" resolves to
// /api/documents/{id}/images/{node} without the renderer knowing the document id.
func writeImage(b *bytes.Buffer, blk docmodel.OutBlock) {
	alt := ""
	if blk.Image != nil && blk.Image.Name != "" {
		alt = blk.Image.Name
	} else if blk.Text != "" {
		alt = blk.Text
	}
	fmt.Fprintf(b, "<img class=\"gd-image\" src=\"images/%s\" alt=\"%s\">",
		url.PathEscape(blk.NodeID), escape(alt))
}

// ---------------------------------------------------------------- helpers

// escape makes text safe for HTML text and attribute positions. Source text is
// shown exactly as parsed, so escaping happens last and nothing is trimmed.
func escape(s string) string { return html.EscapeString(s) }

// cssIdent reduces a role name to characters that are legal in a CSS class
// name and a custom property name.
func cssIdent(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	if b.Len() == 0 {
		return "unknown"
	}
	return b.String()
}

// widthUnits measures text in 字: full-width glyphs count one, everything else
// half. Tabs advance to the next tab stop, approximated as one 字.
func widthUnits(s string) float64 {
	units := 0.0
	for _, r := range s {
		switch {
		case r == '\t':
			units++
		case isFullWidth(r):
			units++
		default:
			units += 0.5
		}
	}
	return units
}

// isFullWidth reports whether a rune is rendered one 字 wide in Chinese
// typography (CJK ideographs, kana, hangul and full-width forms).
func isFullWidth(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115F, // Hangul Jamo
		r >= 0x2E80 && r <= 0x303E, // CJK radicals, Kangxi, punctuation
		r >= 0x3041 && r <= 0x33FF, // kana, CJK compatibility
		r >= 0x3400 && r <= 0x4DBF, // CJK ext A
		r >= 0x4E00 && r <= 0x9FFF, // CJK unified ideographs
		r >= 0xA000 && r <= 0xA4CF, // Yi
		r >= 0xAC00 && r <= 0xD7A3, // Hangul syllables
		r >= 0xF900 && r <= 0xFAFF, // CJK compatibility ideographs
		r >= 0xFE30 && r <= 0xFE6F, // CJK compatibility forms
		r >= 0xFF00 && r <= 0xFF60, // full-width forms
		r >= 0xFFE0 && r <= 0xFFE6,
		r >= 0x20000 && r <= 0x3FFFD: // CJK ext B and beyond
		return true
	}
	return false
}

// sortedRoles returns every role the spec defines, in canonical document order
// first and then any extra roles alphabetically, so the generated stylesheet is
// byte-for-byte deterministic regardless of Go's map iteration order.
func sortedRoles(spec gbstandard.Spec) []docmodel.Role {
	seen := map[docmodel.Role]bool{}
	roles := make([]docmodel.Role, 0, len(spec.Styles))
	for _, r := range docmodel.AllRoles() {
		if _, ok := spec.Styles[r]; ok {
			roles = append(roles, r)
			seen[r] = true
		}
	}
	extra := make([]string, 0, len(spec.Styles))
	for r := range spec.Styles {
		if !seen[r] {
			extra = append(extra, string(r))
		}
	}
	sort.Strings(extra)
	for _, r := range extra {
		roles = append(roles, docmodel.Role(r))
	}
	return roles
}
