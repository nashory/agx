param(
  [string]$BinDir = ""
)

# Start the AGX runtime in the background on Windows and wait until it is ready.
# Mirrors the darwin/linux `runtime-bg` Task: stop any existing runtime, launch a
# detached process with logs under bin/logs, then poll `runtime status` until it
# answers twice in a row (~10s budget).

$ErrorActionPreference = "Stop"

function Resolve-AbsolutePath([string]$Path) {
  $full = [System.IO.Path]::GetFullPath($Path)
  return $full.TrimEnd([System.IO.Path]::DirectorySeparatorChar, [System.IO.Path]::AltDirectorySeparatorChar)
}

# Return $true when the runtime answers `status` with exit code 0. The try/catch
# is required because a not-yet-ready runtime writes to stderr, which PowerShell
# would otherwise promote to a terminating error under $ErrorActionPreference=Stop.
function Test-RuntimeReady([string]$Agx) {
  try { & $Agx runtime status *> $null } catch { return $false }
  return ($LASTEXITCODE -eq 0)
}

$repoRoot = Resolve-AbsolutePath (Join-Path $PSScriptRoot "..")
if ([string]::IsNullOrWhiteSpace($BinDir)) {
  $BinDir = Join-Path $repoRoot "bin"
}
$bin = Resolve-AbsolutePath $BinDir
$agx = Join-Path $bin "agx.exe"

if (-not (Test-Path -LiteralPath $agx)) {
  throw "Missing CLI binary: $agx (run `task build` first)"
}

$logDir = Join-Path $bin "logs"
New-Item -ItemType Directory -Force -Path $logDir | Out-Null
$stdout = Join-Path $logDir "runtime.log"
$stderr = Join-Path $logDir "runtime.err.log"

# Stop any runtime already holding the lock; ignore failures when none is running.
try { & $agx runtime stop *> $null } catch {}
for ($i = 0; $i -lt 50; $i++) {
  if (-not (Test-RuntimeReady $agx)) { break }
  Start-Sleep -Milliseconds 100
}

Write-Output "Starting AGX runtime in the background..."
$runtimeProcess = Start-Process -FilePath $agx -ArgumentList @("runtime", "start") `
  -PassThru -WindowStyle Hidden -RedirectStandardOutput $stdout -RedirectStandardError $stderr

# Poll status until the runtime answers twice in a row, matching the mac task.
$ready = 0
for ($i = 0; $i -lt 100; $i++) {
  if (Test-RuntimeReady $agx) {
    $ready++
    if ($ready -ge 2) {
      Write-Output "AGX runtime is ready."
      exit 0
    }
  } else {
    $ready = 0
    if ($runtimeProcess.HasExited) { break }
  }
  Start-Sleep -Milliseconds 100
}

Write-Output "runtime did not start; see $stderr"
if (Test-Path -LiteralPath $stderr) {
  Get-Content -LiteralPath $stderr -Tail 40
}
exit 1
