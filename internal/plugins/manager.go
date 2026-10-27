package plugins

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"opentools/pkg/tool"
	"opentools/pkg/tool/pluginproto"
)

const (
	maxCatalogSize  = 4 << 20
	maxArchiveSize  = 256 << 20
	maxArchiveFiles = 4096
)

// Manager owns installed packages and talks to a remote package catalog.
// The host has no compiled-in business tools.
type Manager struct {
	root        string
	dataDir     string
	sourcesPath string

	mu       sync.RWMutex
	loaded   map[string]loadedPackage
	sources  []string
	client   *http.Client
	catalog  tool.StoreSnapshot
	lastLoad error
}

type loadedPackage struct {
	manifest pluginproto.Manifest
	dir      string
	exe      string
}

// NewManager scans the plugin directory and returns a manager.
func NewManager(root, dataDir string) (*Manager, error) {
	for _, dir := range []string{root, dataDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("create plugin directory: %w", err)
		}
	}
	m := &Manager{
		root:        root,
		dataDir:     dataDir,
		sourcesPath: filepath.Join(dataDir, "plugin-sources.json"),
		sources:     readSources(filepath.Join(dataDir, "plugin-sources.json")),
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
	if err := m.reload(); err != nil {
		return nil, err
	}
	return m, nil
}

// Tools returns the installed tool adapters.
func (m *Manager) Tools() []tool.Tool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.loaded))
	for id := range m.loaded {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	tools := make([]tool.Tool, 0, len(ids))
	for _, id := range ids {
		item := m.loaded[id]
		tools = append(tools, NewExternalTool(
			item.manifest.Definition,
			item.exe,
			item.manifest.ProtocolVersion,
		))
	}
	return tools
}

// Reload re-scans the installation directory.
func (m *Manager) Reload() error {
	return m.reload()
}

// Snapshot returns the last catalog result merged with installed state.
func (m *Manager) Snapshot() tool.StoreSnapshot {
	m.mu.RLock()
	snapshot := cloneSnapshot(m.catalog)
	installed := make([]loadedPackage, 0, len(m.loaded))
	for _, item := range m.loaded {
		installed = append(installed, item)
	}
	m.mu.RUnlock()
	snapshot.Packages = mergeInstalled(snapshot.Packages, installed)
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
	if err := writeSources(m.sourcesPath, sources); err != nil {
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
func (m *Manager) Refresh(ctx context.Context) (tool.StoreSnapshot, error) {
	sources := m.catalogSources()
	if len(sources) == 0 {
		snapshot := m.Snapshot()
		snapshot.FetchedAt = time.Now().UTC()
		return snapshot, nil
	}
	merged := make(map[string]tool.Package)
	warnings := []string{}
	for _, source := range sources {
		packages, err := m.fetchCatalog(ctx, source)
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
	m.mu.RLock()
	installed := make([]loadedPackage, 0, len(m.loaded))
	for _, item := range m.loaded {
		installed = append(installed, item)
	}
	m.mu.RUnlock()
	packages := make([]tool.Package, 0, len(merged))
	for _, item := range merged {
		packages = append(packages, item)
	}
	packages = mergeInstalled(packages, installed)
	snapshot := tool.StoreSnapshot{
		Packages:  packages,
		Sources:   append([]string(nil), sources...),
		Warnings:  warnings,
		FetchedAt: time.Now().UTC(),
	}
	m.mu.Lock()
	m.catalog = snapshot
	m.mu.Unlock()
	return cloneSnapshot(snapshot), nil
}

// Install downloads, verifies and atomically installs one package.
func (m *Manager) Install(ctx context.Context, item tool.Package) error {
	if strings.TrimSpace(item.DownloadURL) == "" {
		return errors.New("安装包缺少下载地址")
	}
	if !compatible(item, item.Definition.Version) {
		return fmt.Errorf("工具 %s 不支持当前平台或协议", item.Definition.ID)
	}
	data, err := m.fetchBytes(ctx, item.DownloadURL, maxArchiveSize)
	if err != nil {
		return fmt.Errorf("下载工具失败：%w", err)
	}
	if int64(len(data)) > maxArchiveSize {
		return fmt.Errorf("安装包超过 %d MiB", maxArchiveSize>>20)
	}
	sum := sha256.Sum256(data)
	actual := hex.EncodeToString(sum[:])
	if item.SHA256 == "" {
		return errors.New("商店条目缺少 SHA256")
	}
	if !strings.EqualFold(actual, item.SHA256) {
		return fmt.Errorf("安装包 SHA256 校验失败")
	}
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("安装包不是有效 ZIP：%w", err)
	}
	if len(archive.File) > maxArchiveFiles {
		return errors.New("安装包文件数量异常")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	stage, err := os.MkdirTemp(m.root, ".install-*")
	if err != nil {
		return fmt.Errorf("创建安装暂存目录失败：%w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(stage)
		}
	}()
	manifest, executable, err := extractArchive(archive, stage)
	if err != nil {
		return err
	}
	if strings.TrimSpace(manifest.Definition.ID) == "" {
		return errors.New("安装包清单缺少工具 ID")
	}
	if manifest.Definition.ID != item.Definition.ID {
		return fmt.Errorf("安装包 ID %q 与商店条目 %q 不一致", manifest.Definition.ID, item.Definition.ID)
	}
	if err := os.WriteFile(
		filepath.Join(stage, "manifest.json"),
		mustJSON(manifest),
		0o600,
	); err != nil {
		return fmt.Errorf("写入插件清单失败：%w", err)
	}
	if err := validateExecutable(executable, manifest); err != nil {
		return err
	}
	destination := filepath.Join(m.root, safeID(manifest.Definition.ID))
	if _, err := os.Stat(destination); err == nil {
		// 用带时间戳的唯一备份名：固定的 .old 一旦因文件占用清理失败，
		// 会一直卡住后续更新。rename 失败时短暂重试，等杀毒软件放开新文件。
		backup := destination + ".old-" + fmt.Sprint(time.Now().UnixNano())
		var renameErr error
		for attempt := 0; attempt < 5; attempt++ {
			renameErr = os.Rename(destination, backup)
			if renameErr == nil {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		if renameErr != nil {
			return fmt.Errorf("替换已安装工具失败：%w", renameErr)
		}
		// 新写入的 exe 短时间内可能被杀毒软件占用，后台重试清理，
		// 不阻塞安装完成；即使残留也只是无害的 .old-* 目录。
		defer func() {
			go func() {
				for attempt := 0; attempt < 10; attempt++ {
					if os.RemoveAll(backup) == nil {
						return
					}
					time.Sleep(time.Second)
				}
			}()
		}()
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("检查已安装工具失败：%w", err)
	}
	if err := os.Rename(stage, destination); err != nil {
		return fmt.Errorf("启用新工具失败：%w", err)
	}
	cleanup = false
	installed := loadedPackage{
		manifest: manifest,
		dir:      destination,
		exe:      filepath.Join(destination, filepath.FromSlash(manifest.Executable)),
	}
	m.loaded[manifest.Definition.ID] = installed
	m.updateInstalledCatalogLocked()
	return nil
}

// Uninstall removes an installed package. Callers should ensure it has no
// active jobs.
func (m *Manager) Uninstall(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("工具 ID 为空")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	item, ok := m.loaded[id]
	if !ok {
		return fmt.Errorf("工具 %s 未安装", id)
	}
	trash := item.dir + ".remove-" + fmt.Sprint(time.Now().UnixNano())
	if err := os.Rename(item.dir, trash); err != nil {
		return fmt.Errorf("移除工具失败：%w", err)
	}
	delete(m.loaded, id)
	m.updateInstalledCatalogLocked()
	if err := os.RemoveAll(trash); err != nil {
		return fmt.Errorf("工具已停用，但临时文件清理失败：%w", err)
	}
	return nil
}

// Index returns the installed package manifest by ID.
func (m *Manager) Manifest(id string) (pluginproto.Manifest, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	item, ok := m.loaded[id]
	return item.manifest, ok
}

func (m *Manager) reload() error {
	manifestPaths, err := filepath.Glob(filepath.Join(m.root, "*", "manifest.json"))
	if err != nil {
		return fmt.Errorf("scan plugins: %w", err)
	}
	next := make(map[string]loadedPackage, len(manifestPaths))
	var problems []string
	for _, path := range manifestPaths {
		manifest, err := readManifest(path)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		dir := filepath.Dir(path)
		if filepath.Base(dir) != safeID(manifest.Definition.ID) {
			problems = append(problems, fmt.Sprintf("%s: 目录与工具 ID 不一致", path))
			continue
		}
		exe := filepath.Join(dir, filepath.FromSlash(manifest.Executable))
		if err := validateExecutable(exe, manifest); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		if _, duplicate := next[manifest.Definition.ID]; duplicate {
			problems = append(problems, fmt.Sprintf("%s: 重复的工具 ID", path))
			continue
		}
		next[manifest.Definition.ID] = loadedPackage{manifest: manifest, dir: dir, exe: exe}
	}
	m.mu.Lock()
	m.loaded = next
	if len(problems) > 0 {
		m.lastLoad = errors.New(strings.Join(problems, "; "))
	} else {
		m.lastLoad = nil
	}
	if len(problems) > 0 {
		m.catalog.Warnings = append(m.catalog.Warnings, problems...)
	}
	m.updateInstalledCatalogLocked()
	m.mu.Unlock()
	return nil
}

func (m *Manager) catalogSources() []string {
	m.mu.RLock()
	sources := append([]string(nil), m.sources...)
	m.mu.RUnlock()
	if len(sources) == 0 {
		if value := strings.TrimSpace(os.Getenv("OPENTOOLS_CATALOG_URL")); value != "" {
			return splitSources(value)
		}
		local := discoverLocalCatalogs()
		if len(local) > 0 {
			return local
		}
		return []string{defaultCatalogURL}
	}
	return sources
}

func (m *Manager) fetchCatalog(ctx context.Context, source string) ([]tool.Package, error) {
	parsed, err := url.Parse(source)
	if err != nil {
		return nil, err
	}
	switch {
	case filepath.IsAbs(source) || strings.HasPrefix(source, `\\`):
		data, err := os.ReadFile(source)
		if err != nil {
			return nil, err
		}
		if int64(len(data)) > maxCatalogSize {
			return nil, errors.New("目录文件过大")
		}
		return parseCatalog(data, source)
	case parsed.Scheme == "":
		absolute, err := filepath.Abs(source)
		if err != nil {
			return nil, err
		}
		data, err := os.ReadFile(absolute)
		if err != nil {
			return nil, err
		}
		if int64(len(data)) > maxCatalogSize {
			return nil, errors.New("目录文件过大")
		}
		return parseCatalog(data, absolute)
	default:
		return m.fetchCatalogURL(ctx, source, parsed)
	}
}

func (m *Manager) fetchCatalogURL(ctx context.Context, source string, parsed *url.URL) ([]tool.Package, error) {
	switch parsed.Scheme {
	case "", "file", "http", "https":
	default:
		return nil, fmt.Errorf("不支持的目录协议 %q", parsed.Scheme)
	}
	data, err := m.fetchBytes(ctx, source, maxCatalogSize)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxCatalogSize {
		return nil, errors.New("目录文件过大")
	}
	var catalog tool.PackageCatalog
	if err := json.Unmarshal(data, &catalog); err != nil {
		return nil, fmt.Errorf("解析目录失败：%w", err)
	}
	if catalog.SchemaVersion != 1 {
		return nil, fmt.Errorf("不支持目录版本 %d", catalog.SchemaVersion)
	}
	base := parsed
	if base.Scheme == "" {
		absolute, absErr := filepath.Abs(source)
		if absErr != nil {
			return nil, absErr
		}
		base, _ = url.Parse(fileURI(absolute))
	}
	for i := range catalog.Tools {
		reference, parseErr := url.Parse(catalog.Tools[i].DownloadURL)
		if parseErr != nil {
			return nil, fmt.Errorf("工具 %s 的下载地址无效：%w", catalog.Tools[i].Definition.ID, parseErr)
		}
		catalog.Tools[i].DownloadURL = base.ResolveReference(reference).String()
	}
	return catalog.Tools, nil
}

func parseCatalog(data []byte, source string) ([]tool.Package, error) {
	var catalog tool.PackageCatalog
	if err := json.Unmarshal(data, &catalog); err != nil {
		return nil, fmt.Errorf("解析目录失败：%w", err)
	}
	if catalog.SchemaVersion != 1 {
		return nil, fmt.Errorf("不支持目录版本 %d", catalog.SchemaVersion)
	}
	baseDir := filepath.Dir(source)
	for i := range catalog.Tools {
		reference := catalog.Tools[i].DownloadURL
		if !filepath.IsAbs(reference) && !strings.HasPrefix(reference, `\\`) {
			catalog.Tools[i].DownloadURL = filepath.Join(baseDir, filepath.FromSlash(reference))
		}
	}
	return catalog.Tools, nil
}

func (m *Manager) fetchBytes(ctx context.Context, source string, limit int64) ([]byte, error) {
	if filepath.IsAbs(source) || strings.HasPrefix(source, `\\`) {
		data, err := os.ReadFile(source)
		if err != nil {
			return nil, err
		}
		if int64(len(data)) > limit {
			return nil, errors.New("文件体积超过限制")
		}
		return data, nil
	}
	parsed, err := url.Parse(source)
	if err != nil {
		return nil, err
	}
	switch parsed.Scheme {
	case "file":
		return os.ReadFile(filePathFromURL(parsed))
	case "http", "https":
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
		if err != nil {
			return nil, err
		}
		request.Header.Set("User-Agent", "OpenTools")
		response, err := m.client.Do(request)
		if err != nil {
			return nil, err
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("HTTP %d", response.StatusCode)
		}
		data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
		if err != nil {
			return nil, err
		}
		if int64(len(data)) > limit {
			return nil, errors.New("文件体积超过限制")
		}
		return data, nil
	case "":
		return os.ReadFile(source)
	default:
		return nil, fmt.Errorf("不支持的下载协议 %q", parsed.Scheme)
	}
}

func (m *Manager) updateInstalledCatalogLocked() {
	installed := make(map[string]string, len(m.loaded))
	for id, item := range m.loaded {
		installed[id] = item.manifest.Definition.Version
	}
	for i := range m.catalog.Packages {
		item := &m.catalog.Packages[i]
		version, ok := installed[item.Definition.ID]
		item.Installed = ok
		item.InstalledVersion = version
		item.UpdateAvailable = ok && compareVersion(item.Definition.Version, version) > 0
	}
	existing := make(map[string]bool, len(m.catalog.Packages))
	for _, item := range m.catalog.Packages {
		existing[item.Definition.ID] = true
	}
	for id, item := range m.loaded {
		if existing[id] {
			continue
		}
		m.catalog.Packages = append(m.catalog.Packages, tool.Package{
			Definition:       item.manifest.Definition,
			Platform:         item.manifest.Platform,
			ProtocolVersion:  item.manifest.ProtocolVersion,
			Author:           item.manifest.Author,
			Homepage:         item.manifest.Homepage,
			License:          item.manifest.License,
			Compatible:       true,
			Installed:        true,
			InstalledVersion: item.manifest.Definition.Version,
		})
	}
	sort.Slice(m.catalog.Packages, func(i, j int) bool {
		if m.catalog.Packages[i].Definition.Category != m.catalog.Packages[j].Definition.Category {
			return m.catalog.Packages[i].Definition.Category < m.catalog.Packages[j].Definition.Category
		}
		return m.catalog.Packages[i].Definition.Name < m.catalog.Packages[j].Definition.Name
	})
}

func mergeInstalled(source []tool.Package, loaded []loadedPackage) []tool.Package {
	packages := append([]tool.Package(nil), source...)
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
		entry := tool.Package{
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

func extractArchive(archive *zip.Reader, stage string) (pluginproto.Manifest, string, error) {
	var manifest pluginproto.Manifest
	manifestFound := false
	executable := ""
	root, err := filepath.Abs(stage)
	if err != nil {
		return manifest, "", err
	}
	for _, entry := range archive.File {
		name := filepath.Clean(filepath.FromSlash(entry.Name))
		if name == "." || name == "" {
			continue
		}
		if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			return manifest, "", fmt.Errorf("安装包含非法路径 %q", entry.Name)
		}
		destination := filepath.Join(root, name)
		rel, err := filepath.Rel(root, destination)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return manifest, "", fmt.Errorf("安装包含越界路径 %q", entry.Name)
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(destination, 0o700); err != nil {
				return manifest, "", err
			}
			continue
		}
		if entry.Mode()&os.ModeSymlink != 0 {
			return manifest, "", fmt.Errorf("安装包不允许符号链接 %q", entry.Name)
		}
		if entry.UncompressedSize64 > 128<<20 {
			return manifest, "", fmt.Errorf("安装包文件 %q 过大", entry.Name)
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return manifest, "", err
		}
		source, err := entry.Open()
		if err != nil {
			return manifest, "", err
		}
		target, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			source.Close()
			return manifest, "", err
		}
		_, copyErr := io.Copy(target, io.LimitReader(source, 128<<20))
		closeErr := target.Close()
		source.Close()
		if copyErr != nil {
			return manifest, "", copyErr
		}
		if closeErr != nil {
			return manifest, "", closeErr
		}
		if name == "manifest.json" {
			data, err := os.ReadFile(destination)
			if err != nil {
				return manifest, "", err
			}
			if err := json.Unmarshal(data, &manifest); err != nil {
				return manifest, "", fmt.Errorf("解析插件清单失败：%w", err)
			}
			manifestFound = true
		}
	}
	if !manifestFound {
		return manifest, "", errors.New("安装包缺少 manifest.json")
	}
	if err := validateManifest(manifest); err != nil {
		return manifest, "", err
	}
	executable = filepath.Join(root, filepath.FromSlash(manifest.Executable))
	return manifest, executable, nil
}

func validateManifest(manifest pluginproto.Manifest) error {
	if manifest.SchemaVersion != 1 {
		return fmt.Errorf("不支持插件清单版本 %d", manifest.SchemaVersion)
	}
	if !pluginproto.SupportsVersion(manifest.ProtocolVersion) {
		return fmt.Errorf("插件协议版本 %d 不受支持", manifest.ProtocolVersion)
	}
	if manifest.Platform != platform() {
		return fmt.Errorf("插件平台 %s 与当前平台 %s 不匹配", manifest.Platform, platform())
	}
	if err := tool.ValidateDefinition(manifest.Definition); err != nil {
		return err
	}
	if err := validateRelativePath("可执行文件", manifest.Executable); err != nil {
		return err
	}
	return nil
}

func validateExecutable(path string, manifest pluginproto.Manifest) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("插件可执行文件不存在：%w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("插件可执行文件不是普通文件")
	}
	if info.Size() == 0 || info.Size() > 256<<20 {
		return errors.New("插件可执行文件大小异常")
	}
	if runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(path), ".exe") {
		return errors.New("Windows 插件必须使用 .exe")
	}
	return nil
}

func validateRelativePath(label, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s为空", label)
	}
	cleaned := filepath.Clean(filepath.FromSlash(value))
	if filepath.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s路径非法", label)
	}
	return nil
}

func readManifest(path string) (pluginproto.Manifest, error) {
	var manifest pluginproto.Manifest
	data, err := os.ReadFile(path)
	if err != nil {
		return manifest, err
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return manifest, err
	}
	if err := validateManifest(manifest); err != nil {
		return manifest, err
	}
	return manifest, nil
}

func readSources(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var document struct {
		Sources []string `json:"sources"`
	}
	if json.Unmarshal(data, &document) != nil {
		return nil
	}
	out := make([]string, 0, len(document.Sources))
	for _, source := range document.Sources {
		if value := strings.TrimSpace(source); value != "" {
			out = append(out, value)
		}
	}
	return out
}

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

func validateSource(source string) error {
	if filepath.IsAbs(source) || strings.HasPrefix(source, `\\`) {
		return nil
	}
	parsed, err := url.Parse(source)
	if err != nil {
		return fmt.Errorf("商店地址 %q 无效：%w", source, err)
	}
	switch strings.ToLower(parsed.Scheme) {
	case "":
		return nil
	case "file":
		if strings.TrimSpace(filePathFromURL(parsed)) == "" {
			return fmt.Errorf("商店地址 %q 缺少文件路径", source)
		}
	case "http", "https":
		if parsed.Host == "" {
			return fmt.Errorf("商店地址 %q 缺少主机名", source)
		}
	default:
		return fmt.Errorf("商店地址不支持 %q 协议", parsed.Scheme)
	}
	return nil
}

func platform() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}

func compatible(item tool.Package, version string) bool {
	if item.ProtocolVersion != 0 && !pluginproto.SupportsVersion(item.ProtocolVersion) {
		return false
	}
	if item.Platform != "" && item.Platform != platform() {
		return false
	}
	if strings.TrimSpace(item.Definition.ID) == "" || strings.TrimSpace(version) == "" {
		return false
	}
	return true
}

func compareVersion(left, right string) int {
	a := versionParts(left)
	b := versionParts(right)
	for i := 0; i < max(len(a), len(b)); i++ {
		var x, y int
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
	}
	return 0
}

func versionParts(value string) []int {
	value = strings.TrimPrefix(strings.TrimSpace(value), "v")
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == '.' || r == '-' || r == '+'
	})
	out := make([]int, 0, len(fields))
	for _, field := range fields {
		number := 0
		for _, r := range field {
			if r < '0' || r > '9' {
				break
			}
			number = number*10 + int(r-'0')
		}
		out = append(out, number)
	}
	return out
}

func safeID(id string) string {
	var out strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			out.WriteRune(r)
		default:
			out.WriteByte('_')
		}
	}
	return out.String()
}

func cloneSnapshot(source tool.StoreSnapshot) tool.StoreSnapshot {
	out := source
	out.Packages = append([]tool.Package(nil), source.Packages...)
	out.Sources = append([]string(nil), source.Sources...)
	out.Warnings = append([]string(nil), source.Warnings...)
	return out
}

func mustJSON(value any) []byte {
	data, _ := json.MarshalIndent(value, "", "  ")
	return append(data, '\n')
}

func splitSources(value string) []string {
	parts := strings.Split(value, ";")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func discoverLocalCatalogs() []string {
	candidates := []string{
		filepath.Join("store", "catalog.json"),
		filepath.Join("catalog.json"),
	}
	if executable, err := os.Executable(); err == nil {
		dir := filepath.Dir(executable)
		candidates = append(candidates,
			filepath.Join(dir, "store", "catalog.json"),
			filepath.Join(dir, "catalog.json"),
		)
	}
	seen := map[string]bool{}
	out := []string{}
	for _, candidate := range candidates {
		absolute, err := filepath.Abs(candidate)
		if err != nil || seen[absolute] {
			continue
		}
		seen[absolute] = true
		if info, err := os.Stat(absolute); err == nil && info.Mode().IsRegular() {
			out = append(out, absolute)
		}
	}
	return out
}

func fileURI(path string) string {
	path = filepath.ToSlash(path)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return "file://" + path
}

func filePathFromURL(parsed *url.URL) string {
	path := parsed.Path
	if runtime.GOOS == "windows" && len(path) >= 3 && path[0] == '/' && path[2] == ':' {
		path = path[1:]
	}
	return filepath.FromSlash(path)
}
