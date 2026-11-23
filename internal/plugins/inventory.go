package plugins

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/cn-maul/OpenTools/sdk/tool"
	"github.com/cn-maul/OpenTools/sdk/tool/pluginproto"
)

// inventory owns the installed-plugin directory: it scans manifests,
// validates them and answers lookups. It never talks to the network.
type inventory struct {
	root string

	mu      inventoryMutex
	loaded  map[string]loadedPackage
	problem []string
}

// loadedPackage is one verified installation.
type loadedPackage struct {
	manifest pluginproto.Manifest
	dir      string
	exe      string
}

// inventoryMutex is a plain sync.RWMutex; declared here so the inventory is
// self-contained. (The store client and manager share it by embedding.)
type inventoryMutex = syncRWMutex

func newInventory(root string) *inventory {
	return &inventory{root: root, loaded: map[string]loadedPackage{}}
}

// rescan rebuilds the installed set from disk. A malformed manifest becomes
// a warning; it never removes other tools.
func (inv *inventory) rescan() {
	manifestPaths, err := filepath.Glob(filepath.Join(inv.root, "*", "manifest.json"))
	if err != nil {
		inv.mu.Lock()
		inv.problem = []string{fmt.Sprintf("scan plugins: %v", err)}
		inv.mu.Unlock()
		return
	}
	next := make(map[string]loadedPackage, len(manifestPaths))
	var problems []string
	for _, path := range manifestPaths {
		// Install and upgrade leave staging and backup directories behind
		// when the filesystem is busy. They are not installable versions.
		if isTransientDir(filepath.Base(filepath.Dir(path))) {
			continue
		}
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
	inv.mu.Lock()
	inv.loaded = next
	inv.problem = problems
	inv.mu.Unlock()
}

// get returns one installed package.
func (inv *inventory) get(id string) (loadedPackage, bool) {
	inv.mu.RLock()
	defer inv.mu.RUnlock()
	item, ok := inv.loaded[id]
	return item, ok
}

// snapshot returns a stable copy of the installed set plus warnings.
func (inv *inventory) snapshot() (map[string]loadedPackage, []string) {
	inv.mu.RLock()
	defer inv.mu.RUnlock()
	loaded := make(map[string]loadedPackage, len(inv.loaded))
	for id, item := range inv.loaded {
		loaded[id] = item
	}
	return loaded, append([]string(nil), inv.problem...)
}

// remove takes one installation out of the index. The caller owns the
// on-disk removal; this only keeps the view consistent.
func (inv *inventory) forget(id string) {
	inv.mu.Lock()
	delete(inv.loaded, id)
	inv.mu.Unlock()
}

// put records a freshly installed package.
func (inv *inventory) put(item loadedPackage) {
	inv.mu.Lock()
	inv.loaded[item.manifest.Definition.ID] = item
	inv.mu.Unlock()
}

// readManifest reads and validates one manifest.json.
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
		return fmt.Errorf("插件可执行文件不是普通文件")
	}
	if info.Size() == 0 || info.Size() > 256<<20 {
		return fmt.Errorf("插件可执行文件大小异常")
	}
	if runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(path), ".exe") {
		return fmt.Errorf("Windows 插件必须使用 .exe")
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

// platform is the manifest platform string for this build.
func platform() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}

// isTransientDir reports whether a plugin directory name belongs to an
// in-progress install or a leftover backup, none of which are installable.
func isTransientDir(name string) bool {
	return strings.HasPrefix(name, ".install-") ||
		strings.Contains(name, ".old-") ||
		strings.Contains(name, ".remove-")
}

// removeWithRetry deletes a leftover backup directory, retrying because
// antivirus scanners briefly hold freshly written executables on Windows.
func removeWithRetry(path string) {
	for attempt := 0; attempt < 10; attempt++ {
		if os.RemoveAll(path) == nil {
			return
		}
		time.Sleep(time.Second)
	}
}

// safeID maps a tool ID to a filesystem-safe directory name. It must agree
// with the ID rules in tool.ValidateDefinition; unknown characters are
// replaced so an old, malformed manifest can never escape the plugin root.
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
