// Package prochelp provides the process helpers nearly every OpenTools tool
// needs: running PowerShell scripts with correct Windows encoding, and
// registering result artifacts.
//
// These helpers sit on top of the tool contract (sdk/tool) without
// depending on any host internals. A tool may use them or not; they exist
// because the encoding and artifact rules are easy to get subtly wrong.
package prochelp

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"unicode/utf16"

	"github.com/cn-maul/OpenTools/sdk/tool"
)

// WindowsPowerShell is the OS-shipped Windows PowerShell 5.1 executable.
// Tools should prefer it over PowerShell 7 so they run on a clean Windows
// install without extra dependencies.
const WindowsPowerShell = "powershell.exe"

// utf16LE encodes a string as little-endian UTF-16 bytes, the format
// PowerShell's -EncodedCommand expects.
func utf16LE(value string) []byte {
	units := utf16.Encode([]rune(value))
	out := make([]byte, 0, len(units)*2)
	for _, unit := range units {
		out = append(out, byte(unit), byte(unit>>8))
	}
	return out
}

// EncodeScript encodes a PowerShell script for -EncodedCommand and prepends
// the console-encoding preamble so Chinese output survives round-trips.
func EncodeScript(body string) string {
	script := "$OutputEncoding = [Text.UTF8Encoding]::new($false)\n" +
		"[Console]::OutputEncoding = $OutputEncoding\n" + body
	return base64.StdEncoding.EncodeToString(utf16LE(script))
}

// PowerShellCommand builds a canceled-aware exec.Cmd that runs body with
// Windows PowerShell 5.1. The caller executes it and reads StandardOutput /
// StandardError itself.
func PowerShellCommand(ctx context.Context, body string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, WindowsPowerShell,
		"-NoLogo", "-NoProfile", "-NonInteractive",
		"-ExecutionPolicy", "Bypass",
		"-EncodedCommand", EncodeScript(body),
	)
	return cmd
}

// Artifact writes data below env.WorkDir and returns a validated,
// host-ready artifact. The name is the file name inside WorkDir; MIME is
// guessed from the extension when empty.
func Artifact(ctx context.Context, env tool.Environment, name string, data []byte, mime string) (tool.Artifact, error) {
	if err := ctx.Err(); err != nil {
		return tool.Artifact{}, err
	}
	path := filepath.Join(env.WorkDir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return tool.Artifact{}, err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return tool.Artifact{}, err
	}
	absolute, size, err := tool.ResolveArtifact(env, path)
	if err != nil {
		return tool.Artifact{}, err
	}
	if mime == "" {
		mime = MIMEByExtension(path)
	}
	return tool.Artifact{Name: filepath.Base(path), Path: absolute, MIME: mime, Size: size}, nil
}
