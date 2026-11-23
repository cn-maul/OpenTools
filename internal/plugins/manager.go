package plugins

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cn-maul/OpenTools/sdk/registry"
	"github.com/cn-maul/OpenTools/sdk/tool"
	"github.com/cn-maul/OpenTools/sdk/tool/pluginproto"
)

// Manager owns installed packages and talks to a remote package catalog.
// The host has no compiled-in business tools.
//
// The heavy lifting is split across collaborators:
//   - inventory: scans and validates installed packages on disk;
//   - storeClient: fetches catalogs and archives from configured sources;
//   - installer: verifies and atomically installs a downloaded package.
//
// Manager itself keeps the merged store snapshot and hands tool adapters to
// the application layer.
type Manager struct {
	inv       *inventory
	installer *installer
	store     *storeClient

	mu      syncRWMutex
	sources []string
	// catalog holds only remote packages. Installed state is derived from
	// the inventory at read time, so there is a single source of truth.
	catalog registry.StoreSnapshot
	// snapshotMu guards catalog writes; reads clone under mu.
	services tool.HostServices
}

// NewManager scans the plugin directory and returns a manager.
func NewManager(root, dataDir string) (*Manager, error) {
	for _, dir := range []string{root, dataDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("create plugin directory: %w", err)
		}
	}
	inv := newInventory(root)
	fetcher := newStoreClient(filepath.Join(dataDir, "plugin-sources.json"))
	m := &Manager{
		inv:       inv,
		installer: newInstaller(root, fetcher),
		store:     fetcher,
		sources:   readSources(fetcher.sourcesPath),
	}
	inv.rescan()
	return m, nil
}

// SetHostServices installs the dispatcher used for tool-initiated calls.
// It must be set before tools run; later calls pick it up on the next
// install, refresh or reload.
func (m *Manager) SetHostServices(services tool.HostServices) {
	m.mu.Lock()
	m.services = services
	m.mu.Unlock()
}

// HostServices returns the current dispatcher, if any.
func (m *Manager) HostServices() tool.HostServices {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.services
}

// Tools returns the installed tool adapters.
func (m *Manager) Tools() []tool.Tool {
	loaded, _ := m.inv.snapshot()
	ids := make([]string, 0, len(loaded))
	for id := range loaded {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	m.mu.RLock()
	services := m.services
	m.mu.RUnlock()
	tools := make([]tool.Tool, 0, len(ids))
	for _, id := range ids {
		item := loaded[id]
		tools = append(tools, NewExternalTool(
			item.manifest.Definition,
			item.exe,
			item.manifest.ProtocolVersion,
			services,
		))
	}
	return tools
}

// Reload re-scans the installation directory.
func (m *Manager) Reload() error {
	m.inv.rescan()
	return nil
}

// Snapshot returns the last catalog result merged with installed state.
func (m *Manager) Snapshot() registry.StoreSnapshot {
	m.mu.RLock()
	snapshot := cloneSnapshot(m.catalog)
	m.mu.RUnlock()
	loaded, loadWarnings := m.inv.snapshot()
	installed := make([]loadedPackage, 0, len(loaded))
	for _, item := range loaded {
		installed = append(installed, item)
	}
	snapshot.Packages = mergeInstalled(snapshot.Packages, installed)
	snapshot.Warnings = append(snapshot.Warnings, loadWarnings...)
	return snapshot
}

// StoreURL returns the configured sources, joined in the same format accepted
// by SetStoreURL. An empty value means the local default catalog is used.
func (m *Manager) StoreURL() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return strings.Join(m.sources, ";")
}

// SetStoreURL persists the catalog sources used by the store. Multiple
// sources are separated by semicolons; an empty value restores the default
// source discovery.
func (m *Manager) SetStoreURL(raw string) error {
	sources := splitSources(raw)
	for _, source := range sources {
		if err := validateSource(source); err != nil {
			return err
		}
	}
	if err := writeSources(m.store.sourcesPath, sources); err != nil {
		return fmt.Errorf("保存工具商店地址失败：%w", err)
	}
	m.mu.Lock()
	m.sources = sources
	m.mu.Unlock()
	return nil
}

// Refresh fetches every configured catalog and returns the merged snapshot.
// A failed source is reported as a warning while other sources and installed
// packages remain usable.
func (m *Manager) Refresh(ctx context.Context) (registry.StoreSnapshot, error) {
	sources := m.catalogSources()
	if len(sources) == 0 {
		snapshot := m.Snapshot()
		snapshot.FetchedAt = time.Now().UTC()
		return snapshot, nil
	}
	merged := make(map[string]registry.Package)
	warnings := []string{}
	for _, source := range sources {
		packages, err := m.store.fetchCatalog(ctx, source)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s：%v", source, err))
			continue
		}
		for _, item := range packages {
			if item.Definition.ID == "" {
				continue
			}
			item.Compatible = compatible(item, item.Definition.Version)
			merged[item.Definition.ID] = item
		}
	}
	loaded, loadWarnings := m.inv.snapshot()
	installed := make([]loadedPackage, 0, len(loaded))
	for _, item := range loaded {
		installed = append(installed, item)
	}
	warnings = append(warnings, loadWarnings...)
	packages := make([]registry.Package, 0, len(merged))
	for _, item := range merged {
		packages = append(packages, item)
	}
	snapshot := registry.StoreSnapshot{
		Packages:  packages,
		Sources:   append([]string(nil), sources...),
		Warnings:  warnings,
		FetchedAt: time.Now().UTC(),
	}
	m.mu.Lock()
	m.catalog = snapshot
	m.mu.Unlock()
	snapshot.Packages = mergeInstalled(snapshot.Packages, installed)
	return cloneSnapshot(snapshot), nil
}

// Install downloads, verifies and atomically installs one package.
func (m *Manager) Install(ctx context.Context, item registry.Package) error {
	installed, err := m.installer.Install(ctx, item)
	if err != nil {
		return err
	}
	m.inv.put(installed)
	return nil
}

// Uninstall removes an installed package. The application layer rejects a
// tool with an active job before calling this method.
func (m *Manager) Uninstall(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("工具 ID 为空")
	}
	item, ok := m.inv.get(id)
	if !ok {
		return fmt.Errorf("工具 %s 未安装", id)
	}
	trash := item.dir + ".remove-" + fmt.Sprint(time.Now().UnixNano())
	if err := os.Rename(item.dir, trash); err != nil {
		return fmt.Errorf("移除工具失败：%w", err)
	}
	m.inv.forget(id)
	if err := os.RemoveAll(trash); err != nil {
		return fmt.Errorf("工具已停用，但临时文件清理失败：%w", err)
	}
	return nil
}

// Manifest returns the installed package manifest by ID.
func (m *Manager) Manifest(id string) (pluginproto.Manifest, bool) {
	item, ok := m.inv.get(id)
	return item.manifest, ok
}

func (m *Manager) catalogSources() []string {
	m.mu.RLock()
	sources := append([]string(nil), m.sources...)
	m.mu.RUnlock()
	if len(sources) == 0 {
		return m.store.sources()
	}
	return sources
}

func mergeInstalled(source []registry.Package, loaded []loadedPackage) []registry.Package {
	packages := append([]registry.Package(nil), source...)
	existing := make(map[string]bool, len(packages))
	for i := range packages {
		existing[packages[i].Definition.ID] = true
	}
	for _, item := range loaded {
		id := item.manifest.Definition.ID
		if existing[id] {
			for i := range packages {
				if packages[i].Definition.ID == id {
					packages[i].Installed = true
					packages[i].InstalledVersion = item.manifest.Definition.Version
					packages[i].Compatible = compatible(packages[i], packages[i].Definition.Version)
					packages[i].UpdateAvailable =
						compareVersion(packages[i].Definition.Version, item.manifest.Definition.Version) > 0
				}
			}
			continue
		}
		entry := registry.Package{
			Definition:       item.manifest.Definition,
			Platform:         item.manifest.Platform,
			ProtocolVersion:  item.manifest.ProtocolVersion,
			Author:           item.manifest.Author,
			Homepage:         item.manifest.Homepage,
			License:          item.manifest.License,
			Compatible:       true,
			Installed:        true,
			InstalledVersion: item.manifest.Definition.Version,
		}
		entry.Compatible = compatible(entry, entry.Definition.Version)
		packages = append(packages, entry)
		existing[id] = true
	}
	sort.Slice(packages, func(i, j int) bool {
		if packages[i].Definition.Category != packages[j].Definition.Category {
			return packages[i].Definition.Category < packages[j].Definition.Category
		}
		return packages[i].Definition.Name < packages[j].Definition.Name
	})
	return packages
}

func cloneSnapshot(source registry.StoreSnapshot) registry.StoreSnapshot {
	out := source
	out.Packages = append([]registry.Package(nil), source.Packages...)
	out.Sources = append([]string(nil), source.Sources...)
	out.Warnings = append([]string(nil), source.Warnings...)
	return out
}

// writeSources persists the catalog source list atomically, keeping a
// backup so a crash mid-write cannot lose the configured sources.
func writeSources(path string, sources []string) error {
	document := struct {
		Sources []string `json:"sources"`
	}{
		Sources: append([]string{}, sources...),
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".plugin-sources-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(mustJSON(document)); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		backup := path + ".bak-" + fmt.Sprint(time.Now().UnixNano())
		if err := os.Rename(path, backup); err != nil {
			return err
		}
		if err := os.Rename(tempPath, path); err != nil {
			_ = os.Rename(backup, path)
			return err
		}
		return os.Remove(backup)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(tempPath, path)
}
