// Package govdoc formats a document to GB/T 9704-2012 (党政机关公文格式).
//
// It is a thin adapter: parsing, model calling, verification and .docx writing
// live in the vendored engine under engine/ (copied from cn-maul/GovDoc at
// v1.0 lineage). Nothing here re-implements the "no silent rewrite" rule —
// the engine refuses to render a document whose text changed without a declared
// edit, and that refusal surfaces here as a tool error.
package govdoc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	engineconfig "opentools/tools/govdoc/engine/config"
	"opentools/tools/govdoc/engine/docmodel"
	"opentools/tools/govdoc/engine/gbstandard"
	"opentools/tools/govdoc/engine/llm"
	"opentools/tools/govdoc/engine/pipeline"

	"opentools/pkg/tool"
)

// version is the plugin version shown in the tool's title bar.
const version = "1.0.0"

// maxInputBytes bounds the uploaded document. 公文 are text documents; the
// engine's own HTTP server uses the same 32MiB ceiling.
const maxInputBytes = 32 << 20

// definitionValue is computed once: the host calls describe on every start and
// the schema embeds the engine's built-in profile list.
var definitionValue = buildDefinition()

// Tool formats one document.
type Tool struct{}

// New returns the GovDoc tool.
func New() Tool { return Tool{} }

// Definition implements tool.Tool.
func (Tool) Definition() tool.Definition { return definitionValue }

func buildDefinition() tool.Definition {
	profiles := engineconfig.BuiltinProfiles()
	profileValues := make([]string, 0, len(profiles))
	profileLabels := make(map[string]string, len(profiles))
	for _, p := range profiles {
		profileValues = append(profileValues, p.ID)
		profileLabels[p.ID] = p.Name
	}

	return tool.Definition{
		ID:      "document.govdoc-format",
		Name:    "公文格式规范化",
		Summary: "把排版混乱的文档按 GB/T 9704-2012 重排为规范公文，原文逐字可校验。",
		Description: "解析 .docx / .txt / .md，由模型判断章节层次，程序按编号取回原文装配，再按国标输出字号、行距、页边距合规的 .docx。" +
			"模型全程只返回段落编号，不生成正文；任何改写都必须声明为一条带「原文→改后+理由」的改动，未声明的改写会导致导出被拒绝。" +
			"成功后返回成品 .docx、改动清单与逐字比对结果。",
		Category: "文档工具",
		Icon:     "document",
		Keywords: []string{"公文", "排版", "格式", "docx", "word", "国标", "GB/T 9704", "错别字", "段落", "章节"},
		Version:  version,
		Effects:  []tool.Effect{tool.EffectReadFiles, tool.EffectWriteFiles, tool.EffectNetwork, tool.EffectSecrets},
		Input: tool.Object(map[string]*tool.Schema{
			"source": tool.String("源文档", "要重排的 .docx / .txt / .md 文件路径。").
				WithWidget("file", "选择一份文档").
				WithAccept(".docx", ".txt", ".md"),
			"profile": tool.Select(
				"排版规则",
				"决定标题、正文的字体字号、行距与页边距。公文为 GB/T 9704-2012 标准；其它三套用于非公文文档。",
				profileValues...,
			).
				WithDefault(engineconfig.ProfileGovDoc).
				WithEnumLabels(profileLabels),
			"direction": tool.Select(
				"行文方向",
				"只影响发文机关标志距版心上边缘的距离：下行文与平行文 35mm，上行文 80mm。",
				string(gbstandard.DirDown), string(gbstandard.DirUp), string(gbstandard.DirParallel),
			).
				WithDefault(string(gbstandard.DirDown)).
				WithEnumLabels(map[string]string{
					string(gbstandard.DirDown):     "下行文",
					string(gbstandard.DirUp):       "上行文",
					string(gbstandard.DirParallel): "平行文",
				}),
			"endpoint": tool.String("接口地址", "OpenAI 兼容接口地址，例如 https://api.deepseek.com/v1。").
				WithWidget("", "https://api.deepseek.com/v1"),
			"api_key": tool.String("API Key", "只用于本次调用，不会写入任何文件。").
				WithWidget("", "sk-…").
				WithSecret(),
			"model": tool.String("模型", "模型名称，例如 deepseek-chat。").
				WithWidget("", "deepseek-chat"),
			"protocol": tool.Select(
				"接口协议",
				"接口使用的线格式。绝大多数服务用 OpenAI Chat。",
				"openai-chat", "openai-responses", "anthropic",
			).
				WithDefault("openai-chat").
				WithEnumLabels(map[string]string{
					"openai-chat":      "OpenAI Chat",
					"openai-responses": "OpenAI Responses",
					"anthropic":        "Anthropic",
				}),
			"allow_edits": tool.Boolean(
				"允许修正错别字与语病",
				"开启后模型可以提出用词、标点、断句等修正，但每一处都必须给出原文、改后与理由，并在结果里逐条列出；未申报的修改仍会导致导出失败。",
			).WithDefault(false),
			"hint": tool.Text(
				"文档说明",
				"可选。告诉模型这是什么文档（如「这是一份安全生产检查通报」），帮助它判断章节层次。说明不会写进正文。",
				3,
			),
			"timeout_seconds": tool.Number(
				"超时秒数",
				"单次模型调用的超时。长文档建议 180 秒以上。",
			).WithMin(30).WithMax(600).WithDefault(120),
		}, "source", "endpoint", "api_key", "model"),
	}
}

// Run implements tool.Tool.
func (Tool) Run(ctx context.Context, input tool.Input, env tool.Environment, report tool.Reporter) (tool.Result, error) {
	var params struct {
		Source         string  `json:"source"`
		Profile        string  `json:"profile"`
		Direction      string  `json:"direction"`
		Endpoint       string  `json:"endpoint"`
		APIKey         string  `json:"api_key"`
		Model          string  `json:"model"`
		Protocol       string  `json:"protocol"`
		AllowEdits     bool    `json:"allow_edits"`
		Hint           string  `json:"hint"`
		TimeoutSeconds float64 `json:"timeout_seconds"`
	}
	if err := input.Decode(&params); err != nil {
		return tool.Result{}, err
	}

	for _, section := range []tool.Section{
		{ID: "source", Title: "源文档", State: tool.SectionPending},
		{ID: "structure", Title: "结构与校验", State: tool.SectionPending},
		{ID: "output", Title: "成品文档", State: tool.SectionPending},
	} {
		report.Section(section)
	}

	report.Section(tool.Section{ID: "source", Title: "源文档", State: tool.SectionRunning})
	report.Log("info", "正在读取源文档")
	source, err := readSource(params.Source)
	if err != nil {
		return tool.Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}

	spec, err := resolveSpec(params.Profile, params.Direction)
	if err != nil {
		return tool.Result{}, err
	}

	sourceSection := tool.Section{
		ID:    "source",
		Title: "源文档",
		State: tool.SectionReady,
		Blocks: []tool.Block{{
			Kind: tool.BlockFacts,
			Data: []tool.Fact{
				{Label: "文件", Value: filepath.Base(source.name)},
				{Label: "大小", Value: humanBytes(int64(len(source.data)))},
				{Label: "排版规则", Value: profileName(params.Profile)},
				{Label: "行文方向", Value: params.Direction},
			},
		}},
	}
	report.Section(sourceSection)
	report.Progress(1, 3)

	report.Section(tool.Section{ID: "structure", Title: "结构与校验", State: tool.SectionRunning})
	timeout := time.Duration(params.TimeoutSeconds * float64(time.Second))
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	result, err := pipeline.Run(ctx, pipeline.Options{
		SourceName: source.name,
		Data:       source.data,
		Spec:       spec,
		LLM: llm.Config{
			Endpoint: params.Endpoint,
			APIKey:   params.APIKey,
			Model:    params.Model,
			Protocol: params.Protocol,
			Timeout:  timeout,
		},
		Hint:       strings.TrimSpace(params.Hint),
		AllowEdits: params.AllowEdits,
		Stage: func(stage pipeline.Stage) {
			report.Log("info", stage.Message)
			if stage.Done && stage.Name == pipeline.StageAnalyze {
				report.Progress(2, 3)
			}
		},
	})
	if err != nil {
		return tool.Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}
	report.Section(structureSection(result))
	report.Progress(2, 3)

	report.Section(tool.Section{ID: "output", Title: "成品文档", State: tool.SectionRunning})
	name := outputName(source.name, result.Plan)
	path := filepath.Join(env.WorkDir, name)
	if err := os.WriteFile(path, result.Data, 0o644); err != nil {
		return tool.Result{}, fmt.Errorf("写出成品文档失败：%w", err)
	}
	artifactPath, size, err := tool.ResolveArtifact(env, path)
	if err != nil {
		return tool.Result{}, err
	}

	output := outputSection(result, name, size)
	report.Section(output)
	report.Progress(3, 3)
	report.Log("info", "公文生成完成")

	return tool.Result{
		Summary:   summarize(result, name),
		Blocks:    append(structureSection(result).Blocks, output.Blocks...),
		Artifacts: []tool.Artifact{{Name: name, Path: artifactPath, MIME: docxMIME}},
		Warnings:  warnings(result),
	}, nil
}

const docxMIME = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"

type source struct {
	name     string
	data     []byte
	original int
}

func readSource(path string) (source, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return source{}, fmt.Errorf("请先选择要重排的文档")
	}
	info, err := os.Stat(path)
	if err != nil {
		return source{}, fmt.Errorf("找不到源文档：%w", err)
	}
	if info.IsDir() {
		return source{}, fmt.Errorf("源文档是一个文件夹：%s", path)
	}
	if info.Size() > maxInputBytes {
		return source{}, fmt.Errorf("源文档 %s 超过 %d MiB 上限", humanBytes(info.Size()), maxInputBytes>>20)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return source{}, fmt.Errorf("读取源文档失败：%w", err)
	}
	return source{name: filepath.Base(path), data: data, original: len(data)}, nil
}

// resolveSpec turns the selected profile into a Spec. It decodes the profile
// through the engine's own JSON form so the plugin and the desktop app cannot
// drift apart on a renamed field.
func resolveSpec(profile, direction string) (gbstandard.Spec, error) {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		profile = engineconfig.ProfileGovDoc
	}
	var chosen *engineconfig.Profile
	for _, candidate := range engineconfig.BuiltinProfiles() {
		if candidate.ID == profile {
			copied := candidate
			chosen = &copied
			break
		}
	}
	if chosen == nil {
		return gbstandard.Spec{}, fmt.Errorf("未知的排版规则 %q", profile)
	}
	spec, err := chosen.Decode()
	if err != nil {
		return gbstandard.Spec{}, err
	}
	switch gbstandard.Direction(strings.TrimSpace(direction)) {
	case gbstandard.DirUp:
		spec.Direction = gbstandard.DirUp
	case gbstandard.DirParallel:
		spec.Direction = gbstandard.DirParallel
	default:
		spec.Direction = gbstandard.DirDown
	}
	return spec, nil
}

func profileName(id string) string {
	for _, p := range engineconfig.BuiltinProfiles() {
		if p.ID == id {
			return p.Name
		}
	}
	return engineconfig.BuiltinProfiles()[0].Name
}

func structureSection(result *pipeline.Result) tool.Section {
	var blocks []tool.Block
	report := result.Report
	if report != nil {
		tone := tool.ToneSuccess
		if !report.OK() {
			tone = tool.ToneDanger
		}
		blocks = append(blocks, tool.Block{
			Kind:  tool.BlockNotice,
			Title: "结构校验",
			Tone:  tone,
			Text:  report.Summary(),
		})
		blocks = append(blocks, tool.Block{
			Kind: tool.BlockFacts,
			Data: []tool.Fact{
				{Label: "原文段落", Value: fmt.Sprintf("%d 段", report.ContentNodes)},
				{Label: "已收录", Value: fmt.Sprintf("%d 段", report.CoveredNodes)},
				{Label: "重复引用", Value: fmt.Sprintf("%d 处", report.DuplicatedNodes)},
				{Label: "生成标题", Value: fmt.Sprintf("%d 个", report.SynthesizedHeadings)},
			},
		})
	}
	if headings := synthesizedHeadings(result.SynthText); len(headings) > 0 {
		rows := make([][]string, 0, len(headings))
		for _, heading := range headings {
			rows = append(rows, []string{heading})
		}
		blocks = append(blocks, tool.Block{
			Kind:    tool.BlockTable,
			Title:   "自动补写的标题（非原文）",
			Columns: []string{"标题"},
			Rows:    rows,
		})
	}
	if diff := result.Diff; diff != nil && len(diff.Changed) > 0 {
		rows := make([][]string, 0, len(diff.Changed))
		for _, edit := range diff.Changed {
			rows = append(rows, []string{edit.Kind, edit.Before, edit.After, edit.Reason})
		}
		blocks = append(blocks, tool.Block{
			Kind:    tool.BlockTable,
			Title:   "已声明的修改（可在成品中核对）",
			Columns: []string{"类型", "原文", "改后", "理由"},
			Rows:    rows,
		})
	}
	if len(blocks) == 0 {
		blocks = append(blocks, tool.Block{Kind: tool.BlockText, Text: "结构校验完成。"})
	}
	return tool.Section{ID: "structure", Title: "结构与校验", State: tool.SectionReady, Blocks: blocks}
}

func outputSection(result *pipeline.Result, name string, size int64) tool.Section {
	changed, extra := 0, 0
	if result.Diff != nil {
		changed = len(result.Diff.Changed)
		extra = len(result.Diff.Extra)
	}
	return tool.Section{
		ID:    "output",
		Title: "成品文档",
		State: tool.SectionReady,
		Blocks: []tool.Block{{
			Kind: tool.BlockFacts,
			Data: []tool.Fact{
				{Label: "文件", Value: name},
				{Label: "大小", Value: humanBytes(size)},
				{Label: "已声明改动", Value: fmt.Sprintf("%d 处", changed)},
				{Label: "新增文字", Value: fmt.Sprintf("%d 处", extra)},
			},
		}},
	}
}

// synthesizedHeadings picks the generated chapter headings out of SynthText.
// They are the only synthesized strings that start with a Chinese numbering
// prefix; masthead elements and page numbers do not.
func synthesizedHeadings(synth []string) []string {
	var out []string
	for _, text := range synth {
		if looksLikeHeading(text) {
			out = append(out, text)
		}
	}
	return out
}

func looksLikeHeading(text string) bool {
	text = strings.TrimSpace(text)
	for _, prefix := range []string{"一、", "二、", "三、", "四、", "五、", "六、", "七、", "八、", "九、", "十、"} {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	for _, prefix := range []string{"（一）", "（二）", "（三）", "（四）", "（五）"} {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

func summarize(result *pipeline.Result, name string) string {
	total := 0
	if result.Report != nil {
		total = result.Report.CoveredNodes
	}
	changed := 0
	if result.Diff != nil {
		changed = len(result.Diff.Changed)
	}
	if changed > 0 {
		return fmt.Sprintf("已生成 %s：%d 段原文逐字校验通过，另有 %d 处已声明修改。", name, total, changed)
	}
	return fmt.Sprintf("已生成 %s：%d 段原文逐字校验通过，没有改动任何文字。", name, total)
}

func warnings(result *pipeline.Result) []string {
	var out []string
	if result.Report != nil && !result.Report.OK() {
		out = append(out, "结构校验未完全通过："+result.Report.Summary())
	}
	if len(result.FailedEdits) > 0 {
		for _, edit := range result.FailedEdits {
			out = append(out, fmt.Sprintf("改动未生效：%s → %s", edit.Before, edit.Reason))
		}
	}
	return out
}

func outputName(sourceName string, plan *docmodel.Plan) string {
	base := strings.TrimSuffix(sourceName, filepath.Ext(sourceName))
	if plan != nil {
		for _, element := range plan.Elements {
			if element.Role == docmodel.RoleTitle && strings.TrimSpace(plan.Meta.Title) != "" {
				base = plan.Meta.Title
				break
			}
		}
		if strings.TrimSpace(base) == "" || base == strings.TrimSuffix(sourceName, filepath.Ext(sourceName)) {
			if strings.TrimSpace(plan.Meta.Title) != "" {
				base = plan.Meta.Title
			}
		}
	}
	base = sanitizeName(base)
	if base == "" {
		base = "公文"
	}
	return base + ".docx"
}

func sanitizeName(name string) string {
	name = strings.Map(func(r rune) rune {
		switch r {
		case '<', '>', ':', '"', '/', '\\', '|', '?', '*':
			return '_'
		}
		if r < 32 {
			return -1
		}
		return r
	}, strings.TrimSpace(name))
	name = strings.TrimRight(name, ". ")
	if runes := []rune(name); len(runes) > 80 {
		name = string(runes[:80])
	}
	return name
}

func humanBytes(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	value := float64(size)
	for _, name := range []string{"KB", "MB", "GB"} {
		value /= unit
		if value < unit {
			return fmt.Sprintf("%.1f %s", value, name)
		}
	}
	return fmt.Sprintf("%.1f TB", value/unit)
}

// DescribeJSON is used by tests and packaging to inspect the definition
// without starting the RPC loop.
func DescribeJSON() ([]byte, error) { return json.MarshalIndent(definitionValue, "", "  ") }
