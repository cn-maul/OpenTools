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

	"opentools/internal/application"
	"opentools/internal/catalog"
	"opentools/internal/plugins"
	"opentools/internal/storage/sqlite"
	"opentools/internal/tasks"
)

// App is the assembled application and the resources that outlive each UI
// call.
type App struct {
	Service *application.Service
	Store   *sqlite.Store
	Catalog *catalog.Registry
	Plugins *plugins.Manager
	jobs    *tasks.Manager
}

// Open assembles OpenTools using dataDir for the database and job artifacts.
func Open(ctx context.Context, dataDir string, logger *slog.Logger) (*App, error) {
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
	if err := registry.Replace(pluginManager.Tools()); err != nil {
		return closeOnError(err)
	}

	jobs, err := tasks.NewManager(ctx, filepath.Join(dataDir, "jobs"), store, logger)
	if err != nil {
		return closeOnError(err)
	}
	return &App{
		Service: application.New(registry, pluginManager, jobs),
		Store:   store,
		Catalog: registry,
		Plugins: pluginManager,
		jobs:    jobs,
	}, nil
}

// Close releases durable resources.
func (a *App) Close() error {
	return errors.Join(a.jobs.Close(), a.Store.Close())
}
