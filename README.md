# OpenTools

OpenTools is a Go desktop shell and package manager for downloadable tools. The
interface is Vue 3 rendered in a MyGo WebView, and the desktop app and CLI run
through the same application service.

The desktop window uses a custom-drawn title bar with visible minimize,
maximize/restore and close controls. The host version is shown beside the app
name; each tool also carries its own version in the tool header and form.

The core does not bundle business tools. A downloaded tool is an independent
executable discovered from the user's plugin directory; installing or removing
a tool takes effect without rebuilding the host.

## Architecture

```text
MyGo / CLI
    -> internal/application      use cases, no GUI or database packages
        -> internal/catalog      installed tool registry
        -> internal/plugins      package store and JSON-RPC process adapter
        -> internal/tasks        detached jobs, cancellation, event stream
            -> tasks.Store       persistence interface
                -> SQLite
    -> pkg/tool                  standard-library-only tool contract
        -> pkg/tool/pluginproto  versioned JSON-RPC 2.0 process protocol
    -> tools/*                   source for independently built plugin binaries
```

Important boundaries:

- `pkg/tool` never imports Vue, MyGo, SQLite, or application state.
- `internal/appkit` never imports a business tool package. It scans the plugin
  directory and registers lightweight process adapters.
- `internal/application` is shared by the desktop and CLI adapters.
- `internal/desktop` is the only package that knows about MyGo.
- `internal/tasks` owns lifecycle and cancellation; tools only report progress
  and observe `context.Context`.
- Jobs outlive a window. A job is "interrupted" only when its process stops
  refreshing the durable heartbeat, so GUI and CLI instances can safely coexist.
- Tool results are structured blocks plus files under the job work directory.
  The host validates artifact paths before exposing them.
- Plugins are separate executables. The host talks to them over line-delimited
  JSON-RPC 2.0 on stdin/stdout, so a plugin cannot import host internals or
  replace the host's Go runtime.

Each installed directory has this layout:

```text
%APPDATA%\OpenTools\plugins\<tool-id>\
    manifest.json
    <tool executable>
```

Packages are ZIP files containing the same two files at the root. The host
validates the schema version, protocol version, platform, tool ID, executable
path, archive paths, and SHA256 before atomically enabling a package. A failed
download or validation never removes the installed version.

## Plugin protocol

Launch the executable with `describe` to print its definition, or with `rpc` to
serve JSON-RPC on stdio. The host sends `initialize`, then `tool/run`. Plugins
may emit `tool/log` and `tool/progress` notifications before the final result.
Cancellation terminates the child process, so tools cannot leave orphan work
behind when a job is canceled.

## Adding a downloadable tool

1. Create `tools/<name>/tool.go` and implement `tool.Tool`.
2. Give it a stable ID and declare all externally visible effects.
3. Validate `ctx` before and during expensive or side-effecting work.
4. Return a non-empty summary and only the supported result block kinds.
5. Add a `pluginproto.Serve(tool)` executable under `cmd/plugins/<name>`.
6. Publish a ZIP containing `manifest.json` and the executable, then add its
   SHA256 and relative download URL to `store/catalog.json`.

`build.ps1` currently builds the two reference plugins and writes a complete
local store into the release folder. That folder can be served by any static
HTTP server, or used directly while developing.

Input schemas intentionally support a small JSON Schema subset: objects,
strings, numbers, integers, booleans, arrays, defaults, enums, bounds, and UI
hints. This keeps form generation and CLI parsing deterministic; a tool can
decode richer values itself if a future schema extension is needed.

## Development

Prerequisites: Go 1.27+, Node.js 22+, and the platform dependencies required
by MyGo.

```powershell
npm install
npm run generate
npm run typecheck
npm run build:web
go test ./...
go vet ./...
go tool mygo build -platform windows/amd64
```

Run the desktop app with `go tool mygo dev` or the produced executable.

For a release snapshot, use PowerShell 7 (the build script itself is `pwsh`).
The produced application runs on ordinary Windows PowerShell 5.1 and does not
require PowerShell 7.

```powershell
./build.ps1
```

The script runs checks, builds the core and plugin executables, creates plugin
ZIPs plus `store/catalog.json`, then copies the core and installer into a new
timestamped folder under
`release/<platform>-<version>/`, for example
`release/windows-amd64-0.2.0/20261026-144034/`. `BUILD.txt` records the
platform, build time, and executable SHA256. Use `-SkipChecks` while iterating
and `-Platform` to select another target. Add `-ExeOnly` to produce a clean
folder containing only the executable and `BUILD.txt`, without the installer.

The core `OpenTools.exe` contains no business tools. The reference plugins are
in `store/packages/` and are installed on demand.

## CLI

```powershell
go run ./cmd/opentools list
go run ./cmd/opentools store --refresh
go run ./cmd/opentools install system.inventory
go run ./cmd/opentools run system.inventory --scope hardware --json
go run ./cmd/opentools uninstall system.inventory
go run ./cmd/opentools jobs --limit 10
go run ./cmd/opentools job <job-id>
```

Data defaults to `%APPDATA%\OpenTools` on Windows. Set `OPENTOOLS_DATA_DIR` to
use a portable or test directory. CLI output is script-friendly; add `--json`
to print the final job snapshot.

## Network and DNS

The downloadable `network.dns-switcher` plugin manages the IPv4 and DNS settings
of the primary Windows adapter. It runs through the inbox Windows PowerShell 5.1 and
supports:

- detecting the primary adapter while excluding virtual adapters, disconnected
  links, and APIPA addresses;
- ranking candidates by physical link, usable IPv4, default route, Internet
  connectivity, and route/interface metric; ambiguous equal-priority candidates
  are reported instead of being selected silently;
- backing up DHCP mode, IPv4 addresses and prefixes, default gateways, automatic
  and explicit interface metrics, default-route metrics, and DNS mode/addresses;
- benchmarking nine public DNS providers with direct UDP queries, ranked by
  successes, median RTT, and jitter;
- switching DNS with a fresh automatic backup and a confirmation prompt;
- restoring a backup only after checking the adapter GUID and MAC address, with
  post-operation verification and automatic rollback on failure.

Backups use schema version 2 and live in
`%APPDATA%\OpenTools\network\backups`, or the equivalent
`OPENTOOLS_DATA_DIR\network\backups`. Older schema version 1 backups remain
readable. Switching DNS or restoring a backup asks for UAC approval in a
separate, hidden Windows PowerShell 5.1 process. The restore scope is IPv4 and
DNS for the selected physical adapter; IPv6, Wi-Fi credentials, proxy settings,
and Windows Firewall rules are not modified.

## System configuration

The `system.inventory` tool is a read-only local inventory. It reports the
computer and motherboard, firmware and boot mode, CPU, memory modules, active
display adapters, physical disks and volumes, connected network adapters,
operating-system uptime, battery presence, and security state (Secure Boot,
TPM, Defender, firewall, BitLocker, and hardware virtualization). It writes a
dated JSON report into the job's artifact directory.

Windows collection uses the inbox Windows PowerShell 5.1, not PowerShell 7.
The tool owns its platform adapter and does not share runtime code with the
network tool, so either tool can be moved to a downloadable plugin package
without breaking the other.

## Plugin sources

By default the host discovers `store/catalog.json` beside the executable. Open
**设置** in the left rail to use one or more semicolon-separated catalog URLs
(HTTP, HTTPS, or file paths). The setting is stored in
`%APPDATA%\OpenTools\plugin-sources.json` and can be cleared to restore local
discovery. `OPENTOOLS_CATALOG_URL` remains available as a portable fallback.
Installed tools stay usable when every catalog is unreachable.
