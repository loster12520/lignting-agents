<#
.SYNOPSIS
  Start Lignting in development mode (single binary, loopback only).
.EXAMPLE
  .\scripts\dev.ps1 -Mock
  .\scripts\dev.ps1 -Port 8788 -DataDir .lignting-dev2
#>
[CmdletBinding()]
param(
    [string]$DataDir = ".lignting-dev",
    [int]$Port = 8787,
    [string]$Addr = "",
    [switch]$Mock,
    [switch]$Open
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
Push-Location $root
try {
    if (-not $Addr) { $Addr = "127.0.0.1:$Port" }
    $goArgs = @("run", "./cmd/lignting", "--data-dir", $DataDir, "--addr", $Addr)
    if ($Mock) { $goArgs += "--mock-model" }
    Write-Host "Lignting dev server: http://$Addr"
    Write-Host "Data dir:            $DataDir (workspace under it)"
    Write-Host "Model mode:          $(if ($Mock) { 'mock' } else { 'deepseek (needs key in settings)' })"
    Write-Host "Press Ctrl+C to stop."
    if ($Open) {
        Start-Job -ScriptBlock { param($u) Start-Sleep -Seconds 2; Start-Process $u } -ArgumentList "http://$Addr" | Out-Null
    }
    go @goArgs
    exit $LASTEXITCODE
}
finally {
    Pop-Location
}
