package application

import (
	"context"
	"testing"

	"github.com/cn-maul/OpenTools/internal/catalog"
	"github.com/cn-maul/OpenTools/internal/plugins"
	"github.com/cn-maul/OpenTools/internal/tasks"
	"github.com/cn-maul/OpenTools/sdk/tool"
)

// stubTool is an in-process tool for application-layer tests.
type stubTool struct {
	def tool.Definition
}

func (s stubTool) Definition() tool.Definition { return s.def }

func (s stubTool) Run(ctx context.Context, input tool.Input, env tool.Environment, report tool.Reporter) (tool.Result, error) {
	return tool.Result{Summary: "ok"}, nil
}

func newTestService(t *testing.T, def tool.Definition) (*Service, *tasks.Manager) {
	t.Helper()
	registry := catalog.New()
	if err := registry.Add(stubTool{def: def}); err != nil {
		t.Fatal(err)
	}
	store := newFailingStore(t)
	jobs, err := tasks.NewManager(context.Background(), t.TempDir(), t.TempDir(), store, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = jobs.Close() })
	_ = plugins.Manager{}
	return New(registry, nil, jobs), jobs
}

func TestStartRedactsSecretInput(t *testing.T) {
	def := tool.Definition{
		ID:       "app.secret",
		Name:     "测试",
		Summary:  "测试",
		Category: "测试",
		Version:  "1.0.0",
		Input: tool.Object(map[string]*tool.Schema{
			"token": tool.String("令牌", "").WithSecret(),
		}, "token"),
	}
	service, _ := newTestService(t, def)

	job, err := service.Start(context.Background(), "app.secret", map[string]any{"token": "super-secret"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if job.Input["token"] != tool.RedactedPlaceholder {
		t.Fatalf("job input not redacted: %v", job.Input)
	}
}
