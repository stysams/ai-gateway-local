param(
    [string]$Architecture = "amd64"
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
$pngPath = Join-Path $root "desktop\public\icons\appicon.png"
$icoPath = Join-Path $root "build\windows\icon.ico"
$manifestPath = Join-Path $root "build\windows\app.manifest"
$resourcePath = Join-Path $root "cmd\desktop\rsrc_windows_$Architecture.syso"

New-Item -ItemType Directory -Force -Path (Split-Path -Parent $pngPath), (Split-Path -Parent $icoPath) | Out-Null
Add-Type -AssemblyName System.Drawing

function New-RoundedRectanglePath {
    param([float]$X, [float]$Y, [float]$Width, [float]$Height, [float]$Radius)

    $path = [System.Drawing.Drawing2D.GraphicsPath]::new()
    $diameter = $Radius * 2
    $path.AddArc($X, $Y, $diameter, $diameter, 180, 90)
    $path.AddArc($X + $Width - $diameter, $Y, $diameter, $diameter, 270, 90)
    $path.AddArc($X + $Width - $diameter, $Y + $Height - $diameter, $diameter, $diameter, 0, 90)
    $path.AddArc($X, $Y + $Height - $diameter, $diameter, $diameter, 90, 90)
    $path.CloseFigure()
    return $path
}

$bitmap = [System.Drawing.Bitmap]::new(1024, 1024, [System.Drawing.Imaging.PixelFormat]::Format32bppArgb)
$graphics = [System.Drawing.Graphics]::FromImage($bitmap)
$backgroundBrush = [System.Drawing.SolidBrush]::new([System.Drawing.Color]::FromArgb(255, 17, 17, 17))
$glyphPen = [System.Drawing.Pen]::new([System.Drawing.Color]::White, 62)
$channelPen = [System.Drawing.Pen]::new([System.Drawing.Color]::White, 58)
$flowPen = [System.Drawing.Pen]::new([System.Drawing.Color]::White, 54)
$backgroundPath = New-RoundedRectanglePath 56 56 912 912 192
$gatewayPath = New-RoundedRectanglePath 298 240 428 544 88

try {
    $graphics.SmoothingMode = [System.Drawing.Drawing2D.SmoothingMode]::AntiAlias
    $graphics.CompositingQuality = [System.Drawing.Drawing2D.CompositingQuality]::HighQuality
    $graphics.PixelOffsetMode = [System.Drawing.Drawing2D.PixelOffsetMode]::HighQuality
    $graphics.Clear([System.Drawing.Color]::Transparent)
    $graphics.FillPath($backgroundBrush, $backgroundPath)

    foreach ($pen in @($glyphPen, $channelPen, $flowPen)) {
        $pen.StartCap = [System.Drawing.Drawing2D.LineCap]::Round
        $pen.EndCap = [System.Drawing.Drawing2D.LineCap]::Round
        $pen.LineJoin = [System.Drawing.Drawing2D.LineJoin]::Round
    }

    $graphics.DrawPath($glyphPen, $gatewayPath)
    $graphics.DrawLine($channelPen, 210, 340, 210, 684)
    $graphics.DrawLine($channelPen, 814, 340, 814, 684)
    $graphics.DrawLine($flowPen, 390, 384, 634, 384)
    $graphics.DrawLine($flowPen, 390, 512, 634, 512)
    $graphics.DrawLine($flowPen, 390, 640, 554, 640)
    $bitmap.Save($pngPath, [System.Drawing.Imaging.ImageFormat]::Png)
}
finally {
    $gatewayPath.Dispose()
    $backgroundPath.Dispose()
    $flowPen.Dispose()
    $channelPen.Dispose()
    $glyphPen.Dispose()
    $backgroundBrush.Dispose()
    $graphics.Dispose()
    $bitmap.Dispose()
}

$wails = Get-Command wails3 -ErrorAction Stop
& $wails.Source generate icons -input $pngPath -sizes "256,128,64,48,32,16" -windowsfilename $icoPath -macfilename ""
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

& $wails.Source generate syso -manifest $manifestPath -icon $icoPath -out $resourcePath -arch $Architecture
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "Generated $pngPath"
Write-Host "Generated $icoPath"
Write-Host "Generated $resourcePath"
