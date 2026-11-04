// Package llm is the only place in the program that talks to a model.
//
// # What this package is, and is not, responsible for
//
// It builds a prompt that asks the model to *group* the source paragraphs into
// a chapter tree, sends it through github.com/cn-maul/rosetta, parses the
// answer, and — critically — checks its own work against
// docmodel.VerifyPlan, feeding the failures back for another attempt.
//
// It is not responsible for deciding whether a plan is renderable. That
// decision belongs to docmodel.VerifyPlan, and the server applies it once at
// the choke point before anything is stored. This package verifying too is a
// quality measure (a plan that already satisfies the checker is far more
// likely to survive the server's independent check), not an authority.
//
// # Why the prompt is built from ids
//
// The model never sees a request to retype the document. It is given a list of
// `id | kind | text` lines and must answer with a plan that references those
// ids. Content cannot be silently rewritten because content is never asked
// for: the assembler pulls every character from the parsed Document, and
// VerifyPlan rejects any plan that drops or duplicates a paragraph. This is
// the mechanism that makes 原文逐字不变 a property of the design rather than a
// hope about model behaviour.
package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cn-maul/rosetta"

	"opentools/tools/govdoc/engine/docmodel"
)

// ErrNotConfigured is returned when endpoint, key or model is missing.
var ErrNotConfigured = errors.New("llm: 未配置模型连接")

// ErrTruncated is returned when the model's answer was cut off by the output
// cap. A truncated plan would look complete but silently lose chapters, so it
// is treated as a hard failure rather than parsed optimistically.
var ErrTruncated = errors.New("llm: 模型回答被截断（输出长度上限），未能得到完整结构方案")

// maxRepairRounds bounds the self-repair loop. Two rounds is enough for a
// model to fix a missed node or a wrong id; more usually means the prompt is
// unclear and the loop is burning the user's tokens.
const maxRepairRounds = 2

// defaultTimeout bounds one model call when the caller does not set one.
const defaultTimeout = 120 * time.Second

// defaultMaxOutput is the output cap. A plan for a long document is verbose
// JSON, so this is generous; the value is also what makes ErrTruncated
// actionable rather than mysterious.
const defaultMaxOutput = 8192

// Config is the model connection.
type Config struct {
	Endpoint string
	APIKey   string
	Model    string
	// Protocol is "" (defaults to openai-chat), "openai-chat",
	// "openai-responses" or "anthropic".
	Protocol string
	// Timeout bounds one call. Zero applies a 120s default.
	Timeout time.Duration
	// MaxOutputTokens overrides the output cap. Zero applies the default.
	MaxOutputTokens int
	// Temperature is applied when non-nil. Planning is a structuring task, so
	// the caller normally wants a low value; leaving it nil lets the provider
	// use its own default.
	Temperature *float64
}

// Stage is one progress notification shown while a plan is produced.
type Stage struct {
	Name    string `json:"name"`
	Message string `json:"message"`
	Done    bool   `json:"done"`
}

// Stage names, stable so the UI can key on them.
const (
	StageConnect  = "connect"
	StageAnalyze  = "analyze"
	StageRepair   = "repair"
	StageValidate = "validate"
	StageComplete = "complete"
)

// PlanError carries a plan together with the verdict that rejected it.
//
// Returning both matters: when the repair loop gives up, the user is far
// better served by seeing what the model produced and which paragraphs it
// mishandled than by a bare "failed". The server stores the partial plan so
// the 校对 screen opens with something to fix.
type PlanError struct {
	Plan   *docmodel.Plan
	Report *docmodel.Report
	// Cause is the underlying failure, when there was one (an API error, a
	// timeout). It is nil when the loop simply exhausted its rounds.
	Cause error
	// Rounds is how many model calls were made.
	Rounds int
}

func (e *PlanError) Error() string {
	var b strings.Builder
	b.WriteString("llm: 未能生成通过校验的结构方案")
	if e.Report != nil {
		fmt.Fprintf(&b, "（%s）", e.Report.Summary())
	}
	fmt.Fprintf(&b, "，已尝试 %d 次", e.Rounds)
	if e.Cause != nil {
		fmt.Fprintf(&b, "：%v", e.Cause)
	}
	if e.Report != nil {
		if errs := e.Report.Errors(); len(errs) > 0 {
			b.WriteString("。主要问题：")
			for i, d := range errs {
				if i >= 3 {
					fmt.Fprintf(&b, " 等 %d 项", len(errs))
					break
				}
				if i > 0 {
					b.WriteString("；")
				}
				b.WriteString(d.Message)
			}
		}
	}
	return b.String()
}

func (e *PlanError) Unwrap() error { return e.Cause }

// Client produces plans for one configuration. It is safe for concurrent use:
// rosetta.Client is, and this type holds only immutable configuration.
type Client struct {
	cfg    Config
	client *rosetta.Client
}

// New validates the configuration and builds a client.
func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.Endpoint) == "" ||
		strings.TrimSpace(cfg.APIKey) == "" ||
		strings.TrimSpace(cfg.Model) == "" {
		return nil, ErrNotConfigured
	}

	opts := []rosetta.Option{
		rosetta.WithEndpoint(strings.TrimSpace(cfg.Endpoint)),
		rosetta.WithAPIKey(strings.TrimSpace(cfg.APIKey)),
		// The output-cap field name is pinned rather than probed: the probe
		// costs an extra round trip on the first call, and every endpoint this
		// tool targets accepts max_tokens.
		rosetta.WithMaxTokensField("max_tokens"),
		// Thinking-capable models are common now, but planning does not need
		// reasoning traces and they consume the output budget. Leaving
		// Thinking nil disables it.
	}
	switch strings.TrimSpace(cfg.Protocol) {
	case "", "openai-chat":
		opts = append(opts, rosetta.WithProtocol(rosetta.ProtoOpenAIChat))
	case "openai-responses":
		opts = append(opts, rosetta.WithProtocol(rosetta.ProtoOpenAIResponses))
	case "anthropic":
		opts = append(opts, rosetta.WithProtocol(rosetta.ProtoAnthropic))
	default:
		return nil, fmt.Errorf("llm: 不支持的协议 %q（可选 openai-chat、openai-responses、anthropic）", cfg.Protocol)
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	opts = append(opts, rosetta.WithTimeout(timeout))
	// A 401/403 must not be retried pointlessly; the retry budget is kept
	// small so a misconfigured key fails fast and visibly.
	opts = append(opts, rosetta.WithMaxRetries(1))

	client, err := rosetta.NewClient(opts...)
	if err != nil {
		return nil, fmt.Errorf("llm: 创建客户端失败：%w", err)
	}
	return &Client{cfg: cfg, client: client}, nil
}

// Model returns the configured model id.
func (c *Client) Model() string { return c.cfg.Model }

// maxOutput resolves the output cap.
func (c *Client) maxOutput() int {
	if c.cfg.MaxOutputTokens > 0 {
		return c.cfg.MaxOutputTokens
	}
	return defaultMaxOutput
}

// Plan asks the model to restructure doc. progress may be nil.
//
// This is the documented entry point; PlanWithOptions is the same call with
// the optional settings.
func (c *Client) Plan(ctx context.Context, doc *docmodel.Document, progress func(Stage)) (*docmodel.Plan, error) {
	return c.PlanWithOptions(ctx, doc, PlanOptions{}, progress)
}

// PlanOptions carries the per-request analysis settings.
type PlanOptions struct {
	// Hint is the user's note about the document's nature ("这是一份通报").
	// It is guidance about intent, never content, and is never written into a
	// node, so it cannot reach the output document.
	Hint string
	// AllowEdits permits the model to propose content corrections —
	// 错别字, 语病, 用词, 标点, 断句 — in addition to restructuring.
	//
	// When true the model is told it MUST express every correction as a
	// declared edit with a before/after and a reason. When false it is told, as
	// before, that it may not change a character. Either way an undeclared
	// change fails verification, so this flag widens what may be *proposed*
	// without ever widening what may happen silently.
	AllowEdits bool
	// TrustedSource tells the model the text is already clean, so it should not
	// spend effort hunting for errors. It is the default for a document the
	// user has not flagged.
	TrustedSource bool
}

// PlanWithHint is retained for callers that only need a hint.
func (c *Client) PlanWithHint(
	ctx context.Context,
	doc *docmodel.Document,
	hint string,
	progress func(Stage),
) (*docmodel.Plan, error) {
	return c.PlanWithOptions(ctx, doc, PlanOptions{Hint: hint}, progress)
}

// PlanWithOptions asks the model to restructure doc.
func (c *Client) PlanWithOptions(
	ctx context.Context,
	doc *docmodel.Document,
	opts PlanOptions,
	progress func(Stage),
) (*docmodel.Plan, error) {
	if c == nil || c.client == nil {
		return nil, ErrNotConfigured
	}
	if doc == nil {
		return nil, errors.New("llm: 缺少文档")
	}
	if len(doc.ContentNodes()) == 0 {
		return nil, errors.New("llm: 文档中没有可梳理的正文内容")
	}

	notify := func(name, msg string) {
		if progress != nil {
			progress(Stage{Name: name, Message: msg})
		}
	}

	notify(StageConnect, fmt.Sprintf("正在连接模型 %s…", c.cfg.Model))

	// The conversation is built up across repair rounds, so the model can see
	// its own previous answer and the specific objections to it. Replaying the
	// exchange is what makes the repair round effective: a fresh prompt would
	// often reproduce the same mistake.
	messages := []rosetta.Message{
		rosetta.User(buildUserPrompt(doc, opts.Hint, opts.AllowEdits)),
	}

	var (
		lastPlan   *docmodel.Plan
		lastReport *docmodel.Report
		rounds     int
	)

	for attempt := 0; attempt <= maxRepairRounds; attempt++ {
		if err := ctx.Err(); err != nil {
			return lastPlan, &PlanError{Plan: lastPlan, Report: lastReport, Cause: err, Rounds: rounds}
		}

		if attempt == 0 {
			notify(StageAnalyze, "正在分析文档结构…")
		} else {
			notify(StageRepair, fmt.Sprintf("正在修正结构方案（第 %d 次）…", attempt))
		}

		resp, err := c.client.Chat(ctx, &rosetta.ChatRequest{
			Model:           c.cfg.Model,
			System:          systemPrompt(doc, opts.AllowEdits),
			Messages:        messages,
			MaxOutputTokens: c.maxOutput(),
			Temperature:     c.cfg.Temperature,
		})
		rounds++
		if err != nil {
			return lastPlan, &PlanError{Plan: lastPlan, Report: lastReport, Cause: classify(err), Rounds: rounds}
		}
		if resp.StopReason == rosetta.StopLength {
			return lastPlan, &PlanError{
				Plan: lastPlan, Report: lastReport, Rounds: rounds,
				Cause: fmt.Errorf("%w（可增大 MaxOutputTokens，当前 %d）", ErrTruncated, c.maxOutput()),
			}
		}

		text := resp.Text()
		if strings.TrimSpace(text) == "" {
			return lastPlan, &PlanError{
				Plan: lastPlan, Report: lastReport, Rounds: rounds,
				Cause: errors.New("模型返回了空回答"),
			}
		}

		plan, perr := ParsePlan(text, doc)
		if perr != nil {
			// A malformed answer is worth one more attempt with the parse
			// error spelled out: models frequently recover from a JSON slip
			// when told exactly what was wrong.
			messages = append(messages,
				rosetta.Assistant(text),
				rosetta.User(fmt.Sprintf(
					"你上面的回答无法解析为所需的 JSON：%v\n\n"+
						"请重新输出**只包含一个 JSON 对象**的回答，不要包含解释文字或 Markdown 代码围栏。", perr)),
			)
			continue
		}

		// Structure the model may have omitted: a plan with no sections is a
		// parse success but a useless answer, so it goes through the same
		// repair path as a failed verification rather than being returned.
		notify(StageValidate, "正在校验结构方案…")
		report := docmodel.VerifyPlan(doc, plan)
		lastPlan, lastReport = plan, report

		if report.OK() {
			plan.Model = c.cfg.Model
			notify(StageComplete, "分析完成")
			return plan, nil
		}

		if attempt == maxRepairRounds {
			break
		}
		messages = append(messages,
			rosetta.Assistant(text),
			rosetta.User(repairPrompt(report, doc)),
		)
	}

	return lastPlan, &PlanError{Plan: lastPlan, Report: lastReport, Rounds: rounds}
}

// ListModels fetches the endpoint's model catalog, so the UI can offer a list
// instead of asking the user to remember an exact model id.
func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	if c == nil || c.client == nil {
		return nil, ErrNotConfigured
	}
	infos, err := c.client.ListModels(ctx)
	if err != nil {
		return nil, classify(err)
	}
	out := make([]string, 0, len(infos))
	for _, m := range infos {
		if m.ID != "" {
			out = append(out, m.ID)
		}
	}
	return out, nil
}

// classify turns an SDK error into a message that tells the user what to do.
//
// The distinction that matters most is *whose fault* it is: a bad key needs the
// user to change a setting, while an upstream fault needs a retry. Telling
// them apart in the message is what stops a user from re-entering a correct
// key because the provider happened to be down.
func classify(err error) error {
	if err == nil {
		return nil
	}

	var apiErr *rosetta.APIError
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.StatusCode == 401 || apiErr.StatusCode == 403:
			return fmt.Errorf("API Key 无效或无权限（HTTP %d）：请在设置中检查密钥。原始信息：%v",
				apiErr.StatusCode, err)
		case apiErr.StatusCode == 404:
			return fmt.Errorf("接口或模型不存在（HTTP 404）：请检查接口地址与模型名称。原始信息：%v", err)
		case apiErr.StatusCode == 429:
			return fmt.Errorf("请求过于频繁（HTTP 429）：请稍后重试。原始信息：%v", err)
		case apiErr.StatusCode >= 500:
			return fmt.Errorf("模型服务端错误（HTTP %d）：请稍后重试。原始信息：%v",
				apiErr.StatusCode, err)
		default:
			return fmt.Errorf("模型接口返回错误（HTTP %d）：%v", apiErr.StatusCode, err)
		}
	}

	switch {
	case errors.Is(err, rosetta.ErrNoAPIKey):
		return fmt.Errorf("%w：请填写 API Key", ErrNotConfigured)
	case errors.Is(err, rosetta.ErrContextTooLong):
		return fmt.Errorf("文档过长，超出模型上下文窗口：请拆分文档后重试（%v）", err)
	case errors.Is(err, rosetta.ErrUpstreamMalformed):
		return fmt.Errorf("模型返回了无法解析的内容（可能被网关截断）：%v", err)
	case errors.Is(err, rosetta.ErrStreamTruncated):
		return fmt.Errorf("连接被中断：%v", err)
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("请求已取消或超时：%v", err)
	case errors.Is(err, rosetta.ErrInvalidRequest):
		return fmt.Errorf("请求不合法：%v", err)
	}
	return err
}
