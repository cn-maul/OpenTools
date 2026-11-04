// Package gbstandard encodes 《党政机关公文格式》GB/T 9704-2012 as data that
// the DOCX writer and the browser preview both consume.
//
// # Why one shared table
//
// The preview and the generated .docx must agree: a user who approves a
// layout on screen and then opens the file must see the same thing. That
// agreement is only maintainable if both renderers read one style table
// rather than each hard-coding sizes. Every number below therefore lives
// here exactly once, and engine/render/{docx,preview} are forbidden from
// carrying font sizes of their own — a test in this package pins that.
//
// # Normative vs. practical values
//
// Values marked "标准" are taken directly from GB/T 9704-2012 and must not be
// tuned. Values marked "惯例" are the industry-standard choices where the
// standard either leaves the decision to the issuing organ or states a bound
// rather than a number (发文机关标志's size is bounded, not fixed). Those may
// be overridden through Spec, and Default() returns the common practice.
package gbstandard

import (
	"fmt"
	"sort"
	"strings"

	"opentools/tools/govdoc/engine/docmodel"
)

// ---------------------------------------------------------------- units

// mmToPT converts millimetres to PostScript points (1in = 25.4mm = 72pt),
// the unit OOXML uses for page geometry.
func mmToPT(mm float64) float64 { return mm * 72.0 / 25.4 }

// FontSize is a 汉字字号. Chinese typography names sizes rather than
// numbering them in points, and the 公文 standard is written in those names
// ("三号仿宋体字"), so the code models the name and derives points.
type FontSize string

const (
	SizeChuhao     FontSize = "初号" // 42pt
	SizeXiaoChuhao FontSize = "小初" // 36pt
	SizeYihao      FontSize = "一号" // 26pt
	SizeXiaoYi     FontSize = "小一" // 24pt
	SizeErhao      FontSize = "二号" // 22pt
	SizeXiaoEr     FontSize = "小二" // 18pt
	SizeSanhao     FontSize = "三号" // 16pt
	SizeXiaoSan    FontSize = "小三" // 15pt
	SizeSihao      FontSize = "四号" // 14pt
	SizeXiaoSi     FontSize = "小四" // 12pt
	SizeWuhao      FontSize = "五号" // 10.5pt
	SizeXiaoWu     FontSize = "小五" // 9pt
)

// fontPoints maps each 字号 to its point size. These are the conventional
// equivalences used by Word's Chinese locale.
var fontPoints = map[FontSize]float64{
	SizeChuhao: 42, SizeXiaoChuhao: 36, SizeYihao: 26, SizeXiaoYi: 24,
	SizeErhao: 22, SizeXiaoEr: 18, SizeSanhao: 16, SizeXiaoSan: 15,
	SizeSihao: 14, SizeXiaoSi: 12, SizeWuhao: 10.5, SizeXiaoWu: 9,
}

// Points returns the point size of a 字号. An unknown name yields 0, which
// the renderers treat as "inherit", so a typo cannot silently produce a
// wrong-but-plausible size.
func (f FontSize) Points() float64 { return fontPoints[f] }

// Known reports whether the 字号 is one this package defines.
func (f FontSize) Known() bool { _, ok := fontPoints[f]; return ok }

// Font families. The standard names the typeface; the *_GB2312 forms are the
// ones that actually carry a full 公文 glyph set on a Chinese Windows/Office
// install, so they are preferred with the plain name as fallback. OOXML
// accepts a comma-free single name per attribute, so the fallback chain is
// expressed by choosing the primary here and letting the reader substitute.
const (
	// FontFangSong is 仿宋体 — the body typeface ("一般用3号仿宋体字").
	FontFangSong = "仿宋_GB2312"
	// FontHeiti is 黑体 — used for 一级标题 and the 版头 marking lines.
	FontHeiti = "黑体"
	// FontKaiti is 楷体 — used for 二级标题.
	FontKaiti = "楷体_GB2312"
	// FontXiaoBiaoSong is 方正小标宋简体 — the 标题 typeface.
	FontXiaoBiaoSong = "方正小标宋简体"
	// FontSongti is 宋体 — used for 页码.
	FontSongti = "宋体"
	// FontLatin is the typeface for 阿拉伯数字 and Latin letters inside
	// 公文 (惯例: Times New Roman, which pairs with 仿宋).
	FontLatin = "Times New Roman"
)

// fontFallbacks maps a primary 公文 typeface to the names a consumer may
// substitute when the primary is absent. The DOCX writer emits the primary;
// the preview CSS emits the whole chain; the validator reports which are
// missing on the editing machine so a user is not surprised by a silent
// substitution at print time.
//
// Every chain ENDS in a generic CSS family ("serif"/"sans-serif"). That is
// deliberate: a browser that has none of the named CJK faces must still land
// on a serif rather than fall back to its own default, which for Chinese text
// is usually a sans face and would make a 公文 look wrong on screen even
// though the .docx is correct.
var fontFallbacks = map[string][]string{
	// 仿宋 is a serif (宋体-family) face.
	FontFangSong:     {"仿宋_GB2312", "仿宋", "FangSong", "STFangsong", "SimSun", "Songti SC", "serif"},
	FontHeiti:        {"黑体", "SimHei", "Heiti SC", "Microsoft YaHei", "sans-serif"},
	FontKaiti:        {"楷体_GB2312", "楷体", "KaiTi", "STKaiti", "SimSun", "serif"},
	FontXiaoBiaoSong: {"方正小标宋简体", "方正小标宋_GBK", "小标宋", "FZXiaoBiaoSong-B05S", "STZhongsong", "SimSun", "serif"},
	FontSongti:       {"宋体", "SimSun", "Songti SC", "serif"},
	FontLatin:        {"Times New Roman", "Liberation Serif", "Nimbus Roman", "serif"},
}

// genericFamilies are the CSS families a fallback chain may terminate in.
var genericFamilies = map[string]bool{
	"serif": true, "sans-serif": true, "monospace": true,
	"cursive": true, "fantasy": true, "system-ui": true,
}

// FontChain returns the fallback chain for a typeface, primary first.
func FontChain(primary string) []string {
	if chain, ok := fontFallbacks[primary]; ok {
		return chain
	}
	return []string{primary}
}

// ---------------------------------------------------------------- alignment

// Align is a horizontal paragraph alignment.
type Align string

const (
	AlignLeft    Align = "left"
	AlignCenter  Align = "center"
	AlignRight   Align = "right"
	AlignJustify Align = "both"
)

// OOXMLValue returns the w:jc val for this alignment.
func (a Align) OOXMLValue() string {
	switch a {
	case AlignCenter:
		return "center"
	case AlignRight:
		return "right"
	case AlignJustify:
		return "both"
	default:
		return "left"
	}
}

// CSSValue returns the CSS text-align value.
func (a Align) CSSValue() string {
	switch a {
	case AlignCenter:
		return "center"
	case AlignRight:
		return "right"
	case AlignJustify:
		return "justify"
	default:
		return "left"
	}
}

// ---------------------------------------------------------------- page setup

// PageSetup is the 版面 geometry of 公文用纸. All values are normative: the
// standard fixes A4, the four margins (±1mm) and the 版心 of 156mm×225mm.
type PageSetup struct {
	WidthMM        float64 `json:"width_mm"`         // 标准 210
	HeightMM       float64 `json:"height_mm"`        // 标准 297
	MarginTopMM    float64 `json:"margin_top_mm"`    // 标准 37±1
	MarginBottomMM float64 `json:"margin_bottom_mm"` // 标准 35±1
	MarginLeftMM   float64 `json:"margin_left_mm"`   // 标准 28±1
	MarginRightMM  float64 `json:"margin_right_mm"`  // 标准 26±1
	// LinesPerPage and CharsPerLine are the standard's 每面22行×每行28字.
	LinesPerPage int `json:"lines_per_page"` // 标准 22
	CharsPerLine int `json:"chars_per_line"` // 标准 28
	// HeaderMM is the distance from the page edge to the 页码 baseline
	// region (惯例: the page number sits inside the bottom margin).
	HeaderMM float64 `json:"header_mm"`
	FooterMM float64 `json:"footer_mm"`
}

// DefaultPageSetup returns the normative A4 geometry of GB/T 9704-2012 §6.1.
func DefaultPageSetup() PageSetup {
	return PageSetup{
		WidthMM: 210, HeightMM: 297,
		MarginTopMM: 37, MarginBottomMM: 35,
		MarginLeftMM: 28, MarginRightMM: 26,
		LinesPerPage: 22, CharsPerLine: 28,
		HeaderMM: 15, FooterMM: 17.5,
	}
}

// TextWidthMM is 版心 width — 标准 156mm.
func (p PageSetup) TextWidthMM() float64 {
	return p.WidthMM - p.MarginLeftMM - p.MarginRightMM
}

// TextHeightMM is 版心 height — 标准 225mm.
func (p PageSetup) TextHeightMM() float64 {
	return p.HeightMM - p.MarginTopMM - p.MarginBottomMM
}

// LineSpacingPT is the 固定行距 that makes exactly LinesPerPage lines fill
// the 版心. This is how the standard's "每面22行" becomes a number the
// renderers can apply: 225mm ÷ 22 ≈ 28.99pt, which is the fixed line spacing
// practitioners use.
func (p PageSetup) LineSpacingPT() float64 {
	if p.LinesPerPage <= 0 {
		return 0
	}
	return mmToPT(p.TextHeightMM()) / float64(p.LinesPerPage)
}

// CharPitchPT is the horizontal advance per character implied by 每行28字.
//
// Note that this is *not* equal to the body font size, and that is not a bug:
// see CharCondensePT for the geometry behind it.
func (p PageSetup) CharPitchPT() float64 {
	if p.CharsPerLine <= 0 {
		return 0
	}
	return mmToPT(p.TextWidthMM()) / float64(p.CharsPerLine)
}

// CharCondensePT is how much each character must be condensed for
// CharsPerLine characters of the body size to fit inside the 版心.
//
// # Why this exists
//
// GB/T 9704-2012 states three figures that do not quite agree: 版心宽 156mm,
// 每行28字, and 正文三号 (16pt). Taken literally, 28 × 16pt = 448pt =
// 158.0mm, which overshoots the 版心 by about 2mm — so 28 full-width 三号
// characters cannot fit in 156mm with natural spacing. The standard's own
// figures therefore imply a slight condensation, and this is exactly what
// Word's 文档网格 ("document grid", OOXML w:docGrid) does in the 公文
// templates issued with the standard: it pins 28 characters to the line and
// compresses the advance between them to make them fit.
//
// 版心宽 is the authoritative figure here (it is what 页面设置 fixes, and
// what the ±1mm margin tolerance is written against), so the grid absorbs the
// difference. A negative result means the body size can no longer fit
// CharsPerLine characters — the renderer must then fall back to natural
// spacing and accept that 每行28字 is not achievable at that size.
//
// The returned value is per character, in points: subtract it from the
// natural advance, or apply it as negative letter-spacing.
func (p PageSetup) CharCondensePT(bodySize FontSize) float64 {
	pitch := p.CharPitchPT()
	if pitch <= 0 {
		return 0
	}
	size := bodySize.Points()
	if size <= 0 {
		return 0
	}
	// Positive when the glyphs must be squeezed; zero when natural spacing
	// already fits.
	if diff := size - pitch; diff > 0 {
		return diff
	}
	return 0
}

// FitsNaturalSpacing reports whether CharsPerLine characters of bodySize fit
// the 版心 without condensation.
func (p PageSetup) FitsNaturalSpacing(bodySize FontSize) bool {
	return p.CharCondensePT(bodySize) == 0
}

// GridCondensePT returns the per-character condensation the renderers should
// apply to a role, or 0 when the role is not laid out on the character grid.
//
// # Why this exists instead of calling CharCondensePT per role
//
// CharCondensePT answers a narrow question: "if text of this size had to fit
// CharsPerLine characters in the 版心, how much would each glyph have to be
// squeezed?" That is meaningful only for text the standard actually puts on the
// 22×28 character grid — the 正文 and its 层次序数 headings, at 三号.
//
// Applying it to every role is a category error, and a visibly destructive one.
// The formula is `size − pitch`, where pitch is a fixed 版心宽÷28 ≈ 15.79pt. For
// 正文 三号 that is a harmless 0.21pt (1.3%). For 标题 二号 it is 6.21pt — 28% of
// the glyph width — and for 发文机关标志 小初 it is 20.21pt, i.e. 56%. Both are
// centred display elements whose glyphs then visibly overlap.
//
// The standard sizes 标题 (二号) and 发文机关标志 independently and centres them;
// neither has to fit 28 to a line. So the rule is: a role shares the body's
// character grid exactly when it is set at the body's size. Same size ⇒ same
// grid ⇒ same condensation; anything larger is display type.
func (s Spec) GridCondensePT(role docmodel.Role) float64 {
	st, ok := s.Styles[role]
	if !ok {
		return 0
	}
	body, ok := s.Styles[docmodel.RoleBody]
	if !ok {
		return 0
	}
	if st.Size != body.Size {
		return 0
	}
	return s.Page.CharCondensePT(st.Size)
}

// LeadingIndentChars reports how much first-line indentation a paragraph's own
// text already provides, measured in whole 字.
//
// # Why the renderers need this
//
// Chinese authors indent paragraphs by typing two 全角空格 (U+3000) at the
// start — by far the most common way a real 公文 is written. The parser stores
// that leading whitespace verbatim, correctly, because it is part of the file.
// If the renderer then applies the 正文 style's own 首行缩进2字 as well, the
// paragraph comes out indented four characters instead of two.
//
// The fix is to reduce the *style's* indent by whatever the text already
// supplies, never to strip the text: the characters stay in the document, so
// the verbatim guarantee holds, and the printed result is the two characters
// the author intended.
//
// # Only 全角空格 counts, deliberately
//
// 全角空格 is the one marker Chinese authors use *deliberately* for
// indentation. ASCII spaces at the start of a paragraph are almost always an
// artifact — a conversion from PDF, a copy-paste, a stray keystroke — and
// treating them as intent would silently shrink the indent of documents nobody
// asked us to change. So a run of ASCII spaces leaves the style's indent
// alone, which is the conservative choice: the paragraph still looks like a
// 公文 paragraph.
//
// Tabs are likewise not counted; their width is a renderer decision, not
// something the author expressed.
func LeadingIndentChars(text string) float64 {
	var n float64
	for _, r := range text {
		switch r {
		case '\u3000': // 全角空格
			n++
		case ' ', '\t':
			// Not indentation: see above.
		default:
			return n
		}
	}
	return n
}

// EffectiveFirstLineIndent returns the first-line indent a renderer should apply
// to one block: the style's 首行缩进, less whatever the block's own text already
// indents by.
//
// The result is never negative: text indented further than the style asks is
// left alone rather than pulled backwards, since the author's own spacing is the
// stronger signal about intent.
func EffectiveFirstLineIndent(st ParaStyle, text string) float64 {
	style := st.FirstLineIndentChars
	if style <= 0 {
		return 0
	}
	if own := LeadingIndentChars(text); own >= style {
		return 0
	} else {
		return style - own
	}
}

// ---------------------------------------------------------------- direction

// Direction is 行文方向. It changes exactly one normative number: how far the
// 发文机关标志 sits below the top of the 版心.
type Direction string

const (
	// DirDown is 下行文 (to subordinates) — 惯例 default.
	DirDown Direction = "下行文"
	// DirUp is 上行文 (to superiors): 发文机关标志 is placed lower to leave
	// room for 签发人.
	DirUp Direction = "上行文"
	// DirParallel is 平行文 (to peers).
	DirParallel Direction = "平行文"
)

// MastheadTopMM is the distance from the top edge of the 版心 to the top
// edge of 发文机关标志 — 标准 §4.3.2: 35mm for 平行文/下行文, 80mm for 上行文.
func (d Direction) MastheadTopMM() float64 {
	if d == DirUp {
		return 80
	}
	return 35
}

// ---------------------------------------------------------------- style

// LineRule selects how a paragraph's line height is computed.
type LineRule string

const (
	// LineExact is 固定值 — the rule 公文 uses, so 22 lines always fit the
	// 版心 regardless of glyph size.
	LineExact LineRule = "exact"
	// LineAuto is 单倍/多倍行距, used only for blocks the standard does not
	// place on the 22-line grid (e.g. the 版记 table).
	LineAuto LineRule = "auto"
)

// ParaStyle is the complete typography of one 公文 element. It is the unit
// shared by the DOCX writer and the preview.
//
// Indentation is expressed in characters (Chars suffix) because the standard
// is written that way — "每自然段左空二字" means two *characters*, which is
// `w:firstLineChars="200"` in OOXML, not a fixed length. This matters: 2
// characters at 三号 is 32pt, and hard-coding that would break the moment a
// block uses a different size.
type ParaStyle struct {
	Role docmodel.Role `json:"role"`
	// Name is the 公文 element name, for UI and diagnostics.
	Name string `json:"name"`

	Font      string   `json:"font"`       // eastAsia typeface
	LatinFont string   `json:"latin_font"` // ascii/hAnsi typeface for digits
	Size      FontSize `json:"size"`
	Bold      bool     `json:"bold"`
	// Color is RRGGBB, no leading '#'. Only 版头 elements are red.
	Color string `json:"color"`

	Align Align `json:"align"`

	LineRule      LineRule `json:"line_rule"`
	LineSpacingPT float64  `json:"line_spacing_pt"`
	// SpaceBeforeLines / SpaceAfterLines are 空N行, expressed in hundredths
	// of a line to match OOXML's w:beforeLines/w:afterLines units.
	SpaceBeforeLines float64 `json:"space_before_lines"`
	SpaceAfterLines  float64 `json:"space_after_lines"`

	FirstLineIndentChars float64 `json:"first_line_indent_chars"`
	LeftIndentChars      float64 `json:"left_indent_chars"`
	RightIndentChars     float64 `json:"right_indent_chars"`
	// HangingIndentChars is used by the 附件说明 and 页码 runs.
	HangingIndentChars float64 `json:"hanging_indent_chars"`

	// KeepWithNext prevents a heading from being orphaned at a page foot.
	KeepWithNext bool `json:"keep_with_next"`
	// PageBreakBefore forces the block to a new page.
	PageBreakBefore bool `json:"page_break_before,omitempty"`

	// BorderBottom, when set, draws the 公文 separator rule (红色分隔线)
	// or the 版记 rules beneath the paragraph.
	BorderBottom *BorderRule `json:"border_bottom,omitempty"`
	BorderTop    *BorderRule `json:"border_top,omitempty"`

	// OutlineLevel exposes heading levels to Word's navigation pane.
	OutlineLevel int `json:"outline_level,omitempty"`
	// Standard is the GB/T 9704-2012 clause this style follows.
	Standard string `json:"standard,omitempty"`
}

// BorderRule describes one horizontal rule.
type BorderRule struct {
	// Color is RRGGBB.
	Color string `json:"color"`
	// SizeEighthPT is the OOXML w:sz unit (eighths of a point).
	SizeEighthPT int `json:"size_eighth_pt"`
	// SpacePT is the distance between the rule and the text.
	SpacePT float64 `json:"space_pt"`
	// CSSWidth is the equivalent CSS border width, e.g. "1.5pt".
	CSSWidth string `json:"css_width"`
}

// ---------------------------------------------------------------- spec

// Spec is the complete formatting standard: page geometry plus one style per
// role. It is the only formatting input the renderers receive.
type Spec struct {
	Page      PageSetup `json:"page"`
	Direction Direction `json:"direction"`
	// Styles is keyed by role. Every docmodel.Role must be present;
	// ValidateSpec enforces it, so a renderer may look a role up without a
	// second existence check.
	Styles map[docmodel.Role]ParaStyle `json:"styles"`
	// PageNumberSize is 页码's 字号 — 标准 §7.3.6: 四号半角宋体.
	PageNumberSize FontSize `json:"page_number_size"`
	PageNumberFont string   `json:"page_number_font"`
	// PageNumberDash is the 一字线 drawn on each side of the page number.
	PageNumberDash string `json:"page_number_dash"`
}

// 标准 colours: 公文 版头 is red; the exact red of the 国标 sample is a
// saturated vermilion. 惯例: the value most 公文 templates use.
const (
	ColorRed   = "FF0000"
	ColorBlack = "000000"
)

// Default returns the standard style table. Every number is either normative
// (标准) or the common practice (惯例) noted on the field.
func Default() Spec {
	page := DefaultPageSetup()
	sp := page.LineSpacingPT() // ≈28.99pt, 标准 "每面22行"

	base := ParaStyle{
		Font:          FontFangSong,
		LatinFont:     FontLatin,
		Size:          SizeSanhao,
		Color:         ColorBlack,
		Align:         AlignJustify,
		LineRule:      LineExact,
		LineSpacingPT: sp,
		Standard:      "GB/T 9704-2012",
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

	// --- 版头 (标准 §7.2) ---------------------------------------------

	// 份号: 顶格编排在版心左上角第一行，6位阿拉伯数字，3号黑体.
	styles[docmodel.RoleCopyNumber] = mk(docmodel.RoleCopyNumber, "份号", func(s *ParaStyle) {
		s.Font, s.Size, s.Align = FontHeiti, SizeSanhao, AlignLeft
		s.Standard = "GB/T 9704-2012 §7.2.1"
	})

	// 密级和保密期限: 版心左上角第二行，3号黑体.
	styles[docmodel.RoleSecurity] = mk(docmodel.RoleSecurity, "密级和保密期限", func(s *ParaStyle) {
		s.Font, s.Size, s.Align = FontHeiti, SizeSanhao, AlignLeft
		s.Standard = "GB/T 9704-2012 §7.2.2"
	})

	// 紧急程度: 版心左上角，3号黑体.
	styles[docmodel.RoleUrgency] = mk(docmodel.RoleUrgency, "紧急程度", func(s *ParaStyle) {
		s.Font, s.Size, s.Align = FontHeiti, SizeSanhao, AlignLeft
		s.Standard = "GB/T 9704-2012 §7.2.3"
	})

	// 发文机关标志: 红色，小标宋体字，居中。标准给出的是尺寸上限
	// (22mm×15mm) 而非字号，故大小为惯例值并可配置。
	styles[docmodel.RoleMasthead] = mk(docmodel.RoleMasthead, "发文机关标志", func(s *ParaStyle) {
		s.Font, s.Size, s.Align = FontXiaoBiaoSong, SizeXiaoChuhao, AlignCenter
		s.Color = ColorRed
		s.LineRule = LineAuto
		s.LineSpacingPT = 0
		s.SpaceAfterLines = 200 // 其下空二行编排发文字号
		s.KeepWithNext = true
		s.Standard = "GB/T 9704-2012 §7.2.4（字号惯例，标准仅限 22mm×15mm）"
	})

	// 发文字号: 发文机关标志下空二行，居中，3号仿宋.
	styles[docmodel.RoleDocNumber] = mk(docmodel.RoleDocNumber, "发文字号", func(s *ParaStyle) {
		s.Font, s.Size, s.Align = FontFangSong, SizeSanhao, AlignCenter
		s.Standard = "GB/T 9704-2012 §7.2.5"
	})

	// 签发人: 上行文时，发文字号居左空一字，签发人姓名居右空一字.
	styles[docmodel.RoleSignatory] = mk(docmodel.RoleSignatory, "签发人", func(s *ParaStyle) {
		s.Font, s.Size, s.Align = FontFangSong, SizeSanhao, AlignRight
		s.RightIndentChars = 1
		s.Standard = "GB/T 9704-2012 §7.2.5"
	})

	// 红色分隔线: 与版心等宽，发文字号之下4mm处（标准 §7.2.6）。
	// 以一个只有下边框的空段落实现。
	styles[docmodel.RoleRedLine] = mk(docmodel.RoleRedLine, "红色分隔线", func(s *ParaStyle) {
		s.LineRule = LineAuto
		s.LineSpacingPT = 4 * 72 / 25.4 // 4mm
		s.SpaceBeforeLines = 0
		s.SpaceAfterLines = 0
		s.BorderBottom = &BorderRule{Color: ColorRed, SizeEighthPT: 12, CSSWidth: "1.5pt"}
		s.Standard = "GB/T 9704-2012 §7.2.6"
	})

	// --- 主体 (标准 §7.3) ---------------------------------------------

	// 标题: 红色分隔线下空二行，2号小标宋体字，居中.
	styles[docmodel.RoleTitle] = mk(docmodel.RoleTitle, "标题", func(s *ParaStyle) {
		s.Font, s.Size, s.Align = FontXiaoBiaoSong, SizeErhao, AlignCenter
		s.SpaceBeforeLines = 200
		s.LineRule = LineAuto
		s.LineSpacingPT = 0
		s.KeepWithNext = true
		s.Standard = "GB/T 9704-2012 §7.3.1"
	})

	// 主送机关: 标题下空一行，左侧顶格，3号仿宋，回行顶格.
	styles[docmodel.RoleRecipient] = mk(docmodel.RoleRecipient, "主送机关", func(s *ParaStyle) {
		s.Font, s.Size, s.Align = FontFangSong, SizeSanhao, AlignLeft
		s.SpaceBeforeLines = 100
		s.Standard = "GB/T 9704-2012 §7.3.2"
	})

	// 正文: 主送机关下一行，每自然段左空二字，回行顶格，3号仿宋.
	styles[docmodel.RoleBody] = mk(docmodel.RoleBody, "正文", func(s *ParaStyle) {
		s.Font, s.Size, s.Align = FontFangSong, SizeSanhao, AlignJustify
		s.FirstLineIndentChars = 2
		s.Standard = "GB/T 9704-2012 §7.3.3"
	})

	// 一级标题 "一、": 3号黑体（标准 §7.3.3 层次序数）.
	styles[docmodel.RoleHeading1] = mk(docmodel.RoleHeading1, "一级标题（一、）", func(s *ParaStyle) {
		s.Font, s.Size, s.Align = FontHeiti, SizeSanhao, AlignJustify
		s.FirstLineIndentChars = 2
		s.KeepWithNext = true
		s.OutlineLevel = 1
		s.Standard = "GB/T 9704-2012 §7.3.3"
	})

	// 二级标题 "（一）": 3号楷体.
	styles[docmodel.RoleHeading2] = mk(docmodel.RoleHeading2, "二级标题（（一））", func(s *ParaStyle) {
		s.Font, s.Size, s.Align = FontKaiti, SizeSanhao, AlignJustify
		s.FirstLineIndentChars = 2
		s.KeepWithNext = true
		s.OutlineLevel = 2
		s.Standard = "GB/T 9704-2012 §7.3.3"
	})

	// 三级标题 "1.": 3号仿宋.
	styles[docmodel.RoleHeading3] = mk(docmodel.RoleHeading3, "三级标题（1.）", func(s *ParaStyle) {
		s.Font, s.Size, s.Align = FontFangSong, SizeSanhao, AlignJustify
		s.FirstLineIndentChars = 2
		s.KeepWithNext = true
		s.OutlineLevel = 3
		s.Standard = "GB/T 9704-2012 §7.3.3"
	})

	// 四级标题 "（1）": 3号仿宋.
	styles[docmodel.RoleHeading4] = mk(docmodel.RoleHeading4, "四级标题（（1））", func(s *ParaStyle) {
		s.Font, s.Size, s.Align = FontFangSong, SizeSanhao, AlignJustify
		s.FirstLineIndentChars = 2
		s.KeepWithNext = true
		s.OutlineLevel = 4
		s.Standard = "GB/T 9704-2012 §7.3.3"
	})

	// 附件说明: 正文下空一行左空二字编排"附件"二字.
	styles[docmodel.RoleAttachmentNote] = mk(docmodel.RoleAttachmentNote, "附件说明", func(s *ParaStyle) {
		s.Font, s.Size, s.Align = FontFangSong, SizeSanhao, AlignLeft
		s.SpaceBeforeLines = 100
		s.LeftIndentChars = 2
		s.Standard = "GB/T 9704-2012 §7.3.4"
	})

	// 发文机关署名: 正文（或附件说明）下空一行右空二字.
	styles[docmodel.RoleSignature] = mk(docmodel.RoleSignature, "发文机关署名", func(s *ParaStyle) {
		s.Font, s.Size, s.Align = FontFangSong, SizeSanhao, AlignRight
		s.SpaceBeforeLines = 100
		s.RightIndentChars = 2
		s.KeepWithNext = true
		s.Standard = "GB/T 9704-2012 §7.3.5"
	})

	// 成文日期: 署名下一行，右空四字，阿拉伯数字标全年月日.
	styles[docmodel.RoleDate] = mk(docmodel.RoleDate, "成文日期", func(s *ParaStyle) {
		s.Font, s.Size, s.Align = FontFangSong, SizeSanhao, AlignRight
		s.RightIndentChars = 4
		s.Standard = "GB/T 9704-2012 §7.3.5"
	})

	// 附注: 成文日期下一行左空二字，加圆括号.
	styles[docmodel.RoleNote] = mk(docmodel.RoleNote, "附注", func(s *ParaStyle) {
		s.Font, s.Size, s.Align = FontFangSong, SizeSanhao, AlignLeft
		s.LeftIndentChars = 2
		s.Standard = "GB/T 9704-2012 §7.3.7"
	})

	// 附件正文: 另面编排，"附件"二字顶格，3号黑体.
	styles[docmodel.RoleAttachment] = mk(docmodel.RoleAttachment, "附件", func(s *ParaStyle) {
		s.Font, s.Size, s.Align = FontFangSong, SizeSanhao, AlignJustify
		s.FirstLineIndentChars = 2
		s.PageBreakBefore = true
		s.Standard = "GB/T 9704-2012 §7.4"
	})

	// --- 版记 (标准 §7.5) ---------------------------------------------
	// 版记中的字体一般用4号仿宋（惯例），上下各加一条与版心等宽的线。

	styles[docmodel.RoleCC] = mk(docmodel.RoleCC, "抄送机关", func(s *ParaStyle) {
		s.Font, s.Size, s.Align = FontFangSong, SizeSihao, AlignLeft
		s.LineRule = LineAuto
		s.LineSpacingPT = 0
		s.SpaceBeforeLines = 40
		s.SpaceAfterLines = 40
		s.BorderTop = &BorderRule{Color: ColorBlack, SizeEighthPT: 6, CSSWidth: "0.75pt"}
		s.Standard = "GB/T 9704-2012 §7.5.2"
	})

	styles[docmodel.RolePrinter] = mk(docmodel.RolePrinter, "印发机关和印发日期", func(s *ParaStyle) {
		s.Font, s.Size, s.Align = FontFangSong, SizeSihao, AlignLeft
		s.LineRule = LineAuto
		s.LineSpacingPT = 0
		s.SpaceBeforeLines = 40
		s.SpaceAfterLines = 40
		s.BorderBottom = &BorderRule{Color: ColorBlack, SizeEighthPT: 6, CSSWidth: "0.75pt"}
		s.Standard = "GB/T 9704-2012 §7.5.3"
	})

	return Spec{
		Page:           page,
		Direction:      DirDown,
		Styles:         styles,
		PageNumberSize: SizeSihao, // 标准 §7.3.6: 4号半角宋体
		PageNumberFont: FontSongti,
		PageNumberDash: "—",
	}
}

// Style returns the style for a role, and whether it exists.
func (s Spec) Style(r docmodel.Role) (ParaStyle, bool) {
	st, ok := s.Styles[r]
	return st, ok
}

// MustStyle returns the style for a role, or the body style when the role is
// unknown. Renderers use it so a new role can never panic a render pass; the
// gap is reported by ValidateSpec and by a diagnostic in the assembler.
func (s Spec) MustStyle(r docmodel.Role) ParaStyle {
	if st, ok := s.Styles[r]; ok {
		return st
	}
	if st, ok := s.Styles[docmodel.RoleBody]; ok {
		return st
	}
	base := Default()
	return base.Styles[docmodel.RoleBody]
}

// ValidateSpec checks that a spec is complete and internally consistent.
// Renderers may then trust every lookup, and a broken spec fails at startup
// instead of producing a subtly wrong document.
func ValidateSpec(s Spec) []string {
	var problems []string

	if s.Page.WidthMM <= 0 || s.Page.HeightMM <= 0 {
		problems = append(problems, "页面尺寸必须为正数")
	}
	if s.Page.MarginTopMM+s.Page.MarginBottomMM >= s.Page.HeightMM {
		problems = append(problems, "上下页边距之和超过页面高度")
	}
	if s.Page.MarginLeftMM+s.Page.MarginRightMM >= s.Page.WidthMM {
		problems = append(problems, "左右页边距之和超过页面宽度")
	}
	// The 字符网格 (每面N行×每行M字) is only meaningful for a standard that
	// fixes it, which today means 公文. A general document profile must leave it
	// unset — declaring it would condense the body's glyphs to fit a grid the
	// document never asked for, which is the title-overlap defect in another
	// guise.
	//
	// So the rule is: either both are set and positive, or neither is. A
	// half-declared grid is rejected, because each field is used to derive a
	// document-grid value the writers emit, and one without the other has no
	// correct interpretation.
	hasLines := s.Page.LinesPerPage > 0
	hasChars := s.Page.CharsPerLine > 0
	if hasLines != hasChars {
		problems = append(problems,
			"字符网格需成对设置：每面行数与每行字数要么都填，要么都留空")
	}

	for _, role := range docmodel.AllRoles() {
		st, ok := s.Styles[role]
		if !ok {
			problems = append(problems, fmt.Sprintf("缺少 %s 的样式定义", role))
			continue
		}
		if st.Font == "" {
			problems = append(problems, fmt.Sprintf("%s 未指定字体", role))
		}
		if !st.Size.Known() {
			problems = append(problems, fmt.Sprintf("%s 的字号 %q 未知", role, st.Size))
		}
		if st.LineRule == LineExact && st.LineSpacingPT <= 0 {
			problems = append(problems, fmt.Sprintf("%s 使用固定行距但未给出行距值", role))
		}
		if st.FirstLineIndentChars < 0 || st.LeftIndentChars < 0 || st.RightIndentChars < 0 {
			problems = append(problems, fmt.Sprintf("%s 存在负缩进", role))
		}
		if st.Color != "" && !isHex6(st.Color) {
			problems = append(problems, fmt.Sprintf("%s 的颜色 %q 不是六位十六进制", role, st.Color))
		}
		if st.Align == "" {
			problems = append(problems, fmt.Sprintf("%s 未指定对齐方式", role))
		}
	}
	if !s.PageNumberSize.Known() {
		problems = append(problems, fmt.Sprintf("页码字号 %q 未知", s.PageNumberSize))
	}
	sort.Strings(problems)
	return problems
}

func isHex6(s string) bool {
	if len(s) != 6 {
		return false
	}
	for i := 0; i < 6; i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

// Describe renders the style table as text, for `govdoc spec` and for tests
// that want a readable failure diff.
func (s Spec) Describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "GB/T 9704-2012 公文格式（行文方向：%s）\n", s.Direction)
	fmt.Fprintf(&b, "页面 %.0f×%.0fmm  页边距 上%.0f 下%.0f 左%.0f 右%.0f  "+
		"版心 %.0f×%.0fmm  每面%d行×每行%d字  固定行距 %.2fpt\n",
		s.Page.WidthMM, s.Page.HeightMM,
		s.Page.MarginTopMM, s.Page.MarginBottomMM, s.Page.MarginLeftMM, s.Page.MarginRightMM,
		s.Page.TextWidthMM(), s.Page.TextHeightMM(),
		s.Page.LinesPerPage, s.Page.CharsPerLine, s.Page.LineSpacingPT())
	fmt.Fprintf(&b, "发文机关标志上边缘至版心上边缘 %.0fmm\n\n", s.Direction.MastheadTopMM())

	for _, role := range docmodel.AllRoles() {
		st, ok := s.Styles[role]
		if !ok {
			continue
		}
		indent := ""
		if st.FirstLineIndentChars > 0 {
			indent += fmt.Sprintf(" 首行缩进%.0f字", st.FirstLineIndentChars)
		}
		if st.LeftIndentChars > 0 {
			indent += fmt.Sprintf(" 左空%.0f字", st.LeftIndentChars)
		}
		if st.RightIndentChars > 0 {
			indent += fmt.Sprintf(" 右空%.0f字", st.RightIndentChars)
		}
		spacing := " 固定行距"
		if st.LineRule == LineAuto {
			spacing = " 自动行距"
		}
		fmt.Fprintf(&b, "%-14s %-20s %-8s %5.1fpt  颜色#%s%s%s\n",
			role, st.Name, st.Font, st.Size.Points(), st.Color, indent, spacing)
	}
	return b.String()
}
