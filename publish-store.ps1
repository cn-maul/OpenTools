#!/usr/bin/env pwsh
#Requires -Version 7.0

[CmdletBinding()]
param(
    [ValidateSet("windows/amd64")]
    [string]$Platform = "windows/amd64",

    # Gitee personal access token. May also be supplied via OPENTOOLS_GITEE_TOKEN.
    [string]$GiteeToken = "",

    # Gitee repository, e.g. dogge2333/opentools-store.
    [string]$GiteeRepo = "dogge2333/opentools-store",

    # Branch to publish to.
    [string]$Branch = "master",

    # Skip uploading to Gitee; only generate the local store directory.
    [switch]$SkipUpload,

    # Skip Go builds (reuse existing executables under build\plugins).
    [switch]$SkipBuild
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
$PSNativeCommandUseErrorActionPreference = $true

$root = $PSScriptRoot
$outDir = Join-Path $root "store"
$packagesDir = Join-Path $outDir "packages"
$pluginRoot = Join-Path $root "build\plugins"

if (-not $GiteeToken) {
    $GiteeToken = $env:OPENTOOLS_GITEE_TOKEN
}
if (-not $SkipUpload -and -not $GiteeToken) {
    throw "Gitee token required: pass -GiteeToken or set OPENTOOLS_GITEE_TOKEN."
}

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

Write-Host "OpenTools store publish" -ForegroundColor White
Write-Host "  platform : $Platform"
Write-Host "  repo     : $(if ($SkipUpload) { '(local only)' } else { $GiteeRepo })"
Write-Host "  output   : $outDir"

if (-not $SkipBuild) {
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
            @{ Name = "systemconfig"; Output = "system-inventory" }
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
}

# Package every plugin: run describe to build the manifest, zip exe + manifest.
New-Item -ItemType Directory -Path $packagesDir -Force | Out-Null
$toolSpecs = @(
    @{ Name = "network-config"; Executable = "network-config.exe" },
    @{ Name = "lan-inventory"; Executable = "lan-inventory.exe" },
    @{ Name = "system-inventory"; Executable = "system-inventory.exe" }
)
$catalogTools = @()
Add-Type -AssemblyName System.IO.Compression
Add-Type -AssemblyName System.IO.Compression.FileSystem

foreach ($spec in $toolSpecs) {
    $source = Join-Path $pluginRoot $spec.Executable
    if (-not (Test-Path -LiteralPath $source -PathType Leaf)) {
        throw "Plugin executable missing: $source (build first or drop -SkipBuild)"
    }
    $description = & $source describe | ConvertFrom-Json
    if ($LASTEXITCODE -ne 0) {
        throw "Plugin describe failed: $($spec.Name)"
    }
    $version = $description.version
    if (-not $version) {
        throw "Plugin $($spec.Name) did not report a version"
    }
    $packageName = "$($spec.Name)-$version.zip"
    $manifest = [ordered]@{
        schemaVersion = 1
        protocolVersion = 2
        platform = $Platform
        executable = $spec.Executable
        author = "OpenTools"
        homepage = "https://gitee.com/$GiteeRepo"
        license = "MIT"
        definition = $description
    }
    $stagingDir = Join-Path $pluginRoot ("package-" + $spec.Name)
    if (Test-Path -LiteralPath $stagingDir) {
        Remove-Item -LiteralPath $stagingDir -Recurse -Force
    }
    New-Item -ItemType Directory -Path $stagingDir -Force | Out-Null
    Copy-Item -LiteralPath $source -Destination (Join-Path $stagingDir $spec.Executable) -Force
    $manifest | ConvertTo-Json -Depth 100 |
        Set-Content -LiteralPath (Join-Path $stagingDir "manifest.json") -Encoding utf8NoBOM
    $archive = Join-Path $packagesDir $packageName
    if (Test-Path -LiteralPath $archive) {
        Remove-Item -LiteralPath $archive -Force
    }
    $zip = [System.IO.Compression.ZipFile]::Open($archive, [System.IO.Compression.ZipArchiveMode]::Create)
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
        definition = $description
        platform = $Platform
        protocolVersion = 2
        author = "OpenTools"
        homepage = "https://gitee.com/$GiteeRepo"
        license = "MIT"
        downloadUrl = "packages/$packageName"
        sha256 = $hash
        size = $size
        compatible = $true
        installed = $false
        updateAvailable = $false
    }
    Write-Host "  packaged $($packageName) ($size bytes)"
}

$catalog = [ordered]@{
    schemaVersion = 1
    generatedAt = (Get-Date).ToUniversalTime().ToString("o")
    tools = $catalogTools
}
$catalogPath = Join-Path $outDir "catalog.json"
$catalog | ConvertTo-Json -Depth 100 |
    Set-Content -LiteralPath $catalogPath -Encoding utf8NoBOM
Write-Host "`nLocal store generated: $outDir" -ForegroundColor Green

if ($SkipUpload) {
    Write-Host "Upload skipped (-SkipUpload)." -ForegroundColor Yellow
    return
}

# Publish to Gitee via the contents API. Creating a file uses POST; updating
# an existing file requires PUT with the current blob SHA.
function Publish-GiteeFile {
    param(
        [Parameter(Mandatory)]
        [string]$Repo,
        [Parameter(Mandatory)]
        [string]$Token,
        [Parameter(Mandatory)]
        [string]$Branch,
        [Parameter(Mandatory)]
        [string]$Path,
        [Parameter(Mandatory)]
        [string]$LocalFile,
        [Parameter(Mandatory)]
        [string]$Message
    )
    $content = [Convert]::ToBase64String([IO.File]::ReadAllBytes($LocalFile))
    $apiUrl = "https://gitee.com/api/v5/repos/$Repo/contents/$Path"
    $existing = $null
    $getUrl = "https://gitee.com/api/v5/repos/$Repo/contents/$Path" + "?access_token=$Token&ref=$Branch"
    $getResp = curl.exe -s -w "`n%{http_code}" --noproxy "*" --max-time 30 $getUrl
    $lines = $getResp -split "`n"
    $statusCode = $lines[-1]
    if ($statusCode -eq "200") {
        $existing = ($lines[0..($lines.Count - 2)] -join "`n") | ConvertFrom-Json
    }
    $payload = @{
        access_token = $Token
        content = $content
        branch = $Branch
        message = $Message
    }
    if ($existing) {
        $payload.sha = $existing.sha
        $method = "PUT"
    } else {
        $method = "POST"
    }
    $json = $payload | ConvertTo-Json -Depth 3
    $tempJson = New-TemporaryFile
    $json | Set-Content -LiteralPath $tempJson -Encoding utf8NoBOM
    try {
        $resp = curl.exe -s -w "`n%{http_code}" -X $method --noproxy "*" --max-time 120 `
            -H "Content-Type: application/json" --data-binary "@$tempJson" $apiUrl
        $lines = $resp -split "`n"
        $statusCode = $lines[-1]
        $body = $lines[0..($lines.Count - 2)] -join "`n"
        if ($statusCode -ne "200" -and $statusCode -ne "201") {
            throw "Gitee upload failed for $Path (HTTP $statusCode): $body"
        }
        if ($existing) {
            Write-Host "  updated $Path"
        } else {
            Write-Host "  created $Path"
        }
    } finally {
        Remove-Item -LiteralPath $tempJson -Force -ErrorAction SilentlyContinue
    }
}

Write-Host "`nPublishing to gitee.com/$GiteeRepo ($Branch)" -ForegroundColor Cyan
Publish-GiteeFile -Repo $GiteeRepo -Token $GiteeToken -Branch $Branch `
    -Path "catalog.json" -LocalFile $catalogPath -Message "publish catalog"
foreach ($spec in $toolSpecs) {
    $description = & (Join-Path $pluginRoot $spec.Executable) describe | ConvertFrom-Json
    $packageName = "$($spec.Name)-$($description.version).zip"
    Publish-GiteeFile -Repo $GiteeRepo -Token $GiteeToken -Branch $Branch `
        -Path "packages/$packageName" -LocalFile (Join-Path $packagesDir $packageName) `
        -Message "publish $packageName"
}

Write-Host "`nPublished. Store URL:" -ForegroundColor Green
Write-Host "  https://gitee.com/$GiteeRepo/raw/$Branch/catalog.json"
