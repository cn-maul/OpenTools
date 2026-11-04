// Package assemble turns a verified structural plan into a renderable
// document.
//
// # Why this package exists separately
//
// Two invariants of the project are enforced here and nowhere else:
//
//  1. Every character of source content that reaches the output is copied
//     from docmodel.Node.Text. This is the only file in the program that
//     reads a node and writes an OutBlock, so "原文未被改写" can be audited
//     by reading one file rather than by trusting the model.
//  2. All 公文 scaffolding — 层次序数 numbering ("一、" "（一）" "1." "（1）"),
//     the 版头/主体/版记 page grouping, the derived date line — is computed
//     from the plan's structure. None of it is model-authored prose, so a
//     hallucination cannot introduce text into the document.
//
// Build refuses to run on a plan that fails VerifyPlan. It is deliberately
// impossible to render around a defect: a document that silently dropped a
// paragraph would be worse than no document.
package assemble

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"opentools/tools/govdoc/engine/docmodel"
	"opentools/tools/govdoc/engine/gbstandard"
)

// looseDateRe matches the date shapes a model or a metadata form produces,
// capturing year, month and day so they can be reprinted in the standard's
// form. Ordered alternation: 年月日 first, then the numeric separators.
var looseDateRe = regexp.MustCompile(`^\s*(\d{4})\s*[年\-/.]\s*(\d{1,2})\s*[月\-/.]\s*(\d{1,2})\s*日?\s*$`)

// ErrPlanNotRenderable is returned when the plan fails verification. The
// assembler refuses to guess: a plan that loses or duplicates content must be
// fixed, never rendered around.
var ErrPlanNotRenderable = errors.New("assemble: 结构方案未通过校验")

// Result carries the rendered document plus what a reviewer needs to audit it.
//
// # What makes the output trustworthy now that content may change
//
// The original design promised the text was untouched; the user needs typos
// fixed, so the promise is now the stronger and more useful one: *no change can
// happen silently*. Three lists make that checkable:
//
//   - SynthText — every character the program generated that has no source
//     node (编号, 版头 slots). Nothing else may be invented.
//   - Diff.Changed — every declared edit that reached the output, with its
//     before/after and the reason, so the user can review the diff.
//   - Diff.Altered — any difference from the source that was NOT declared.
//     This must be empty; if it is not, the export is refused.
type Result struct {
	Doc *docmodel.Doc
	// SynthText lists every output string that has no source node: numbering
	// prefixes and the generated element slots. A correctly assembled
	// document's Diff.Extra should equal (or be a subset of) this list.
	SynthText []string
	// Diff is the comparison against the source, including the change log.
	Diff *docmodel.VerbatimDiff
	// Report is the plan verdict. Non-nil and OK()==true on success.
	Report *docmodel.Report
	// FailedEdits are declared edits whose target text was not found. They are
	// reported rather than dropped: a model asking to fix a typo that is not
	// present is information the user needs when judging the plan.
	FailedEdits []docmodel.UnappliedEdit
}

// Build resolves a plan into a renderable document.
//
// The sequence is: verify → place 版头 → place 标题/主送机关 → walk the
// section tree emitting numbered headings and body blocks → place 附件说明/
// 署名/成文日期/附注 → place 版记.
func Build(doc *docmodel.Document, plan *docmodel.Plan, spec gbstandard.Spec) (*Result, error) {
	if doc == nil {
		return nil, fmt.Errorf("%w: 缺少源文档", ErrPlanNotRenderable)
	}
	report := docmodel.VerifyPlan(doc, plan)
	if !report.OK() {
		return &Result{Report: report}, fmt.Errorf("%w: %s", ErrPlanNotRenderable, report.Summary())
	}

	problems := gbstandard.ValidateSpec(spec)
	if len(problems) > 0 {
		return &Result{Report: report},
			fmt.Errorf("%w: 格式规范不完整：%s", ErrPlanNotRenderable, strings.Join(problems, "；"))
	}

	a := &assembler{
		doc:   doc,
		plan:  plan,
		spec:  spec,
		used:  map[string]bool{},
		bound: map[docmodel.Role]string{},
	}
	for _, e := range plan.Elements {
		if _, dup := a.bound[e.Role]; !dup {
			a.bound[e.Role] = e.NodeID
		}
	}

	a.buildHeader()
	a.buildBody()
	a.buildFooter()

	out := &docmodel.Doc{Meta: plan.Meta, Blocks: a.blocks}
	diff := docmodel.VerifyVerbatim(doc, out, plan)

	// FailedEdits and Diff.Unapplied describe the same thing — declared edits
	// that did not reach the text — so they must not disagree. The assembler
	// only notices edits it tried to apply (via nodeText), so an edit the
	// assembler never reached would be missing here while still appearing in
	// the diff; that asymmetry is what makes a report confusing to act on.
	// The diff is the authority: it saw every declared edit.
	failed := a.failedEdits
	if diff != nil && len(diff.Unapplied) > 0 {
		seen := make(map[string]bool, len(failed))
		for _, f := range failed {
			seen[f.NodeID+"\x00"+f.Before] = true
		}
		for _, u := range diff.Unapplied {
			key := u.NodeID + "\x00" + u.Before
			if seen[key] {
				continue
			}
			seen[key] = true
			failed = append(failed, u)
		}
	}

	return &Result{
		Doc:         out,
		SynthText:   a.synth,
		Diff:        diff,
		Report:      report,
		FailedEdits: failed,
	}, nil
}

// assembler accumulates output blocks and tracks which source nodes have
// already been emitted, so an element node cannot be printed twice.
type assembler struct {
	doc    *docmodel.Document
	plan   *docmodel.Plan
	spec   gbstandard.Spec
	blocks []docmodel.OutBlock
	// synth collects the auto-generated strings, in emission order.
	synth []string
	// used marks source node ids already emitted.
	used map[string]bool
	// bound maps an element role to its source node id.
	bound map[docmodel.Role]string
	// counters drives the 层次序数 numbering.
	counters [5]int
	// failedEdits collects declared edits whose Before text was not found, so
	// Build can report them instead of letting them vanish.
	failedEdits []docmodel.UnappliedEdit
}

// nodeText returns a source node's text with its declared edits applied.
//
// This is the ONLY place in the program that changes source text, and it never
// does so without a declaration: every character that differs from the upload
// is attributable to a docmodel.Edit the user can see, with a Before, an After
// and a stated reason. An edit whose Before is not found is recorded in
// failedEdits rather than silently skipped, because "the model asked to fix a
// typo that is not there" is something the user should be told.
func (a *assembler) nodeText(nodeID string) string {
	n, ok := a.doc.Node(nodeID)
	if !ok {
		return ""
	}
	edits := a.plan.EditsFor(nodeID)
	if len(edits) == 0 {
		return n.Text
	}
	out, failed := docmodel.AppliedText(n.Text, edits)
	for _, f := range failed {
		a.failedEdits = append(a.failedEdits, docmodel.UnappliedEdit{
			NodeID: f.NodeID, Kind: string(f.Kind),
			Before: truncateRunes(f.Before, 60),
			Reason: "在原文中未找到待替换内容，该修改未生效",
		})
	}
	return out
}

// truncateRunes shortens on rune boundaries, so a Chinese excerpt is never cut
// mid-character.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// emit appends a block carrying a source node's text, with that node's
// declared edits applied.
//
// The text comes from nodeText, which returns the source verbatim when there
// are no edits for the node and applies only edits the plan declared
// otherwise. Editing the text of an element slot is deliberately allowed: a
// 标题 with a typo is exactly the thing a user wants fixed, and the change
// still has to be declared to survive verification.
func (a *assembler) emit(role docmodel.Role, nodeID string, opts ...blockOpt) {
	n, ok := a.doc.Node(nodeID)
	if !ok {
		// VerifyPlan already rejected unknown ids, so reaching here means
		// the caller bypassed verification. Failing loudly beats emitting a
		// blank paragraph the user would not notice until printing.
		panic(fmt.Sprintf("assemble: 节点 %q 不存在（方案未经校验？）", nodeID))
	}
	a.used[nodeID] = true
	b := docmodel.OutBlock{
		Role:   role,
		Text:   a.nodeText(nodeID),
		NodeID: nodeID,
		Runs:   n.Runs,
		Rows:   n.Rows,
		Image:  n.Image,
	}
	// Flag the block when a declared edit changed its text, so the renderers
	// can show the user which paragraphs were touched. Only edits that
	// actually alter something count: an edit whose After equals its Before
	// leaves the text identical and must not be advertised as a change.
	for _, ed := range a.plan.EditsFor(nodeID) {
		if ed.Before == ed.After {
			continue
		}
		b.Edited = true
		b.EditKinds = append(b.EditKinds, string(ed.Kind))
	}
	for _, o := range opts {
		o(&b)
	}
	a.blocks = append(a.blocks, b)
}

// emitSynth appends a block whose text this package generated. Every string
// it produces is recorded in synth, so the reviewer can see all invented text.
func (a *assembler) emitSynth(role docmodel.Role, text string, opts ...blockOpt) {
	b := docmodel.OutBlock{Role: role, Text: text}
	for _, o := range opts {
		o(&b)
	}
	a.blocks = append(a.blocks, b)
	if text != "" {
		a.synth = append(a.synth, text)
	}
}

type blockOpt func(*docmodel.OutBlock)

// withNumber attaches the 层次序数 prefix and records it as synthesized.
func withNumber(prefix string) blockOpt {
	return func(b *docmodel.OutBlock) { b.AutoNumber = prefix }
}

func withLevel(level int) blockOpt {
	return func(b *docmodel.OutBlock) { b.Level = level }
}

func withPageBreak() blockOpt {
	return func(b *docmodel.OutBlock) { b.PageBreakBefore = true }
}

func withSpaceBeforeLines(v float64) blockOpt {
	return func(b *docmodel.OutBlock) { b.SpaceBeforeLines = v }
}

// textOf returns a bound node's verbatim text, and whether it was bound.
// textOf returns a bound node's text (with its declared edits applied), and
// whether the role was bound at all.
func (a *assembler) textOf(role docmodel.Role) (string, bool) {
	id, ok := a.bound[role]
	if !ok || id == "" {
		return "", false
	}
	if _, ok := a.doc.Node(id); !ok {
		return "", false
	}
	return a.nodeText(id), true
}

// ---------------------------------------------------------------- 版头

// buildHeader emits 份号 → 密级 → 紧急程度 → 发文机关标志 → 发文字号/
// 签发人 → 红色分隔线, per GB/T 9704-2012 §7.2.
//
// Elements that are neither bound to a source node nor present in Meta are
// skipped entirely rather than emitted blank: a 公文 without 密级 must not
// print an empty line where the 密级 would go.
func (a *assembler) buildHeader() {
	header := []struct {
		role docmodel.Role
		meta string
	}{
		{docmodel.RoleCopyNumber, a.plan.Meta.CopyNumber},
		{docmodel.RoleSecurity, a.plan.Meta.Security},
		{docmodel.RoleUrgency, a.plan.Meta.Urgency},
	}

	for _, h := range header {
		if id, ok := a.bound[h.role]; ok && !a.used[id] {
			a.emit(h.role, id)
			continue
		}
		if strings.TrimSpace(h.meta) != "" {
			a.emitSynth(h.role, strings.TrimSpace(h.meta))
		}
	}

	// 发文机关标志. §4.3.2 fixes its distance from the top of the 版心 at
	// 35mm (平行文/下行文) or 80mm (上行文); the style table cannot express a
	// per-document offset, so it is attached to the block. The offset is
	// converted from millimetres into the style's line-height units.
	if id, ok := a.bound[docmodel.RoleMasthead]; ok && !a.used[id] {
		a.emit(docmodel.RoleMasthead, id, withSpaceBeforeLines(a.mastheadOffsetLines()))
	} else if strings.TrimSpace(a.plan.Meta.IssuingOrg) != "" {
		a.emitSynth(docmodel.RoleMasthead, strings.TrimSpace(a.plan.Meta.IssuingOrg),
			withSpaceBeforeLines(a.mastheadOffsetLines()))
	}

	// 发文字号, then 签发人 (上行文 only).
	if id, ok := a.bound[docmodel.RoleDocNumber]; ok && !a.used[id] {
		a.emit(docmodel.RoleDocNumber, id)
	} else if strings.TrimSpace(a.plan.Meta.DocNumber) != "" {
		a.emitSynth(docmodel.RoleDocNumber, strings.TrimSpace(a.plan.Meta.DocNumber))
	}
	if id, ok := a.bound[docmodel.RoleSignatory]; ok && !a.used[id] {
		a.emit(docmodel.RoleSignatory, id)
	} else if strings.TrimSpace(a.plan.Meta.Signatory) != "" {
		a.emitSynth(docmodel.RoleSignatory, "签发人："+strings.TrimSpace(a.plan.Meta.Signatory))
	}

	// 红色分隔线: an empty paragraph carrying only a bottom border. It is
	// emitted only when the document actually has a 版头 to close off.
	if len(a.blocks) > 0 {
		a.emitSynth(docmodel.RoleRedLine, "")
	}
}

// mastheadOffsetLines converts the 行文方向-dependent offset from the top of
// the 版心 into the "hundredths of a line" unit the style table uses, net of
// the space the style itself already adds.
func (a *assembler) mastheadOffsetLines() float64 {
	sp := a.spec.Styles[docmodel.RoleMasthead].LineSpacingPT
	if sp <= 0 {
		// The masthead style is 自动行距, so measure with the body's fixed
		// line height — that is the grid the 标准 counts lines on.
		sp = a.spec.Page.LineSpacingPT()
	}
	if sp <= 0 {
		return 0
	}
	mm := a.spec.Direction.MastheadTopMM()
	lines := (mm * 72.0 / 25.4) / sp
	// The style already contributes SpaceAfterLines; the offset is expressed
	// as space *before* so the two compose instead of overwriting.
	return lines * 100
}

// ---------------------------------------------------------------- 主体

// buildBody emits 标题 → 主送机关 → 正文/章节 → 附件说明 → 署名 → 成文日期
// → 附注, per §7.3.
func (a *assembler) buildBody() {
	// 标题. A bound node keeps its own wording; otherwise Meta.Title is a
	// synthesized line.
	if id, ok := a.bound[docmodel.RoleTitle]; ok && !a.used[id] {
		a.emit(docmodel.RoleTitle, id)
	} else if strings.TrimSpace(a.plan.Meta.Title) != "" {
		a.emitSynth(docmodel.RoleTitle, strings.TrimSpace(a.plan.Meta.Title))
	}

	if id, ok := a.bound[docmodel.RoleRecipient]; ok && !a.used[id] {
		a.emit(docmodel.RoleRecipient, id)
	} else if strings.TrimSpace(a.plan.Meta.Recipient) != "" {
		a.emitSynth(docmodel.RoleRecipient, strings.TrimSpace(a.plan.Meta.Recipient))
	}

	// 前言: body prose that sits directly under 主送机关, before the first
	// 层次序数 heading. It is emitted as unnumbered 正文 paragraphs — the
	// standard gives 正文 no numbering, so nothing is invented here, and the
	// 层次序数 sequence stays free to start at 一、 on the first real heading.
	for _, id := range a.plan.PreambleNodeIDs {
		if a.used[id] {
			continue
		}
		if _, ok := a.doc.Node(id); !ok {
			continue
		}
		a.emit(docmodel.RoleBody, id)
	}

	for i := range a.plan.Sections {
		a.emitSection(&a.plan.Sections[i], 1)
	}

	// 附件说明.
	if id, ok := a.bound[docmodel.RoleAttachmentNote]; ok && !a.used[id] {
		a.emit(docmodel.RoleAttachmentNote, id)
	} else if strings.TrimSpace(a.plan.Meta.AttachmentNote) != "" {
		a.emitSynth(docmodel.RoleAttachmentNote, strings.TrimSpace(a.plan.Meta.AttachmentNote))
	}

	// 发文机关署名 and 成文日期.
	if id, ok := a.bound[docmodel.RoleSignature]; ok && !a.used[id] {
		a.emit(docmodel.RoleSignature, id)
	} else if strings.TrimSpace(a.plan.Meta.Signature) != "" {
		a.emitSynth(docmodel.RoleSignature, strings.TrimSpace(a.plan.Meta.Signature))
	}
	if id, ok := a.bound[docmodel.RoleDate]; ok && !a.used[id] {
		a.emit(docmodel.RoleDate, id)
	} else if strings.TrimSpace(a.plan.Meta.Date) != "" {
		a.emitSynth(docmodel.RoleDate, normalizeDate(a.plan.Meta.Date))
	}

	// 附注.
	if id, ok := a.bound[docmodel.RoleNote]; ok && !a.used[id] {
		a.emit(docmodel.RoleNote, id)
	} else if strings.TrimSpace(a.plan.Meta.Note) != "" {
		note := strings.TrimSpace(a.plan.Meta.Note)
		// §7.3.7: 附注 加圆括号.
		if !strings.HasPrefix(note, "（") && !strings.HasPrefix(note, "(") {
			note = "（" + note + "）"
		}
		a.emitSynth(docmodel.RoleNote, note)
	}
}

// emitSection renders one chapter and its subtree.
//
// The numbering is generated here from the tree position, never taken from
// the model, which is what makes 层次序数 deterministic. When the section's
// heading node already begins with its own number (a common case in real
// 公文 that were written correctly), that number is used as-is and no prefix
// is added — otherwise the heading would read "一、一、总体要求".
func (a *assembler) emitSection(s *docmodel.Section, depth int) {
	level := s.Level
	if level < 1 {
		level = depth
	}
	if level > 4 {
		level = 4
	}

	// When the heading comes from the source, its own 层次序数 is
	// authoritative about the heading's depth — the text carries that number
	// and cannot be rewritten, so honouring it is the only way the printed
	// number can agree with the text.
	//
	// Without this, a plan that puts a "（一）…" paragraph at section level 1
	// would print "三、（一）检查范围。…": a level-1 number glued to a level-2
	// heading, which is both wrong and visibly so. A model can easily produce
	// that mismatch (it is inferring structure, and the source's own numbering
	// is the stronger signal), so the assembler resolves it rather than
	// depending on the plan being perfect.
	if s.HeadingNodeID != "" && !a.used[s.HeadingNodeID] {
		if _, lvl, ok := detectHeadingNumber(a.nodeText(s.HeadingNodeID)); ok {
			if lvl != level {
				level = lvl
			}
		}
	}

	role := docmodel.HeadingRole(level)

	// Advance this level's counter and reset deeper ones.
	a.counters[level]++
	for d := level + 1; d <= 4; d++ {
		a.counters[d] = 0
	}

	number := a.numberFor(level)

	if s.HeadingNodeID != "" && !a.used[s.HeadingNodeID] {
		// The heading text comes from the source (with any declared edit
		// applied) and already carries its own 层次序数 when it has one; level
		// was reconciled with that form above, so adopting the number here
		// keeps the sequence coherent.
		text := a.nodeText(s.HeadingNodeID)
		if own, ok := parseOwnNumber(level, text); ok {
			a.counters[level] = own
			a.emit(role, s.HeadingNodeID, withLevel(level))
		} else {
			a.emit(role, s.HeadingNodeID, withLevel(level), withNumber(number))
		}
	} else {
		// No source heading: the title is model-authored. It is recorded as
		// synthesized so the audit listing shows it.
		title := strings.TrimSpace(s.Title)
		if title == "" {
			title = fmt.Sprintf("第%d部分", a.counters[level])
		}
		if hasOwnNumber(level, title) {
			if own, ok := parseOwnNumber(level, title); ok {
				a.counters[level] = own
			}
			a.emitSynth(role, title, withLevel(level))
		} else {
			a.emitSynth(role, title, withLevel(level), withNumber(number))
		}
	}

	for _, id := range s.BodyNodeIDs {
		if a.used[id] {
			// VerifyPlan rejects double claims, so this is unreachable for a
			// verified plan; the guard keeps a hand-edited plan from
			// printing a paragraph twice.
			continue
		}
		n, ok := a.doc.Node(id)
		if !ok {
			continue
		}
		// A node's own kind decides how it is rendered; the section only
		// decides where it sits.
		switch n.Kind {
		case docmodel.KindTable:
			a.emit(docmodel.RoleBody, id)
		case docmodel.KindImage:
			a.emit(docmodel.RoleBody, id)
		case docmodel.KindHeading:
			// A source heading used as body text (the model put it under a
			// different parent than its level implies) still renders as a
			// heading, at its own level, so the numbering stays coherent.
			lvl := n.Level
			if lvl < 1 || lvl > 4 {
				lvl = level
			}
			a.emit(docmodel.HeadingRole(lvl), id, withLevel(lvl))
		default:
			a.emit(docmodel.RoleBody, id)
		}
	}

	for i := range s.Children {
		a.emitSection(&s.Children[i], level+1)
	}
}

// numberFor renders the 层次序数 prefix for a level, per §7.3.3:
//
//	一级 "一、"   二级 "（一）"   三级 "1."   四级 "（1）"
func (a *assembler) numberFor(level int) string {
	n := a.counters[level]
	if n <= 0 {
		n = 1
	}
	switch level {
	case 1:
		return chineseNumeral(n) + "、"
	case 2:
		return "（" + chineseNumeral(n) + "）"
	case 3:
		return fmt.Sprintf("%d.", n)
	default:
		return fmt.Sprintf("（%d）", n)
	}
}

// ---------------------------------------------------------------- 版记

// buildFooter emits 抄送机关 and 印发机关和印发日期, per §7.5.
func (a *assembler) buildFooter() {
	if id, ok := a.bound[docmodel.RoleCC]; ok && !a.used[id] {
		a.emit(docmodel.RoleCC, id)
	} else if strings.TrimSpace(a.plan.Meta.CC) != "" {
		a.emitSynth(docmodel.RoleCC, "抄送："+strings.TrimSpace(a.plan.Meta.CC))
	}

	if id, ok := a.bound[docmodel.RolePrinter]; ok && !a.used[id] {
		a.emit(docmodel.RolePrinter, id)
	} else if strings.TrimSpace(a.plan.Meta.Printer) != "" || strings.TrimSpace(a.plan.Meta.PrintDate) != "" {
		line := strings.TrimSpace(a.plan.Meta.Printer)
		if d := strings.TrimSpace(a.plan.Meta.PrintDate); d != "" {
			if line != "" {
				line += "  "
			}
			line += normalizeDate(d) + "印发"
		}
		a.emitSynth(docmodel.RolePrinter, line)
	}
}

// ---------------------------------------------------------------- helpers

// chineseNumeral renders 1..99 as the 层次序数 form the standard uses.
// 一级标题 counts to at least 十, so 11 must read "十一" rather than "一一".
func chineseNumeral(n int) string {
	if n <= 0 {
		return "一"
	}
	if n < 10 {
		return string([]rune("一二三四五六七八九")[n-1])
	}
	if n == 10 {
		return "十"
	}
	if n < 20 {
		return "十" + string([]rune("一二三四五六七八九")[n-11])
	}
	if n < 100 {
		rest := n % 10
		if rest == 0 {
			return string([]rune("一二三四五六七八九")[n/10-1]) + "十"
		}
		return string([]rune("一二三四五六七八九")[n/10-1]) + "十" +
			string([]rune("一二三四五六七八九")[rest-1])
	}
	// Beyond 99 a 公文 has deeper problems than numbering; fall back to a
	// form that is at least unambiguous.
	return fmt.Sprintf("%d", n)
}

// parseOwnNumber reads the 层次序数 a heading already carries, returning its
// numeric value. Like hasOwnNumber it accepts the separator variants Word
// produces, but it recovers the number so the per-level counter can continue
// from the source instead of restarting at one.
//
// ok is false when the text carries no prefix of the right shape.
func parseOwnNumber(level int, text string) (int, bool) {
	t := strings.TrimLeft(text, " \t\u3000")
	if t == "" {
		return 0, false
	}
	r := []rune(t)

	switch level {
	case 1:
		// "一、" "十二、"
		i := 0
		for i < len(r) && strings.ContainsRune("一二三四五六七八九十", r[i]) {
			i++
		}
		if i == 0 || i >= len(r) || (r[i] != '、' && r[i] != '.') {
			return 0, false
		}
		if n, ok := parseChineseNumeral(string(r[:i])); ok {
			return n, true
		}
		return 0, false

	case 2:
		// "（一）" or "(一)"
		if len(r) >= 3 && (r[0] == '（' || r[0] == '(') {
			j := 1
			for j < len(r) && strings.ContainsRune("一二三四五六七八九十", r[j]) {
				j++
			}
			if j > 1 && j < len(r) && (r[j] == '）' || r[j] == ')') {
				if n, ok := parseChineseNumeral(string(r[1:j])); ok {
					return n, true
				}
			}
		}
		return 0, false

	case 3:
		// "1." "12、"
		i := 0
		for i < len(r) && r[i] >= '0' && r[i] <= '9' {
			i++
		}
		if i == 0 || i >= len(r) || (r[i] != '.' && r[i] != '、' && r[i] != '．') {
			return 0, false
		}
		return parseInt(string(r[:i]))

	default:
		// "（1）" or "(1)"
		if len(r) >= 3 && (r[0] == '（' || r[0] == '(') {
			j := 1
			for j < len(r) && r[j] >= '0' && r[j] <= '9' {
				j++
			}
			if j > 1 && j < len(r) && (r[j] == '）' || r[j] == ')') {
				return parseInt(string(r[1:j]))
			}
		}
		return 0, false
	}
}

// parseChineseNumeral converts the 层次序数 numeral forms used by 公文
// (一 … 十, 十一 … 九十九) back to an integer.
func parseChineseNumeral(s string) (int, bool) {
	const digits = "一二三四五六七八九"
	runes := []rune(s)
	if len(runes) == 0 {
		return 0, false
	}

	// digit returns the value of a single numeral rune, or -1.
	//
	// digits is iterated as []rune because `for i, d := range string` yields a
	// BYTE offset for i. With a multi-byte 汉字 table that offset is wrong
	// (二 sits at byte 3), so the rune slice is what makes the index a value.
	digitRunes := []rune(digits)
	digit := func(r rune) int {
		for i, d := range digitRunes {
			if d == r {
				return i + 1
			}
		}
		return -1
	}

	// 十 is a numeral in its own right but is not in the 一..九 table, so it is
	// matched before the single-digit case.
	if s == "十" {
		return 10, true
	}

	// Plain 一..九.
	if len(runes) == 1 {
		if v := digit(runes[0]); v > 0 {
			return v, true
		}
		return 0, false
	}

	// Locate 十 by rune index. (Using strings.IndexRune here would yield a
	// BYTE offset, which is wrong for any multi-character input — 二 is three
	// bytes, so "二十一" would index as if 十 sat at byte 4.)
	ten := -1
	for i, r := range runes {
		if r == '十' {
			ten = i
			break
		}
	}
	if ten < 0 {
		return 0, false
	}

	tens := 1
	if ten > 0 {
		v := digit(runes[ten-1])
		if v < 0 {
			return 0, false
		}
		tens = v
	}
	ones := 0
	if ten+1 < len(runes) {
		// Exactly one digit may follow 十.
		if ten+2 != len(runes) {
			return 0, false
		}
		v := digit(runes[ten+1])
		if v < 0 {
			return 0, false
		}
		ones = v
	}
	return tens*10 + ones, true
}

// parseInt parses a run of ASCII digits.
func parseInt(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
		if n > 1_000_000 {
			return 0, false
		}
	}
	return n, true
}

// detectHeadingNumber reads a heading's own 层次序数, returning both its
// numeric value and which level the *form* implies (1 → "一、", 2 → "（一）",
// 3 → "1.", 4 → "（1）").
//
// It exists because the form is unambiguous even when the plan's level is
// wrong: "（一）检查范围" is a level-2 heading by construction. Reporting the
// implied level lets the assembler correct a mismatched section rather than
// printing a level-1 number in front of level-2 text.
//
// ok is false when the text carries no recognisable prefix.
func detectHeadingNumber(text string) (value int, level int, ok bool) {
	for lvl := 1; lvl <= 4; lvl++ {
		if v, found := parseOwnNumber(lvl, text); found {
			return v, lvl, true
		}
	}
	return 0, 0, false
}

// hasOwnNumber reports whether text already starts with the 层次序数 prefix
// appropriate to its level. Real 公文 very often do, and prefixing again
// would visibly corrupt the heading.
func hasOwnNumber(level int, text string) bool {
	t := strings.TrimLeft(text, " \t\u3000")
	if t == "" {
		return false
	}
	r := []rune(t)
	switch level {
	case 1:
		// "一、" "十二、"
		i := 0
		for i < len(r) && strings.ContainsRune("一二三四五六七八九十", r[i]) {
			i++
		}
		return i > 0 && i < len(r) && (r[i] == '、' || r[i] == '.')
	case 2:
		// "（一）" or "(一)"
		if len(r) >= 3 && (r[0] == '（' || r[0] == '(') {
			j := 1
			for j < len(r) && strings.ContainsRune("一二三四五六七八九十", r[j]) {
				j++
			}
			return j > 1 && j < len(r) && (r[j] == '）' || r[j] == ')')
		}
		return false
	case 3:
		// "1." "12、"
		i := 0
		for i < len(r) && r[i] >= '0' && r[i] <= '9' {
			i++
		}
		return i > 0 && i < len(r) && (r[i] == '.' || r[i] == '、' || r[i] == '．')
	default:
		// "（1）" or "(1)"
		if len(r) >= 3 && (r[0] == '（' || r[0] == '(') {
			j := 1
			for j < len(r) && r[j] >= '0' && r[j] <= '9' {
				j++
			}
			return j > 1 && j < len(r) && (r[j] == '）' || r[j] == ')')
		}
		return false
	}
}

// normalizeDate enforces §7.3.5.2: Arabic numerals, four-digit year, no zero
// padding. It only rewrites machine-supplied dates; a date bound to a source
// node is emitted verbatim and never reaches this function.
func normalizeDate(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	// Already the required shape.
	if matched, _ := matchDate(s); matched {
		return unfix(s)
	}
	if y, m, d, ok := parseLooseDate(s); ok {
		return fmt.Sprintf("%d年%d月%d日", y, m, d)
	}
	return s
}

// matchDate reports whether s already looks like "YYYY年M月D日".
func matchDate(s string) (bool, string) { return looseDateRe.MatchString(s), s }

// parseLooseDate extracts a year/month/day from the common shapes a model or
// a metadata form produces: "2024-05-01", "2024/5/1", "2024年05月01日",
// "2024.5.1".
func parseLooseDate(s string) (int, int, int, bool) {
	m := looseDateRe.FindStringSubmatch(s)
	if m == nil {
		return 0, 0, 0, false
	}
	var y, mo, d int
	if _, err := fmt.Sscanf(m[1]+" "+m[2]+" "+m[3], "%d %d %d", &y, &mo, &d); err != nil {
		return 0, 0, 0, false
	}
	if y < 1000 || y > 9999 || mo < 1 || mo > 12 || d < 1 || d > 31 {
		return 0, 0, 0, false
	}
	return y, mo, d, true
}

// unfix rewrites a matched date into the standard's form, dropping padding.
func unfix(s string) string {
	y, mo, d, ok := parseLooseDate(s)
	if !ok {
		return s
	}
	return fmt.Sprintf("%d年%d月%d日", y, mo, d)
}
