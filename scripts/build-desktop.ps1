param(
    [string]$Version = "0.1.0-test",
    [string]$Commit = "unknown",
    [string]$Output = ""
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
$isWindowsBuild = $IsWindows -or $env:OS -eq "Windows_NT"
if (-not $Output) {
    $Output = if ($isWindowsBuild) { "bin/ai-gateway-desktop.exe" } else { "bin/ai-gateway-desktop" }
}
$outputPath = [IO.Path]::GetFullPath((Join-Path $root $Output))
$outputDir = Split-Path -Parent $outputPath

Push-Location $root
try {
    npm --prefix desktop ci
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    npm --prefix desktop run build
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

    New-Item -ItemType Directory -Force -Path $outputDir | Out-Null
    $builtAt = [DateTime]::UtcNow.ToString("yyyy-MM-ddTHH:mm:ssZ")
    $linkFlags = "-w -s -X ai-gateway/internal/version.Version=$Version -X ai-gateway/internal/version.Commit=$Commit -X ai-gateway/internal/version.BuildTime=$builtAt"
    if ($isWindowsBuild) {
        $linkFlags += " -H windowsgui"
    }
    go build -tags production -trimpath -buildvcs=false -ldflags $linkFlags -o $outputPath ./cmd/desktop
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    Write-Host "Built $outputPath"
}
finally {
    Pop-Location
}
