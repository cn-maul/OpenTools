//go:build windows

package elevation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/cn-maul/OpenTools/sdk/tool"
)

const (
	seeMaskNoCloseProcess = 0x00000040
	seeMaskNoAsync        = 0x00000100
	swHide                = 0
)

// shellExecuteInfo mirrors SHELLEXECUTEINFOW. The layout matches the Win32
// struct on both 32- and 64-bit Windows because every field is naturally
// aligned.
type shellExecuteInfo struct {
	size          uint32
	mask          uint32
	hwnd          uintptr
	verb          *uint16
	file          *uint16
	parameters    *uint16
	directory     *uint16
	show          int32
	instance      uintptr
	idList        uintptr
	class         *uint16
	classKey      uintptr
	hotKey        uint32
	icon          uintptr
	processHandle uintptr
}

var (
	shell32             = syscall.NewLazyDLL("shell32.dll")
	procShellExecuteExW = shell32.NewProc("ShellExecuteExW")
)

var errElevationCanceled = errors.New("用户取消了管理员授权")

// runElevated starts program through the "runas" verb, waits for it, and
// returns its exit code and captured output. The host owns the UAC prompt;
// the tool only supplies what to run.
func runElevated(ctx context.Context, request tool.ElevateRequest) (tool.ElevateResult, error) {
	var result tool.ElevateResult

	tempDir, err := os.MkdirTemp("", "opentools-elevate-*")
	if err != nil {
		return result, fmt.Errorf("创建提权临时目录失败：%w", err)
	}
	defer os.RemoveAll(tempDir)

	stdoutPath := filepath.Join(tempDir, "stdout.txt")
	stderrPath := filepath.Join(tempDir, "stderr.txt")

	program := request.Program
	args := append([]string(nil), request.Args...)
	if strings.TrimSpace(request.Script) != "" {
		scriptPath := filepath.Join(tempDir, "task.ps1")
		// Windows PowerShell 5.1 needs a UTF-8 BOM to read non-ASCII text.
		if err := os.WriteFile(scriptPath, []byte("\ufeff"+request.Script), 0o600); err != nil {
			return result, fmt.Errorf("写入提权脚本失败：%w", err)
		}
		args = append([]string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", scriptPath}, args...)
	}

	// Start-Process captures the child's raw bytes without re-encoding, and
	// quoting each argument keeps Chinese paths intact.
	launcher := fmt.Sprintf(`$ErrorActionPreference = "Stop"
$args = @(%s)
$p = Start-Process -FilePath '%s' -ArgumentList $args -NoNewWindow -Wait -PassThru `+
		`-RedirectStandardOutput '%s' -RedirectStandardError '%s'
exit $p.ExitCode
`,
		quoteList(args), quoteSingle(program), quoteSingle(stdoutPath), quoteSingle(stderrPath))
	launcherPath := filepath.Join(tempDir, "launch-elevated.ps1")
	if err := os.WriteFile(launcherPath, []byte("\ufeff"+launcher), 0o600); err != nil {
		return result, fmt.Errorf("写入提权启动脚本失败：%w", err)
	}

	powershell := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	if _, err := os.Stat(powershell); err != nil {
		powershell = "powershell.exe"
	}

	exitCode, runErr := shellExecuteRunAs(
		ctx,
		powershell,
		"-NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "+quoteArgument(launcherPath),
		tempDir,
	)
	result.ExitCode = exitCode
	if data, err := os.ReadFile(stdoutPath); err == nil {
		result.Stdout = strings.TrimSpace(string(data))
	}
	if data, err := os.ReadFile(stderrPath); err == nil {
		result.Stderr = strings.TrimSpace(string(data))
	}
	return result, runErr
}

// shellExecuteRunAs launches file elevated and waits for it to exit.
func shellExecuteRunAs(ctx context.Context, file, parameters, directory string) (int, error) {
	verb, err := windows.UTF16PtrFromString("runas")
	if err != nil {
		return -1, err
	}
	filePtr, err := windows.UTF16PtrFromString(file)
	if err != nil {
		return -1, err
	}
	paramsPtr, err := windows.UTF16PtrFromString(parameters)
	if err != nil {
		return -1, err
	}
	dirPtr, err := windows.UTF16PtrFromString(directory)
	if err != nil {
		return -1, err
	}

	info := shellExecuteInfo{
		mask:       seeMaskNoCloseProcess | seeMaskNoAsync,
		verb:       verb,
		file:       filePtr,
		parameters: paramsPtr,
		directory:  dirPtr,
		show:       swHide,
	}
	info.size = uint32(unsafe.Sizeof(info))

	ok, _, callErr := procShellExecuteExW.Call(uintptr(unsafe.Pointer(&info)))
	if ok == 0 {
		if errors.Is(callErr, windows.ERROR_CANCELLED) || callErr == syscall.Errno(1223) {
			return -1, errElevationCanceled
		}
		return -1, fmt.Errorf("请求管理员权限失败：%w", callErr)
	}
	process := windows.Handle(info.processHandle)
	if process == 0 {
		return -1, errors.New("提权进程没有返回句柄")
	}
	defer windows.CloseHandle(process)

	waited, err := windows.WaitForSingleObject(process, windows.INFINITE)
	if err != nil {
		return -1, fmt.Errorf("等待提权进程失败：%w", err)
	}
	if waited == windows.WAIT_OBJECT_0 {
		if ctx.Err() != nil {
			return -1, ctx.Err()
		}
	} else {
		// The wait was abandoned because the context ended; the elevated
		// child keeps running because this process cannot terminate it.
		return -1, ctx.Err()
	}

	var code uint32
	if err := windows.GetExitCodeProcess(process, &code); err != nil {
		return -1, fmt.Errorf("读取提权进程退出码失败：%w", err)
	}
	return int(code), nil
}

// quoteSingle quotes a PowerShell single-quoted string literal.
func quoteSingle(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func quoteList(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, quoteSingle(value))
	}
	return strings.Join(quoted, ", ")
}

// quoteArgument quotes one command-line argument for powershell.exe.
func quoteArgument(value string) string {
	if !strings.ContainsAny(value, " \t\"") {
		return value
	}
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}
