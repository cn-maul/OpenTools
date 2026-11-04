package preview

import (
	"fmt"
	"strings"

	"opentools/tools/govdoc/engine/docmodel"
	"opentools/tools/govdoc/engine/gbstandard"
)

// 复核层配色：屏幕上标注「此块已被声明的改动修改过」。
//
// 这几个值不是排版量值 —— 它们不参与字号、行距、页边距，因此不来自
// gbstandard.Spec（Spec 管的是纸上的样子，管不到界面如何提示）。用琥珀底 +
// 左侧色条而非整块变色，是为了让被改动的段落仍能被正常阅读：用户要判断的是
// 「改得对不对」，把字埋进高饱和度底色里反而看不清。与国标红色（#FF0000，
// 保留给发文机关标志与红色分隔线）刻意区分开。
const (
	reviewMarkBg    = "rgba(255, 196, 0, 0.16)"
	reviewMarkBar   = "#c98a00"
	reviewMarkInset = "1px"
)

// stylesheet renders the whole preview stylesheet from spec.
//
// Two rules govern it:
//
//  1. Every magnitude is a CSS custom property defined on .gd-root and read
//     back by name below. No rule body carries a literal mm/pt/px size, so
//     reading the variable block tells a reviewer the exact numbers the
//     standard prescribed.
//  2. The .gd-page sheet is the *only* part of the application that uses these
//     values. The surrounding application chrome belongs to web/src/styles and
//     uses the HeroUI-derived design tokens; the paper must stay 国标.
func (l *layout) stylesheet() string {
	var b strings.Builder

	writeRootVars(&b, l)
	writePageRules(&b, l)
	writeRoleRules(&b, l)
	writeChromeRules(&b, l)
	return b.String()
}

// ---------------------------------------------------------------- CSS variables

// writeRootVars emits the spec as custom properties. Names are stable and
// documented so the tests can assert that the standard's numbers are present.
func writeRootVars(b *strings.Builder, l *layout) {
	spec := l.spec
	p := spec.Page

	b.WriteString("/* ============================================================\n")
	b.WriteString("   GovDoc 预览样式 —— 全部量值由 gbstandard.Spec 生成\n")
	fmt.Fprintf(b, "   标准 %s\n", spec.Styles[docmodel.RoleBody].Standard)
	b.WriteString("   ============================================================ */\n")

	b.WriteString(".gd-root {\n")

	// --- 版面 (标准 §6.1: A4, 版心 156mm×225mm, 每面22行×每行28字) ---
	fmt.Fprintf(b, "  --gd-page-w: %s;\n", mm(p.WidthMM))
	fmt.Fprintf(b, "  --gd-page-h: %s;\n", mm(p.HeightMM))
	fmt.Fprintf(b, "  --gd-margin-top: %s;\n", mm(p.MarginTopMM))
	fmt.Fprintf(b, "  --gd-margin-bottom: %s;\n", mm(p.MarginBottomMM))
	fmt.Fprintf(b, "  --gd-margin-left: %s;\n", mm(p.MarginLeftMM))
	fmt.Fprintf(b, "  --gd-margin-right: %s;\n", mm(p.MarginRightMM))
	fmt.Fprintf(b, "  --gd-text-w: %s;\n", mm(p.TextWidthMM()))
	fmt.Fprintf(b, "  --gd-text-h: %s;\n", mm(p.TextHeightMM()))
	fmt.Fprintf(b, "  --gd-lines-per-page: %d;\n", p.LinesPerPage)
	fmt.Fprintf(b, "  --gd-chars-per-line: %d;\n", p.CharsPerLine)
	fmt.Fprintf(b, "  --gd-page-header: %s;\n", mm(p.HeaderMM))
	fmt.Fprintf(b, "  --gd-page-footer: %s;\n", mm(p.FooterMM))

	// 固定行距: 225mm ÷ 22 行 ≈ 28.99pt
	fmt.Fprintf(b, "  --gd-line-spacing: %s;\n", pt(p.LineSpacingPT()))
	fmt.Fprintf(b, "  --gd-char-pitch: %s;\n", pt(p.CharPitchPT()))

	// 行文方向 (标准 §4.3.2: 下行/平行文 35mm, 上行文 80mm)
	fmt.Fprintf(b, "  --gd-masthead-top: %s;\n", mm(spec.Direction.MastheadTopMM()))
	fmt.Fprintf(b, "  --gd-head-top-used: %s;\n", pt(l.headTopHeightPT()))
	// 发文机关标志上边缘距版心上边缘 masthead-top，其中已被 份号/密级/紧急程度
	// 占用 head-top-used，余下即间距（标准 §7.2.1–7.2.4）。
	fmt.Fprintf(b, "  --gd-head-reserve: %s;\n", pt(l.headReservePT()))

	// 页码 (标准 §7.3.6: 4号半角宋体)
	fmt.Fprintf(b, "  --gd-pageno-size: %s;\n", pt(spec.PageNumberSize.Points()))
	fmt.Fprintf(b, "  --gd-pageno-font: %s;\n", fontFamily(spec.PageNumberFont))

	// --- 颜色 ---
	fmt.Fprintf(b, "  --gd-black: #%s;\n", hexOr(spec.Styles[docmodel.RoleBody].Color, gbstandard.ColorBlack))
	fmt.Fprintf(b, "  --gd-red: #%s;\n", hexOr(spec.Styles[docmodel.RoleMasthead].Color, gbstandard.ColorRed))
	fmt.Fprintf(b, "  --gd-rule-red: #%s;\n", borderColor(spec.Styles[docmodel.RoleRedLine].BorderBottom))

	// 主体 字号/字体
	body := spec.Styles[docmodel.RoleBody]
	fmt.Fprintf(b, "  --gd-body-size: %s;\n", pt(body.Size.Points()))
	fmt.Fprintf(b, "  --gd-body-font: %s;\n", fontFamily(body.Font))
	fmt.Fprintf(b, "  --gd-latin-font: %s;\n", fontFamily(body.LatinFont))
	// 每字宽度: 版心宽 ÷ 每行28字 (标准 §6.1). 国标的三个数字 (版心156mm、
	// 每行28字、正文三号16pt) 并非自然相容 —— 28×16pt = 158mm 超出 2mm，
	// 国标自带的 Word 模板用「文档网格」钉住每行 28 字并压缩字距。
	// CharCondensePT 给出这个压缩量，预览据此复刻，保证每行也是 28 字。
	fmt.Fprintf(b, "  --gd-char-w: %s;\n", pt(p.CharPitchPT()))
	fmt.Fprintf(b, "  --gd-body-track: %s;\n", pt(p.CharCondensePT(body.Size)))
	fmt.Fprintf(b, "  --gd-line-box: %s;\n", pt(l.linePT))

	// --- 每个 公文元素一行 ---
	for _, role := range sortedRoles(spec) {
		st := spec.Styles[role]
		id := cssIdent(string(role))
		fmt.Fprintf(b, "\n  /* %s —— %s */\n", role, st.Name)
		fmt.Fprintf(b, "  --gd-%s-size: %s;\n", id, pt(st.Size.Points()))
		fmt.Fprintf(b, "  --gd-%s-font: %s;\n", id, fontFamily(st.Font))
		fmt.Fprintf(b, "  --gd-%s-latin: %s;\n", id, fontFamily(st.LatinFont))
		fmt.Fprintf(b, "  --gd-%s-color: #%s;\n", id, hexOr(st.Color, gbstandard.ColorBlack))
		fmt.Fprintf(b, "  --gd-%s-weight: %d;\n", id, boolInt(st.Bold, 700, 400))
		fmt.Fprintf(b, "  --gd-%s-align: %s;\n", id, st.Align.CSSValue())
		fmt.Fprintf(b, "  --gd-%s-line: %s;\n", id, pt(st.LineSpacingPT))
		fmt.Fprintf(b, "  --gd-%s-before: %s;\n", id, pt(st.SpaceBeforeLines/100*l.linePT))
		fmt.Fprintf(b, "  --gd-%s-after: %s;\n", id, pt(st.SpaceAfterLines/100*l.linePT))
		fmt.Fprintf(b, "  --gd-%s-indent-first: %s;\n", id, pt(st.FirstLineIndentChars*p.CharPitchPT()))
		fmt.Fprintf(b, "  --gd-%s-indent-left: %s;\n", id, pt(st.LeftIndentChars*p.CharPitchPT()))
		fmt.Fprintf(b, "  --gd-%s-indent-right: %s;\n", id, pt(st.RightIndentChars*p.CharPitchPT()))
		fmt.Fprintf(b, "  --gd-%s-hang: %s;\n", id, pt(st.HangingIndentChars*p.CharPitchPT()))
		fmt.Fprintf(b, "  --gd-%s-track: %s;\n", id, pt(l.spec.GridCondensePT(role)))
	}

	// --- 复核层（非国标量值）---
	//
	// 「已修改」高亮是屏幕上的**审计**标记，不是排版参数：它只改背景与左侧
	// 色条，绝不参与字号/行距/页边距。因此这几个值刻意与上方的国标变量分开放，
	// 一眼能看出哪一组来自 Spec、哪一组只是界面的复核提示。
	fmt.Fprintf(b, "\n  /* 复核层：已声明改动的高亮（非国标排版量值） */\n")
	fmt.Fprintf(b, "  --gd-edited-bg: %s;\n", reviewMarkBg)
	fmt.Fprintf(b, "  --gd-edited-bar: %s;\n", reviewMarkBar)
	fmt.Fprintf(b, "  --gd-edited-inset: %s;\n", reviewMarkInset)

	b.WriteString("}\n\n")
}

// ---------------------------------------------------------------- sheet + grid

// writePageRules emits the sheet and the 版心.
func writePageRules(b *strings.Builder, l *layout) {
	b.WriteString(`/* ---- 纸张：模拟 A4 单页 ---- */
/* 该片段既会被内联进应用页面，也会被 iframe 直接当作文档加载。
   :has() 让这两条重置只在「本片段就是整个文档」时生效，避免污染宿主页面。 */
html:has(> body > .gd-root),
body:has(> .gd-root) {
  margin: 0;
  padding: 0;
  background: #8a8a8a;
}

.gd-root {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: calc(var(--gd-page-h) * 0.04);
  padding: calc(var(--gd-page-h) * 0.03) 0;
  background: #8a8a8a;
  font-family: var(--gd-body-font), "Microsoft YaHei", sans-serif;
  color: var(--gd-black);
  -webkit-font-smoothing: antialiased;
}

.gd-page {
  position: relative;
  box-sizing: border-box;
  width: var(--gd-page-w);
  height: var(--gd-page-h);
  padding: var(--gd-margin-top) var(--gd-margin-right) var(--gd-margin-bottom) var(--gd-margin-left);
  background: #ffffff;
  box-shadow: 0 2px 6px rgba(0, 0, 0, 0.28), 0 8px 24px rgba(0, 0, 0, 0.18);
  overflow: hidden;
}

/* 版心：宽 --gd-text-w，高 --gd-text-h */
.gd-page > .gd-head,
.gd-body {
  width: var(--gd-text-w);
}

.gd-body {
  height: var(--gd-text-h);
  overflow: hidden;
}

/* ---- 版头 (标准 §7.2) ---- */
.gd-head {
  /* 份号/密级/紧急程度占据版心左上角，随后留白至 发文机关标志 上边缘：
     留白 = 行文方向规定的距离 − 左上角实际高度 (--gd-head-reserve)。 */
  width: var(--gd-text-w);
}

.gd-head-top {
  height: auto;
}

.gd-head-gap {
  height: var(--gd-head-reserve);
}

.gd-head-main {
  height: auto;
}

/* ---- 版记 (标准 §7.5)：末页底端 ---- */
.gd-page > .gd-foot {
  position: absolute;
  left: var(--gd-margin-left);
  right: var(--gd-margin-right);
  bottom: var(--gd-margin-bottom);
  width: var(--gd-text-w);
}

/* ---- 页码 (标准 §7.3.6)：一字线 + 4号宋体 ---- */
.gd-pageno {
  position: absolute;
  bottom: calc(var(--gd-margin-bottom) - var(--gd-page-footer));
  display: flex;
  align-items: center;
  gap: 0.35em;
  font-family: var(--gd-pageno-font);
  font-size: var(--gd-pageno-size);
  line-height: 1;
  color: var(--gd-black);
}

.gd-pageno--right {
  right: var(--gd-margin-right);
}

.gd-pageno--left {
  left: var(--gd-margin-left);
}

.gd-pageno-dash {
  letter-spacing: 0;
}

`)

	// 主体块统一落在固定行网格上 (标准 §6.1 "每面22行"，固定行距)
	b.WriteString(`/* ---- 正文块：固定行距网格 ---- */
.gd-body > .gd-block {
  margin: 0;
  line-height: var(--gd-line-box);
}

/*
 * 逐字不变：源文本里的换行、行首全角空格与行尾空格都是原文的一部分，
 * 必须原样显示。浏览器默认会把这些空白折叠成单个空格，那就不再是原文了。
 * 表格单元格自带 padding 与 border，不参与该规则。
 */
.gd-block {
  white-space: pre-wrap;
  overflow-wrap: break-word;
}

.gd-paper-table {
  white-space: normal;
}

.gd-body > .gd-block > .gd-paper-table {
  width: 100%;
}
`)

	b.WriteString("\n")

	// 版头块的默认行盒
	b.WriteString(`.gd-head .gd-block {
  margin: 0;
}
`)
	b.WriteString("\n")

	// 版记块使用自动行距
	b.WriteString(`.gd-foot .gd-block {
  margin: 0;
}
`)
	b.WriteString("\n")
}

// ---------------------------------------------------------------- roles

// writeRoleRules emits one rule per role, reading the variables written by
// writeRootVars. The values never appear literally here.
func writeRoleRules(b *strings.Builder, l *layout) {
	b.WriteString("/* ---- 各 公文元素 (role) 样式，数值全部来自上方变量 ---- */\n")
	for _, role := range sortedRoles(l.spec) {
		st := l.spec.Styles[role]
		id := cssIdent(string(role))

		fmt.Fprintf(b, ".gd-role-%s {\n", id)
		fmt.Fprintf(b, "  font-family: var(--gd-%s-font);\n", id)
		fmt.Fprintf(b, "  font-size: var(--gd-%s-size);\n", id)
		fmt.Fprintf(b, "  font-weight: var(--gd-%s-weight);\n", id)
		fmt.Fprintf(b, "  color: var(--gd-%s-color);\n", id)
		fmt.Fprintf(b, "  text-align: var(--gd-%s-align);\n", id)
		fmt.Fprintf(b, "  line-height: var(--gd-%s-line);\n", id)
		fmt.Fprintf(b, "  margin-top: var(--gd-%s-before);\n", id)
		fmt.Fprintf(b, "  margin-bottom: var(--gd-%s-after);\n", id)
		fmt.Fprintf(b, "  text-indent: calc(var(--gd-%s-indent-first) - var(--gd-%s-hang));\n", id, id)
		fmt.Fprintf(b, "  padding-left: var(--gd-%s-indent-left);\n", id)
		fmt.Fprintf(b, "  padding-right: var(--gd-%s-indent-right);\n", id)
		// 文档网格式字距压缩：只施加在真正落在字符网格上的元素（即与正文
		// 同字号者）。国标的「每行28字」约束的是正文三号；标题（二号）与
		// 发文机关标志（小初）是居中按自身尺寸排版的展示性文字，并不需要
		// 每行放 28 字。若对它们套用同一压缩量，标题要压掉 28%、标志要压掉
		// 56%，字形就会互相重叠。
		if cond := l.spec.GridCondensePT(role); cond > 0 {
			fmt.Fprintf(b, "  letter-spacing: calc(-1 * var(--gd-%s-track));\n", id)
		}
		if st.KeepWithNext {
			b.WriteString("  break-after: avoid;\n  page-break-after: avoid;\n")
		}
		if st.PageBreakBefore {
			b.WriteString("  break-before: page;\n  page-break-before: always;\n")
		}
		// A Latin face for digits, as 公文 pairs 阿拉伯数字 with Times New Roman.
		fmt.Fprintf(b, "  unicode-bidi: isolate;\n")
		b.WriteString("}\n")

		if st.LatinFont != "" && st.LatinFont != st.Font {
			fmt.Fprintf(b, ".gd-role-%s .gd-latin { font-family: var(--gd-%s-latin); }\n", id, id)
		}
		writeBorderRules(b, id, st)
	}

	// 空块（红色分隔线等）只保留下边框，不占字形高度。
	// 类名 gd-rule-block 刻意不同于应用壳的 .gd-empty，纸张规则不外泄。
	b.WriteString(`
/* ---- 空块：红色分隔线与版记横线 ---- */
.gd-block.gd-rule-block {
  min-height: 0;
}

`)
}

// writeBorderRules emits the 红色分隔线 (标准 §7.2.6) and 版记 rules.
func writeBorderRules(b *strings.Builder, id string, st gbstandard.ParaStyle) {
	if st.BorderBottom != nil {
		br := st.BorderBottom
		fmt.Fprintf(b, ".gd-role-%s {\n", id)
		fmt.Fprintf(b, "  border-bottom: %s solid #%s;\n", cssWidthOr(br, "1px"), hexOr(br.Color, gbstandard.ColorRed))
		fmt.Fprintf(b, "  padding-bottom: %s;\n", pt(br.SpacePT))
		b.WriteString("}\n")
	}
	if st.BorderTop != nil {
		br := st.BorderTop
		fmt.Fprintf(b, ".gd-role-%s {\n", id)
		fmt.Fprintf(b, "  border-top: %s solid #%s;\n", cssWidthOr(br, "1px"), hexOr(br.Color, gbstandard.ColorBlack))
		fmt.Fprintf(b, "  padding-top: %s;\n", pt(br.SpacePT))
		b.WriteString("}\n")
	}
}

// ---------------------------------------------------------------- chrome

// writeChromeRules styles the things the fragment itself creates: the 层次序数
// prefix and the provenance attributes the UI layer highlights. It contains no
// typographic magnitude beyond what the variables already carry.
func writeChromeRules(b *strings.Builder, l *layout) {
	b.WriteString(`/* ---- 层次序数（装配器生成，非原文）---- */
.gd-autonum {
  font-weight: inherit;
}

.gd-role-heading1,
.gd-role-heading2,
.gd-role-heading3,
.gd-role-heading4 {
  font-weight: inherit;
}

/* ---- 已声明的改动：复核高亮（非国标排版）----
 *
 * 被修改的块带 data-edited="1"（由 writeBlock 输出）。这里只加背景与左侧
 * 色条，**不动** font-size / line-height / margin / padding —— 纸张度量
 * 只能来自 gbstandard.Spec，一旦高亮改变盒模型，屏幕上看到的行数与分页就
 * 不再等于成品，预览也就失去了意义。
 *
 * 用 outline 而非 border 画左侧色条也是同一考虑：outline 不参与布局。
 */
.gd-block[data-edited='1'] {
  background-color: var(--gd-edited-bg);
  outline: var(--gd-edited-inset) solid var(--gd-edited-bar);
  outline-offset: 0;
  border-radius: 2px;
}

/* data-edit-kinds 把改动类型写在属性上，供应用外壳的图例说明与自动化检查
 * 读取；它不产生任何视觉副作用，此处不再重复声明。 */

/* ---- 表格 ---- */
.gd-paper-table {
  border-collapse: collapse;
  width: 100%;
  font-size: var(--gd-body-size);
  font-family: var(--gd-body-font);
  line-height: calc(var(--gd-line-box) * 0.9);
}

.gd-paper-table td {
  border: 1px solid var(--gd-black);
  padding: 0.15em 0.4em;
  vertical-align: top;
  text-align: left;
  text-indent: 0;
}

/* ---- 图片 ---- */
.gd-image {
  max-width: 100%;
  height: auto;
  display: block;
  margin: 0 auto;
}

/* ---- 屏幕上的纸张：窄屏等比缩小，而不是重新分页 ---- */
@media screen and (max-width: 1024px) {
  .gd-root {
    align-items: flex-start;
    overflow-x: auto;
  }
}

@media print {
  .gd-root {
    gap: 0;
    padding: 0;
    background: #ffffff;
  }

  .gd-page {
    box-shadow: none;
    break-after: page;
    page-break-after: always;
  }

  .gd-page:last-child {
    break-after: auto;
    page-break-after: auto;
  }
}

@media (prefers-reduced-motion: reduce) {
  .gd-root * {
    transition: none;
  }
}
`)
}

// ---------------------------------------------------------------- formatting

// mm formats a millimetre value as a CSS length, trimming trailing zeros so the
// emitted stylesheet is stable and readable.
func mm(v float64) string { return trimFloat(v) + "mm" }

// pt formats a point value as a CSS length.
func pt(v float64) string { return trimFloat(v) + "pt" }

// fontFamily turns a typeface into a CSS font-family fallback chain using the
// standard's own substitution table, so a machine without 仿宋_GB2312 falls
// back through 仿宋 → FangSong → STFangsong before a generic family.
func fontFamily(primary string) string {
	if primary == "" {
		return "serif"
	}
	chain := gbstandard.FontChain(primary)
	quoted := make([]string, 0, len(chain)+1)
	hasGeneric := false
	for _, name := range chain {
		if isGenericFamily(name) {
			hasGeneric = true
		}
		quoted = append(quoted, quoteFont(name))
	}
	// 中文字体的通用兜底：全部具名字体都缺失时仍能落到可用字形。
	if !hasGeneric {
		quoted = append(quoted, "serif")
	}
	return strings.Join(quoted, ", ")
}

// isGenericFamily reports whether a CSS family is a generic keyword, which must
// not be quoted and makes an explicit fallback redundant.
func isGenericFamily(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "serif", "sans-serif", "monospace", "cursive", "fantasy",
		"system-ui", "ui-serif", "ui-sans-serif", "ui-monospace":
		return true
	}
	return false
}

// quoteFont quotes a family name that contains a space or non-ASCII character,
// as CSS requires, and leaves simple identifiers bare.
func quoteFont(name string) string {
	if name == "" {
		return "serif"
	}
	simple := true
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
		default:
			simple = false
		}
	}
	if simple {
		return name
	}
	return `"` + strings.ReplaceAll(name, `"`, "") + `"`
}

// trimFloat renders a float without a trailing ".000000", which keeps the
// generated CSS diff-stable and easy to read.
func trimFloat(v float64) string {
	s := fmt.Sprintf("%.4f", v)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	if s == "" || s == "-0" {
		return "0"
	}
	return s
}

// hexOr renders a spec colour as a CSS hex colour, defaulting when the spec
// leaves the field empty.
func hexOr(color, fallback string) string {
	c := strings.TrimPrefix(strings.TrimSpace(color), "#")
	if c == "" {
		c = fallback
	}
	return strings.ToLower(c)
}

// borderColor returns a border rule's colour with a fallback.
func borderColor(br *gbstandard.BorderRule) string {
	if br == nil {
		return gbstandard.ColorRed
	}
	return hexOr(br.Color, gbstandard.ColorRed)
}

// cssWidthOr prefers the spec's own CSS width for a rule.
func cssWidthOr(br *gbstandard.BorderRule, fallback string) string {
	if br != nil && br.CSSWidth != "" {
		return br.CSSWidth
	}
	return fallback
}

func boolInt(v bool, yes, no int) int {
	if v {
		return yes
	}
	return no
}
