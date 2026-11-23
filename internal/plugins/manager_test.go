package plugins

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/cn-maul/OpenTools/sdk/registry"
	"github.com/cn-maul/OpenTools/sdk/tool"
	"github.com/cn-maul/OpenTools/sdk/tool/pluginproto"
)

func sha256Sum(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// buildTestArchive produces a valid plugin ZIP: manifest.json + tool.exe.
func buildTestArchive(t *testing.T, id, version string) []byte {
	t.Helper()
	exeName := "tool.exe"
	if runtime.GOOS != "windows" {
		exeName = "tool"
	}
	manifest := pluginproto.Manifest{
		SchemaVersion:   1,
		ProtocolVersion: pluginproto.Version,
		Platform:        runtime.GOOS + "/" + runtime.GOARCH,
		Executable:      exeName,
		Definition: tool.Definition{
			ID:       id,
			Name:     "测试工具",
			Summary:  "测试",
			Category: "测试",
			Version:  version,
			Input:    tool.Object(map[string]*tool.Schema{}),
		},
	}
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for _, entry := range []struct {
		name string
		data []byte
	}{
		{"manifest.json", manifestData},
		{exeName, []byte("fake executable")},
	} {
		file, err := writer.Create(entry.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write(entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func newTestManager(t *testing.T) (*Manager, string) {
	t.Helper()
	root := t.TempDir()
	manager, err := NewManager(filepath.Join(root, "plugins"), root)
	if err != nil {
		t.Fatal(err)
	}
	return manager, root
}

func TestInstallAndUninstall(t *testing.T) {
	manager, _ := newTestManager(t)
	data := buildTestArchive(t, "test.alpha", "1.0.0")
	sum := sha256Sum(data)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(data)
	}))
	defer server.Close()

	item := registry.Package{
		Definition:  testDefinition("test.alpha", "1.0.0"),
		Platform:    runtime.GOOS + "/" + runtime.GOARCH,
		DownloadURL: server.URL + "/test.alpha.zip",
		SHA256:      sum,
	}
	if err := manager.Install(context.Background(), item); err != nil {
		t.Fatalf("install: %v", err)
	}
	manifest, ok := manager.Manifest("test.alpha")
	if !ok {
		t.Fatal("installed tool not found")
	}
	if manifest.Definition.Version != "1.0.0" {
		t.Fatalf("version = %q, want 1.0.0", manifest.Definition.Version)
	}
	tools := manager.Tools()
	if len(tools) != 1 {
		t.Fatalf("tools = %d, want 1", len(tools))
	}

	if err := manager.Uninstall("test.alpha"); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, ok := manager.Manifest("test.alpha"); ok {
		t.Fatal("tool still present after uninstall")
	}
}

func TestInstallRejectsWrongSHA256(t *testing.T) {
	manager, _ := newTestManager(t)
	data := buildTestArchive(t, "test.beta", "1.0.0")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(data)
	}))
	defer server.Close()

	item := registry.Package{
		Definition:  testDefinition("test.beta", "1.0.0"),
		Platform:    runtime.GOOS + "/" + runtime.GOARCH,
		DownloadURL: server.URL + "/test.beta.zip",
		SHA256:      "0000000000000000000000000000000000000000000000000000000000000000",
	}
	if err := manager.Install(context.Background(), item); err == nil {
		t.Fatal("install with wrong SHA256 should fail")
	}
	if _, ok := manager.Manifest("test.beta"); ok {
		t.Fatal("failed install must not register the tool")
	}
}

func TestUpgradeReplacesAndKeepsOldOnFailure(t *testing.T) {
	manager, root := newTestManager(t)
	v1 := buildTestArchive(t, "test.gamma", "1.0.0")
	v2 := buildTestArchive(t, "test.gamma", "2.0.0")

	serve := func(payload []byte) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(payload)
		}))
	}

	install := func(payload []byte, version string) error {
		server := serve(payload)
		defer server.Close()
		return manager.Install(context.Background(), registry.Package{
			Definition:  testDefinition("test.gamma", version),
			Platform:    runtime.GOOS + "/" + runtime.GOARCH,
			DownloadURL: server.URL + "/test.gamma.zip",
			SHA256:      sha256Sum(payload),
		})
	}

	if err := install(v1, "1.0.0"); err != nil {
		t.Fatalf("install v1: %v", err)
	}
	if err := install(v2, "2.0.0"); err != nil {
		t.Fatalf("install v2: %v", err)
	}
	manifest, _ := manager.Manifest("test.gamma")
	if manifest.Definition.Version != "2.0.0" {
		t.Fatalf("version = %q, want 2.0.0", manifest.Definition.Version)
	}

	// A corrupt v3 must fail and keep 2.0.0 installed.
	bad := []byte("not a zip")
	if err := install(bad, "3.0.0"); err == nil {
		t.Fatal("corrupt archive should fail")
	}
	manifest, _ = manager.Manifest("test.gamma")
	if manifest.Definition.Version != "2.0.0" {
		t.Fatalf("after failed upgrade version = %q, want 2.0.0", manifest.Definition.Version)
	}
	_ = root
}

func TestRescanSkipsTransientDirs(t *testing.T) {
	manager, root := newTestManager(t)
	data := buildTestArchive(t, "test.delta", "1.0.0")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(data)
	}))
	defer server.Close()
	if err := manager.Install(context.Background(), registry.Package{
		Definition:  testDefinition("test.delta", "1.0.0"),
		Platform:    runtime.GOOS + "/" + runtime.GOARCH,
		DownloadURL: server.URL + "/test.delta.zip",
		SHA256:      sha256Sum(data),
	}); err != nil {
		t.Fatalf("install: %v", err)
	}

	// A leftover backup directory must not become a duplicate install.
	backup := filepath.Join(root, "plugins", "test.delta.old-123")
	if err := os.MkdirAll(backup, 0o700); err != nil {
		t.Fatal(err)
	}
	manager.Reload()
	loaded, warnings := manager.inv.snapshot()
	if len(loaded) != 1 {
		t.Fatalf("loaded = %d, want 1", len(loaded))
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
}

func TestSnapshotMergesInstalled(t *testing.T) {
	manager, _ := newTestManager(t)
	data := buildTestArchive(t, "test.epsilon", "1.0.0")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(data)
	}))
	defer server.Close()
	if err := manager.Install(context.Background(), registry.Package{
		Definition:  testDefinition("test.epsilon", "1.0.0"),
		Platform:    runtime.GOOS + "/" + runtime.GOARCH,
		DownloadURL: server.URL + "/test.epsilon.zip",
		SHA256:      sha256Sum(data),
	}); err != nil {
		t.Fatalf("install: %v", err)
	}
	snapshot := manager.Snapshot()
	if len(snapshot.Packages) != 1 {
		t.Fatalf("packages = %d, want 1", len(snapshot.Packages))
	}
	pkg := snapshot.Packages[0]
	if !pkg.Installed || pkg.InstalledVersion != "1.0.0" || !pkg.Compatible {
		t.Fatalf("unexpected package state: %+v", pkg)
	}
}

func TestCompareVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.2.10", "1.2.9", 1},
		{"v1.2.0", "1.2.0", 0},
		{"1.2.0-beta", "1.2.0", 0},
		{"2.0.0", "1.9.9", 1},
	}
	for _, item := range cases {
		if got := compareVersion(item.a, item.b); got != item.want {
			t.Errorf("compareVersion(%q, %q) = %d, want %d", item.a, item.b, got, item.want)
		}
	}
}

func testDefinition(id, version string) tool.Definition {
	return tool.Definition{
		ID:       id,
		Name:     "测试工具",
		Summary:  "测试",
		Category: "测试",
		Version:  version,
		Input:    tool.Object(map[string]*tool.Schema{}),
	}
}
