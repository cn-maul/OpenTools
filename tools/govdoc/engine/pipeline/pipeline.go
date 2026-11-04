// Package pipeline runs the whole GovDoc conversion in one call.
//
// The HTTP server splits this work across requests so a user can review and
// correct the chapter tree before exporting. Anything that merely wants the
// finished document — a CLI, a downloaded OpenTools plugin, a batch script —
// has no such need, and threading the intermediate state itself would force
// every caller to reimplement the same order of operations.
//
// The invariants are unchanged because the steps are the same ones the server
// uses: the model only ever returns node ids, assemble.Build re-verifies the
// plan, and an undeclared rewrite still fails the build.
package pipeline

import (
	"context"
	"fmt"
	"strings"

	"opentools/tools/govdoc/engine/assemble"
	"opentools/tools/govdoc/engine/docmodel"
	"opentools/tools/govdoc/engine/gbstandard"
	"opentools/tools/govdoc/engine/llm"
	"opentools/tools/govdoc/engine/parse"
	"opentools/tools/govdoc/engine/render/docx"
)

// Options is one conversion request.
type Options struct {
	// SourceName is the uploaded file name; its extension selects the parser.
	SourceName string
	// Data is the uploaded document. It is never written to disk.
	Data []byte
	// Spec is the formatting profile to apply.
	Spec gbstandard.Spec
	// LLM is the model connection.
	LLM llm.Config
	// Hint is the user's note about what kind of document this is. It is sent
	// to the model and never into the document.
	Hint string
	// AllowEdits lets the model propose 错别字/语病 corrections, each of which
	// must be declared as a docmodel.Edit.
	AllowEdits bool
	// Stage receives coarse progress. It may be nil.
	Stage func(Stage)
}

// Stage is one user-facing progress step. Done marks the end of a phase.
type Stage struct {
	Name    string
	Message string
	Done    bool
}

// Stage names, stable so a caller can key on them.
const (
	StageParse    = "parse"
	StageAnalyze  = "analyze"
	StageAssemble = "assemble"
	StageRender   = "render"
)

// Result is the finished conversion.
type Result struct {
	Document *docmodel.Document
	Plan     *docmodel.Plan
	// Data is the .docx bytes.
	Data []byte
	// SynthText lists every output string with no source node, so a caller can
	// show exactly what was added.
	SynthText []string
	Diff      *docmodel.VerbatimDiff
	Report    *docmodel.Report
	// FailedEdits are declared edits whose Before text was not found.
	FailedEdits []docmodel.UnappliedEdit
}

// Run parses, analyzes, assembles and renders one document.
func Run(ctx context.Context, opts Options) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	opts.Stage = nonNilStage(opts.Stage)

	opts.Stage(Stage{Name: StageParse, Message: fmt.Sprintf("正在解析 %s …", opts.SourceName)})
	doc, err := parse.Parse(opts.SourceName, opts.Data)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	opts.Stage(Stage{Name: StageParse, Message: fmt.Sprintf("已解析 %d 个段落", len(doc.Nodes)), Done: true})

	client, err := llm.New(opts.LLM)
	if err != nil {
		return nil, err
	}
	plan, err := client.PlanWithOptions(ctx, doc, llm.PlanOptions{
		Hint:       opts.Hint,
		AllowEdits: opts.AllowEdits,
	}, func(stage llm.Stage) {
		opts.Stage(Stage{Name: StageAnalyze, Message: stage.Message, Done: stage.Done})
	})
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	opts.Stage(Stage{Name: StageAssemble, Message: "正在装配文档并逐字校验…"})
	built, err := assemble.Build(doc, plan, opts.Spec)
	if err != nil {
		// A failed build still carries the report, which is the only useful
		// thing to show when the plan dropped or duplicated a paragraph.
		if built != nil && built.Report != nil {
			return nil, fmt.Errorf("%w：%s", err, summarizeReport(built.Report))
		}
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	opts.Stage(Stage{Name: StageAssemble, Message: fmt.Sprintf("校验通过，原文 %d 段全部收录", built.Report.CoveredNodes), Done: true})

	opts.Stage(Stage{Name: StageRender, Message: "正在生成 .docx …"})
	data, err := docx.Render(built.Doc, opts.Spec)
	if err != nil {
		return nil, err
	}
	opts.Stage(Stage{Name: StageRender, Message: fmt.Sprintf("已生成 %d KB 文档", (len(data)+512)/1024), Done: true})

	if diff := built.Diff; diff != nil && len(diff.Altered) > 0 {
		return nil, fmt.Errorf("导出被拒绝：有 %d 处原文被改写却没有声明", len(diff.Altered))
	}

	return &Result{
		Document:    doc,
		Plan:        plan,
		Data:        data,
		SynthText:   built.SynthText,
		Diff:        built.Diff,
		Report:      built.Report,
		FailedEdits: built.FailedEdits,
	}, nil
}

func nonNilStage(stage func(Stage)) func(Stage) {
	if stage != nil {
		return stage
	}
	return func(Stage) {}
}

func summarizeReport(report *docmodel.Report) string {
	var parts []string
	for _, d := range report.Errors() {
		parts = append(parts, strings.TrimSpace(d.Message))
	}
	if len(parts) == 0 {
		return report.Summary()
	}
	return strings.Join(parts, "；")
}
