# Smoke-test the GovDoc plugin over the real JSON-RPC wire protocol.
#
# The CLI deletes a job's work directory when the job ends, so the exported
# .docx cannot be inspected afterwards. This drives the plugin process directly:
# initialize, tool/run, then read the artifact back from the work directory.
param(
    [Parameter(Mandatory)] [string]$Exe,
    [Parameter(Mandatory)] [string]$Source,
    [Parameter(Mandatory)] [string]$WorkDir,
    [string]$Endpoint = "http://127.0.0.1:8899/v1",
    [string]$Model = "stub-model",
    [string]$Profile = "govdoc"
)

$ErrorActionPreference = "Stop"
if (Test-Path -LiteralPath $WorkDir) {
    Get-ChildItem -LiteralPath $WorkDir -Force | Remove-Item -Recurse -Force
} else {
    New-Item -ItemType Directory -Path $WorkDir -Force | Out-Null
}

$init = @{
    jsonrpc = "2.0"; id = 1; method = "initialize"
    params  = @{ protocolVersion = 2; toolId = "document.govdoc-format"; workDir = $WorkDir; dataDir = $WorkDir }
} | ConvertTo-Json -Depth 6 -Compress

$run = @{
    jsonrpc = "2.0"; id = 2; method = "tool/run"
    params  = @{
        input = @{
            source = $Source; endpoint = $Endpoint; api_key = "stub-key"
            model = $Model; profile = $Profile; direction = "下行文"
        }
    }
} | ConvertTo-Json -Depth 6 -Compress

$response = @($init, $run) | & $Exe rpc
$result = $response | ForEach-Object { $_ | ConvertFrom-Json } | Where-Object { $_.id -eq 2 }
if (-not $result) { throw "no tool/run response" }
if ($result.error) { throw "tool error: $($result.error.message)" }

$result.result | ConvertTo-Json -Depth 10
Get-ChildItem -LiteralPath $WorkDir -File | Select-Object Name, Length
