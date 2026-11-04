//go:build windows

package startupmgr

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
	"syscall"

	"opentools/pkg/tool"
)

func runPowerShell(ctx context.Context, script string) ([]byte, error) {
	powershell, err := windowsPowerShell()
	if err != nil {
		return nil, err
	}
	script = "$OutputEncoding = New-Object System.Text.UTF8Encoding $false\n" +
		"[Console]::OutputEncoding = $OutputEncoding\n" +
		script
	// The script is written to a temporary UTF-8 (BOM) file rather than
	// passed with -EncodedCommand: the collection script is larger than the
	// Windows argument limit, and a BOM keeps Chinese text intact for
	// Windows PowerShell 5.1.
	file, err := os.CreateTemp("", "opentools-startupmgr-*.ps1")
	if err != nil {
		return nil, fmt.Errorf("创建临时脚本失败：%w", err)
	}
	path := file.Name()
	defer os.Remove(path)
	if _, err := file.Write(append([]byte{0xEF, 0xBB, 0xBF}, []byte(script)...)); err != nil {
		file.Close()
		return nil, fmt.Errorf("写入临时脚本失败：%w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("关闭临时脚本失败：%w", err)
	}
	cmd := exec.CommandContext(
		ctx,
		powershell,
		"-NoLogo",
		"-NoProfile",
		"-NonInteractive",
		"-ExecutionPolicy",
		"Bypass",
		"-File",
		path,
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil, context.Canceled
		}
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return nil, fmt.Errorf("Windows PowerShell 执行失败：%s", detail)
	}
	return stdout.Bytes(), nil
}

// runToggle performs one startup state change in an elevated process, so the
// host asks for UAC once and the result is reported back as JSON.
func runToggle(ctx context.Context, job toggleJob, report tool.Reporter) (string, error) {
	temp, err := os.MkdirTemp("", "opentools-startup-*")
	if err != nil {
		return "", fmt.Errorf("创建临时目录失败：%w", err)
	}
	defer os.RemoveAll(temp)

	inputPath := filepath.Join(temp, "input.json")
	outputPath := filepath.Join(temp, "result.json")
	scriptPath := filepath.Join(temp, "startup-elevated.ps1")
	launcherPath := filepath.Join(temp, "launch-elevated.ps1")

	data, err := json.Marshal(job)
	if err != nil {
		return "", err
	}
	// Windows PowerShell 5.1 reads a BOM-less file as ANSI, which corrupts
	// the Chinese text. Every file the script reads or executes therefore
	// carries a UTF-8 BOM.
	if err := os.WriteFile(inputPath, withBOM(data), 0o600); err != nil {
		return "", fmt.Errorf("写入启动项参数失败：%w", err)
	}
	if err := os.WriteFile(scriptPath, withBOM([]byte(startupToggleScript)), 0o600); err != nil {
		return "", fmt.Errorf("写入启动项脚本失败：%w", err)
	}
	if err := os.WriteFile(launcherPath, withBOM([]byte(startupLauncher)), 0o600); err != nil {
		return "", fmt.Errorf("写入提权启动脚本失败：%w", err)
	}

	powershell, err := windowsPowerShell()
	if err != nil {
		return "", err
	}
	report.Log("info", "正在请求管理员权限修改启动项")
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
		"OT_STARTUP_INPUT="+inputPath,
		"OT_STARTUP_OUTPUT="+outputPath,
		"OT_STARTUP_SCRIPT="+scriptPath,
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
		if result, ok := readToggleResult(outputPath); ok {
			if strings.TrimSpace(result.Error) == "" {
				result.Error = launcherDetail(stdout, stderr, runErr)
			}
			if result.Error == "" {
				result.Error = runErr.Error()
			}
			return "", fmt.Errorf("修改失败：%s", result.Error)
		}
		detail := launcherDetail(stdout, stderr, runErr)
		if detail == "" {
			return "", fmt.Errorf("修改未完成（可能取消了 UAC）：%s", runErr)
		}
		return "", fmt.Errorf("修改未完成（可能取消了 UAC）：%s", detail)
	}
	result, ok := readToggleResult(outputPath)
	if !ok {
		return "", errors.New("启动项脚本没有返回结果")
	}
	if !result.OK {
		if strings.TrimSpace(result.Error) == "" {
			result.Error = "未知错误"
		}
		return "", fmt.Errorf("修改失败：%s", result.Error)
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

func readToggleResult(path string) (toggleResult, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return toggleResult{}, false
	}
	// Windows PowerShell 5.1 writes UTF-8 with a BOM, which is not valid JSON.
	var result toggleResult
	if err := json.Unmarshal(trimJSON(data), &result); err != nil {
		return toggleResult{}, false
	}
	return result, true
}

func trimJSON(data []byte) []byte {
	return bytes.TrimPrefix(bytes.TrimSpace(data), []byte{0xEF, 0xBB, 0xBF})
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
