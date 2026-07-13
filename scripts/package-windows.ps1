$ErrorActionPreference = "Stop"

$Root = Resolve-Path (Join-Path $PSScriptRoot "..")
$Version = if ($env:VERSION) { $env:VERSION } else { "dev" }
$GoOS = if ($env:GOOS) { $env:GOOS } else { "windows" }
$GoArch = if ($env:GOARCH) { $env:GOARCH } else { "amd64" }

if ($GoOS -ne "windows") {
  throw "package-windows requires GOOS=windows"
}
if ($GoArch -ne "amd64" -and $GoArch -ne "arm64") {
  throw "unsupported Windows architecture: $GoArch"
}

$DistDir = Join-Path $Root "dist"
$BuildDir = Join-Path $DistDir "windows-build"
$Stage = Join-Path $BuildDir "agx-windows-$GoArch"
$Archive = Join-Path $DistDir "AGX-windows-$GoArch.zip"

function Remove-PathUnderDist($Path) {
  if (-not (Test-Path $Path)) {
    return
  }
  $resolvedDist = (Resolve-Path $DistDir).Path
  $resolvedPath = (Resolve-Path $Path).Path
  if (-not $resolvedPath.StartsWith($resolvedDist, [System.StringComparison]::OrdinalIgnoreCase)) {
    throw "refusing to remove path outside dist: $resolvedPath"
  }
  Remove-Item -LiteralPath $resolvedPath -Recurse -Force
}

$Commit = "unknown"
try {
  $Commit = (& git -C $Root rev-parse --short HEAD 2>$null).Trim()
} catch {
  $Commit = "unknown"
}
$Date = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
$Ldflags = "-X main.version=$Version -X main.commit=$Commit -X main.date=$Date"

New-Item -ItemType Directory -Force -Path $DistDir | Out-Null
Remove-PathUnderDist $BuildDir
if (Test-Path $Archive) {
  Remove-Item -LiteralPath $Archive -Force
}
New-Item -ItemType Directory -Force -Path $Stage | Out-Null

Write-Output "==> Building AGX CLI (windows/$GoArch, $Version)"
Push-Location $Root
try {
  $env:GOOS = "windows"
  $env:GOARCH = $GoArch
  & go build -ldflags $Ldflags -o (Join-Path $Stage "agx.exe") ./cmd/agx
} finally {
  Pop-Location
}

Write-Output "==> Building frontend"
& npm --prefix (Join-Path $Root "desktop/frontend") ci
& npm --prefix (Join-Path $Root "desktop/frontend") run build

Write-Output "==> Building AGX Desktop (windows/$GoArch, $Version)"
Push-Location $Root
try {
  $env:GOOS = "windows"
  $env:GOARCH = $GoArch
  & go build -tags "desktop,production" -ldflags $Ldflags -o (Join-Path $Stage "agx-desktop.exe") ./desktop
} finally {
  Pop-Location
}

Copy-Item -LiteralPath (Join-Path $Root "LICENSE") -Destination (Join-Path $Stage "LICENSE")
Copy-Item -LiteralPath (Join-Path $Root "README.md") -Destination (Join-Path $Stage "README.md")
Copy-Item -LiteralPath (Join-Path $Root "docs/WINDOWS.md") -Destination (Join-Path $Stage "WINDOWS.md")
Copy-Item -LiteralPath (Join-Path $Root "docs/INSTALL.md") -Destination (Join-Path $Stage "INSTALL.md")
New-Item -ItemType Directory -Force -Path (Join-Path $Stage "scripts") | Out-Null
Copy-Item -LiteralPath (Join-Path $Root "scripts/run-windows-desktop-preview.ps1") -Destination (Join-Path $Stage "scripts/run-windows-desktop-preview.ps1")

Write-Output "==> Creating $Archive"
Compress-Archive -Path (Join-Path $Stage "*") -DestinationPath $Archive -Force

Write-Output "==> Release artifact"
Get-Item $Archive | Format-Table Name, Length, LastWriteTime
