param(
    [string]$Version = "0.1.0-test",
    [string]$Commit = "unknown",
    [string]$OutputDir = "dist/cross"
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
$outputRoot = [IO.Path]::GetFullPath((Join-Path $root $OutputDir))
New-Item -ItemType Directory -Force -Path $outputRoot | Out-Null
$builtAt = [DateTime]::UtcNow.ToString("yyyy-MM-ddTHH:mm:ssZ")
$linkFlags = "-w -s -X ai-gateway/internal/version.Version=$Version -X ai-gateway/internal/version.Commit=$Commit -X ai-gateway/internal/version.BuildTime=$builtAt"

$oldGOOS = $env:GOOS
$oldGOARCH = $env:GOARCH
$oldCGO = $env:CGO_ENABLED
Push-Location $root
try {
    $targets = @(
        @{ OS = "linux"; Arch = "amd64" },
        @{ OS = "linux"; Arch = "arm64" },
        @{ OS = "darwin"; Arch = "amd64" },
        @{ OS = "darwin"; Arch = "arm64" }
    )
    foreach ($target in $targets) {
        $env:GOOS = $target.OS
        $env:GOARCH = $target.Arch
        $env:CGO_ENABLED = "0"
        $name = "ai-gateway-$Version-$($target.OS)-$($target.Arch)"
        go build -trimpath -buildvcs=false -ldflags $linkFlags -o (Join-Path $outputRoot $name) ./cmd/gateway
        if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
        Write-Host "Built $name"
    }
    Write-Host "SKIPPED: Wails desktop cross-builds require the target platform's native WebView/GTK toolchain; run build-desktop.ps1 natively on macOS or Linux."
}
finally {
    $env:GOOS = $oldGOOS
    $env:GOARCH = $oldGOARCH
    $env:CGO_ENABLED = $oldCGO
    Pop-Location
}
