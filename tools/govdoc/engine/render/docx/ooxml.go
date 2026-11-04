package docx

import (
	"encoding/xml"
	"fmt"
	"math"
	"strings"

	"opentools/tools/govdoc/engine/docmodel"
	"opentools/tools/govdoc/engine/gbstandard"
)

// This file builds the XML text of the OOXML parts. It deliberately keeps the
// unit conversions in one place: every number it writes is derived from
// gbstandard.Spec, and the only literal constants below are unit conversion
// factors (mm→twip, pt→twip, pt→half-point, char→hundredth-of-a-char) plus the
// OOXML schema vocabulary itself.

// ---------------------------------------------------------------- namespaces

const (
	nsW    = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
	nsR    = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
	nsWP   = "http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing"
	nsA    = "http://schemas.openxmlformats.org/drawingml/2006/main"
	nsPic  = "http://schemas.openxmlformats.org/drawingml/2006/picture"
	nsCT   = "http://schemas.openxmlformats.org/package/2006/content-types"
	nsPR   = "http://schemas.openxmlformats.org/package/2006/relationships"
	nsCP   = "http://schemas.openxmlformats.org/package/2006/metadata/core-properties"
	nsDC   = "http://purl.org/dc/elements/1.1/"
	nsDCT  = "http://purl.org/dc/terms/"
	nsXSI  = "http://www.w3.org/2001/XMLSchema-instance"
	nsExt  = "http://schemas.openxmlformats.org/officeDocument/2006/extended-properties"
	nsVT   = "http://schemas.openxmlformats.org/officeDocument/2006/docPropsVTypes"
	nsW10  = "urn:schemas-microsoft-com:office:word"
	nsWord = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
)

// Relationship type URIs.
const (
	relOfficeDocument = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument"
	relCoreProps      = "http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties"
	relAppProps       = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/extended-properties"
	relStyles         = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles"
	relSettings       = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/settings"
	relFooter         = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/footer"
	relImage          = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/image"
)

const xmlDecl = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"

// ---------------------------------------------------------------- units

// twipsPerMM: 1in = 25.4mm = 1440 twips.
const twipsPerMM = 1440.0 / 25.4

// emuPerTwip: 1 twip = 635 EMU (914400 EMU/inch ÷ 1440 twips/inch).
const emuPerTwip = 635

// mmToTwips converts millimetres (the unit gbstandard uses) to twips (the
// unit w:pgSz/w:pgMar use).
func mmToTwips(mm float64) int { return int(math.Round(mm * twipsPerMM)) }

// ptToTwips converts points to twentieths of a point.
func ptToTwips(pt float64) int { return int(math.Round(pt * 20)) }

// ptToHalfPoints converts points to the half-points w:sz uses.
func ptToHalfPoints(pt float64) int { return int(math.Round(pt * 2)) }

// charsToHundredths converts a 字数 count to the hundredths-of-a-character
// unit OOXML uses for w:firstLineChars and friends (1 字 = 100).
func charsToHundredths(chars float64) int { return int(math.Round(chars * 100)) }

// esc escapes text for use in XML character data or an attribute value.
// encoding/xml's EscapeText covers <, >, &, quotes and the whitespace
// characters Word cares about (\t, \n, \r) as numeric references.
func esc(s string) string {
	var b strings.Builder
	if err := xml.EscapeText(&b, []byte(s)); err != nil {
		// strings.Builder never fails; keep the defensive branch total.
		return ""
	}
	return b.String()
}

// ---------------------------------------------------------------- page

func (r *render) textWidthTwips() int {
	return mmToTwips(r.spec.Page.TextWidthMM())
}

func (r *render) textHeightTwips() int {
	return mmToTwips(r.spec.Page.TextHeightMM())
}

// sectPrXML writes the section properties: page size, the four 页边距, the
// footer reference (页码) and the 22-line document grid.
func (r *render) sectPrXML(footerRelID string) string {
	var b strings.Builder
	b.WriteString("<w:sectPr>")
	if footerRelID != "" {
		fmt.Fprintf(&b, `<w:footerReference w:type="default" r:id="%s"/>`, esc(footerRelID))
	}
	fmt.Fprintf(&b, `<w:pgSz w:w="%d" w:h="%d"/>`,
		mmToTwips(r.spec.Page.WidthMM), mmToTwips(r.spec.Page.HeightMM))
	fmt.Fprintf(&b, `<w:pgMar w:top="%d" w:right="%d" w:bottom="%d" w:left="%d" w:header="%d" w:footer="%d" w:gutter="0"/>`,
		mmToTwips(r.spec.Page.MarginTopMM),
		mmToTwips(r.spec.Page.MarginRightMM),
		mmToTwips(r.spec.Page.MarginBottomMM),
		mmToTwips(r.spec.Page.MarginLeftMM),
		mmToTwips(r.spec.Page.HeaderMM),
		mmToTwips(r.spec.Page.FooterMM))
	b.WriteString(`<w:pgNumType w:start="1"/>`)
	// The 版心 is 每面 LinesPerPage 行 × 每行 CharsPerLine 字. Both come from
	// the spec, so the grid is whatever the page setup implies rather than the
	// familiar 22×28.
	//
	// The character half needs care. GB/T 9704-2012's own figures do not quite
	// agree — 28 × 三号(16pt) = 448pt = 158.0mm against a 156mm 版心 — so 28
	// full-width characters only fit if their advance is condensed slightly.
	// That is what Word's 文档网格 does in the 公文 templates issued with the
	// standard, and gbstandard models it as PageSetup.CharCondensePT. 版心宽 is
	// treated as authoritative and the grid absorbs the difference, which is
	// why the requested font size is never reduced to make the line fit.
	//
	// w:charSpace is the grid's condensation in 1/4096 em, so a per-character
	// condensation of C points on a size-S font is round(C/S*4096).
	if lp := r.spec.Page.LineSpacingPT(); lp > 0 {
		gridType, charSpace := "lines", 0
		if body := r.spec.MustStyle(docmodel.RoleBody).Size; !r.spec.Page.FitsNaturalSpacing(body) {
			if sizePT := body.Points(); sizePT > 0 {
				gridType = "linesAndChars"
				charSpace = int(math.Round(r.spec.Page.CharCondensePT(body) / sizePT * 4096))
			}
		}
		fmt.Fprintf(&b, `<w:docGrid w:type="%s" w:linePitch="%d" w:charSpace="%d"/>`,
			gridType, ptToTwips(lp), charSpace)
	}
	b.WriteString("</w:sectPr>")
	return b.String()
}

// ---------------------------------------------------------------- style plumbing

// styleID maps a role to a valid OOXML styleId, e.g. heading1 →
// "GovdocHeading1", copy_number → "GovdocCopyNumber".
func styleID(role docmodel.Role) string {
	var b strings.Builder
	b.WriteString("Govdoc")
	for _, part := range strings.Split(string(role), "_") {
		if part == "" {
			continue
		}
		b.WriteString(strings.ToUpper(part[:1]))
		b.WriteString(part[1:])
	}
	return b.String()
}

func styleName(st gbstandard.ParaStyle, role docmodel.Role) string {
	if st.Name != "" {
		return st.Name
	}
	return string(role)
}

// runProps writes the <w:rPr> of one run. Fonts, size, weight and colour all
// come from the spec's ParaStyle; an unknown 字号 (Points()==0) emits no w:sz
// at all rather than a guessed size.
func runProps(st gbstandard.ParaStyle, bold, italic bool) string {
	var b strings.Builder
	b.WriteString("<w:rPr>")

	latin := st.LatinFont
	if latin == "" {
		latin = st.Font
	}
	if latin != "" || st.Font != "" {
		b.WriteString("<w:rFonts")
		if latin != "" {
			fmt.Fprintf(&b, ` w:ascii="%s" w:hAnsi="%s" w:cs="%s"`, esc(latin), esc(latin), esc(latin))
		}
		if st.Font != "" {
			fmt.Fprintf(&b, ` w:eastAsia="%s"`, esc(st.Font))
		}
		b.WriteString("/>")
	}
	if bold || st.Bold {
		b.WriteString("<w:b/><w:bCs/>")
	}
	if italic {
		b.WriteString("<w:i/><w:iCs/>")
	}
	if st.Color != "" {
		fmt.Fprintf(&b, `<w:color w:val="%s"/>`, esc(st.Color))
	}
	if pt := st.Size.Points(); pt > 0 {
		hp := ptToHalfPoints(pt)
		fmt.Fprintf(&b, `<w:sz w:val="%d"/><w:szCs w:val="%d"/>`, hp, hp)
	}

	b.WriteString("</w:rPr>")
	return b.String()
}

// paraProps writes the <w:pPr> of one paragraph. Element order follows the
// CT_PPr schema sequence — pStyle first, then keepNext, pageBreakBefore, pBdr,
// spacing, ind, jc, outlineLvl — because Word rejects a part that reorders it,
// and a w:pStyle outside w:pPr is simply ignored by strict readers.
//
// text is the paragraph's final text, needed so the first-line indent can be
// reduced by whatever indentation the text already carries.
func (r *render) paraProps(st gbstandard.ParaStyle, pageBreak bool, styleID, text string) string {
	var b strings.Builder
	b.WriteString("<w:pPr>")
	if styleID != "" {
		fmt.Fprintf(&b, `<w:pStyle w:val="%s"/>`, esc(styleID))
	}
	if st.KeepWithNext {
		b.WriteString("<w:keepNext/>")
	}
	if pageBreak || st.PageBreakBefore {
		b.WriteString("<w:pageBreakBefore/>")
	}
	b.WriteString(borderXML(st.BorderTop, st.BorderBottom))
	b.WriteString(r.spacingXML(st))
	b.WriteString(indXML(st, text))
	if st.Align != "" {
		fmt.Fprintf(&b, `<w:jc w:val="%s"/>`, esc(st.Align.OOXMLValue()))
	}
	if st.OutlineLevel > 0 {
		// spec.OutlineLevel is 1-based (一、 is 1); w:outlineLvl is 0-based.
		fmt.Fprintf(&b, `<w:outlineLvl w:val="%d"/>`, st.OutlineLevel-1)
	}
	b.WriteString("</w:pPr>")
	return b.String()
}

// lineHeightPT is the height of one line for 空N行 computations: the style's
// own 固定行距 when it has one, else the page's grid line height, else the
// font size. Never a literal.
func (r *render) lineHeightPT(st gbstandard.ParaStyle) float64 {
	if st.LineSpacingPT > 0 {
		return st.LineSpacingPT
	}
	if lp := r.spec.Page.LineSpacingPT(); lp > 0 {
		return lp
	}
	return st.Size.Points()
}

// spacingXML writes <w:spacing>. SpaceBeforeLines/SpaceAfterLines are
// documented as hundredths of a line, which is exactly OOXML's
// w:beforeLines/w:afterLines unit, so they are written through unchanged; the
// absolute w:before/w:after twin is derived from the spec's line height so
// readers that ignore the *Lines attributes still show the 空行.
func (r *render) spacingXML(st gbstandard.ParaStyle) string {
	var attrs []string

	if st.SpaceBeforeLines != 0 {
		attrs = append(attrs, fmt.Sprintf(`w:beforeLines="%d"`, int(math.Round(st.SpaceBeforeLines))))
		attrs = append(attrs, fmt.Sprintf(`w:before="%d"`, ptToTwips(st.SpaceBeforeLines/100*r.lineHeightPT(st))))
	}
	if st.SpaceAfterLines != 0 {
		attrs = append(attrs, fmt.Sprintf(`w:afterLines="%d"`, int(math.Round(st.SpaceAfterLines))))
		attrs = append(attrs, fmt.Sprintf(`w:after="%d"`, ptToTwips(st.SpaceAfterLines/100*r.lineHeightPT(st))))
	}

	switch {
	case st.LineSpacingPT > 0:
		// A positive LineSpacingPT is a height in points — the 固定行距 of the
		// 22-line grid, and equally the 4mm height the 红色分隔线 style asks for.
		// It must be written as lineRule="exact", because lineRule="auto"
		// reinterprets the number as 240ths of a line.
		attrs = append(attrs, fmt.Sprintf(`w:line="%d"`, ptToTwips(st.LineSpacingPT)), `w:lineRule="exact"`)
	case st.LineRule == gbstandard.LineAuto && len(attrs) > 0:
		// w:lineRule only carries meaning beside a w:line value, so it is
		// attached to an existing spacing element rather than emitted alone.
		attrs = append(attrs, `w:lineRule="auto"`)
	}

	if len(attrs) == 0 {
		return ""
	}
	return "<w:spacing " + strings.Join(attrs, " ") + "/>"
}

// indXML writes <w:ind>. 公文 indentation is specified in 字, so the character
// unit attribute is authoritative (w:firstLineChars="200" = 空二字) and the
// point-valued twin is emitted for readers that only understand
// w:firstLine/w:left/w:right. The twin is chars × the style's 字号, which is
// exactly what makes 空二字 scale with the font.
//
// firstLineChars is the *effective* first-line indent, not the style's: a
// paragraph whose own text already begins with two 全角空格 (how Chinese
// authors indent) must not be indented a second time. text is the block's
// final text, so the correction follows any declared edits.
func indXML(st gbstandard.ParaStyle, text string) string {
	fontPT := st.Size.Points()
	var attrs []string
	add := func(chars float64, charsAttr, ptAttr string) {
		if chars == 0 {
			return
		}
		attrs = append(attrs, fmt.Sprintf(`%s="%d"`, charsAttr, charsToHundredths(chars)))
		if fontPT > 0 {
			attrs = append(attrs, fmt.Sprintf(`w:%s="%d"`, ptAttr, ptToTwips(chars*fontPT)))
		}
	}
	add(gbstandard.EffectiveFirstLineIndent(st, text), "w:firstLineChars", "firstLine")
	add(st.LeftIndentChars, "w:leftChars", "left")
	add(st.RightIndentChars, "w:rightChars", "right")
	add(st.HangingIndentChars, "w:hangingChars", "hanging")
	if len(attrs) == 0 {
		return ""
	}
	return "<w:ind " + strings.Join(attrs, " ") + "/>"
}

// borderXML writes <w:pBdr> from the spec's BorderRule values. A rule without
// a size is treated as absent rather than given an invented width.
func borderXML(top, bottom *gbstandard.BorderRule) string {
	if !borderUsable(top) && !borderUsable(bottom) {
		return ""
	}
	var b strings.Builder
	b.WriteString("<w:pBdr>")
	write := func(tag string, br *gbstandard.BorderRule) {
		if !borderUsable(br) {
			return
		}
		color := br.Color
		if color == "" {
			color = "auto"
		}
		fmt.Fprintf(&b, `<w:%s w:val="single" w:sz="%d" w:space="%d" w:color="%s"/>`,
			tag, br.SizeEighthPT, int(math.Round(br.SpacePT)), esc(color))
	}
	write("top", top)
	write("bottom", bottom)
	b.WriteString("</w:pBdr>")
	return b.String()
}

func borderUsable(br *gbstandard.BorderRule) bool {
	return br != nil && br.SizeEighthPT > 0
}

// run writes one text run.
func (r *render) run(st gbstandard.ParaStyle, text string, bold, italic bool) string {
	if text == "" {
		return ""
	}
	return "<w:r>" + runProps(st, bold, italic) +
		`<w:t xml:space="preserve">` + esc(text) + "</w:t></w:r>"
}

// paragraphXML writes one text paragraph. The 层次序数, when the assembler
// supplied one, is a run of its own in front of the body text so that the
// source text always occupies an identifiable run (or run sequence) verbatim.
func (r *render) paragraphXML(blk docmodel.OutBlock, st gbstandard.ParaStyle) string {
	var b strings.Builder
	b.WriteString("<w:p>")
	b.WriteString(r.paraProps(st, blk.PageBreakBefore, r.styleIDFor(blk.Role), blk.Text))

	if blk.AutoNumber != "" {
		b.WriteString(r.run(st, blk.AutoNumber, false, false))
	}
	if len(blk.Runs) > 0 {
		for _, run := range blk.Runs {
			b.WriteString(r.run(st, run.Text, run.Bold, run.Italic))
		}
	} else {
		b.WriteString(r.run(st, blk.Text, false, false))
	}
	b.WriteString("</w:p>")
	return b.String()
}

// ---------------------------------------------------------------- tables

// tableBorders picks the rule weight for a real 公文 table from the spec. The
// standard does not define a table style, so the 版记 rules (which are the
// standard's own ±0.75pt black rules) are used as the table grid, falling back
// to the role's own border and then to no border at all — never to a
// hard-coded width.
func (r *render) tableBorders(st gbstandard.ParaStyle) string {
	br := st.BorderTop
	if !borderUsable(br) {
		br = st.BorderBottom
	}
	if !borderUsable(br) {
		if cc, ok := r.spec.Styles[docmodel.RoleCC]; ok {
			br = cc.BorderTop
		}
	}
	if !borderUsable(br) {
		if pr, ok := r.spec.Styles[docmodel.RolePrinter]; ok {
			br = pr.BorderBottom
		}
	}
	if !borderUsable(br) {
		return ""
	}
	color := br.Color
	if color == "" {
		color = "auto"
	}
	edge := fmt.Sprintf(`w:val="single" w:sz="%d" w:space="%d" w:color="%s"`,
		br.SizeEighthPT, int(math.Round(br.SpacePT)), esc(color))
	var b strings.Builder
	b.WriteString("<w:tblBorders>")
	for _, tag := range []string{"top", "left", "bottom", "right", "insideH", "insideV"} {
		fmt.Fprintf(&b, "<w:%s %s/>", tag, edge)
	}
	b.WriteString("</w:tblBorders>")
	return b.String()
}

// tableXML writes a real <w:tbl>. Column widths split the 版心 evenly, so the
// table is exactly as wide as the text column without any literal width.
func (r *render) tableXML(blk docmodel.OutBlock, st gbstandard.ParaStyle) string {
	rows := blk.Rows
	cols := 0
	for _, row := range rows {
		if len(row) > cols {
			cols = len(row)
		}
	}
	if cols == 0 {
		return ""
	}

	total := r.textWidthTwips()
	colW := make([]int, cols)
	base, rem := total/cols, total%cols
	for i := range colW {
		colW[i] = base
		if i < rem {
			colW[i]++
		}
	}

	// Cell text sits in the same typeface and size as the role asks for, but
	// without the 空二字 paragraph indentation, which would misalign a cell.
	cellSt := st
	cellSt.FirstLineIndentChars, cellSt.LeftIndentChars = 0, 0
	cellSt.RightIndentChars, cellSt.HangingIndentChars = 0, 0

	var b strings.Builder
	b.WriteString("<w:tbl><w:tblPr>")
	fmt.Fprintf(&b, `<w:tblW w:w="%d" w:type="dxa"/>`, total)
	if st.Align != "" {
		fmt.Fprintf(&b, `<w:jc w:val="%s"/>`, esc(st.Align.OOXMLValue()))
	}
	b.WriteString(r.tableBorders(st))
	b.WriteString(`<w:tblLayout w:type="fixed"/>`)
	b.WriteString("</w:tblPr><w:tblGrid>")
	for _, w := range colW {
		fmt.Fprintf(&b, `<w:gridCol w:w="%d"/>`, w)
	}
	b.WriteString("</w:tblGrid>")

	for _, row := range rows {
		b.WriteString("<w:tr><w:trPr><w:cantSplit/></w:trPr>")
		for i := 0; i < cols; i++ {
			text := ""
			if i < len(row) {
				text = row[i]
			}
			fmt.Fprintf(&b, `<w:tc><w:tcPr><w:tcW w:w="%d" w:type="dxa"/></w:tcPr>`, colW[i])
			b.WriteString("<w:p>")
			b.WriteString(r.paraProps(cellSt, false, "", ""))
			b.WriteString(r.run(cellSt, text, false, false))
			b.WriteString("</w:p></w:tc>")
		}
		b.WriteString("</w:tr>")
	}
	b.WriteString("</w:tbl>")
	return b.String()
}

// ---------------------------------------------------------------- images

// imageParagraphXML writes a paragraph holding one inline picture. The picture
// is scaled to the 版心 width and capped by the 版心 height, so the geometry
// comes from spec.Page rather than from a literal.
func (r *render) imageParagraphXML(blk docmodel.OutBlock, st gbstandard.ParaStyle) (string, error) {
	img, err := r.addImage(blk.Image)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("<w:p>")
	b.WriteString(r.paraProps(st, blk.PageBreakBefore, r.styleIDFor(blk.Role), blk.Text))
	b.WriteString("<w:r>")
	b.WriteString(runProps(st, false, false))
	b.WriteString(r.drawingXML(img))
	b.WriteString("</w:r></w:p>")
	return b.String(), nil
}

// styleIDFor returns the style id a paragraph should reference, or "" when the
// spec defines no style for the role (in which case MustStyle's 正文 fallback
// would otherwise mislabel the paragraph).
func (r *render) styleIDFor(role docmodel.Role) string {
	if _, ok := r.spec.Style(role); !ok {
		return ""
	}
	return styleID(role)
}

func (r *render) drawingXML(img embeddedImage) string {
	id := r.nextDrawingID()
	var b strings.Builder
	b.WriteString("<w:drawing>")
	fmt.Fprintf(&b, `<wp:inline distT="0" distB="0" distL="0" distR="0">`)
	fmt.Fprintf(&b, `<wp:extent cx="%d" cy="%d"/>`, img.cx, img.cy)
	b.WriteString(`<wp:effectExtent l="0" t="0" r="0" b="0"/>`)
	fmt.Fprintf(&b, `<wp:docPr id="%d" name="%s"/>`, id, esc(img.name))
	b.WriteString(`<wp:cNvGraphicFramePr><a:graphicFrameLocks xmlns:a="` + nsA + `" noChangeAspect="1"/></wp:cNvGraphicFramePr>`)
	b.WriteString(`<a:graphic xmlns:a="` + nsA + `">`)
	b.WriteString(`<a:graphicData uri="` + nsPic + `">`)
	b.WriteString(`<pic:pic xmlns:pic="` + nsPic + `">`)
	fmt.Fprintf(&b, `<pic:nvPicPr><pic:cNvPr id="%d" name="%s"/><pic:cNvPicPr/></pic:nvPicPr>`, id, esc(img.name))
	fmt.Fprintf(&b, `<pic:blipFill><a:blip r:embed="%s"/><a:stretch><a:fillRect/></a:stretch></pic:blipFill>`, esc(img.relID))
	fmt.Fprintf(&b, `<pic:spPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="%d" cy="%d"/></a:xfrm>`, img.cx, img.cy)
	b.WriteString(`<a:prstGeom prst="rect"><a:avLst/></a:prstGeom></pic:spPr>`)
	b.WriteString("</pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing>")
	return b.String()
}

// ---------------------------------------------------------------- styles part

// stylesXML writes word/styles.xml: a neutral doc-default run properties
// block, plus one paragraph style per role so a user who opens the file in
// Word can see and re-apply 公文 styles by name.
//
// pPrDefault is deliberately NOT taken from the body style: docDefaults apply
// to every paragraph that does not override them, and 正文's 空二字 would then
// silently indent table cells and the 页码 footer as well.
func (r *render) stylesXML() string {
	base := r.spec.MustStyle(docmodel.RoleBody)
	defaultRun := gbstandard.ParaStyle{
		Font:      base.Font,
		LatinFont: base.LatinFont,
		Size:      base.Size,
		Color:     base.Color,
	}

	var b strings.Builder
	b.WriteString(xmlDecl)
	b.WriteString(`<w:styles xmlns:w="` + nsW + `" xmlns:r="` + nsR + `">`)
	b.WriteString("<w:docDefaults>")
	b.WriteString("<w:rPrDefault>" + runProps(defaultRun, false, false) + "</w:rPrDefault>")
	b.WriteString("<w:pPrDefault><w:pPr/></w:pPrDefault>")
	b.WriteString("</w:docDefaults>")
	b.WriteString(`<w:style w:type="paragraph" w:default="1" w:styleId="Normal">` +
		`<w:name w:val="Normal"/><w:qFormat/></w:style>`)
	b.WriteString(`<w:style w:type="character" w:default="1" w:styleId="DefaultParagraphFont">` +
		`<w:name w:val="Default Paragraph Font"/><w:semiHidden/><w:unhideWhenUsed/></w:style>`)

	for _, role := range docmodel.AllRoles() {
		st, ok := r.spec.Styles[role]
		if !ok {
			continue
		}
		fmt.Fprintf(&b, `<w:style w:type="paragraph" w:styleId="%s">`, styleID(role))
		fmt.Fprintf(&b, `<w:name w:val="%s"/>`, esc(styleName(st, role)))
		b.WriteString(`<w:basedOn w:val="Normal"/><w:qFormat/>`)
		// A style's own pPr must not carry w:pStyle (that would be a
		// self-reference), so no style id is passed here.
		b.WriteString(r.paraProps(st, false, "", ""))
		b.WriteString(runProps(st, false, false))
		b.WriteString("</w:style>")
	}
	b.WriteString("</w:styles>")
	return b.String()
}

// ---------------------------------------------------------------- footer part

// footerXML writes the 页码 paragraph: 一字线 on each side of a PAGE field, in
// the spec's PageNumberSize/PageNumberFont (标准 §7.3.6: 四号半角宋体).
func (r *render) footerXML() string {
	pn := gbstandard.ParaStyle{
		Font:      r.spec.PageNumberFont,
		LatinFont: r.spec.PageNumberFont,
		Size:      r.spec.PageNumberSize,
		Color:     gbstandard.ColorBlack,
		Align:     gbstandard.AlignCenter,
	}

	var b strings.Builder
	b.WriteString(xmlDecl)
	b.WriteString(`<w:ftr xmlns:w="` + nsW + `" xmlns:r="` + nsR + `">`)
	b.WriteString("<w:p>")
	b.WriteString("<w:pPr>")
	fmt.Fprintf(&b, `<w:jc w:val="%s"/>`, esc(pn.Align.OOXMLValue()))
	b.WriteString("</w:pPr>")

	dash := r.spec.PageNumberDash
	if dash != "" {
		b.WriteString(r.run(pn, dash, false, false))
		b.WriteString(r.run(pn, " ", false, false))
	}
	// A run-level PAGE field rather than w:fldSimple, so the field's runs stay
	// direct children of the paragraph and every reader (including python-docx)
	// sees the number text.
	b.WriteString("<w:r>" + runProps(pn, false, false) + `<w:fldChar w:fldCharType="begin"/></w:r>`)
	b.WriteString("<w:r>" + runProps(pn, false, false) + `<w:instrText xml:space="preserve"> PAGE </w:instrText></w:r>`)
	b.WriteString("<w:r>" + runProps(pn, false, false) + `<w:fldChar w:fldCharType="separate"/></w:r>`)
	b.WriteString("<w:r>" + runProps(pn, false, false) + `<w:t>1</w:t></w:r>`)
	b.WriteString("<w:r>" + runProps(pn, false, false) + `<w:fldChar w:fldCharType="end"/></w:r>`)
	if dash != "" {
		b.WriteString(r.run(pn, " ", false, false))
		b.WriteString(r.run(pn, dash, false, false))
	}
	b.WriteString("</w:p></w:ftr>")
	return b.String()
}

// ---------------------------------------------------------------- package parts

// contentTypesXML lists the parts, and the image defaults only when an image
// of that type is actually embedded.
func contentTypesXML(imageDefaults map[string]string) string {
	var b strings.Builder
	b.WriteString(xmlDecl)
	b.WriteString(`<Types xmlns="` + nsCT + `">`)
	b.WriteString(`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>`)
	b.WriteString(`<Default Extension="xml" ContentType="application/xml"/>`)

	exts := make([]string, 0, len(imageDefaults))
	for ext := range imageDefaults {
		exts = append(exts, ext)
	}
	sortStrings(exts)
	for _, ext := range exts {
		fmt.Fprintf(&b, `<Default Extension="%s" ContentType="%s"/>`, esc(ext), esc(imageDefaults[ext]))
	}

	overrides := []struct{ part, ct string }{
		{"/word/document.xml", "application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"},
		{"/word/styles.xml", "application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"},
		{"/word/settings.xml", "application/vnd.openxmlformats-officedocument.wordprocessingml.settings+xml"},
		{"/word/footer1.xml", "application/vnd.openxmlformats-officedocument.wordprocessingml.footer+xml"},
		{"/docProps/core.xml", "application/vnd.openxmlformats-package.core-properties+xml"},
		{"/docProps/app.xml", "application/vnd.openxmlformats-officedocument.extended-properties+xml"},
	}
	for _, o := range overrides {
		fmt.Fprintf(&b, `<Override PartName="%s" ContentType="%s"/>`, esc(o.part), esc(o.ct))
	}
	b.WriteString("</Types>")
	return b.String()
}

func packageRelsXML() string {
	var b strings.Builder
	b.WriteString(xmlDecl)
	b.WriteString(`<Relationships xmlns="` + nsPR + `">`)
	fmt.Fprintf(&b, `<Relationship Id="rId1" Type="%s" Target="word/document.xml"/>`, relOfficeDocument)
	fmt.Fprintf(&b, `<Relationship Id="rId2" Type="%s" Target="docProps/core.xml"/>`, relCoreProps)
	fmt.Fprintf(&b, `<Relationship Id="rId3" Type="%s" Target="docProps/app.xml"/>`, relAppProps)
	b.WriteString("</Relationships>")
	return b.String()
}

// documentRelsXML wires the parts and media the document references.
func documentRelsXML(rels []relationship) string {
	var b strings.Builder
	b.WriteString(xmlDecl)
	b.WriteString(`<Relationships xmlns="` + nsPR + `">`)
	for _, rel := range rels {
		fmt.Fprintf(&b, `<Relationship Id="%s" Type="%s" Target="%s"/>`, esc(rel.id), esc(rel.typ), esc(rel.target))
	}
	b.WriteString("</Relationships>")
	return b.String()
}

// corePropsXML uses a fixed timestamp so that rendering the same document
// twice produces byte-identical bytes.
func corePropsXML(title, creator string) string {
	const stamp = "2024-01-01T00:00:00Z"
	var b strings.Builder
	b.WriteString(xmlDecl)
	b.WriteString(`<cp:coreProperties xmlns:cp="` + nsCP + `" xmlns:dc="` + nsDC + `" xmlns:dcterms="` + nsDCT +
		`" xmlns:dcmitype="http://purl.org/dc/dcmitype/" xmlns:xsi="` + nsXSI + `">`)
	fmt.Fprintf(&b, `<dc:title>%s</dc:title>`, esc(title))
	fmt.Fprintf(&b, `<dc:creator>%s</dc:creator>`, esc(creator))
	fmt.Fprintf(&b, `<cp:lastModifiedBy>%s</cp:lastModifiedBy>`, esc(creator))
	fmt.Fprintf(&b, `<dcterms:created xsi:type="dcterms:W3CDTF">%s</dcterms:created>`, stamp)
	fmt.Fprintf(&b, `<dcterms:modified xsi:type="dcterms:W3CDTF">%s</dcterms:modified>`, stamp)
	b.WriteString("</cp:coreProperties>")
	return b.String()
}

func appPropsXML() string {
	var b strings.Builder
	b.WriteString(xmlDecl)
	b.WriteString(`<Properties xmlns="` + nsExt + `" xmlns:vt="` + nsVT + `">`)
	b.WriteString(`<Application>GovDoc</Application>`)
	b.WriteString(`<DocSecurity>0</DocSecurity>`)
	b.WriteString(`<AppVersion>1.0</AppVersion>`)
	b.WriteString("</Properties>")
	return b.String()
}

// settingsXML keeps the tools' defaults minimal. compressPunctuation is the
// setting 公文 typesetting wants (编辑/印刷時按标点挤压), and compatibilityMode
// 15 makes Word treat the file as a modern .docx.
func settingsXML() string {
	var b strings.Builder
	b.WriteString(xmlDecl)
	b.WriteString(`<w:settings xmlns:w="` + nsW + `" xmlns:r="` + nsR + `" xmlns:m="http://schemas.openxmlformats.org/officeDocument/2006/math" xmlns:w10="` + nsW10 + `">`)
	b.WriteString(`<w:zoom w:percent="100"/>`)
	b.WriteString(`<w:characterSpacingControl w:val="compressPunctuation"/>`)
	b.WriteString(`<w:compat>`)
	b.WriteString(`<w:compatSetting w:name="compatibilityMode" w:uri="http://schemas.microsoft.com/office/word" w:val="15"/>`)
	b.WriteString(`</w:compat>`)
	b.WriteString("</w:settings>")
	return b.String()
}
