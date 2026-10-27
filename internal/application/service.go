// Package application contains the use cases shared by the desktop UI and
// the CLI. It knows about tools and jobs, but nothing about MyGo or SQLite.
package application

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"opentools/internal/catalog"
	"opentools/internal/plugins"
	"opentools/internal/tasks"
	"opentools/pkg/tool"
)

// Service is the application boundary for every transport.
type Service struct {
	catalog *catalog.Registry
	plugins *plugins.Manager
	jobs    *tasks.Manager
}

// New returns an application service.
func New(catalog *catalog.Registry, plugins *plugins.Manager, jobs *tasks.Manager) *Service {
	return &Service{catalog: catalog, plugins: plugins, jobs: jobs}
}

// Tools lists the installed tools.
func (s *Service) Tools() []tool.Definition {
	return s.catalog.Definitions()
}

// Tool returns one definition.
func (s *Service) Tool(id string) (tool.Definition, error) {
	t, ok := s.catalog.Get(id)
	if !ok {
		return tool.Definition{}, fmt.Errorf("tool %q is not installed", id)
	}
	return t.Definition(), nil
}

// Start validates input and starts a detached job.
func (s *Service) Start(ctx context.Context, toolID string, values map[string]any) (tasks.Job, error) {
	t, ok := s.catalog.Get(strings.TrimSpace(toolID))
	if !ok {
		return tasks.Job{}, fmt.Errorf("tool %q is not installed", toolID)
	}
	def := t.Definition()
	input, err := tool.Prepare(def.Input, values)
	if err != nil {
		return tasks.Job{}, err
	}
	job, err := s.jobs.Start(ctx, t, input)
	if err == nil {
		s.jobs.TightenArtifactPermissions(job.ID)
	}
	return job, err
}

// Get returns a job snapshot.
func (s *Service) Get(ctx context.Context, id string) (tasks.Job, error) {
	return s.jobs.Get(ctx, id)
}

// List returns recent jobs.
func (s *Service) List(ctx context.Context, limit int) ([]tasks.Job, error) {
	return s.jobs.List(ctx, limit)
}

// Cancel requests cancellation of a running job.
func (s *Service) Cancel(ctx context.Context, id string) (tasks.Job, error) {
	return s.jobs.Cancel(ctx, id)
}

// Subscribe streams job updates.
func (s *Service) Subscribe() (<-chan tasks.Event, func()) {
	return s.jobs.Subscribe()
}

// Artifact returns the validated absolute path of one result artifact.
func (s *Service) Artifact(ctx context.Context, jobID, name string) (string, error) {
	job, err := s.jobs.Get(ctx, jobID)
	if err != nil {
		return "", err
	}
	if job.Result == nil {
		return "", fmt.Errorf("job %s has no result", jobID)
	}
	for _, artifact := range job.Result.Artifacts {
		if artifact.Name == name || filepath.Base(artifact.Path) == name {
			if artifact.Path == "" {
				return "", fmt.Errorf("artifact %q has no path", name)
			}
			return artifact.Path, nil
		}
	}
	return "", fmt.Errorf("artifact %q was not found", name)
}

// PluginStore returns the last known package catalog.
func (s *Service) PluginStore() tool.StoreSnapshot {
	return s.plugins.Snapshot()
}

// PluginStoreURL returns the user-configured catalog sources.
func (s *Service) PluginStoreURL() string {
	return s.plugins.StoreURL()
}

// SavePluginStoreURL persists catalog sources and refreshes the store.
func (s *Service) SavePluginStoreURL(ctx context.Context, value string) (tool.StoreSnapshot, error) {
	if err := s.plugins.SetStoreURL(value); err != nil {
		return s.plugins.Snapshot(), err
	}
	return s.RefreshPluginStore(ctx)
}

// RefreshPluginStore fetches configured repositories without disabling
// already-installed tools when the network is unavailable.
func (s *Service) RefreshPluginStore(ctx context.Context) (tool.StoreSnapshot, error) {
	snapshot, err := s.plugins.Refresh(ctx)
	if err != nil {
		return snapshot, err
	}
	if err := s.reloadPlugins(); err != nil {
		return snapshot, err
	}
	return s.plugins.Snapshot(), nil
}

// InstallPlugin installs one package from the current store snapshot.
func (s *Service) InstallPlugin(ctx context.Context, id string) (tool.StoreSnapshot, error) {
	id = strings.TrimSpace(id)
	var selected *tool.Package
	for _, item := range s.plugins.Snapshot().Packages {
		if item.Definition.ID == id {
			copy := item
			selected = &copy
			break
		}
	}
	if selected == nil {
		return s.plugins.Snapshot(), fmt.Errorf("商店中没有工具 %q", id)
	}
	if err := s.plugins.Install(ctx, *selected); err != nil {
		return s.plugins.Snapshot(), err
	}
	if err := s.reloadPlugins(); err != nil {
		return s.plugins.Snapshot(), err
	}
	return s.plugins.Snapshot(), nil
}

// UninstallPlugin removes a downloaded tool.
func (s *Service) UninstallPlugin(ctx context.Context, id string) (tool.StoreSnapshot, error) {
	id = strings.TrimSpace(id)
	if _, ok := s.plugins.Manifest(id); !ok {
		return s.plugins.Snapshot(), fmt.Errorf("工具 %q 未安装", id)
	}
	if err := s.plugins.Uninstall(id); err != nil {
		return s.plugins.Snapshot(), err
	}
	if err := s.reloadPlugins(); err != nil {
		return s.plugins.Snapshot(), err
	}
	return s.plugins.Snapshot(), nil
}

func (s *Service) reloadPlugins() error {
	return s.catalog.Replace(s.plugins.Tools())
}
