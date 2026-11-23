// Package appkit assembles the application without importing a GUI toolkit.
package appkit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/cn-maul/OpenTools/internal/application"
	"github.com/cn-maul/OpenTools/internal/catalog"
	"github.com/cn-maul/OpenTools/internal/hostservice"
	"github.com/cn-maul/OpenTools/internal/plugins"
	"github.com/cn-maul/OpenTools/internal/storage/sqlite"
	"github.com/cn-maul/OpenTools/internal/tasks"
	"github.com/cn-maul/OpenTools/sdk/tool"
)

// App is the assembled application and the resources that outlive each UI
// call. Transports only use Service; the remaining components are wiring
// details owned by this package.
type App struct {
	Service *application.Service

	store   *sqlite.Store
	catalog *catalog.Registry
	plugins *plugins.Manager
	jobs    *tasks.Manager
}

// Options control assembly for one transport.
type Options struct {
	// UI provides the native surfaces tools may request through host
	// services. A nil UI means a headless assembly (the CLI): secrets and
	// config still work, dialogs, notifications and elevation are refused.
	UI hostservice.UI
	// ClearHistoryOnClose deletes job records and artifacts when the process
	// exits. Only the desktop shell owns the data directory this way; CLI
	// queries share it with a running desktop and must preserve history.
	ClearHistoryOnClose bool
}

// Open assembles OpenTools using dataDir for the database and job artifacts.
func Open(ctx context.Context, dataDir string, logger *slog.Logger, options Options) (*App, error) {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}

	store, err := sqlite.Open(ctx, filepath.Join(dataDir, "opentools.db"))
	if err != nil {
		return nil, err
	}
	closeOnError := func(err error) (*App, error) {
		_ = store.Close()
		return nil, err
	}

	registry := catalog.New()
	pluginManager, err := plugins.NewManager(
		filepath.Join(dataDir, "plugins"),
		dataDir,
	)
	if err != nil {
		return closeOnError(err)
	}

	services := hostservice.New(hostservice.Options{
		DataDir: dataDir,
		UI:      options.UI,
		Logger:  logger,
		Required: func(toolID string) ([]tool.Capability, bool) {
			manifest, ok := pluginManager.Manifest(toolID)
			if !ok {
				return nil, false
			}
			return manifest.Definition.Requires, true
		},
	})
	pluginManager.SetHostServices(services)

	if err := registry.Replace(pluginManager.Tools()); err != nil {
		return closeOnError(err)
	}

	jobs, err := tasks.NewManager(
		ctx,
		filepath.Join(dataDir, "jobs"),
		dataDir,
		store,
		logger,
		options.ClearHistoryOnClose,
	)
	if err != nil {
		return closeOnError(err)
	}
	return &App{
		Service: application.New(registry, pluginManager, jobs),
		store:   store,
		catalog: registry,
		plugins: pluginManager,
		jobs:    jobs,
	}, nil
}

// Close releases durable resources.
func (a *App) Close() error {
	return errors.Join(a.jobs.Close(), a.store.Close())
}
