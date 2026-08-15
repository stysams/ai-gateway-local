# ai-gateway unified verification (docs/v1-scheme.md §16.3).
# Runs the full Go verification suite, then the desktop suite once the
# desktop slice exists. Components that cannot run on this machine (or do not
# exist yet) are printed as explicit SKIPPED lines, never silently omitted,
# and a SKIPPED step is never reported as PASSED.

$ErrorActionPreference = "Stop"

function Invoke-Step {
    param([string]$Name, [scriptblock]$Body)
    Write-Host ""
    Write-Host "==> $Name"
    & $Body
    if ($LASTEXITCODE -ne 0) {
        Write-Host "FAILED: $Name (exit $LASTEXITCODE)" -ForegroundColor Red
        exit $LASTEXITCODE
    }
    Write-Host "PASSED: $Name" -ForegroundColor Green
}

# Invoke-RaceTest runs `go test -race` only when the race detector can
# actually work: cgo must be enabled AND the compiler named by `go env CC`
# must be executable. go env CC returns either a bare command name (resolved
# through PATH) or an absolute path. Anything else is an explicit SKIPPED;
# a failing race run is a hard FAILED, never masked.
function Invoke-RaceTest {
    $cgo = (& go env CGO_ENABLED).Trim()
    $cc = (& go env CC).Trim()

    $ccOk = $false
    if ($cc) {
        if (Split-Path -IsAbsolute $cc) {
            $ccOk = Test-Path -LiteralPath $cc
        } else {
            $ccOk = $null -ne (Get-Command $cc -ErrorAction SilentlyContinue)
        }
    }

    if ($cgo -ne "1" -or -not $ccOk) {
        Write-Host "SKIPPED: go test -race requires cgo enabled (CGO_ENABLED=$cgo) and an executable C compiler (go env CC=$cc); install a C toolchain (e.g. mingw-w64 gcc) to enable the race detector"
        return
    }

    go test -race ./...
    if ($LASTEXITCODE -ne 0) {
        Write-Host "FAILED: go test -race ./... (exit $LASTEXITCODE)" -ForegroundColor Red
        exit $LASTEXITCODE
    }
    Write-Host "PASSED: go test -race ./..." -ForegroundColor Green
}

# Project root is two levels up from this script (scripts/verify.ps1).
$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
Push-Location $root
try {
    Invoke-Step "go fmt ./..." { go fmt ./... }
    Invoke-Step "go vet ./..." { go vet ./... }
    Invoke-Step "go test ./..." { go test ./... }

    Write-Host ""
    Write-Host "==> go test -race ./..."
    Invoke-RaceTest

    if (Test-Path (Join-Path $root "desktop\package.json")) {
        Invoke-Step "npm --prefix desktop ci" { npm --prefix desktop ci }
        Invoke-Step "npm --prefix desktop run lint" { npm --prefix desktop run lint }
        Invoke-Step "npm --prefix desktop run test" { npm --prefix desktop run test }
        Invoke-Step "npm --prefix desktop run build" { npm --prefix desktop run build }
        Invoke-Step "npm --prefix desktop run test:e2e" { npm --prefix desktop run test:e2e }
    } else {
        Write-Host ""
        Write-Host "==> desktop suite"
        Write-Host "SKIPPED: desktop package.json is missing; no desktop suite is available"
    }

    Write-Host ""
    Write-Host "All verification steps finished." -ForegroundColor Green
}
finally {
    Pop-Location
}
