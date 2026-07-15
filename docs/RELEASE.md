# Release Checklist

Use this checklist before tagging a public AGX release.

## Required Verification

Run the release gate from a clean working tree:

```bash
task release-verify
```

The gate runs:

- Go tests for all packages.
- Desktop frontend production build.
- CLI and Desktop production builds.
- CLI help, version, runtime help, and doctor smoke checks.
- Desktop embedded asset checks.
- Release artifact secret scan when artifacts exist in `dist/`.

After packaging final artifacts, run the same gate with artifact presence
required:

```bash
AGX_REQUIRE_RELEASE_ARTIFACTS=1 task release-verify
```

Then generate and verify checksums:

```bash
task release-checksums
shasum -a 256 -c dist/checksums.txt
```

## Packaging Order

Build platform artifacts before publishing:

```bash
task package-macos VERSION=vX.Y.Z
task package-linux VERSION=vX.Y.Z
AGX_REQUIRE_RELEASE_ARTIFACTS=1 task release-verify
task release-checksums
```

macOS packaging requires macOS host tools such as `hdiutil`. Linux CLI/runtime
artifacts can be built on Linux or macOS with Go cross-compilation.

## Compatibility Notes

Before release, review changes to:

- `internal/config/config.go`
- `internal/db/migrations.go`
- Runtime API DTOs in `internal/runtime/api_types.go`
- Desktop API types in `desktop/frontend/src/types.ts`

Any new config key must have a default or preserve older config files. Any new
database state must be created through idempotent migrations and include a
backfill or explicit compatibility note.

## Artifact Safety

Public artifacts must not contain:

- `.agx/` directories
- local config files
- SQLite databases
- `.env` files
- token, secret, or credential files
- private runbooks or internal docs

`task release-verify` scans `.tar.gz` and `.dmg` artifacts in `dist/` when they
exist. Use `AGX_RELEASE_DIST_DIR=/path/to/dist` to scan a different directory.

## Supported Assets

Current release packaging supports:

- macOS arm64 Desktop and CLI archive
- Linux amd64 CLI/runtime archive
- Linux arm64 CLI/runtime archive
- Ubuntu Docker image from `docker/`

Keep `README.md`, `docs/INSTALL.md`, and this file in sync when platforms,
installation paths, or package names change.
