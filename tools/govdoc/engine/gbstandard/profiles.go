package gbstandard

import (
	"opentools/tools/govdoc/engine/docmodel"
)

// This file provides formatting profiles other than 公文.
//
// # Why profiles and not new renderers
//
// The tool's pipeline is document-type agnostic: it parses a document into
// paragraphs, arranges them into a structure, and renders roles through a style
// table. Everything that makes a document *look* like a 公文 — page size,
// margins, the 22×28 grid, which typeface is 标题 versus 正文, how lines are
// spaced — lives in Spec. So another document type is another Spec, not another
// renderer.
//
// The role vocabulary (标题/正文/主送机关/抄送…) is 公文-flavoured, which is why
// these profiles are built from the same table rather than a new model: a thesis
// still has a 标题 and a 正文, and the roles a document does not use stay unused.
// A second, generic role vocabulary would be a much larger change with no
// benefit for the formats people actually ask for.
//
// # Leading is always an absolute height
//
// These profiles express line spacing in points, never as a multiple. The
// writers emit lineRule="exact" whenever a point value is present, because
// OOXML's "auto" reinterprets w:line as 240ths of a line. Computing the height
// here keeps both writers unchanged and produces the same result a multiple
// would: 1.5 lines of 小四 is 18pt, and 固定值 18pt is that same 18pt.

// builder assembles a Spec for a plain document type.
type builder struct {
	page      PageSetup
	bodyFont  string
	bodySize  FontSize
	linePT    float64 // leading height in points; 0 → 1.2 × font size
	indent    float64 // 首行缩进字数
	titleFont string
	titleSize FontSize
	h1Font    string
	h2Font    string
	h3Font    string
	before    float64 // space before a heading, in hundredths of a line
}

// build fills every role, so ValidateSpec finds no missing role — a profile that
// leaves a role unset would make a renderer fall back silently, which is exactly
// the kind of surprise a profile should not contain.
func (b builder) build() Spec {
	line := b.linePT
	if line <= 0 {
		line = b.bodySize.Points() * 1.2
	}
	base := ParaStyle{
		Font:          b.bodyFont,
		LatinFont:     FontLatin,
		Size:          b.bodySize,
		Color:         ColorBlack,
		Align:         AlignJustify,
		LineRule:      LineExact,
		LineSpacingPT: line,
	}
	mk := func(role docmodel.Role, name string, mut func(*ParaStyle)) ParaStyle {
		s := base
		s.Role = role
		s.Name = name
		if mut != nil {
			mut(&s)
		}
		return s
	}

	styles := map[docmodel.Role]ParaStyle{}

	// 标题: centred and larger. Bold is set here rather than left to the reader,
	// because a profile is a statement of how the document should look.
	styles[docmodel.RoleTitle] = mk(docmodel.RoleTitle, "标题", func(s *ParaStyle) {
		s.Font, s.Size, s.Align = b.titleFont, b.titleSize, AlignCenter
		s.Bold = true
		s.LineSpacingPT = b.titleSize.Points() * 1.4
		s.SpaceAfterLines = 200
		s.OutlineLevel = 1
		s.KeepWithNext = true
	})

	// 正文 with a first-line indent, the Chinese convention.
	styles[docmodel.RoleBody] = mk(docmodel.RoleBody, "正文", func(s *ParaStyle) {
		s.FirstLineIndentChars = b.indent
	})

	// Headings get progressively smaller fonts, each with 空N行 above.
	hspec := []struct {
		role docmodel.Role
		name string
		font string
		size FontSize
		lvl  int
	}{
		{docmodel.RoleHeading1, "一级标题", b.h1Font, SizeSanhao, 1},
		{docmodel.RoleHeading2, "二级标题", b.h2Font, SizeSihao, 2},
		{docmodel.RoleHeading3, "三级标题", b.h3Font, SizeXiaoSi, 3},
		{docmodel.RoleHeading4, "四级标题", b.bodyFont, SizeXiaoSi, 4},
	}
	for _, h := range hspec {
		h := h
		styles[h.role] = mk(h.role, h.name, func(s *ParaStyle) {
			s.Font, s.Size = h.font, h.size
			s.Align = AlignLeft
			s.LineSpacingPT = h.size.Points() * 1.4
			s.SpaceBeforeLines = b.before
			s.OutlineLevel = h.lvl
			s.KeepWithNext = true
		})
	}

	// Document furniture. These are rarely used outside 公文, but they must be
	// present so a role lookup never falls back to an unexpected default.
	furniture := []struct {
		role docmodel.Role
		name string
		size FontSize
	}{
		{docmodel.RoleRecipient, "主送机关", b.bodySize},
		{docmodel.RoleSignature, "署名", b.bodySize},
		{docmodel.RoleDate, "日期", b.bodySize},
		{docmodel.RoleCC, "抄送", b.bodySize},
		{docmodel.RoleNote, "附注", SizeXiaoSi},
		{docmodel.RoleAttachmentNote, "附件说明", b.bodySize},
		{docmodel.RoleAttachment, "附件正文", b.bodySize},
		{docmodel.RoleCopyNumber, "份号", b.bodySize},
		{docmodel.RoleSecurity, "密级", b.bodySize},
		{docmodel.RoleUrgency, "紧急程度", b.bodySize},
		{docmodel.RoleDocNumber, "发文字号", b.bodySize},
		{docmodel.RoleSignatory, "签发人", b.bodySize},
		{docmodel.RolePrinter, "印发机关", SizeXiaoSi},
	}
	for _, f := range furniture {
		f := f
		styles[f.role] = mk(f.role, f.name, func(s *ParaStyle) {
			s.Size = f.size
			s.Align = AlignLeft
			s.FirstLineIndentChars = 0
		})
	}

	styles[docmodel.RoleMasthead] = mk(docmodel.RoleMasthead, "发文机关标志", func(s *ParaStyle) {
		s.Size = b.titleSize
		s.Align = AlignCenter
		s.LineSpacingPT = b.titleSize.Points() * 1.4
	})

	// 红色分隔线 is a 公文 element. On other document types the role carries no
	// rule and no colour, so it renders as an empty spacer rather than drawing a
	// red line through someone's thesis.
	styles[docmodel.RoleRedLine] = mk(docmodel.RoleRedLine, "分隔线", func(s *ParaStyle) {
		s.Align = AlignCenter
		s.LineSpacingPT = 4
	})

	return Spec{
		Page:      b.page,
		Direction: DirDown,
		Styles:    styles,
		// No 一字线 flanking the number: that is a 公文 convention, and on other
		// document types a plain number is what readers expect.
		PageNumberSize: SizeWuhao,
		PageNumberFont: FontSongti,
		PageNumberDash: "",
	}
}

// pageA4Standard returns A4 with conventional margins and no 字符网格.
//
// LinesPerPage/CharsPerLine are 0 for these profiles: the grid concept only makes
// sense for a standard that fixes a characters-per-line rule, and a document that
// declared one would have its glyphs condensed to fit it.
func pageA4Standard() PageSetup {
	return PageSetup{
		WidthMM:        210,
		HeightMM:       297,
		MarginTopMM:    25.4,
		MarginBottomMM: 25.4,
		MarginLeftMM:   31.8,
		MarginRightMM:  31.8,
		HeaderMM:       15,
		FooterMM:       17.5,
	}
}

// Generic is a plain Chinese document: 宋体 小四, 18pt leading (1.5 倍),
// 首行缩进 2 字. The sensible starting point for anything that is not a 公文.
func Generic() Spec {
	return builder{
		page:      pageA4Standard(),
		bodyFont:  FontSongti,
		bodySize:  SizeXiaoSi,
		linePT:    18, // 1.5 × 12pt
		indent:    2,
		titleFont: FontHeiti,
		titleSize: SizeSanhao,
		h1Font:    FontHeiti,
		h2Font:    FontHeiti,
		h3Font:    FontSongti,
		before:    100,
	}.build()
}

// Thesis is an academic paper. It differs from Generic where theses actually
// differ from ordinary prose — 五号 (10.5pt) body text and a tighter 17pt
// leading, the format most institutions ask for — so that choosing between the
// two actually changes the page.
func Thesis() Spec {
	return builder{
		page:      pageA4Standard(),
		bodyFont:  FontSongti,
		bodySize:  SizeWuhao,
		linePT:    17,
		indent:    2,
		titleFont: FontHeiti,
		titleSize: SizeSanhao,
		h1Font:    FontHeiti,
		h2Font:    FontHeiti,
		h3Font:    FontSongti,
		before:    100,
	}.build()
}

// Report is a work report / briefing: 仿宋 小三 at a roomy 28pt leading, which is
// what makes it readable when printed and read aloud.
func Report() Spec {
	return builder{
		page:      pageA4Standard(),
		bodyFont:  FontFangSong,
		bodySize:  SizeXiaoSan,
		linePT:    28,
		indent:    2,
		titleFont: FontHeiti,
		titleSize: SizeErhao,
		h1Font:    FontHeiti,
		h2Font:    FontHeiti,
		h3Font:    FontKaiti,
		before:    100,
	}.build()
}
