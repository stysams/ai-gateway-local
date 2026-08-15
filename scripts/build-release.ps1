param(
    [string]$Version = "0.1.0-test",
    [string]$Commit = "unknown",
    [string]$OutputDir = "dist"
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
$outputRoot = [IO.Path]::GetFullPath((Join-Path $root $OutputDir))
$rootPrefix = [IO.Path]::GetFullPath($root).TrimEnd('\') + '\'
if (-not $outputRoot.StartsWith($rootPrefix, [StringComparison]::OrdinalIgnoreCase)) {
    throw "OutputDir must resolve inside the repository: $outputRoot"
}
$packageName = "ai-gateway-$Version-windows-amd64"
$stage = Join-Path $outputRoot $packageName
$archive = Join-Path $outputRoot ($packageName + ".zip")

Push-Location $root
try {
    npm --prefix desktop ci
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    npm --prefix desktop run build
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

    if (Test-Path -LiteralPath $stage) { Remove-Item -LiteralPath $stage -Recurse -Force }
    New-Item -ItemType Directory -Force -Path (Join-Path $stage "docs") | Out-Null
    $builtAt = [DateTime]::UtcNow.ToString("yyyy-MM-ddTHH:mm:ssZ")
    $common = "-w -s -X ai-gateway/internal/version.Version=$Version -X ai-gateway/internal/version.Commit=$Commit -X ai-gateway/internal/version.BuildTime=$builtAt"
    go build -trimpath -buildvcs=false -ldflags $common -o (Join-Path $stage "ai-gateway.exe") ./cmd/gateway
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    go build -tags production -trimpath -buildvcs=false -ldflags ($common + " -H windowsgui") -o (Join-Path $stage "ai-gateway-desktop.exe") ./cmd/desktop
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

    Copy-Item -LiteralPath "README.md", "LICENSE" -Destination $stage
    Copy-Item -LiteralPath "docs\install.md" -Destination (Join-Path $stage "docs")
    Compress-Archive -LiteralPath $stage -DestinationPath $archive -Force
    Write-Host "Built $archive"
}
finally {
    Pop-Location
}
