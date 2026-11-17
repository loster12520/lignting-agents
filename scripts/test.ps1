<#
.SYNOPSIS
  Run the default quality gate: gofmt check + go vet + go test ./...
.DESCRIPTION
  P1 gate for R0-R9. The acceptance suite is separate (scripts/ac.ps1) because it
  is slower and may need a DeepSeek key.
.EXAMPLE
  .\scripts\test.ps1
  .\scripts\test.ps1 -Short -NoVet
#>
[CmdletBinding()]
param(
    [switch]$Short,
    [switch]$NoVet,
    [switch]$NoFmt
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
Push-Location $root
try {
    if (-not $NoFmt) {
        $gofmt = Join-Path (Join-Path (go env GOROOT) "bin") "gofmt"
        Write-Host "== gofmt =="
        $unformatted = @(& $gofmt -l . 2>$null)
        if ($LASTEXITCODE -ne 0) {
            Write-Host "gofmt failed to run (GOROOT=$(go env GOROOT))" -ForegroundColor Red
            exit 1
        }
        if ($unformatted.Count -gt 0) {
            Write-Host "These files need gofmt -w:" -ForegroundColor Red
            $unformatted | ForEach-Object { Write-Host "  $_" }
            exit 1
        }
    }

    if (-not $NoVet) {
        Write-Host "== go vet =="
        go vet ./...
        if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    }

    Write-Host "== go test =="
    if ($Short) { go test -short ./... } else { go test ./... }
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

    Write-Host "PASS: fmt + vet + unit/integration tests" -ForegroundColor Green
    exit 0
}
finally {
    Pop-Location
}
