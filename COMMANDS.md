# AGX Commands

This guide lists the common commands for running AGX by platform.

`agx launch` starts or verifies the runtime and can connect Discord. It does not
open the Desktop app. Start Desktop separately with the platform-specific
Desktop binary.

## macOS

Build from the repo:

```bash
npm --prefix desktop/frontend ci
npm --prefix desktop/frontend run build
go build -o bin/agx ./cmd/agx
go build -tags "desktop,production" -o bin/agx-desktop ./desktop
```

Start the runtime only:

```bash
./bin/agx launch --platform macos --skip-discord
```

Start the runtime and connect Discord:

```bash
./bin/agx launch --platform macos --discord-server-id <server-id>
```

Open Desktop:

```bash
./bin/agx-desktop
```

Stop the runtime:

```bash
./bin/agx runtime stop
```

## Windows

Build from the repo in PowerShell:

```powershell
npm --prefix desktop/frontend ci
npm --prefix desktop/frontend run build
go build -o bin/agx.exe ./cmd/agx
go build -tags "desktop,production" -o bin/agx-desktop.exe ./desktop
```

Start the runtime only:

```powershell
.\bin\agx.exe launch --platform windows --skip-discord
```

Start the runtime and connect Discord:

```powershell
.\bin\agx.exe launch --platform windows --discord-server-id <server-id>
```

Open Desktop:

```powershell
.\bin\agx-desktop.exe
```

Stop the runtime:

```powershell
.\bin\agx.exe runtime stop
```

For preview validation without touching the default AGX config directory:

```powershell
.\scripts\run-windows-desktop-preview.ps1
.\scripts\run-windows-desktop-preview.ps1 -Run
```

## Linux

Build from the repo:

```bash
npm --prefix desktop/frontend ci
npm --prefix desktop/frontend run build
go build -o bin/agx ./cmd/agx
go build -tags "desktop,production" -o bin/agx-desktop ./desktop
```

Start the runtime only:

```bash
./bin/agx launch --platform linux --skip-discord
```

Start the runtime and connect Discord:

```bash
./bin/agx launch --platform linux --discord-server-id <server-id>
```

Open Desktop:

```bash
./bin/agx-desktop
```

Stop the runtime:

```bash
./bin/agx runtime stop
```

## Useful Runtime Commands

Check runtime status:

```bash
agx runtime status
```

Run diagnostics:

```bash
agx doctor
```

List projects and tasks:

```bash
agx project list
agx task list
```

Create a task:

```bash
agx task create <project> "<prompt>"
```

Send a message to a task:

```bash
agx send <task-id> "<message>"
```

Show task logs:

```bash
agx logs <task-id>
```

Stop a task:

```bash
agx stop <task-id>
```

## Voice STT

Prepare local Whisper voice transcription:

```bash
agx voice-stt setup
```

On Windows from the repo build:

```powershell
.\bin\agx.exe voice-stt setup
```

