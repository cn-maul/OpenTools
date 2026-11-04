package docmodel

import (
	"fmt"
	"sort"
	"strings"
)

// ---------------------------------------------------------------- diagnostics

// Severity classifies a diagnostic.
type Severity string

const (
	// SevError means the plan is not renderable: it loses, duplicates or
	// invents content, or it is structurally malformed.
	SevError Severity = "error"
	// SevWarning means the plan is renderable but a reviewer should look:
	// text was reordered, a heading was synthesized, boilerplate was
	// dropped deliberately.
	SevWarning Severity = "warning"
	// SevInfo is provenance information, never a problem.
	SevInfo Severity = "info"
)

// Diagnostic is one verification finding. Code is a stable machine-readable
// identifier so the UI and tests can match findings without parsing prose.
type Diagnostic struct {
	Code     string   `json:"code"`
	Severity Severity `json:"severity"`
	Message  string   `json:"message"`
	// NodeIDs names the content involved, when applicable.
	NodeIDs []string `json:"node_ids,omitempty"`
}

// Report is the outcome of VerifyPlan.
type Report struct {
	Diagnostics []Diagnostic `json:"diagnostics"`
	// ContentNodes is how many prose-carrying nodes the source had.
	ContentNodes int `json:"content_nodes"`
	// CoveredNodes is how many of them the plan accounts for.
	CoveredNodes int `json:"covered_nodes"`
	// DuplicatedNodes is how many were referenced more than once.
	DuplicatedNodes int `json:"duplicated_nodes"`
	// UnknownRefs is how many references point at ids the source lacks.
	UnknownRefs int `json:"unknown_refs"`
	// ReorderedSections counts sections whose body order differs from the
	// source order.
	ReorderedSections int `json:"reordered_sections"`
	// SynthesizedHeadings counts headings the plan supplied itself rather
	// than pointing at existing source text.
	SynthesizedHeadings int `json:"synthesized_headings"`
	// BoundElements counts 公文 element slots bound to source nodes.
	BoundElements int `json:"bound_elements"`
	// HeadingsAsBody counts source heading paragraphs claimed as section body,
	// which makes them render as a second heading of that level.
	HeadingsAsBody int `json:"headings_as_body"`
}

// OK reports whether the plan is renderable: no error-severity diagnostics
// and every content node accounted for exactly once.
func (r *Report) OK() bool {
	if r == nil {
		return false
	}
	if r.DuplicatedNodes > 0 || r.UnknownRefs > 0 {
		return false
	}
	if r.CoveredNodes != r.ContentNodes {
		return false
	}
	for _, d := range r.Diagnostics {
		if d.Severity == SevError {
			return false
		}
	}
	return true
}

// Errors returns only the error-severity diagnostics.
func (r *Report) Errors() []Diagnostic {
	var out []Diagnostic
	for _, d := range r.Diagnostics {
		if d.Severity == SevError {
			out = append(out, d)
		}
	}
	return out
}

// Summary renders a one-line human summary.
func (r *Report) Summary() string {
	if r == nil {
		return "未校验"
	}
	status := "通过"
	if !r.OK() {
		status = "未通过"
	}
	return fmt.Sprintf("%s：正文节点 %d/%d 已覆盖，重复 %d，无效引用 %d",
		status, r.CoveredNodes, r.ContentNodes, r.DuplicatedNodes, r.UnknownRefs)
}

// BindableRoles lists the 公文 element slots a node may be bound to through
// Plan.Elements: exactly the slots the assembler places in the 版头/主体/版记
// skeleton by looking up a bound node.
//
// The list is closed on purpose. If an unknown role were accepted, VerifyPlan
// would count the node as claimed — so the coverage check would pass — while
// the assembler, which looks the role up by its canonical constant, would
// never emit it. The paragraph would vanish from the output with every check
// reporting success. Rejecting unknown roles here turns that silent data loss
// into a visible error.
func BindableRoles() []Role {
	return []Role{
		RoleCopyNumber, RoleSecurity, RoleUrgency, RoleMasthead,
		RoleDocNumber, RoleSignatory, RoleTitle, RoleRecipient,
		RoleAttachmentNote, RoleSignature, RoleDate, RoleNote,
		RoleCC, RolePrinter,
	}
}

// bindableSet is the lookup form of BindableRoles.
var bindableSet = func() map[Role]bool {
	out := make(map[Role]bool, len(BindableRoles()))
	for _, r := range BindableRoles() {
		out[r] = true
	}
	return out
}()

// ---------------------------------------------------------------- verification

// VerifyPlan checks a plan against its source document and decides whether it
// may be rendered. It is the single enforcement point of the project's
// central promise — 梳理结构但不改变原文 — so it is deliberately strict and
// purely structural: it never inspects "meaning", only identity and coverage.
//
// The rules, in the order they are reported:
//
//  1. unknown_ref  — a plan references a node id the document does not have.
//  2. duplicate    — a content node is claimed twice (as a heading and/or as
//     body in more than one place). Copying content is how a
//     "restructure" silently becomes a "rewrite", so this is
//     an error, not a warning.
//  3. uncovered    — a content node is claimed nowhere, i.e. content was
//     dropped. This is the failure mode the user fears most,
//     so it is an error.
//  4. heading_mismatch — a section points HeadingNodeID at a node but
//     supplies a different Title. Either it should have used
//     the node's text verbatim, or it should not have claimed
//     the node at all.
//  5. bad_level    — a child section is not deeper than its parent.
//  6. reordered    — body nodes within a section are out of source order and
//     PreserveOrder is unset. Renderable, but surfaced.
//  7. synthesized  — a heading with no HeadingNodeID. Renderable, but the
//     user should know the title is model-authored.
func VerifyPlan(doc *Document, plan *Plan) *Report {
	rep := &Report{}
	if plan == nil {
		rep.Diagnostics = append(rep.Diagnostics, Diagnostic{
			Code: "no_plan", Severity: SevError, Message: "未生成结构方案",
		})
		return rep
	}

	byID := doc.ByID()
	contentIDs := doc.ContentIDs()
	contentSet := make(map[string]bool, len(contentIDs))
	for _, id := range contentIDs {
		contentSet[id] = true
	}
	rep.ContentNodes = len(contentIDs)

	// order gives each id its source position, for the reorder check.
	order := make(map[string]int, len(doc.Nodes))
	if doc != nil {
		for _, n := range doc.Nodes {
			order[n.ID] = n.Order
		}
	}

	// claims counts how many times each prose-carrying node is claimed.
	claims := make(map[string]int, len(contentIDs))
	claimedAs := make(map[string][]string, len(contentIDs))

	claim := func(id, where string) {
		if id == "" {
			return
		}
		if _, ok := byID[id]; !ok {
			// Reported as a separate, aggregated diagnostic below; but a
			// reference to a node that does not exist must never inflate
			// the coverage count, so it is not recorded as a claim.
			return
		}
		if !contentSet[id] {
			// A non-prose node (page break) referenced as content: it is
			// neither coverage nor a duplicate, but it is a modelling
			// mistake worth flagging once.
			return
		}
		claims[id]++
		claimedAs[id] = append(claimedAs[id], where)
	}

	var unknown []string
	seenUnknown := map[string]bool{}
	noteUnknown := func(id string) {
		if id == "" || seenUnknown[id] {
			return
		}
		if _, ok := byID[id]; !ok {
			seenUnknown[id] = true
			unknown = append(unknown, id)
		}
	}

	// Element bindings are claimed before the tree walk, because a bound
	// node is by definition not body content: claiming it here is what stops
	// the source's own 标题/成文日期 paragraphs from being reported as
	// dropped content.
	rep.BoundElements = len(plan.Elements)
	for i, e := range plan.Elements {
		if e.NodeID == "" {
			rep.Diagnostics = append(rep.Diagnostics, Diagnostic{
				Code: "bad_element", Severity: SevError,
				Message: fmt.Sprintf("要素绑定 #%d（%s）缺少 node_id", i+1, e.Role),
			})
			continue
		}
		noteUnknown(e.NodeID)
		switch {
		case e.Role == RoleBody:
			rep.Diagnostics = append(rep.Diagnostics, Diagnostic{
				Code: "bad_element", Severity: SevError,
				Message: fmt.Sprintf("节点 %s 被绑定为正文（body）；正文内容必须通过章节的 body_node_ids 收录", e.NodeID),
				NodeIDs: []string{e.NodeID},
			})
			// A wrong slot must not count as coverage: the assembler would
			// never emit the node, so crediting it here would hide the loss.
			continue
		case !bindableSet[e.Role]:
			rep.Diagnostics = append(rep.Diagnostics, Diagnostic{
				Code: "bad_element", Severity: SevError,
				Message: fmt.Sprintf(
					"节点 %s 被绑定到未知要素 %q；可用要素：标题、主送机关、发文字号、发文机关标志、份号、密级、紧急程度、签发人、附件说明、发文机关署名、成文日期、附注、抄送机关、印发机关",
					e.NodeID, e.Role),
				NodeIDs: []string{e.NodeID},
			})
			continue
		}
		claim(e.NodeID, fmt.Sprintf("要素:%s", e.Role))
	}
	seenElementRole := map[Role]string{}
	for _, e := range plan.Elements {
		if e.Role == "" || e.NodeID == "" {
			continue
		}
		if prev, dup := seenElementRole[e.Role]; dup {
			rep.Diagnostics = append(rep.Diagnostics, Diagnostic{
				Code: "duplicate_element", Severity: SevError,
				Message: fmt.Sprintf("要素 %s 被绑定了多个节点（%s 与 %s）", e.Role, prev, e.NodeID),
				NodeIDs: []string{prev, e.NodeID},
			})
			continue
		}
		seenElementRole[e.Role] = e.NodeID
	}

	// Preamble content is claimed before the tree walk: it belongs under
	// 主送机关 rather than inside any chapter, so it must count as covered
	// without requiring a fabricated section for it.
	for _, id := range plan.PreambleNodeIDs {
		noteUnknown(id)
		claim(id, "前言")
	}

	// walk validates the tree and records every claim.
	var walk func(list []Section, parentLevel int, path string) bool
	walk = func(list []Section, parentLevel int, path string) bool {
		ok := true
		for i := range list {
			s := &list[i]
			where := fmt.Sprintf("%s/%s", path, s.ID)
			if s.ID == "" {
				rep.Diagnostics = append(rep.Diagnostics, Diagnostic{
					Code: "missing_section_id", Severity: SevError,
					Message: fmt.Sprintf("第 %d 个章节缺少 id（%s）", i+1, where),
				})
				ok = false
			}
			if s.Level < 1 {
				rep.Diagnostics = append(rep.Diagnostics, Diagnostic{
					Code: "bad_level", Severity: SevError,
					Message: fmt.Sprintf("章节 %s 的层级 %d 非法（应为 1-4）", s.ID, s.Level),
					NodeIDs: []string{s.ID},
				})
				ok = false
			}
			if parentLevel > 0 && s.Level <= parentLevel {
				rep.Diagnostics = append(rep.Diagnostics, Diagnostic{
					Code: "bad_level", Severity: SevError,
					Message: fmt.Sprintf("章节 %s 的层级 %d 未深于父级 %d", s.ID, s.Level, parentLevel),
					NodeIDs: []string{s.ID},
				})
				ok = false
			}

			if s.HeadingNodeID != "" {
				noteUnknown(s.HeadingNodeID)
				claim(s.HeadingNodeID, "标题:"+s.ID)
				if n, found := doc.Node(s.HeadingNodeID); found {
					if strings.TrimSpace(s.Title) != strings.TrimSpace(n.Text) {
						rep.Diagnostics = append(rep.Diagnostics, Diagnostic{
							Code: "heading_mismatch", Severity: SevError,
							Message: fmt.Sprintf(
								"章节 %s 的标题与原文不一致：原文 %q，方案 %q",
								s.ID, truncate(n.Text, 30), truncate(s.Title, 30)),
							NodeIDs: []string{s.HeadingNodeID},
						})
						ok = false
					}
				}
			} else {
				rep.SynthesizedHeadings++
				rep.Diagnostics = append(rep.Diagnostics, Diagnostic{
					Code: "synthesized_heading", Severity: SevWarning,
					Message: fmt.Sprintf("章节 %s 的标题 %q 由模型生成，原文中无对应标题节点",
						s.ID, truncate(s.Title, 30)),
				})
			}

			if len(s.BodyNodeIDs) == 0 && len(s.Children) == 0 && s.HeadingNodeID == "" {
				rep.Diagnostics = append(rep.Diagnostics, Diagnostic{
					Code: "empty_section", Severity: SevWarning,
					Message: fmt.Sprintf("章节 %s 没有任何内容", s.ID),
				})
			}

			prev := -1
			ascending := true
			for _, id := range s.BodyNodeIDs {
				noteUnknown(id)
				claim(id, "正文:"+s.ID)
				if o, found := order[id]; found {
					if prev >= 0 && o < prev {
						ascending = false
					}
					prev = o
				}
				// A source heading listed as section *body* is rendered as a
				// heading by the assembler (its own level wins), which puts a
				// second heading of that level on the page. The likely cause
				// is a heading paragraph that should have been named by some
				// section's heading_node_id: the coverage tally looks perfect,
				// so nothing else fires, and the duplicate is visible only on
				// the printed page.
				if n, found := doc.Node(id); found && n.Kind == KindHeading {
					rep.HeadingsAsBody++
					rep.Diagnostics = append(rep.Diagnostics, Diagnostic{
						Code: "heading_as_body", Severity: SevWarning,
						Message: fmt.Sprintf(
							"章节 %s 把原文标题段 %s（%q）当作正文收录，成品中该标题会再次出现；"+
								"若它本就是章节标题，请改由某个章节的 heading_node_id 指向它",
							s.ID, id, truncate(n.Text, 30)),
						NodeIDs: []string{id},
					})
				}
			}
			if !ascending {
				rep.ReorderedSections++
				sev := SevWarning
				msg := fmt.Sprintf("章节 %s 的正文顺序与原文不一致（原文顺序被调整）", s.ID)
				if s.PreserveOrder {
					// The model declared the reorder deliberately; still
					// visible, but as information rather than a warning.
					sev = SevInfo
					msg = fmt.Sprintf("章节 %s 的正文顺序经模型确认后调整", s.ID)
				} else {
					rep.Diagnostics = append(rep.Diagnostics, Diagnostic{
						Code: "reordered", Severity: sev, Message: msg,
						NodeIDs: s.BodyNodeIDs,
					})
					if !walk(s.Children, s.Level, where) {
						ok = false
					}
					continue
				}
				rep.Diagnostics = append(rep.Diagnostics, Diagnostic{
					Code: "reordered", Severity: sev, Message: msg,
					NodeIDs: s.BodyNodeIDs,
				})
			}
			if !walk(s.Children, s.Level, where) {
				ok = false
			}
		}
		return ok
	}
	walk(plan.Sections, 0, "")

	// A synthesized 标题 that duplicates a paragraph already covered as body
	// prints the title twice. This is the shape of a real mistake, found by
	// inspecting a generated document: an upload whose own first line is the
	// 标题, where the plan both leaves that paragraph in a section's body AND
	// fills Meta.Title, so the document opens with the same sentence twice.
	// Coverage is complete and no node is duplicated, so no other rule fires —
	// the defect is visible only on the printed page.
	//
	// It runs after walk() because it needs the final claim tally.
	if !hasElement(plan, RoleTitle) {
		if mt := strings.TrimSpace(plan.Meta.Title); mt != "" {
			if id := findNodeByExactText(doc, mt); id != "" && claims[id] > 0 {
				rep.Diagnostics = append(rep.Diagnostics, Diagnostic{
					Code: "duplicate_title", Severity: SevError,
					Message: fmt.Sprintf(
						"标题重复：原文段落 %s 的内容与 meta.title 相同，且该段落已被收录为正文，"+
							"成品中标题会出现两次。请改为把该段落绑定为标题（elements 中 role=title），"+
							"或清空 meta.title。原文：%q",
						id, truncate(mt, 40)),
					NodeIDs: []string{id},
				})
			}
		}
	}

	rep.UnknownRefs = len(unknown)
	for _, id := range unknown {
		rep.Diagnostics = append(rep.Diagnostics, Diagnostic{
			Code: "unknown_ref", Severity: SevError,
			Message: fmt.Sprintf("方案引用了原文中不存在的节点 %q", id),
			NodeIDs: []string{id},
		})
	}

	// Coverage and duplication, reported per node so the UI can highlight
	// the exact paragraphs at issue.
	var missing, dup []string
	for _, id := range contentIDs {
		switch c := claims[id]; {
		case c == 0:
			missing = append(missing, id)
		case c > 1:
			dup = append(dup, id)
		}
	}
	rep.CoveredNodes = len(contentIDs) - len(missing)
	rep.DuplicatedNodes = len(dup)

	for _, id := range missing {
		rep.Diagnostics = append(rep.Diagnostics, Diagnostic{
			Code: "uncovered_content", Severity: SevError,
			Message: fmt.Sprintf("原文段落 %s 未被任何章节收录（内容丢失）：%q",
				id, truncate(doc.Text(id), 40)),
			NodeIDs: []string{id},
		})
	}
	for _, id := range dup {
		rep.Diagnostics = append(rep.Diagnostics, Diagnostic{
			Code: "duplicate_content", Severity: SevError,
			Message: fmt.Sprintf("原文段落 %s 被重复收录 %d 次（%s）：%q",
				id, claims[id], strings.Join(claimedAs[id], ", "), truncate(doc.Text(id), 40)),
			NodeIDs: []string{id},
		})
	}

	sort.SliceStable(rep.Diagnostics, func(i, j int) bool {
		rank := func(s Severity) int {
			switch s {
			case SevError:
				return 0
			case SevWarning:
				return 1
			default:
				return 2
			}
		}
		if rank(rep.Diagnostics[i].Severity) != rank(rep.Diagnostics[j].Severity) {
			return rank(rep.Diagnostics[i].Severity) < rank(rep.Diagnostics[j].Severity)
		}
		return rep.Diagnostics[i].Code < rep.Diagnostics[j].Code
	})
	return rep
}

// hasElement reports whether a role is bound in the plan.
func hasElement(plan *Plan, role Role) bool {
	if plan == nil {
		return false
	}
	for _, e := range plan.Elements {
		if e.Role == role && e.NodeID != "" {
			return true
		}
	}
	return false
}

// findNodeByExactText returns the id of the first prose node whose text equals
// want once surrounding whitespace is ignored, or "".
//
// Comparison ignores leading/trailing whitespace only: an interior difference
// means the two are genuinely different strings, and treating them as equal
// would let a near-miss slip through.
func findNodeByExactText(doc *Document, want string) string {
	want = strings.TrimSpace(want)
	if want == "" || doc == nil {
		return ""
	}
	for _, n := range doc.ContentNodes() {
		if strings.TrimSpace(n.Text) == want {
			return n.ID
		}
	}
	return ""
}

// ------------------------------------------------------- verbatim enforcement

// VerbatimDiff explains where an assembled document deviates from its source.
type VerbatimDiff struct {
	// Missing are source texts absent from the output.
	Missing []string `json:"missing,omitempty"`
	// Extra are output texts absent from the source. For a correctly
	// assembled document these must all be synthesized scaffolding
	// (masthead, numbering, generated dates) or heading titles.
	Extra []string `json:"extra,omitempty"`
	// Altered are source nodes whose output text differs from the source
	// while sharing the same node id, and where the difference is NOT
	// covered by a declared edit. This list must always be empty: it is the
	// definition of an undeclared (i.e. silent) rewrite.
	Altered []string `json:"altered,omitempty"`
	// Changed are the declared edits that were actually applied, in order.
	// These are not failures — they are the audit trail the user reviews.
	Changed []AppliedEdit `json:"changed,omitempty"`
	// Unapplied are declared edits whose Before text was not found, so they
	// had no effect. They are not failures of the document (the source text
	// is intact), but they mean the plan asked for something impossible and
	// the user should know.
	Unapplied []UnappliedEdit `json:"unapplied,omitempty"`
}

// AppliedEdit records one declared change that reached the output.
type AppliedEdit struct {
	NodeID string `json:"node_id"`
	Kind   string `json:"kind"`
	Label  string `json:"label"`
	Before string `json:"before"`
	After  string `json:"after"`
	Reason string `json:"reason,omitempty"`
}

// UnappliedEdit records one declared change that could not be applied.
type UnappliedEdit struct {
	NodeID string `json:"node_id,omitempty"`
	Kind   string `json:"kind"`
	Before string `json:"before"`
	Reason string `json:"reason"`
}

// Clean reports whether no source content was lost and no undeclared change
// was made. Declared edits are permitted and are returned separately in
// Changed for the user to review.
func (v *VerbatimDiff) Clean() bool {
	return v != nil && len(v.Missing) == 0 && len(v.Altered) == 0
}

// VerifyVerbatim compares an assembled document against its source, taking the
// plan's declared edits into account.
//
// # The rule this enforces
//
//	原文可以被修改，但每一处修改都必须显式声明、可见、可审计。
//
// For each node it compares the output against the source text *with that
// node's declared edits applied*. A difference that the plan declared shows up
// in Changed and is not a failure; a difference it did not declare shows up in
// Altered and fails the export. So the guarantee is no longer "the text is
// untouched" but the stronger, more useful "no change can happen silently" —
// which is what lets the tool fix a typo without becoming a tool that rewrites
// documents behind the user's back.
//
// # What "payload" means per node kind
//
// Prose nodes are compared by text. Tables and images are compared by their
// real payload — Rows and Image bytes — because a table's Node.Text is
// deliberately empty: inventing a flattened string for it would itself be a
// fabrication, and the parser is right to refuse. Comparing only Text would
// therefore report a perfectly rendered table as missing content, which would
// in turn make a correct document fail the export gate. This function is the
// export gate, so it has to understand every kind it can be handed.
//
// Extra output text is returned for inspection but is not itself a failure: a
// 公文 legitimately contains text the source did not, such as 层次序数
// numbering ("一、") and a masthead. Because every extra string is reported, a
// reviewer can confirm that each one is scaffolding rather than model prose.
func VerifyVerbatim(doc *Document, out *Doc, plan *Plan) *VerbatimDiff {
	diff := &VerbatimDiff{}
	if doc == nil || out == nil {
		diff.Missing = doc.ContentIDs()
		return diff
	}

	// Group the output by provenance so each block is compared against the
	// source node it claims to reproduce.
	type emitted struct {
		text  []string
		rows  [][][]string
		image []int
	}
	outByNode := make(map[string]*emitted)
	var extras []string

	get := func(id string) *emitted {
		e, ok := outByNode[id]
		if !ok {
			e = &emitted{}
			outByNode[id] = e
		}
		return e
	}

	for _, b := range out.Blocks {
		if b.NodeID == "" {
			if b.Text != "" {
				extras = append(extras, b.Text)
			}
			continue
		}
		e := get(b.NodeID)
		if b.Text != "" {
			e.text = append(e.text, b.Text)
		}
		if len(b.Rows) > 0 {
			e.rows = append(e.rows, b.Rows)
		}
		if b.Image != nil {
			e.image = append(e.image, len(b.Image.Data))
		}
	}

	for _, n := range doc.ContentNodes() {
		e := outByNode[n.ID]
		if e == nil {
			diff.Missing = append(diff.Missing, describeNode(n))
			continue
		}

		switch n.Kind {
		case KindTable:
			// Compare the actual cell matrix. Text is not consulted: the
			// parser leaves it empty on purpose.
			if len(e.rows) == 0 {
				diff.Missing = append(diff.Missing, describeNode(n))
				continue
			}
			if !sameTable(n.Rows, e.rows[0]) {
				diff.Altered = append(diff.Altered, fmt.Sprintf(
					"%s: 表格内容被改动（原文 %d行×%d列 → 输出 %d行×%d列）",
					n.ID, len(n.Rows), maxCols(n.Rows), len(e.rows[0]), maxCols(e.rows[0])))
			}

		case KindImage:
			// An image's identity is its bytes. A node that carries a
			// filename in Text must also have its picture emitted, or the
			// document lost a figure while still containing its caption.
			if n.Image != nil && len(n.Image.Data) > 0 {
				if len(e.image) == 0 {
					diff.Missing = append(diff.Missing, describeNode(n))
					continue
				}
				if e.image[0] != len(n.Image.Data) {
					diff.Altered = append(diff.Altered, fmt.Sprintf(
						"%s: 图片数据长度改变（原文 %d 字节 → 输出 %d 字节）",
						n.ID, len(n.Image.Data), e.image[0]))
				}
			} else if len(e.text) == 0 && len(e.image) == 0 {
				diff.Missing = append(diff.Missing, describeNode(n))
			}

		default:
			if len(e.text) == 0 {
				diff.Missing = append(diff.Missing, describeNode(n))
				continue
			}
			// A node may legitimately be emitted as more than one block when
			// the extra pieces are the node's own text split by the
			// assembler; their concatenation is what must match.
			got := strings.Join(e.text, "")

			// What the output is compared against: the source text with this
			// node's declared edits applied. An undeclared difference is then
			// exactly a difference from this expectation.
			var nodeEdits []Edit
			if plan != nil {
				nodeEdits = plan.EditsFor(n.ID)
			}
			expected, failed := AppliedText(n.Text, nodeEdits)

			for _, f := range failed {
				diff.Unapplied = append(diff.Unapplied, UnappliedEdit{
					NodeID: f.NodeID, Kind: string(f.Kind), Before: truncate(f.Before, 60),
					Reason: "在原文中未找到待替换内容，该修改未生效",
				})
			}

			if got != expected {
				diff.Altered = append(diff.Altered,
					fmt.Sprintf("%s: 原文 %q → 输出 %q（未声明的修改）",
						n.ID, truncate(expected, 60), truncate(got, 60)))
				continue
			}

			// Applied and matching: record it for the change log.
			//
			// Only edits that ACTUALLY reached the text are listed. Listing
			// every declared edit would be wrong twice over: it would advertise
			// a change that did not happen (an edit whose Before was not found),
			// and it would claim credit for a no-op. The change log is what the
			// user reviews to decide whether to trust the document, so it must
			// describe the document, not the plan's intentions.
			for _, ed := range appliedEdits(nodeEdits, n.Text) {
				diff.Changed = append(diff.Changed, AppliedEdit{
					NodeID: n.ID, Kind: string(ed.Kind), Label: EditLabel(ed.Kind),
					Before: truncate(ed.Before, 120), After: truncate(ed.After, 120),
					Reason: ed.Reason,
				})
			}
		}
	}

	// Edits targeting an unknown node never reach the loop above, so they would
	// otherwise be reported nowhere.
	//
	// A nil plan is treated as "no declared edits", which is what a caller
	// checking a document without one means; it must not panic.
	var declaredEdits []Edit
	if plan != nil {
		declaredEdits = plan.Edits
	}
	for _, ed := range declaredEdits {
		if ed.NodeID == "" {
			// An edit with no target node cannot be applied: Edits are applied
			// *within* one node's text, so there is nothing to anchor this to.
			//
			// This case used to be skipped outright, which meant a model asking
			// for a new heading got no heading, no change-log entry, and no
			// diagnostic — a silent loss, and exactly what the "no change without
			// a declaration" rule exists to prevent. It is reported instead.
			//
			// Generating a heading the source lacks is supported, but through
			// the structure rather than through an Edit: a Section with a Title
			// and no HeadingNodeID emits a heading block and is counted in
			// Report.SynthesizedHeadings. The reason below says so, because a
			// diagnostic the caller cannot act on is only half a diagnostic.
			diff.Unapplied = append(diff.Unapplied, UnappliedEdit{
				Kind:   string(ed.Kind),
				Before: truncate(ed.Before, 60),
				Reason: "该修改没有指定目标段落（node_id 为空），无法应用；" +
					"若要为原文补一个段落标题，请给章节填 title 并留空 heading_node_id。",
			})
			continue
		}
		if _, ok := doc.Node(ed.NodeID); !ok {
			diff.Unapplied = append(diff.Unapplied, UnappliedEdit{
				NodeID: ed.NodeID, Kind: string(ed.Kind), Before: truncate(ed.Before, 60),
				Reason: "目标节点不存在，该修改未生效",
			})
		}
	}

	for _, e := range extras {
		diff.Extra = append(diff.Extra, truncate(e, 60))
	}
	return diff
}

// appliedEdits returns the subset of edits that would actually change src:
// those whose Before is present at its declared occurrence and whose After
// differs from Before.
//
// It exists so the change log can describe what happened to the document rather
// than what the plan intended. Both exclusions matter:
//
//   - an edit whose Before is absent changes nothing, and belongs in Unapplied;
//   - an edit with After == Before changes nothing, and belongs nowhere.
//
// Reported consequences are derived from the same function the assembler used to
// produce the text, so the log cannot drift from the document.
func appliedEdits(edits []Edit, src string) []Edit {
	var out []Edit
	// Walk the text forward the way AppliedText does, tracking each edit's
	// effect in order.
	cur := src
	for _, e := range edits {
		if e.Before == "" || e.Before == e.After {
			continue
		}
		idx := nthIndex(cur, e.Before, e.Occurrence)
		if idx < 0 {
			continue
		}
		out = append(out, e)
		cur = cur[:idx] + e.After + cur[idx+len(e.Before):]
	}
	return out
}

// describeNode renders a node for a Missing diagnostic, using whatever payload
// the node actually has (a table has no text to quote).
func describeNode(n Node) string {
	switch {
	case n.Kind == KindTable:
		return fmt.Sprintf("%s: 表格 %d行×%d列 %s",
			n.ID, len(n.Rows), maxCols(n.Rows), truncate(n.Text, 40))
	case n.Kind == KindImage:
		name := ""
		if n.Image != nil {
			name = n.Image.Name
		}
		return fmt.Sprintf("%s: 图片 %s", n.ID, name)
	default:
		return fmt.Sprintf("%s: %s", n.ID, truncate(n.Text, 60))
	}
}

// sameTable reports whether two cell matrices are identical.
func sameTable(a, b [][]string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return false
		}
		for j := range a[i] {
			if a[i][j] != b[i][j] {
				return false
			}
		}
	}
	return true
}

// maxCols returns the widest row's length.
func maxCols(rows [][]string) int {
	n := 0
	for _, r := range rows {
		if len(r) > n {
			n = len(r)
		}
	}
	return n
}
