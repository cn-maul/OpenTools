//go:build windows

package printercheck

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

func runPowerShell(ctx context.Context, script string) ([]byte, error) {
	executable := filepath.Join(
		os.Getenv("SystemRoot"),
		"System32",
		"WindowsPowerShell",
		"v1.0",
		"powershell.exe",
	)
	if _, err := os.Stat(executable); err != nil {
		found, lookupErr := exec.LookPath("powershell.exe")
		if lookupErr != nil {
			return nil, errors.New("未找到 Windows PowerShell 5.1")
		}
		executable = found
	}
	script = "$OutputEncoding = New-Object System.Text.UTF8Encoding $false\n" +
		"[Console]::OutputEncoding = $OutputEncoding\n" +
		script
	// The script is written to a temporary UTF-8 (BOM) file rather than
	// passed with -EncodedCommand: the collection script is larger than the
	// Windows argument limit, and a BOM keeps Chinese text intact for
	// Windows PowerShell 5.1.
	file, err := os.CreateTemp("", "opentools-printer-*.ps1")
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
		executable,
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
