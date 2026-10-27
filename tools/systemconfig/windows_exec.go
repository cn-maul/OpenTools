//go:build windows

package systemconfig

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

// runPowerShell uses the inbox Windows PowerShell 5.1 so the tool does not
// depend on PowerShell 7 being installed on the user's computer.
func runPowerShell(ctx context.Context, script string) ([]byte, error) {
	cmd, err := powerShellCommand(ctx, script)
	if err != nil {
		return nil, err
	}
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

// runPowerShellLines streams stdout one line at a time. The Windows
// inventory script emits fixed markers, so sections become visible while the
// remaining probes are still running.
func runPowerShellLines(ctx context.Context, script string, handle func(string) error) error {
	cmd, err := powerShellCommand(ctx, script)
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("打开 PowerShell 输出失败：%w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 Windows PowerShell 失败：%w", err)
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64<<10), 16<<20)
	for scanner.Scan() {
		if err := handle(scanner.Text()); err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return err
		}
	}
	scanErr := scanner.Err()
	waitErr := cmd.Wait()
	if errors.Is(ctx.Err(), context.Canceled) {
		return context.Canceled
	}
	if scanErr != nil {
		return fmt.Errorf("读取 PowerShell 输出失败：%w", scanErr)
	}
	if waitErr != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = waitErr.Error()
		}
		return fmt.Errorf("Windows PowerShell 执行失败：%s", detail)
	}
	return nil
}

func powerShellCommand(ctx context.Context, script string) (*exec.Cmd, error) {
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
	// Force UTF-8 stdout before converting the result to JSON. The script is
	// passed as UTF-16LE so Chinese text is independent of the console code page.
	script = "$OutputEncoding = New-Object System.Text.UTF8Encoding $false\n" +
		"[Console]::OutputEncoding = $OutputEncoding\n" +
		script
	encoded := base64.StdEncoding.EncodeToString(utf16LE(script))
	cmd := exec.CommandContext(
		ctx,
		executable,
		"-NoLogo",
		"-NoProfile",
		"-NonInteractive",
		"-ExecutionPolicy",
		"Bypass",
		"-EncodedCommand",
		encoded,
	)
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	return cmd, nil
}

func utf16LE(value string) []byte {
	units := utf16.Encode([]rune(value))
	out := make([]byte, 0, len(units)*2)
	for _, unit := range units {
		out = append(out, byte(unit), byte(unit>>8))
	}
	return out
}
