param(
  [string]$Tags = "desktop,production",
  [string]$Output = "bin/agx-desktop.exe"
)

# Build the AGX Desktop binary on Windows. Kept in a script (not an inline
# Taskfile command) so Task's POSIX shell does not eat PowerShell $vars.

$ErrorActionPreference = "Stop"

New-Item -ItemType Directory -Force -Path "bin" | Out-Null

go build -tags $Tags -o $Output ./desktop
exit $LASTEXITCODE
