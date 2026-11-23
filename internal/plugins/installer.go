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
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cn-maul/OpenTools/sdk/registry"
	"github.com/cn-maul/OpenTools/sdk/tool/pluginproto"
)

// installer downloads, verifies and atomically installs package archives
// below the plugin root. Install failures roll back to the previous
// version; leftover backup directories are cleaned in the background.
type installer struct {
	root    string
	fetcher *storeClient
}

func newInstaller(root string, fetcher *storeClient) *installer {
	return &installer{root: root, fetcher: fetcher}
}

// Install downloads and installs one package, replacing any existing
// version. On success it returns the loaded package.
func (ins *installer) Install(ctx context.Context, item registry.Package) (loadedPackage, error) {
	if strings.TrimSpace(item.DownloadURL) == "" {
		return loadedPackage{}, errors.New("安装包缺少下载地址")
	}
	if !compatible(item, item.Definition.Version) {
		return loadedPackage{}, fmt.Errorf("工具 %s 不支持当前平台或协议", item.Definition.ID)
	}
	data, err := ins.fetcher.fetchBytes(ctx, item.DownloadURL, maxArchiveSize)
	if err != nil {
		return loadedPackage{}, fmt.Errorf("下载工具失败：%w", err)
	}
	if int64(len(data)) > maxArchiveSize {
		return loadedPackage{}, fmt.Errorf("安装包超过 %d MiB", maxArchiveSize>>20)
	}
	sum := sha256.Sum256(data)
	actual := hex.EncodeToString(sum[:])
	if item.SHA256 == "" {
		return loadedPackage{}, errors.New("商店条目缺少 SHA256")
	}
	if !strings.EqualFold(actual, item.SHA256) {
		return loadedPackage{}, fmt.Errorf("安装包 SHA256 校验失败")
	}
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return loadedPackage{}, fmt.Errorf("安装包不是有效 ZIP：%w", err)
	}
	if len(archive.File) > maxArchiveFiles {
		return loadedPackage{}, errors.New("安装包文件数量异常")
	}

	stage, err := os.MkdirTemp(ins.root, ".install-*")
	if err != nil {
		return loadedPackage{}, fmt.Errorf("创建安装暂存目录失败：%w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(stage)
		}
	}()
	manifest, executable, err := extractArchive(archive, stage)
	if err != nil {
		return loadedPackage{}, err
	}
	if strings.TrimSpace(manifest.Definition.ID) == "" {
		return loadedPackage{}, errors.New("安装包清单缺少工具 ID")
	}
	if manifest.Definition.ID != item.Definition.ID {
		return loadedPackage{}, fmt.Errorf("安装包 ID %q 与商店条目 %q 不一致", manifest.Definition.ID, item.Definition.ID)
	}
	if err := os.WriteFile(
		filepath.Join(stage, "manifest.json"),
		mustJSON(manifest),
		0o600,
	); err != nil {
		return loadedPackage{}, fmt.Errorf("写入插件清单失败：%w", err)
	}
	if err := validateExecutable(executable, manifest); err != nil {
		return loadedPackage{}, err
	}
	destination := filepath.Join(ins.root, safeID(manifest.Definition.ID))
	var backup string
	if _, err := os.Stat(destination); err == nil {
		// 用带时间戳的唯一备份名：固定的 .old 一旦因文件占用清理失败，
		// 会一直卡住后续更新。rename 失败时短暂重试，等杀毒软件放开新文件。
		backup = destination + ".old-" + fmt.Sprint(time.Now().UnixNano())
		var renameErr error
		for attempt := 0; attempt < 5; attempt++ {
			renameErr = os.Rename(destination, backup)
			if renameErr == nil {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		if renameErr != nil {
			return loadedPackage{}, fmt.Errorf("替换已安装工具失败：%w", renameErr)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return loadedPackage{}, fmt.Errorf("检查已安装工具失败：%w", err)
	}
	if err := os.Rename(stage, destination); err != nil {
		// Enabling the new version failed after the old one was moved aside.
		// Put the old version back so a failed upgrade never removes a tool.
		if backup != "" {
			if restoreErr := os.Rename(backup, destination); restoreErr != nil {
				return loadedPackage{}, fmt.Errorf("启用新工具失败：%w（旧版本回滚也失败，备份在 %s）", err, backup)
			}
		}
		return loadedPackage{}, fmt.Errorf("启用新工具失败，已回滚到原版本：%w", err)
	}
	cleanup = false
	if backup != "" {
		// 新写入的 exe 短时间内可能被杀毒软件占用，后台重试清理，
		// 不阻塞安装完成；即使残留也只是无害的 .old-* 目录。
		go removeWithRetry(backup)
	}
	return loadedPackage{
		manifest: manifest,
		dir:      destination,
		exe:      filepath.Join(destination, filepath.FromSlash(manifest.Executable)),
	}, nil
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

func compatible(item registry.Package, version string) bool {
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

func mustJSON(value any) []byte {
	data, _ := json.MarshalIndent(value, "", "  ")
	return append(data, '\n')
}
