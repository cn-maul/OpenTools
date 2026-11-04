package llm

import (
	"encoding/json"
	"fmt"
	"strings"

	"opentools/tools/govdoc/engine/docmodel"
)

// systemPrompt states the task and the rule the whole project rests on.
//
// # The rule, and how it changed
//
// Originally: never change a character. The user needs the tool to fix a
// 错别字 or a 语病, so the rule is now the stronger one —
//
//	原文可以被修改，但每一处修改都必须显式声明、可见、可审计。
//
// The prompt therefore has two modes, and the difference matters for safety: in
// both, an *undeclared* change fails verification. Allowing edits widens what
// the model may propose, never what may happen silently.
func systemPrompt(doc *docmodel.Document, allowEdits bool) string {
	var b strings.Builder
	b.WriteString(`你是一名中国政府公文处理专家，熟悉《党政机关公文格式》GB/T 9704-2012。

你的任务：读懂给定文档的内容与逻辑，把它梳理成一份层次正确的公文结构方案（章节树）。

【最重要的规则】
1. 你只能通过**引用段落编号（node id）**来组织章节。你输出的正文部分只有编号，没有文字。
   系统会用编号把原文取回来，所以正文不会因为你的输出而改变。
2. 原文中**每一个** kind 不是 page_break 的段落，都必须被恰好引用一次：
   - 要么作为某个章节的 body_node_ids 成员；
   - 要么作为某个章节的 heading_node_id；
   - 要么（若是标题、主送机关、发文机关署名、成文日期、抄送机关等公文要素）放进 elements 里绑定到对应要素。
   漏掉任何一个段落，方案都会被判为不合格并退回重做。
3. **不得重复引用**同一段落。同一个编号出现在两处，方案同样不合格。
`)
	if allowEdits {
		b.WriteString(`
【允许的内容修改（用户已开启）】
用户允许你对原文中的下列问题提出修改：
  typo            错别字
  grammar         语病、语句不通
  wording         用词不严谨、不规范
  punctuation     标点使用不当
  sentence_split  长句难读，需要断句
  other           其他确有必要的文字修正
（注意：**新增段落标题不属于 edits**，写法见下面「章节层次」一节。）

修改**必须**写进 edits 数组，每一条都要给出 before / after / reason：
  { "node_id": "n0003", "kind": "typo", "before": "含辛如苦", "after": "含辛茹苦",
    "reason": "成语误写，应为「含辛茹苦」" }

硬性要求：
- **node_id 必填**：edits 是在某一段原文内部替换文字，必须指明改哪一段。
- before 必须是该节点原文里**逐字出现**的一段（含标点），否则这条修改不会生效。
- after 是你建议替换成的内容。只改必须改的地方，**不要顺手润色**：
  能不改就不改，改动越少越好。不要改变原意，不要调整语序，不要合并或拆分段落。
- reason 用中文简短说明为什么必须改（错在哪、依据是什么）。
- 同一个段落有多处修改时，拆成多条 edits，不要用一条大的 before 覆盖整段。
- 若某段没有任何问题，就**不要**为它产生 edit。
- 若原文已经很规范，edits 可以为空数组——这是完全正常的。
- **绝对不要**为了"让文字更好看"而修改；只修正客观错误。
`)
	} else {
		b.WriteString(`
【禁止修改原文】
你**不得改写、增删、润色、概括、合并或拆分**任何原文内容。一个字都不能改。
不要输出 edits 数组（或输出空数组）。
`)
	}
	b.WriteString(`
【章节层次】
- level 1 ～ 4 分别对应公文层次序数：一、/（一）/ 1. / （1）。
- 子章节的 level 必须大于其父章节的 level。
- 如果原文中已有标题段落（kind 为 heading），请用 heading_node_id 指向它，
  并且该章节的 title 必须与那段原文**完全一致**。不要另造一个名字。
- 只有当原文确实没有对应标题时，才使用自己拟定的 title（这种情况会被标注为"模型生成标题"，
  供人工复核）。也就是说：**原文缺标题时，补标题的办法是给章节填 title 并留空
  heading_node_id**，系统会把它生成为一个标题段落——不要试图用 edits 来新增标题。

【elements：把原文里已有的公文要素认出来】
如果原文包含下列要素段落，请放进 elements 数组绑定，不要把它们当普通正文：
  title（标题）、recipient（主送机关）、doc_number（发文字号）、issuing_org（发文机关标志）、
  signature（发文机关署名）、date（成文日期）、cc（抄送机关）、printer（印发机关）、
  note（附注）、attachment_note（附件说明）、copy_number（份号）、security（密级）、
  urgency（紧急程度）、signatory（签发人）
role 只能用上面这些取值，不得自创。

【preamble_node_ids：标题之下的引言段】
公文在主送机关之后、第一个层次序数标题（"一、"）之前，通常有一段交代缘由的正文，
例如"为深入贯彻落实……，现将有关事项通知如下。"。
这段文字属于正文，但**不是章节**：请把它放进 preamble_node_ids 数组，不要为它编造一个章节标题。
为它造章节会凭空多出一个原文没有的标题，并占用"一、"这个编号，导致后面真正的
"一、……"重复编号。

【meta】
可以用 meta 补充原文中没有、但导出公文时需要的字段（如 doc_number、issuing_org、date 等）。
注意 date 若由你填写，请用阿拉伯数字、不补零，例如 2024年5月1日。
meta 中**不要**填写 title——标题请通过 elements 绑定到原文的标题段落，以保持原文一字不改。

【输出格式】
只输出**一个 JSON 对象**，不要有任何解释文字，不要用 Markdown 代码围栏。

JSON 结构：
{
  "meta": { "doc_number": "", "issuing_org": "", "date": "", "signature": "" },
  "elements": [ { "role": "title", "node_id": "n0001" } ],
  "preamble_node_ids": ["n0003"],
  "edits": [],
  "sections": [
    {
      "id": "s1",
      "title": "章节标题文字",
      "level": 1,
      "heading_node_id": "n0002",
      "body_node_ids": ["n0003", "n0004"],
      "children": [
        { "id": "s1a", "title": "子章节", "level": 2, "body_node_ids": ["n0005"], "children": [] }
      ]
    }
  ],
  "notes": ["你对结构判断的简短说明，或存疑之处"]
}
`)
	return b.String()
}

// buildUserPrompt renders the document as an addressable list.
//
// Only `id | kind | text` is sent. The model needs the text to judge what
// belongs together, but it is explicitly told to answer with ids only, so the
// text it sees never becomes text it produces.
//
// Long tables are summarised rather than dumped: a 200-row annex table would
// consume the context window and tell the model nothing about the chapter
// structure that its caption does not already say.
func buildUserPrompt(doc *docmodel.Document, hint string, allowEdits bool) string {
	var b strings.Builder

	fmt.Fprintf(&b, "文档名称：%s\n", doc.SourceName)
	fmt.Fprintf(&b, "共 %d 个段落，其中需要收录的正文段落 %d 个。\n\n",
		len(doc.Nodes), len(doc.ContentNodes()))

	if strings.TrimSpace(hint) != "" {
		fmt.Fprintf(&b, "用户提示（关于本文性质，仅供参考，不是要写入正文的内容）：%s\n\n",
			strings.TrimSpace(hint))
	}
	if allowEdits {
		b.WriteString("用户已允许修正原文中的错别字/语病/用词/标点，请把每处修改写进 edits。\n")
		b.WriteString("注意：只改客观错误，能不改就不改；每处修改的 before 必须是原文中逐字出现的片段。\n\n")
	}

	b.WriteString("段落清单（格式：编号 | 类型 | 内容）：\n")
	b.WriteString("────────────────────────────────\n")

	for _, n := range doc.Nodes {
		switch n.Kind {
		case docmodel.KindPageBreak:
			fmt.Fprintf(&b, "%s | 分页符 | (无需收录)\n", n.ID)
		case docmodel.KindTable:
			fmt.Fprintf(&b, "%s | 表格(%d行×%d列) | %s\n",
				n.ID, len(n.Rows), tableCols(n.Rows), firstLine(n.Text, 80))
		case docmodel.KindImage:
			fmt.Fprintf(&b, "%s | 图片 | %s\n", n.ID, firstLine(n.Text, 80))
		case docmodel.KindHeading:
			fmt.Fprintf(&b, "%s | 标题(源层级%d) | %s\n", n.ID, n.Level, n.Text)
		case docmodel.KindListItem:
			fmt.Fprintf(&b, "%s | 列表项 | %s\n", n.ID, n.Text)
		default:
			fmt.Fprintf(&b, "%s | 正文 | %s\n", n.ID, n.Text)
		}
	}

	b.WriteString("────────────────────────────────\n\n")
	fmt.Fprintf(&b, "请把这 %d 个段落组织成章节树，并输出 JSON。",
		len(doc.ContentNodes()))
	return b.String()
}

// repairPrompt restates the specific failures so the model can fix exactly
// them. Generic feedback ("try again") tends to reproduce the same mistake.
func repairPrompt(report *docmodel.Report, doc *docmodel.Document) string {
	var b strings.Builder
	b.WriteString("你上一版的方案没有通过校验，请修正后重新输出完整的 JSON。\n\n")
	fmt.Fprintf(&b, "校验结果：%s\n\n", report.Summary())

	b.WriteString("必须修正的问题：\n")
	for _, d := range report.Errors() {
		fmt.Fprintf(&b, "- [%s] %s\n", d.Code, d.Message)
	}

	// Spelling out the missing paragraphs' ids (and a short excerpt) is what
	// makes the repair actionable; the model otherwise has to re-scan a long
	// list and may guess wrong.
	if missing := missingNodeIDs(report); len(missing) > 0 {
		b.WriteString("\n下列段落没有被收录，必须补上（请放进合适的章节 body_node_ids）：\n")
		for _, id := range missing {
			fmt.Fprintf(&b, "  %s | %s\n", id, firstLine(doc.Text(id), 60))
		}
	}

	b.WriteString("\n再次强调：不得改写任何原文；每个段落必须且只能被引用一次；")
	b.WriteString("只输出一个 JSON 对象，不要任何解释文字或 Markdown 围栏。")
	return b.String()
}

// missingNodeIDs extracts the node ids named by uncovered-content diagnostics.
func missingNodeIDs(report *docmodel.Report) []string {
	var out []string
	seen := map[string]bool{}
	for _, d := range report.Diagnostics {
		if d.Code != "uncovered_content" {
			continue
		}
		for _, id := range d.NodeIDs {
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	return out
}

// tableCols reports a table's column count from its widest row.
func tableCols(rows [][]string) int {
	n := 0
	for _, r := range rows {
		if len(r) > n {
			n = len(r)
		}
	}
	return n
}

// firstLine truncates for the prompt. rune-based, so a Chinese excerpt is not
// cut mid-character.
func firstLine(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// ParsePlan decodes a model answer into a plan.
//
// It is exported, and separate from the call, because parsing a model answer
// is the part most likely to need hardening against real-world sloppiness:
// Markdown fences, a sentence of preamble, a trailing comma. Keeping it a pure
// function of the text makes every one of those cases a unit test rather than
// something discovered in production.
//
// doc is used to repair a specific class of near-miss: a section whose Title
// copies a heading node's text but which forgot to set HeadingNodeID. That
// pattern is common and unambiguous, so it is resolved here instead of
// spending a repair round on it.
func ParsePlan(text string, doc *docmodel.Document) (*docmodel.Plan, error) {
	raw, err := extractJSONObject(text)
	if err != nil {
		return nil, err
	}

	// Decode strictly first: a plan with a misspelled field is a plan whose
	// element bindings were silently dropped, which is exactly the kind of
	// loss the project refuses to tolerate.
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	var plan docmodel.Plan
	if err := dec.Decode(&plan); err != nil {
		// The model may add a harmless extra key; retry leniently so one
		// invented field does not cost a whole repair round. The strict error
		// is still reported if the lenient pass also fails.
		if lerr := json.Unmarshal([]byte(raw), &plan); lerr != nil {
			return nil, fmt.Errorf("无法解析模型返回的 JSON：%v", err)
		}
	}

	normalizePlan(&plan, doc)

	// A syntactically valid but empty plan is not a usable answer. Returning it
	// would let the caller treat `{}` as success and store a plan with no
	// chapters, so it is rejected here and goes through the repair path like
	// any other bad answer.
	if len(plan.Elements) == 0 && len(plan.Sections) == 0 {
		return nil, fmt.Errorf("模型返回的方案为空：既没有章节也没有要素绑定")
	}
	return &plan, nil
}

// extractJSONObject pulls the JSON object out of a model answer, tolerating a
// code fence and surrounding prose.
func extractJSONObject(text string) (string, error) {
	s := strings.TrimSpace(text)
	if s == "" {
		return "", fmt.Errorf("模型返回了空回答")
	}

	// Strip a Markdown fence, with or without a language tag.
	if strings.HasPrefix(s, "```") {
		if nl := strings.IndexByte(s, '\n'); nl >= 0 {
			s = s[nl+1:]
		}
		if end := strings.LastIndex(s, "```"); end >= 0 {
			s = s[:end]
		}
		s = strings.TrimSpace(s)
	}

	// Take the outermost braces. A model that prefixes "好的，这是方案：" or
	// appends a closing remark is common enough that rejecting the answer
	// outright would burn a repair round for nothing.
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start < 0 || end < 0 || end <= start {
		return "", fmt.Errorf("模型回答中未找到 JSON 对象")
	}
	return s[start : end+1], nil
}

// normalizePlan repairs the unambiguous near-misses described on ParsePlan.
func normalizePlan(plan *docmodel.Plan, doc *docmodel.Document) {
	if doc == nil || plan == nil {
		return
	}
	if plan.Sections == nil {
		plan.Sections = []docmodel.Section{}
	}
	if plan.Elements == nil {
		plan.Elements = []docmodel.ElementBinding{}
	}
	// The UI iterates these arrays directly. A nil slice marshals to JSON null,
	// and `null.some(...)` throws — which is the same class of bug that made the
	// review screen crash at the exact moment validation passed. Normalising
	// here means the client never has to guard.
	if plan.Edits == nil {
		plan.Edits = []docmodel.Edit{}
	}
	if plan.PreambleNodeIDs == nil {
		plan.PreambleNodeIDs = []string{}
	}
	if plan.Notes == nil {
		plan.Notes = []string{}
	}

	// The model sometimes fills meta.title instead of binding the source's
	// title paragraph. Binding it keeps the title verbatim; leaving it in meta
	// would make the source's own title paragraph count as uncovered content.
	if strings.TrimSpace(plan.Meta.Title) != "" && !hasElement(plan, docmodel.RoleTitle) {
		if id := findNodeByText(doc, plan.Meta.Title); id != "" {
			plan.Elements = append(plan.Elements, docmodel.ElementBinding{
				Role: docmodel.RoleTitle, NodeID: id,
			})
			plan.Meta.Title = ""
		}
	}

	normalizeSections(plan.Sections, doc)

	// A section that names a heading node but left Title empty is trivially
	// fixable from the node itself.
	var fill func([]docmodel.Section)
	fill = func(list []docmodel.Section) {
		for i := range list {
			s := &list[i]
			if strings.TrimSpace(s.Title) == "" && s.HeadingNodeID != "" {
				s.Title = doc.Text(s.HeadingNodeID)
			}
			fill(s.Children)
		}
	}
	fill(plan.Sections)
}

// normalizeSections fixes a heading reference the model expressed only as a
// matching Title, and assigns missing section ids.
func normalizeSections(list []docmodel.Section, doc *docmodel.Document) {
	for i := range list {
		s := &list[i]
		if s.ID == "" {
			s.ID = fmt.Sprintf("s%d", i+1)
		}
		if s.HeadingNodeID == "" && strings.TrimSpace(s.Title) != "" {
			// Only adopt a node when the title matches it exactly; a partial
			// match would silently assert an equivalence the model did not
			// mean, and heading_mismatch exists precisely to catch that.
			if id := findNodeByText(doc, s.Title); id != "" {
				s.HeadingNodeID = id
			}
		}
		normalizeSections(s.Children, doc)
	}
}

// hasElement reports whether a role is already bound.
func hasElement(plan *docmodel.Plan, role docmodel.Role) bool {
	for _, e := range plan.Elements {
		if e.Role == role {
			return true
		}
	}
	return false
}

// findNodeByText returns the id of the first prose node whose text equals
// want (ignoring surrounding whitespace), or "".
func findNodeByText(doc *docmodel.Document, want string) string {
	want = strings.TrimSpace(want)
	if want == "" {
		return ""
	}
	for _, n := range doc.ContentNodes() {
		if strings.TrimSpace(n.Text) == want {
			return n.ID
		}
	}
	return ""
}
