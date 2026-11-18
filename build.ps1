#!/usr/bin/env pwsh
#Requires -Version 7.0

[CmdletBinding()]
param(
    [ValidateSet("windows/amd64", "windows/arm64", "darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64")]
    [string]$Platform = "windows/amd64",

    [string]$Version = "0.3.3",

    [string]$OutRoot = (Join-Path $PSScriptRoot "release"),

    # Tool repositories to build plugins from, in order. Each must expose
    # tools/plugins.json and cmd/plugins/<name>. The repository is expected
    # to replace github.com/cn-maul/OpenTools with its local checkout.
    [string[]]$ToolRepository = @(),

    # Where the primary tool repository lives when it is not a sibling of
    # this checkout. Defaults to ..\opentool-kit.
    [string]$ToolRepositoryRoot = (Join-Path (Split-Path $PSScriptRoot -Parent) "opentool-kit"),

    # Build the core with no plugins at all, ignoring the default tool
    # repository. Useful for verifying the framework on its own.
    [switch]$CoreOnly,

    [switch]$ExeOnly,

    [switch]$SkipChecks,

    [switch]$Force
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
$PSNativeCommandUseErrorActionPreference = $true
$OutputEncoding = [System.Text.UTF8Encoding]::new($false)
[Console]::OutputEncoding = $OutputEncoding

$root = $PSScriptRoot
$configPath = Join-Path $root "mygo.json"
if (-not (Test-Path -LiteralPath $configPath -PathType Leaf)) {
    $configPath = Join-Path $root "mygo.config.ts"
}
if ($configPath.EndsWith(".json")) {
    $config = Get-Content -LiteralPath $configPath -Raw | ConvertFrom-Json
    $configVersion = $config.version
    # mygo build names the Windows installer <slug>-setup-<version>-<arch>.exe.
    $configSlug = if ($config.PSObject.Properties["slug"] -and $config.slug) {
        $config.slug
    } else {
        $config.name.ToLowerInvariant()
    }
} else {
    $configVersion = (
        Select-String -LiteralPath $configPath -Pattern 'version:\s*"([^"]+)"' |
            Select-Object -First 1
    ).Matches.Groups[1].Value
    $configSlug = (
        Select-String -LiteralPath $configPath -Pattern 'slug:\s*"([^"]+)"' |
            Select-Object -First 1
    ).Matches.Groups[1].Value
    if (-not $configSlug) {
        $configSlug = (
            Select-String -LiteralPath $configPath -Pattern 'name:\s*"([^"]+)"' |
                Select-Object -First 1
        ).Matches.Groups[1].Value.ToLowerInvariant()
    }
}
if ($Version -ne $configVersion) {
    throw "Version mismatch: -Version $Version, $(Split-Path $configPath -Leaf) $configVersion."
}

$outRootPath = [System.IO.Path]::GetFullPath($OutRoot)
$stamp = Get-Date -Format "yyyyMMdd-HHmmss"
$safePlatform = $Platform.Replace("/", "-")
$baseName = "$safePlatform-$Version"
$targetDir = Join-Path (Join-Path $outRootPath $baseName) $stamp
$pluginRoot = Join-Path $root "build\plugins"

if (-not $ToolRepository -or $ToolRepository.Count -eq 0) {
    if ($CoreOnly) {
        $ToolRepository = @()
    } elseif (Test-Path -LiteralPath $ToolRepositoryRoot -PathType Container) {
        $ToolRepository = @($ToolRepositoryRoot)
    }
}

# Each tool repository contributes its own plugins. The core never carries
# business tools, so a build without any repository produces a bare app.
$pluginList = @()
foreach ($repository in $ToolRepository) {
    $repositoryPath = [System.IO.Path]::GetFullPath($repository)
    $manifest = Join-Path $repositoryPath "tools\plugins.json"
    if (-not (Test-Path -LiteralPath $manifest -PathType Leaf)) {
        throw "Tool repository $repositoryPath has no tools\plugins.json."
    }
    foreach ($plugin in (Get-Content -LiteralPath $manifest -Raw | ConvertFrom-Json).plugins) {
        $pluginList += [pscustomobject]@{
            Name       = $plugin.name
            Output     = $plugin.output
            Author     = $plugin.author
            Homepage   = $plugin.homepage
            Repository = $repositoryPath
        }
    }
}
if ($ToolRepository -and $pluginList.Count -eq 0) {
    throw "No plugins defined in the tool repositories' tools\plugins.json."
}

$extension = if ($Platform.StartsWith("windows/")) { ".exe" } else { "" }

function Invoke-Checked {
    param(
        [Parameter(Mandatory)]
        [scriptblock]$Command,
        [Parameter(Mandatory)]
        [string]$Label
    )

    Write-Host "`n==> $Label" -ForegroundColor Cyan
    & $Command
    if ($LASTEXITCODE -ne 0) {
        throw "$Label failed with exit code $LASTEXITCODE."
    }
}

function Resolve-Executable {
    $buildRoot = Join-Path $root "build"
    $names = if ($Platform.StartsWith("windows/")) {
        @("OpenTools.exe")
    } else {
        @("OpenTools", "OpenTools.app")
    }
    foreach ($name in $names) {
        $direct = Get-ChildItem -LiteralPath (Join-Path $buildRoot $safePlatform) -Filter $name -ErrorAction SilentlyContinue |
            Select-Object -First 1
        if ($direct) {
            return $direct.FullName
        }
    }
    throw "The build did not produce an executable for $Platform."
}

if (Get-Process -Name "opentools" -ErrorAction SilentlyContinue) {
    Write-Warning "OpenTools is running. Close it before copying the new build."
}

Write-Host "OpenTools release build" -ForegroundColor White
Write-Host "  platform : $Platform"
Write-Host "  version  : $Version"
Write-Host "  output   : $targetDir"

if (-not $SkipChecks) {
    $strayTools = @()
    foreach ($relative in @("tools", "cmd\plugins")) {
        $candidate = Join-Path $root $relative
        if (Test-Path -LiteralPath $candidate) {
            $strayTools += $relative
        }
    }
    if ($strayTools.Count -gt 0) {
        throw "The core repository must not contain business tools: $($strayTools -join ', ')."
    }
    Invoke-Checked { Set-Location $root; go test ./... } "Go checks"
    Invoke-Checked { Set-Location $root; go vet ./... } "Go vet"
}

Invoke-Checked {
    Set-Location $root
    $buildArgs = @("tool", "mygo", "build", "-platform", $Platform)
    & go @buildArgs
} "MyGo build"

Invoke-Checked {
    Set-Location $root
    if (Test-Path -LiteralPath $pluginRoot) {
        Remove-Item -LiteralPath $pluginRoot -Recurse -Force
    }
    New-Item -ItemType Directory -Path $pluginRoot -Force | Out-Null
    $goos, $goarch = $Platform.Split("/")
    foreach ($plugin in $pluginList) {
        $env:GOOS = $goos
        $env:GOARCH = $goarch
        $env:CGO_ENABLED = "0"
        $output = Join-Path $pluginRoot ($plugin.Output + $extension)
        Push-Location $plugin.Repository
        try {
            & go build -trimpath -ldflags="-s -w" -o $output "./cmd/plugins/$($plugin.Name)"
        } finally {
            Pop-Location
        }
        if ($LASTEXITCODE -ne 0) {
            throw "Plugin build failed: $($plugin.Name) ($($plugin.Repository))"
        }
    }
    Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED -ErrorAction SilentlyContinue
} "Plugin builds"

$executable = Resolve-Executable
if (Test-Path -LiteralPath $targetDir) {
    if (-not $Force) {
        throw "Output directory already exists: $targetDir"
    }
    Remove-Item -LiteralPath $targetDir -Recurse -Force
}
New-Item -ItemType Directory -Path $targetDir -Force | Out-Null

$artifactName = if ($Platform.StartsWith("windows/")) { "OpenTools.exe" } else { Split-Path $executable -Leaf }
Copy-Item -LiteralPath $executable -Destination (Join-Path $targetDir $artifactName) -Force

$storeDir = Join-Path $targetDir "store"
$packagesDir = Join-Path $storeDir "packages"
New-Item -ItemType Directory -Path $packagesDir -Force | Out-Null
$catalogTools = @()

foreach ($plugin in $pluginList) {
    $pluginExe = $plugin.Output + $extension
    $source = Join-Path $pluginRoot $pluginExe
    if (-not (Test-Path -LiteralPath $source -PathType Leaf)) {
        throw "Plugin executable missing: $source"
    }
    $description = & $source describe
    if ($LASTEXITCODE -ne 0) {
        throw "Plugin describe failed: $($plugin.Output)"
    }
    $definition = $description | ConvertFrom-Json
    if (-not $definition.version) {
        throw "Plugin $($plugin.Output) did not report a version"
    }
    $packageName = "$($plugin.Output)-$($definition.version).zip"
    $packageDir = Join-Path $packagesDir $plugin.Output
    $manifest = [ordered]@{
        schemaVersion = 1
        protocolVersion = 2
        platform = $Platform
        executable = $pluginExe
        author = $plugin.Author
        homepage = $plugin.Homepage
        license = "MIT"
        definition = $definition
    }
    $stagingDir = Join-Path $pluginRoot ("package-" + $plugin.Output)
    if (Test-Path -LiteralPath $stagingDir) {
        Remove-Item -LiteralPath $stagingDir -Recurse -Force
    }
    New-Item -ItemType Directory -Path $stagingDir -Force | Out-Null
    $packageExe = Join-Path $stagingDir $pluginExe
    Copy-Item -LiteralPath $source -Destination $packageExe -Force
    $manifest | ConvertTo-Json -Depth 100 | Set-Content -LiteralPath (Join-Path $stagingDir "manifest.json") -Encoding utf8NoBOM
    $archive = Join-Path $packagesDir $packageName
    Add-Type -AssemblyName System.IO.Compression
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $zip = [System.IO.Compression.ZipFile]::Open(
        $archive,
        [System.IO.Compression.ZipArchiveMode]::Create
    )
    try {
        foreach ($name in @("manifest.json", $pluginExe)) {
            [System.IO.Compression.ZipFileExtensions]::CreateEntryFromFile(
                $zip,
                (Join-Path $stagingDir $name),
                $name,
                [System.IO.Compression.CompressionLevel]::Optimal
            ) | Out-Null
        }
    } finally {
        $zip.Dispose()
    }
    $hash = (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash.ToLowerInvariant()
    $size = (Get-Item -LiteralPath $archive).Length
    $catalogTools += [ordered]@{
        definition = $definition
        platform = $Platform
        protocolVersion = 2
        author = $plugin.Author
        homepage = $plugin.Homepage
        license = "MIT"
        downloadUrl = "packages/$packageName"
        sha256 = $hash
        size = $size
        compatible = $true
        installed = $false
        updateAvailable = $false
    }
}

$catalog = [ordered]@{
    schemaVersion = 1
    generatedAt = (Get-Date).ToUniversalTime().ToString("o")
    tools = $catalogTools
}
$catalog | ConvertTo-Json -Depth 100 | Set-Content -LiteralPath (Join-Path $storeDir "catalog.json") -Encoding utf8NoBOM

$arch = $Platform.Split("/")[1]
$installerName = "$configSlug-setup-$Version-$arch.exe"
$installer = Join-Path $root "build\$safePlatform\$installerName"
if (-not $ExeOnly -and $Platform.StartsWith("windows/")) {
    # A missing installer is a broken release, not a reason to ship exe-only.
    if (-not (Test-Path -LiteralPath $installer -PathType Leaf)) {
        throw "Installer missing: $installer. Run 'go tool mygo build -platform $Platform' without -skip-installer."
    }
    Copy-Item -LiteralPath $installer -Destination $targetDir -Force
}

$hash = Get-FileHash -LiteralPath (Join-Path $targetDir $artifactName) -Algorithm SHA256
$manifest = @(
    "OpenTools $Version"
    "Platform: $Platform"
    "Built: $(Get-Date -Format o)"
    "SHA256: $($hash.Hash)"
    "Core contains no bundled business tools."
    "Plugins: store/catalog.json"
) -join [Environment]::NewLine
Set-Content -LiteralPath (Join-Path $targetDir "BUILD.txt") -Value $manifest -Encoding utf8NoBOM

Write-Host "`nBuild complete." -ForegroundColor Green
Get-ChildItem -LiteralPath $targetDir | Select-Object Name, Length | Format-Table -AutoSize
Write-Output $targetDir
