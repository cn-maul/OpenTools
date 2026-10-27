// Package desktop is the only application package that knows about MyGo.
// It adapts the transport-neutral application service to the page.
package desktop

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/egoist/mygo"

	"opentools/internal/appdata"
	"opentools/internal/application"
	"opentools/internal/tasks"
	"opentools/pkg/tool"
)

// Jobs is bound to the frontend as the Jobs service.
type Jobs struct {
	app     *application.Service
	version string
}

// OpenFileFilter is one native file-picker filter.
type OpenFileFilter struct {
	Description string   `json:"description"`
	Extensions  []string `json:"extensions"`
}

// NewJobs returns the desktop adapter.
func NewJobs(app *application.Service, version string) *Jobs {
	return &Jobs{app: app, version: version}
}

// Tools returns every installed tool definition.
func (s *Jobs) Tools() []tool.Definition {
	return s.app.Tools()
}

// AppVersion returns the running host version.
func (s *Jobs) AppVersion() string {
	return s.version
}

// PluginStore returns installed and downloadable tools.
func (s *Jobs) PluginStore() tool.StoreSnapshot {
	return s.app.PluginStore()
}

// PluginStoreURL returns the configured catalog sources.
func (s *Jobs) PluginStoreURL() string {
	return s.app.PluginStoreURL()
}

// SavePluginStoreURL persists catalog sources and refreshes the store.
func (s *Jobs) SavePluginStoreURL(ctx context.Context, value string) (tool.StoreSnapshot, error) {
	return s.app.SavePluginStoreURL(ctx, value)
}

// RefreshPluginStore downloads the latest package catalogs.
func (s *Jobs) RefreshPluginStore(ctx context.Context) (tool.StoreSnapshot, error) {
	return s.app.RefreshPluginStore(ctx)
}

// InstallPlugin downloads and installs one tool package.
func (s *Jobs) InstallPlugin(ctx context.Context, id string) (tool.StoreSnapshot, error) {
	return s.app.InstallPlugin(ctx, id)
}

// UninstallPlugin removes one installed tool package.
func (s *Jobs) UninstallPlugin(ctx context.Context, id string) (tool.StoreSnapshot, error) {
	return s.app.UninstallPlugin(ctx, id)
}

// Start creates a detached job. Input is opaque JSON at this boundary so
// adding a field to a tool never requires regenerating a union in the host.
func (s *Jobs) Start(ctx context.Context, toolID string, input map[string]any) (tasks.Job, error) {
	return s.app.Start(ctx, toolID, input)
}

// Get returns a job snapshot.
func (s *Jobs) Get(ctx context.Context, id string) (tasks.Job, error) {
	return s.app.Get(ctx, id)
}

// List returns recent jobs.
func (s *Jobs) List(ctx context.Context, limit int) ([]tasks.Job, error) {
	return s.app.List(ctx, limit)
}

// Cancel requests cancellation.
func (s *Jobs) Cancel(ctx context.Context, id string) (tasks.Job, error) {
	return s.app.Cancel(ctx, id)
}

// RevealArtifact opens a job artifact in the file manager.
func (s *Jobs) RevealArtifact(ctx context.Context, jobID, name string) error {
	path, err := s.app.Artifact(ctx, jobID, name)
	if err != nil {
		return err
	}
	mygo.Shell.ShowItemInFolder(path)
	return nil
}

// CopyArtifactPath writes an artifact path to the clipboard.
func (s *Jobs) CopyArtifactPath(ctx context.Context, jobID, name string) error {
	path, err := s.app.Artifact(ctx, jobID, name)
	if err != nil {
		return err
	}
	mygo.Clipboard.WriteText(path)
	return nil
}

// ChooseDirectory opens the native folder picker.
func (s *Jobs) ChooseDirectory(ctx context.Context) (string, error) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent:      mygo.CallerWindow(ctx),
		Title:       "选择文件夹",
		Directory:   true,
		ButtonLabel: "选择",
	})
	if err != nil || len(paths) == 0 {
		return "", err
	}
	return paths[0], nil
}

// ChooseFile opens the native file picker and returns its path.
func (s *Jobs) ChooseFile(ctx context.Context, filters []OpenFileFilter) (string, error) {
	options := make([]mygo.FileFilter, 0, len(filters))
	for _, filter := range filters {
		options = append(options, mygo.FileFilter{
			Name:       filter.Description,
			Extensions: filter.Extensions,
		})
	}
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent:      mygo.CallerWindow(ctx),
		Title:       "选择文件",
		ButtonLabel: "选择",
		Filters:     options,
	})
	if err != nil || len(paths) == 0 {
		return "", err
	}
	return paths[0], nil
}

// ReadTextFile loads a text file selected by the user into the editor.
func (s *Jobs) ReadTextFile(ctx context.Context, path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.Size() > 8<<20 {
		return "", fmt.Errorf("文件大于 8 MiB")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if strings.IndexByte(string(data), 0) >= 0 {
		return "", fmt.Errorf("文件不是文本格式")
	}
	return string(data), nil
}

// DataDirectory returns the folder holding history and artifacts.
func (s *Jobs) DataDirectory() (string, error) {
	return appdata.Dir()
}

// JobEvent is broadcast to pages whenever a job changes.
type JobEvent struct {
	Kind string    `json:"kind"`
	Job  tasks.Job `json:"job"`
}

// JobsChanged carries durable task updates to every window.
var JobsChanged = mygo.NewEvent[JobEvent]("jobs:changed")

// StartEventForwarder broadcasts task events to pages.
func StartEventForwarder(ctx context.Context, app *application.Service) {
	events, unsubscribe := app.Subscribe()
	defer unsubscribe()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			_ = JobsChanged.Broadcast(JobEvent{Kind: event.Kind, Job: event.Job})
		}
	}
}
