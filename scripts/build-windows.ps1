param(
  [string]$Version = "dev",
  [string]$Output = "bin/agx.exe"
)

# Build the AGX CLI on Windows. Kept in a script (not an inline Taskfile command)
# because Task runs commands through a POSIX shell that would expand the $vars in
# an inline PowerShell one-liner before PowerShell ever sees them.

$ErrorActionPreference = "Stop"

New-Item -ItemType Directory -Force -Path "bin" | Out-Null

$commit = "unknown"
try { $commit = (& git rev-parse --short HEAD 2>$null).Trim() } catch {}
$date = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")

go build -ldflags "-X main.version=$Version -X main.commit=$commit -X main.date=$date" -o $Output ./cmd/agx
exit $LASTEXITCODE
