// Package docmodel holds the frozen data contracts that every stage of the
// GovDoc pipeline shares: the source document model produced by parsers, the
// structure plan proposed by the LLM, and the render model consumed by the
// formatting engine and the DOCX writer.
//
// # The verbatim invariant
//
// Node.Text is copied byte-for-byte from the uploaded document and MUST NOT be
// rewritten by any later stage. Every downstream stage addresses content by
// Node.ID instead of restating its text. This is what makes "不改变原文"
// mechanically checkable rather than a matter of trusting the model: the
// restructure step may only reorder and regroup node ids, and the assembler
// pulls the final text back out of the Document by id. No LLM-authored prose
// ever reaches the output for a content node.
package docmodel

import (
	"fmt"
	"sort"
	"strings"
)

// ---------------------------------------------------------------- source side

// NodeKind classifies one addressable block of the uploaded document.
type NodeKind string

const (
	// KindParagraph is ordinary body prose.
	KindParagraph NodeKind = "paragraph"
	// KindHeading is a block the source itself already marked as a heading
	// (Word "Heading N" style, a Markdown "#" line, ...).
	KindHeading NodeKind = "heading"
	// KindListItem is a list entry.
	KindListItem NodeKind = "list_item"
	// KindTable is a tabular block.
	KindTable NodeKind = "table"
	// KindImage is an inline picture carried through from the source.
	KindImage NodeKind = "image"
	// KindPageBreak is an explicit page break in the source. It carries no
	// prose and is therefore exempt from the content accounting rules.
	KindPageBreak NodeKind = "page_break"
)

// CarriesProse reports whether a node holds text that the output document
// must reproduce. Non-prose nodes (page breaks) are outside the accounting
// rules enforced by VerifyPlan.
func (k NodeKind) CarriesProse() bool { return k != KindPageBreak }

// Run is a span of text sharing one inline style, preserved from the source.
type Run struct {
	Text   string `json:"text"`
	Bold   bool   `json:"bold,omitempty"`
	Italic bool   `json:"italic,omitempty"`
	// SourceFont is the source document's font name for this span, when known.
	SourceFont string `json:"source_font,omitempty"`
}

// ImageRef carries an embedded picture through the pipeline unchanged.
type ImageRef struct {
	Name     string `json:"name"`
	MimeType string `json:"mime_type"`
	// Data is the raw image payload. It is never serialized into API JSON:
	// a preview must reference the image through an endpoint instead of
	// inlining megabytes of base64 into every plan round trip.
	Data []byte `json:"-"`
}

// Node is one addressable block of the uploaded document.
type Node struct {
	// ID is stable within one document ("n0001", ...). Later stages refer
	// to content only by ID, so text can never be silently replaced.
	ID string `json:"id"`
	// Kind classifies the block.
	Kind NodeKind `json:"kind"`
	// Level is the source heading level (1-based) for KindHeading, else 0.
	Level int `json:"level,omitempty"`
	// Text is the VERBATIM source text. Parser stages write it; the
	// assembler reads it. No stage may rewrite it.
	Text string `json:"text"`
	// Runs preserves inline styling for text nodes.
	Runs []Run `json:"runs,omitempty"`
	// Rows holds cell text for KindTable.
	Rows [][]string `json:"rows,omitempty"`
	// HeaderRow is true when Rows[0] is a header row.
	HeaderRow bool `json:"header_row,omitempty"`
	// Image holds the payload for KindImage.
	Image *ImageRef `json:"-"`
	// Order is the 0-based position in the source.
	Order int `json:"order"`
	// SourceStyle is the source's own style name, e.g. "Heading 1".
	SourceStyle string `json:"source_style,omitempty"`
	// SourcePage is the 1-based page the block appeared on, when the
	// parser can determine it. Empty in the API JSON when unknown.
	SourcePage int `json:"source_page,omitempty"`
}

// Document is a parsed upload.
type Document struct {
	SourceName string `json:"source_name"`
	// Format is the detected container: "docx", "txt" or "md".
	Format string `json:"format"`
	Nodes  []Node `json:"nodes"`
}

// ContentNodes returns the prose-carrying nodes in source order. These are
// exactly the nodes that VerifyPlan accounts for.
func (d *Document) ContentNodes() []Node {
	if d == nil {
		return nil
	}
	out := make([]Node, 0, len(d.Nodes))
	for _, n := range d.Nodes {
		if n.Kind.CarriesProse() {
			out = append(out, n)
		}
	}
	return out
}

// ContentIDs returns the ids of every prose-carrying node, in source order.
func (d *Document) ContentIDs() []string {
	nodes := d.ContentNodes()
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.ID)
	}
	return out
}

// ByID indexes the document's nodes by id.
func (d *Document) ByID() map[string]Node {
	out := make(map[string]Node, len(d.Nodes))
	if d == nil {
		return out
	}
	for _, n := range d.Nodes {
		out[n.ID] = n
	}
	return out
}

// Node looks one node up by id.
func (d *Document) Node(id string) (Node, bool) {
	if d == nil {
		return Node{}, false
	}
	for _, n := range d.Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return Node{}, false
}

// Text returns a node's verbatim text by id, or "" when the id is unknown.
func (d *Document) Text(id string) string {
	if n, ok := d.Node(id); ok {
		return n.Text
	}
	return ""
}

// ---------------------------------------------------------------- plan side

// Section is one node of the chapter tree proposed by the LLM. It never
// carries body prose: it names which source nodes belong to it.
type Section struct {
	ID string `json:"id"`
	// Title is the chapter heading text.
	Title string `json:"title"`
	// Level is the 层次序数 depth: 1 renders as "一、", 2 as "（一）",
	// 3 as "1." and 4 as "（1）" under GB/T 9704-2012.
	Level int `json:"level"`
	// HeadingNodeID points at the source node whose verbatim text IS this
	// heading. It is set when the source already contains the heading, in
	// which case Title must equal that node's text. When empty, Title is
	// considered synthesized and is reported as such by VerifyPlan.
	HeadingNodeID string `json:"heading_node_id,omitempty"`
	// BodyNodeIDs lists the source content nodes that make up this
	// section's body, in the order they must be emitted. When
	// PreserveOrder is true these must ascend in source order.
	BodyNodeIDs []string `json:"body_node_ids,omitempty"`
	// PreserveOrder is false only when the model deliberately reordered
	// paragraphs inside the section. VerifyPlan still accepts it, but flags
	// it so the user can see that the text sequence changed.
	PreserveOrder bool `json:"preserve_order,omitempty"`
	// Children are nested sections, which must have a deeper Level.
	Children []Section `json:"children,omitempty"`
	// Rationale is the model's short justification, surfaced in the UI so
	// a reviewer can judge the restructuring.
	Rationale string `json:"rationale,omitempty"`
}

// Meta is document-level metadata. Every field is optional; the assembler
// only emits the parts the user (or the model) actually supplied.
type Meta struct {
	// CopyNumber is 份号, e.g. "000012".
	CopyNumber string `json:"copy_number,omitempty"`
	// Security is 密级和保密期限, e.g. "秘密★1年".
	Security string `json:"security,omitempty"`
	// Urgency is 紧急程度, e.g. "特急".
	Urgency string `json:"urgency,omitempty"`
	// IssuingOrg is 发文机关标志, e.g. "国务院办公厅文件".
	IssuingOrg string `json:"issuing_org,omitempty"`
	// DocNumber is 发文字号, e.g. "国办发〔2024〕15号".
	DocNumber string `json:"doc_number,omitempty"`
	// Signatory is 签发人, for 上行文.
	Signatory string `json:"signatory,omitempty"`
	// Title is 标题.
	Title string `json:"title,omitempty"`
	// Recipient is 主送机关.
	Recipient string `json:"recipient,omitempty"`
	// AttachmentNote is 附件说明.
	AttachmentNote string `json:"attachment_note,omitempty"`
	// Signature is 发文机关署名.
	Signature string `json:"signature,omitempty"`
	// Date is 成文日期. GB/T 9704-2012 §7.3.5.2 requires Arabic numerals
	// with a full four-digit year and no zero padding, e.g. "2024年5月1日"
	// (not "二〇二四年五月一日" and not "2024年05月01日").
	Date string `json:"date,omitempty"`
	// Note is 附注.
	Note string `json:"note,omitempty"`
	// CC is 抄送机关.
	CC string `json:"cc,omitempty"`
	// Printer is 印发机关.
	Printer string `json:"printer,omitempty"`
	// PrintDate is 印发日期.
	PrintDate string `json:"print_date,omitempty"`
}

// Plan is the LLM's complete restructuring proposal. It is the unit the
// user reviews and edits in the UI before rendering.
type Plan struct {
	Meta Meta `json:"meta"`
	// Elements binds source nodes to the 公文 element slots they already
	// occupy. A real uploaded 公文 usually contains its own 标题, 主送机关,
	// 发文机关署名, 成文日期 and 抄送 as ordinary paragraphs; without a way
	// to say "node n0003 IS the 标题", VerifyPlan would report those nodes
	// as dropped content, and the assembler could not place them.
	//
	// Binding them also keeps the wording verbatim: the assembler renders
	// the bound node's own text rather than Meta's copy, so a title the
	// model paraphrased in Meta cannot leak into the output.
	Elements []ElementBinding `json:"elements,omitempty"`
	// PreambleNodeIDs lists content that belongs directly under 主送机关,
	// before the first 层次序数 heading.
	//
	// Nearly every real 公文 opens with such a paragraph — the 缘由
	// ("为深入贯彻落实……，现将有关事项通知如下。") that states why the
	// document exists. It is body prose but it is not a chapter, so it fits
	// neither an element slot nor a section. Without this field the only way
	// to cover it would be to invent a chapter for it, which would print a
	// fabricated heading such as "一、通知引言" into a document whose author
	// wrote no such thing, and would consume the 层次序数 number that the
	// first real heading then repeats.
	PreambleNodeIDs []string  `json:"preamble_node_ids,omitempty"`
	Sections        []Section `json:"sections"`
	// Edits declares content changes the plan wants to make to source text.
	//
	// # Why this exists
	//
	// The original design admitted no content change at all: the model could
	// only group node ids. That is the safest possible rule, but it makes the
	// tool useless for the very documents people actually bring it — ones with
	// a typo, a run-on sentence, or a chapter that never had a heading. The user
	// asked for the ability to fix those, so the rule becomes:
	//
	//	原文可以被修改，但每一处修改都必须显式声明、可见、可审计。
	//
	// An undeclared change is still an error (VerifyVerbatim catches it), so
	// nothing can be rewritten silently. A declared change carries its own
	// before/after and reason, which the UI shows as a diff and the export
	// reports in a change log.
	Edits []Edit `json:"edits,omitempty"`
	// Notes are model observations shown to the user (ambiguities,
	// dropped boilerplate, low-confidence decisions).
	Notes []string `json:"notes,omitempty"`
	// Model names the model that produced the plan, for provenance.
	Model string `json:"model,omitempty"`
}

// EditKind classifies one declared content change.
type EditKind string

const (
	// EditTypo fixes a 错别字.
	EditTypo EditKind = "typo"
	// EditGrammar fixes 语病 or 语句不通 (a run-on, a missing subject, a
	// broken parallel structure).
	EditGrammar EditKind = "grammar"
	// EditWording tightens 用词不严谨 (a vague or non-standard term replaced
	// with the one a 公文 would use).
	EditWording EditKind = "wording"
	// EditPunctuation fixes 标点 usage.
	EditPunctuation EditKind = "punctuation"
	// EditSentenceSplit breaks a run-on sentence for readability.
	EditSentenceSplit EditKind = "sentence_split"
	// EditHeading inserts a heading the source lacked, so a long chapter can
	// be navigated. It is separate from the others because it adds structure
	// rather than correcting text.
	EditHeading EditKind = "heading"
	// EditOther is any remaining correction, which the UI groups last.
	EditOther EditKind = "other"
)

// AllEditKinds lists every kind, in the order the UI should present them.
func AllEditKinds() []EditKind {
	return []EditKind{
		EditTypo, EditGrammar, EditWording,
		EditPunctuation, EditSentenceSplit, EditHeading, EditOther,
	}
}

// EditLabel returns the Chinese label for a kind.
func EditLabel(k EditKind) string {
	switch k {
	case EditTypo:
		return "错别字"
	case EditGrammar:
		return "语病/语句不通"
	case EditWording:
		return "用词不严谨"
	case EditPunctuation:
		return "标点"
	case EditSentenceSplit:
		return "断句"
	case EditHeading:
		return "新增标题"
	default:
		return "其他修改"
	}
}

// Edit is one declared change to source text. Every field is required: the
// Before must match the source exactly (so the change cannot be applied to
// something else), and the Reason is what makes the change auditable by a
// human rather than merely visible.
type Edit struct {
	// NodeID names the source node being changed, and is required: an Edit
	// replaces text *within* one node, so with no node to anchor to there is
	// nothing to change and the edit cannot be applied.
	//
	// In particular, EditHeading does NOT go through here. Generating a heading
	// the source lacks is a structural act, not a text substitution, and is
	// expressed as a Section with a Title and no HeadingNodeID: the assembler
	// emits the heading block and counts it in Report.SynthesizedHeadings. An
	// Edit with an empty NodeID is reported as unapplied rather than ignored,
	// so it can never disappear silently.
	NodeID string `json:"node_id,omitempty"`
	// Kind classifies the change.
	Kind EditKind `json:"kind"`
	// Before is the exact source substring being replaced. Verification
	// requires it to occur in the node, which is what pins the edit to real
	// text instead of letting it rewrite whatever it finds.
	Before string `json:"before"`
	// After is the replacement text.
	After string `json:"after"`
	// Reason explains why the change is correct, in Chinese, for the change
	// log the user reviews.
	Reason string `json:"reason,omitempty"`
	// Occurrence selects which match of Before to replace when it appears more
	// than once (0-based). Defaults to the first.
	Occurrence int `json:"occurrence,omitempty"`
}

// EditCount returns how many edits the plan declares.
func (p *Plan) EditCount() int {
	if p == nil {
		return 0
	}
	return len(p.Edits)
}

// EditsFor returns the edits targeting one node, in declaration order. It is
// nil-safe, so a caller without a plan can ask without guarding.
func (p *Plan) EditsFor(nodeID string) []Edit {
	if p == nil {
		return nil
	}
	var out []Edit
	for _, e := range p.Edits {
		if e.NodeID == nodeID {
			out = append(out, e)
		}
	}
	return out
}

// AppliedText returns a node's text with that node's edits applied, plus the
// edits that could not be applied (because Before was not found).
//
// Application is left to right and each edit consumes its own match, so a
// Before that appears twice is replaced at the declared occurrence rather than
// at every position. This is the only function in the program that changes
// source text, and every change it makes is accompanied by the Edit that
// authorised it — which is what keeps "modified" different from "rewritten".
func AppliedText(src string, edits []Edit) (string, []Edit) {
	out := src
	var failed []Edit

	// Apply in order; each edit's Before must still be present in the text as
	// produced so far, which naturally orders edits on the same node.
	pending := make([]Edit, len(edits))
	copy(pending, edits)

	for _, e := range pending {
		if e.Before == "" {
			// Insertion-style edits are handled by the caller (headings); an
			// empty Before here would otherwise replace at position 0
			// unpredictably.
			failed = append(failed, e)
			continue
		}
		idx := nthIndex(out, e.Before, e.Occurrence)
		if idx < 0 {
			failed = append(failed, e)
			continue
		}
		out = out[:idx] + e.After + out[idx+len(e.Before):]
	}
	return out, failed
}

// nthIndex returns the byte offset of the n-th (0-based) occurrence of sub, or
// -1. Occurrence below zero is treated as zero.
func nthIndex(s, sub string, occurrence int) int {
	if occurrence < 0 {
		occurrence = 0
	}
	from := 0
	for i := 0; ; i++ {
		j := strings.Index(s[from:], sub)
		if j < 0 {
			return -1
		}
		pos := from + j
		if i == occurrence {
			return pos
		}
		// Advance past this match; +1 (not +len(sub)) so overlapping candidates
		// are still found, which matters for short Before values like "的的".
		from = pos + 1
	}
}

// ElementBinding assigns one source node to one 公文 element slot.
type ElementBinding struct {
	// Role is the slot the node fills: RoleTitle, RoleRecipient, RoleDate, ...
	// It must be a role the assembler places in the 版头/主体/版记 skeleton;
	// binding a node to RoleBody is meaningless (body content is reached
	// through sections) and is reported as an error.
	Role Role `json:"role"`
	// NodeID is the source node whose verbatim text fills the slot.
	NodeID string `json:"node_id"`
}

// ElementNodeIDs returns the bound node ids, in declaration order.
func (p *Plan) ElementNodeIDs() []string {
	if p == nil {
		return nil
	}
	out := make([]string, 0, len(p.Elements))
	for _, e := range p.Elements {
		out = append(out, e.NodeID)
	}
	return out
}

// Walk visits every section in depth-first pre-order. Returning false from fn
// stops the walk.
func (p *Plan) Walk(fn func(*Section) bool) {
	var rec func(list []Section) bool
	rec = func(list []Section) bool {
		for i := range list {
			if !fn(&list[i]) {
				return false
			}
			if !rec(list[i].Children) {
				return false
			}
		}
		return true
	}
	rec(p.Sections)
}

// BodyIDs returns every body node id referenced anywhere in the tree, in the
// order the sections appear.
func (p *Plan) BodyIDs() []string {
	var out []string
	p.Walk(func(s *Section) bool {
		out = append(out, s.BodyNodeIDs...)
		return true
	})
	return out
}

// HeadingIDs returns every referenced heading node id, in tree order.
func (p *Plan) HeadingIDs() []string {
	var out []string
	p.Walk(func(s *Section) bool {
		if s.HeadingNodeID != "" {
			out = append(out, s.HeadingNodeID)
		}
		return true
	})
	return out
}

// ---------------------------------------------------------------- render side

// Role identifies a 公文 element and therefore which GB/T 9704-2012 style
// applies to it. The formatting engine maps Role to a ParaStyle; the DOCX
// writer and the browser preview both render from that same mapping, which is
// what keeps the on-screen preview and the generated file in agreement.
type Role string

const (
	RoleCopyNumber     Role = "copy_number"     // 份号
	RoleSecurity       Role = "security"        // 密级和保密期限
	RoleUrgency        Role = "urgency"         // 紧急程度
	RoleMasthead       Role = "masthead"        // 发文机关标志
	RoleDocNumber      Role = "doc_number"      // 发文字号
	RoleSignatory      Role = "signatory"       // 签发人
	RoleRedLine        Role = "red_line"        // 红色分隔线
	RoleTitle          Role = "title"           // 标题
	RoleRecipient      Role = "recipient"       // 主送机关
	RoleBody           Role = "body"            // 正文
	RoleHeading1       Role = "heading1"        // 一、
	RoleHeading2       Role = "heading2"        // （一）
	RoleHeading3       Role = "heading3"        // 1.
	RoleHeading4       Role = "heading4"        // （1）
	RoleAttachmentNote Role = "attachment_note" // 附件说明
	RoleSignature      Role = "signature"       // 发文机关署名
	RoleDate           Role = "date"            // 成文日期
	RoleNote           Role = "note"            // 附注
	RoleCC             Role = "cc"              // 抄送机关
	RolePrinter        Role = "printer"         // 印发机关和印发日期
	RoleAttachment     Role = "attachment"      // 附件正文
)

// AllRoles lists every role in canonical document order. The formatting
// engine must define a style for each; ValidateSpec enforces that.
func AllRoles() []Role {
	return []Role{
		RoleCopyNumber, RoleSecurity, RoleUrgency, RoleMasthead,
		RoleDocNumber, RoleSignatory, RoleRedLine, RoleTitle,
		RoleRecipient, RoleBody, RoleHeading1, RoleHeading2,
		RoleHeading3, RoleHeading4, RoleAttachmentNote,
		RoleSignature, RoleDate, RoleNote, RoleCC, RolePrinter,
		RoleAttachment,
	}
}

// HeadingRole maps a 层次序数 depth (1..4) to its role. Depth values outside
// that range clamp to the deepest supported level.
func HeadingRole(level int) Role {
	switch level {
	case 1:
		return RoleHeading1
	case 2:
		return RoleHeading2
	case 3:
		return RoleHeading3
	case 4:
		return RoleHeading4
	}
	if level < 1 {
		return RoleBody
	}
	return RoleHeading4
}

// OutBlock is one block of the rendered document. Text is copied verbatim
// from the source node named by NodeID; only Role, numbering and page
// placement are decided downstream.
type OutBlock struct {
	Role Role   `json:"role"`
	Text string `json:"text"`
	// NodeID is the provenance of Text, empty for synthesized blocks
	// (masthead, separator, generated date line).
	NodeID string `json:"node_id,omitempty"`
	// Level is the heading depth for heading roles, else 0.
	Level int `json:"level,omitempty"`
	// AutoNumber is the 层次序数 prefix the writer must render before
	// Text, e.g. "一、" or "（一）". The assembler computes it so the
	// numbering is deterministic and never model-authored.
	AutoNumber string `json:"auto_number,omitempty"`
	// Runs preserves source inline styling for this block.
	Runs  []Run      `json:"runs,omitempty"`
	Rows  [][]string `json:"rows,omitempty"`
	Image *ImageRef  `json:"-"`
	// PageBreakBefore forces this block onto a new page.
	PageBreakBefore bool `json:"page_break_before,omitempty"`
	// SpaceBeforeLines adds 空N行 above this block, on top of whatever the
	// role's own style already specifies. It is expressed in hundredths of a
	// line, matching OOXML's w:beforeLines.
	//
	// The assembler needs it because 发文机关标志's vertical position depends
	// on 行文方向 rather than on the element itself: GB/T 9704-2012 §4.3.2
	// places it 35mm below the top of the 版心 for 平行文/下行文 but 80mm for
	// 上行文 (which must leave room for 签发人). That is a document-level
	// offset the per-role style table cannot express, so it travels on the
	// block.
	SpaceBeforeLines float64 `json:"space_before_lines,omitempty"`
	// Edited reports whether this block's text differs from the source because
	// a declared Edit was applied to it.
	//
	// The renderers need this to show the user which paragraphs were changed:
	// the preview highlights them, and the writer can mark them the same way.
	// It is derived from the applied edits, never set by hand, and it says
	// only *that* something changed — the before/after detail lives in
	// VerbatimDiff.Changed, which is what the user actually reviews.
	Edited bool `json:"edited,omitempty"`
	// EditKinds lists the kinds of change applied to this block, so a renderer
	// can label the highlight ("错别字" vs "新增标题") without re-deriving it.
	EditKinds []string `json:"edit_kinds,omitempty"`
}

// Doc is the fully resolved document handed to the formatting engine and the
// writer.
type Doc struct {
	Meta   Meta       `json:"meta"`
	Blocks []OutBlock `json:"blocks"`
}

// Text returns the concatenation of every block's text, used by tests that
// assert verbatim preservation end to end.
func (d *Doc) Text() string {
	var b strings.Builder
	for _, blk := range d.Blocks {
		if blk.Text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(blk.Text)
	}
	return b.String()
}

// String renders a compact debug view.
func (d *Doc) String() string {
	var b strings.Builder
	for _, blk := range d.Blocks {
		fmt.Fprintf(&b, "%-16s %s\n", blk.Role, truncate(blk.Text, 60))
	}
	return b.String()
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// SortNodes orders nodes by their source position. Parsers call it before
// returning so downstream code may assume Nodes is in source order.
func SortNodes(nodes []Node) {
	sort.SliceStable(nodes, func(i, j int) bool { return nodes[i].Order < nodes[j].Order })
}
