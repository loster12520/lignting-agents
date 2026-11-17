<#
.SYNOPSIS
  Run the 15 acceptance tests (tests/ac, build tag acceptance).
.DESCRIPTION
  Default model mode is mock (deterministic, CI-friendly). Release runs must use
  -Mode deepseek with DEEPSEEK_API_KEY set; -Strict turns "endpoint missing" skips
  into failures so a skeleton cannot pass silently.
.EXAMPLE
  .\scripts\ac.ps1
  .\scripts\ac.ps1 -Run TestAC01 -Strict
  .\scripts\ac.ps1 -Mode deepseek -OutFile .\ac-report.log
#>
[CmdletBinding()]
param(
    [string]$Run = "",
    [ValidateSet("mock", "deepseek")]
    [string]$Mode = "mock",
    [switch]$Strict,
    [string]$OutFile = ""
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
Push-Location $root
try {
    $env:AC_MODEL_MODE = $Mode
    if ($Strict) { $env:AC_STRICT = "1" } else { Remove-Item Env:AC_STRICT -ErrorAction SilentlyContinue }
    if ($Mode -eq "deepseek" -and -not $env:DEEPSEEK_API_KEY) {
        Write-Host "WARN: -Mode deepseek without DEEPSEEK_API_KEY; model-dependent ACs must fail with a readable error." -ForegroundColor Yellow
    }

    $goArgs = @("test", "-tags=acceptance", "-count=1", "-v", "-timeout", "30m", "./tests/ac")
    if ($Run) { $goArgs += @("-run", $Run) }

    Write-Host "AC run: mode=$Mode strict=$($Strict.IsPresent) run=$(if ($Run) { $Run } else { 'all' })"
    if ($OutFile) {
        go @goArgs 2>&1 | Tee-Object -FilePath $OutFile
    } else {
        go @goArgs
    }
    $code = $LASTEXITCODE
    Write-Host "AC exit=$code (report: $(if ($OutFile) { $OutFile } else { 'stdout' }))"
    exit $code
}
finally {
    Pop-Location
}
