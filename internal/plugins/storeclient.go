package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/cn-maul/OpenTools/sdk/registry"
)

const (
	maxCatalogSize  = 4 << 20
	maxArchiveSize  = 256 << 20
	maxArchiveFiles = 4096
)

// storeClient fetches package catalogs and archives over the configured
// sources (HTTP, HTTPS, file and UNC paths).
type storeClient struct {
	sourcesPath string
	client      *http.Client
}

func newStoreClient(sourcesPath string) *storeClient {
	return &storeClient{
		sourcesPath: sourcesPath,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// sources returns the configured catalog sources, resolved in this order:
// saved settings, OPENTOOLS_CATALOG_URL, catalogs next to the executable,
// then the default online source.
func (c *storeClient) sources() []string {
	sources := readSources(c.sourcesPath)
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

// fetchCatalog downloads and parses one source into packages.
func (c *storeClient) fetchCatalog(ctx context.Context, source string) ([]registry.Package, error) {
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
			return nil, errorsNew("目录文件过大")
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
			return nil, errorsNew("目录文件过大")
		}
		return parseCatalog(data, absolute)
	default:
		return c.fetchCatalogURL(ctx, source, parsed)
	}
}

func (c *storeClient) fetchCatalogURL(ctx context.Context, source string, parsed *url.URL) ([]registry.Package, error) {
	switch parsed.Scheme {
	case "", "file", "http", "https":
	default:
		return nil, fmt.Errorf("不支持的目录协议 %q", parsed.Scheme)
	}
	data, err := c.fetchBytes(ctx, source, maxCatalogSize)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxCatalogSize {
		return nil, errorsNew("目录文件过大")
	}
	var catalog registry.PackageCatalog
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

func parseCatalog(data []byte, source string) ([]registry.Package, error) {
	var catalog registry.PackageCatalog
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

// fetchBytes reads a URL or file with a hard size limit.
func (c *storeClient) fetchBytes(ctx context.Context, source string, limit int64) ([]byte, error) {
	if filepath.IsAbs(source) || strings.HasPrefix(source, `\\`) {
		data, err := os.ReadFile(source)
		if err != nil {
			return nil, err
		}
		if int64(len(data)) > limit {
			return nil, errorsNew("文件体积超过限制")
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
		response, err := c.client.Do(request)
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
			return nil, errorsNew("文件体积超过限制")
		}
		return data, nil
	case "":
		return os.ReadFile(source)
	default:
		return nil, fmt.Errorf("不支持的下载协议 %q", parsed.Scheme)
	}
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
