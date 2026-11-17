<#
.SYNOPSIS
  Run Go benchmarks and print the v0.0.1 performance targets.
.DESCRIPTION
  Targets (requirements AC-06 / NFR-PERF):
    - 100k messages search p95 <= 300ms, Chinese 2-char p95 <= 150ms
    - message searchable <= 100ms after write
    - cold start <= 2s
  Seed corpora from tests/bench (added as R1 search lands); acceptance benchmarks
  run with -AC.
.EXAMPLE
  .\scripts\benchmark.ps1
  .\scripts\benchmark.ps1 -AC -Pattern BenchmarkSearch
#>
[CmdletBinding()]
param(
    [string]$Pattern = ".",
    [string]$BenchTime = "1x",
    [switch]$AC
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
Push-Location $root
try {
    Write-Host "Benchmarks: pattern=$Pattern benchtime=$BenchTime"
    Write-Host "Targets: 100k msgs p95<=300ms (zh 2-char<=150ms), index fresh<=100ms, cold start<=2s"
    if ($AC) {
        $goArgs = @("test", "-tags=acceptance", "-run", "^$", "-bench", $Pattern, "-benchmem", "-benchtime", $BenchTime, "./tests/ac")
    } else {
        $goArgs = @("test", "-run", "^$", "-bench", $Pattern, "-benchmem", "-benchtime", $BenchTime, "./...")
    }
    go @goArgs
    exit $LASTEXITCODE
}
finally {
    Pop-Location
}
