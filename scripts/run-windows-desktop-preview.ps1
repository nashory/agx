param(
  [string]$BinDir = "",
  [string]$ConfigDir = (Join-Path $env:TEMP "agx-windows-desktop-preview"),
  [switch]$Run,
  [switch]$ResetConfig,
  [switch]$NoDesktop
)

$ErrorActionPreference = "Stop"

function Test-IsWindows {
  return [System.Runtime.InteropServices.RuntimeInformation]::IsOSPlatform([System.Runtime.InteropServices.OSPlatform]::Windows)
}

function Get-DefaultBinDir {
  $scriptParent = Resolve-AbsolutePath (Join-Path $PSScriptRoot "..")
  if (Test-Path -LiteralPath (Join-Path $scriptParent "agx.exe")) {
    return $scriptParent
  }
  $repoBin = Join-Path $scriptParent "bin"
  if (Test-Path -LiteralPath (Join-Path $repoBin "agx.exe")) {
    return $repoBin
  }
  return Join-Path (Resolve-Path ".") "bin"
}

function Resolve-AbsolutePath([string]$Path) {
  $full = [System.IO.Path]::GetFullPath($Path)
  return $full.TrimEnd([System.IO.Path]::DirectorySeparatorChar, [System.IO.Path]::AltDirectorySeparatorChar)
}

function Assert-IsSafePreviewConfig([string]$Path) {
  if ([string]::IsNullOrWhiteSpace($Path)) {
    throw "ConfigDir is required"
  }
  $full = Resolve-AbsolutePath $Path
  $homeConfig = Resolve-AbsolutePath (Join-Path $HOME ".config\agx")
  $tempRoot = Resolve-AbsolutePath $env:TEMP

  if ($full.Equals($homeConfig, [System.StringComparison]::OrdinalIgnoreCase)) {
    throw "Refusing to use the default AGX config directory: $full"
  }
  if ($full.Equals((Resolve-AbsolutePath $HOME), [System.StringComparison]::OrdinalIgnoreCase)) {
    throw "Refusing to use the home directory as AGX_CONFIG_DIR: $full"
  }
  if (-not $full.StartsWith($tempRoot, [System.StringComparison]::OrdinalIgnoreCase) -and
      -not $full.Contains("agx-windows-desktop-preview")) {
    throw "ConfigDir must be under TEMP or contain 'agx-windows-desktop-preview': $full"
  }
  return $full
}

function Remove-PreviewConfig([string]$Path) {
  $full = Assert-IsSafePreviewConfig $Path
  if (-not (Test-Path -LiteralPath $full)) {
    return
  }
  Remove-Item -LiteralPath $full -Recurse -Force
}

if (-not (Test-IsWindows)) {
  throw "Windows Desktop preview must be run on Windows"
}

$BinDir = if ([string]::IsNullOrWhiteSpace($BinDir)) { Get-DefaultBinDir } else { $BinDir }
$bin = Resolve-AbsolutePath $BinDir
$agx = Join-Path $bin "agx.exe"
$desktop = Join-Path $bin "agx-desktop.exe"
$config = Assert-IsSafePreviewConfig $ConfigDir

if (-not (Test-Path -LiteralPath $agx)) {
  throw "Missing CLI binary: $agx"
}
if (-not $NoDesktop -and -not (Test-Path -LiteralPath $desktop)) {
  throw "Missing Desktop binary: $desktop"
}

Write-Output "AGX Windows Desktop preview"
Write-Output "  CLI:        $agx"
if (-not $NoDesktop) {
  Write-Output "  Desktop:    $desktop"
}
Write-Output "  Config dir: $config"
Write-Output ""

if (-not $Run) {
  Write-Output "Plan only. Re-run with -Run to start an isolated runtime and Desktop."
  Write-Output "This script refuses to use the default AGX config directory."
  exit 0
}

if ($ResetConfig) {
  Write-Output "Resetting isolated preview config: $config"
  Remove-PreviewConfig $config
}
New-Item -ItemType Directory -Force -Path $config | Out-Null

$previousConfig = $env:AGX_CONFIG_DIR
$env:AGX_CONFIG_DIR = $config
try {
  $stdout = Join-Path $config "preview-runtime.stdout.log"
  $stderr = Join-Path $config "preview-runtime.stderr.log"
  Write-Output "Starting isolated runtime..."
  $runtimeProcess = Start-Process -FilePath $agx -ArgumentList @("runtime", "start") -PassThru -WindowStyle Hidden -RedirectStandardOutput $stdout -RedirectStandardError $stderr

  $ready = $false
  for ($i = 0; $i -lt 60; $i++) {
    Start-Sleep -Milliseconds 250
    & $agx runtime status *> $null
    if ($LASTEXITCODE -eq 0) {
      $ready = $true
      break
    }
    if ($runtimeProcess.HasExited) {
      break
    }
  }
  if (-not $ready) {
    Write-Output "Runtime did not become ready. stderr:"
    if (Test-Path -LiteralPath $stderr) {
      Get-Content -LiteralPath $stderr -Tail 40
    }
    throw "isolated runtime failed to start"
  }

  Write-Output "Isolated runtime is ready."
  if (-not $NoDesktop) {
    Write-Output "Starting Desktop with isolated AGX_CONFIG_DIR..."
    Start-Process -FilePath $desktop -WorkingDirectory $bin | Out-Null
  }

  Write-Output ""
  Write-Output "Stop the isolated runtime with:"
  Write-Output "  `$env:AGX_CONFIG_DIR = '$config'"
  Write-Output "  & '$agx' runtime stop"
} finally {
  $env:AGX_CONFIG_DIR = $previousConfig
}
