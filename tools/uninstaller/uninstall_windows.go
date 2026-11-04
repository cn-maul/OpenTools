//go:build windows

package uninstaller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"opentools/pkg/tool"
)

// runUninstall launches the app's own uninstaller in an elevated process, so
// the host asks for UAC once and the result is reported back as JSON.
func runUninstall(ctx context.Context, entry appEntry, report tool.Reporter) (string, error) {
	temp, err := os.MkdirTemp("", "opentools-uninstall-*")
	if err != nil {
		return "", fmt.Errorf("创建临时目录失败：%w", err)
	}
	defer os.RemoveAll(temp)

	inputPath := filepath.Join(temp, "input.json")
	outputPath := filepath.Join(temp, "result.json")
	scriptPath := filepath.Join(temp, "uninstall-elevated.ps1")
	launcherPath := filepath.Join(temp, "launch-elevated.ps1")

	payload := map[string]any{
		"key":  entry.Key,
		"name": entry.Name,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	// Windows PowerShell 5.1 reads a BOM-less file as ANSI, which corrupts
	// the Chinese text. Every file the script reads or executes therefore
	// carries a UTF-8 BOM.
	if err := os.WriteFile(inputPath, withBOM(data), 0o600); err != nil {
		return "", fmt.Errorf("写入卸载参数失败：%w", err)
	}
	if err := os.WriteFile(scriptPath, withBOM([]byte(uninstallActionScript)), 0o600); err != nil {
		return "", fmt.Errorf("写入卸载脚本失败：%w", err)
	}
	if err := os.WriteFile(launcherPath, withBOM([]byte(uninstallLauncher)), 0o600); err != nil {
		return "", fmt.Errorf("写入提权启动脚本失败：%w", err)
	}

	powershell, err := windowsPowerShell()
	if err != nil {
		return "", err
	}
	report.Log("info", "正在请求管理员权限执行卸载")
	cmd := exec.CommandContext(
		ctx,
		powershell,
		"-NoLogo",
		"-NoProfile",
		"-NonInteractive",
		"-ExecutionPolicy",
		"Bypass",
		"-File",
		launcherPath,
	)
	cmd.Env = append(os.Environ(),
		"OT_UNINSTALL_INPUT="+inputPath,
		"OT_UNINSTALL_OUTPUT="+outputPath,
		"OT_UNINSTALL_SCRIPT="+scriptPath,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	if runErr != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return "", context.Canceled
		}
		// The elevated script writes its own error message, which is far more
		// useful than the exit code of the launcher.
		if result, ok := readResult(outputPath); ok {
			if strings.TrimSpace(result.Error) == "" {
				result.Error = launcherDetail(stdout, stderr, runErr)
			}
			if result.Error == "" {
				result.Error = runErr.Error()
			}
			return "", fmt.Errorf("卸载失败：%s", result.Error)
		}
		detail := launcherDetail(stdout, stderr, runErr)
		if detail == "" {
			return "", fmt.Errorf("卸载未完成（可能取消了 UAC）：%s", runErr)
		}
		return "", fmt.Errorf("卸载未完成（可能取消了 UAC）：%s", detail)
	}
	result, ok := readResult(outputPath)
	if !ok {
		return "", errors.New("卸载脚本没有返回结果")
	}
	if !result.OK {
		if strings.TrimSpace(result.Error) == "" {
			result.Error = "未知错误"
		}
		return "", fmt.Errorf("卸载失败：%s", result.Error)
	}
	return result.Message, nil
}

func withBOM(data []byte) []byte {
	return append([]byte{0xEF, 0xBB, 0xBF}, data...)
}

// launcherDetail collects whatever the launcher printed, so a failed
// elevation is not reported as a bare exit code.
func launcherDetail(stdout, stderr bytes.Buffer, runErr error) string {
	parts := make([]string, 0, 3)
	for _, buffer := range []bytes.Buffer{stdout, stderr} {
		text := strings.TrimSpace(buffer.String())
		if text != "" {
			parts = append(parts, text)
		}
	}
	if len(parts) == 0 {
		if runErr != nil {
			return runErr.Error()
		}
		return ""
	}
	return strings.Join(parts, "；")
}

func readResult(path string) (uninstallResult, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return uninstallResult{}, false
	}
	// Windows PowerShell 5.1 writes UTF-8 with a BOM, which is not valid JSON.
	var result uninstallResult
	if err := json.Unmarshal(trimJSON(data), &result); err != nil {
		return uninstallResult{}, false
	}
	return result, true
}

// windowsPowerShell returns the system Windows PowerShell 5.1 executable.
func windowsPowerShell() (string, error) {
	executable := filepath.Join(
		os.Getenv("SystemRoot"),
		"System32",
		"WindowsPowerShell",
		"v1.0",
		"powershell.exe",
	)
	if _, err := os.Stat(executable); err == nil {
		return executable, nil
	}
	found, lookupErr := exec.LookPath("powershell.exe")
	if lookupErr != nil {
		return "", errors.New("未找到 Windows PowerShell 5.1")
	}
	return found, nil
}
