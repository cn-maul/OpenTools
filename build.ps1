#!/usr/bin/env pwsh
#Requires -Version 7.0

[CmdletBinding()]
param(
    [ValidateSet("windows/amd64", "windows/arm64", "darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64")]
    [string]$Platform = "windows/amd64",

    [string]$Version = "0.3.3",

    [string]$OutRoot = (Join-Path $PSScriptRoot "release"),

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
$configVersion = (
    Select-String -LiteralPath (Join-Path $root "mygo.config.ts") -Pattern 'version:\s*"([^"]+)"' |
        Select-Object -First 1
).Matches.Groups[1].Value
if ($Version -ne $configVersion) {
    throw "Version mismatch: -Version $Version, mygo.config.ts $configVersion."
}

$outRootPath = [System.IO.Path]::GetFullPath($OutRoot)
$stamp = Get-Date -Format "yyyyMMdd-HHmmss"
$safePlatform = $Platform.Replace("/", "-")
$baseName = "$safePlatform-$Version"
$targetDir = Join-Path (Join-Path $outRootPath $baseName) $stamp
$pluginRoot = Join-Path $root "build\plugins"

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
    Invoke-Checked { Set-Location $root; go test ./... } "Go checks"
    Invoke-Checked { Set-Location $root; go vet ./... } "Go vet"
    Invoke-Checked { Set-Location $root; npm run typecheck } "TypeScript checks"
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
    $extension = if ($Platform.StartsWith("windows/")) { ".exe" } else { "" }
    $goos, $goarch = $Platform.Split("/")
    foreach ($plugin in @(
        @{ Name = "networkconfig"; Output = "network-config" },
        @{ Name = "laninventory"; Output = "lan-inventory" },
        @{ Name = "systemconfig"; Output = "system-inventory" },
        @{ Name = "printercheck"; Output = "printer-check" }
    )) {
        $env:GOOS = $goos
        $env:GOARCH = $goarch
        $env:CGO_ENABLED = "0"
        $output = Join-Path $pluginRoot ($plugin.Output + $extension)
        & go build -trimpath -ldflags="-s -w" -o $output "./cmd/plugins/$($plugin.Name)"
        if ($LASTEXITCODE -ne 0) {
            throw "Plugin build failed: $($plugin.Name)"
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
$pluginSpecs = @(
    @{
        Name = "network-config"
        Executable = "network-config.exe"
        Package = "network-config-1.2.0.zip"
        Author = "OpenTools"
        Homepage = "https://github.com/"
    },
    @{
        Name = "lan-inventory"
        Executable = "lan-inventory.exe"
        Package = "lan-inventory-1.3.0.zip"
        Author = "OpenTools"
        Homepage = "https://github.com/"
    },
    @{
        Name = "system-inventory"
        Executable = "system-inventory.exe"
        Package = "system-inventory-1.3.0.zip"
        Author = "OpenTools"
        Homepage = "https://github.com/"
    },
    @{
        Name = "printer-check"
        Executable = "printer-check.exe"
        Package = "printer-check-1.1.0.zip"
        Author = "OpenTools"
        Homepage = "https://github.com/"
    }
)

foreach ($spec in $pluginSpecs) {
    $source = Join-Path $pluginRoot $spec.Executable
    if (-not (Test-Path -LiteralPath $source -PathType Leaf)) {
        throw "Plugin executable missing: $source"
    }
    $description = & $source describe
    if ($LASTEXITCODE -ne 0) {
        throw "Plugin describe failed: $($spec.Name)"
    }
    $definition = $description | ConvertFrom-Json
    $packageDir = Join-Path $packagesDir $spec.Name
    $manifest = [ordered]@{
        schemaVersion = 1
        protocolVersion = 2
        platform = $Platform
        executable = $spec.Executable
        author = $spec.Author
        homepage = $spec.Homepage
        license = "MIT"
        definition = $definition
    }
    $stagingDir = Join-Path $pluginRoot ("package-" + $spec.Name)
    if (Test-Path -LiteralPath $stagingDir) {
        Remove-Item -LiteralPath $stagingDir -Recurse -Force
    }
    New-Item -ItemType Directory -Path $stagingDir -Force | Out-Null
    $packageExe = Join-Path $stagingDir $spec.Executable
    Copy-Item -LiteralPath $source -Destination $packageExe -Force
    $manifest | ConvertTo-Json -Depth 100 | Set-Content -LiteralPath (Join-Path $stagingDir "manifest.json") -Encoding utf8NoBOM
    $archive = Join-Path $packagesDir $spec.Package
    Add-Type -AssemblyName System.IO.Compression
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $zip = [System.IO.Compression.ZipFile]::Open(
        $archive,
        [System.IO.Compression.ZipArchiveMode]::Create
    )
    try {
        foreach ($name in @("manifest.json", $spec.Executable)) {
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
        author = $spec.Author
        homepage = $spec.Homepage
        license = "MIT"
        downloadUrl = "packages/$($spec.Package)"
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

$installer = Join-Path $root "build\$safePlatform\OpenTools Setup $Version amd64.exe"
if (-not $ExeOnly -and $Platform -eq "windows/amd64" -and (Test-Path -LiteralPath $installer -PathType Leaf)) {
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
