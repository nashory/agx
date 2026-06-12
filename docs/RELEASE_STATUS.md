# AGX Release Status

This document captures the current public-release and Homebrew distribution
state for maintainers. It intentionally avoids secrets, tokens, local config
values, and signing credentials.

Last updated: 2026-06-05.

## Current Repository State

- Main AGX repo: `nashory/agx`
- Current release-prep branch: `agx/merge-public-release-docs`
- Current public docs commit on `origin/main`: `19d4eab`
- Current preview release tag: `v0.1.0-rc.1`
- Current release target commit: `717edef`
- Current repo visibility during testing: private

The README and install docs now present Homebrew as the primary install path.
GitHub Release assets remain the binary source that the Homebrew formula and
cask download.

## Public-Facing Docs Completed

README now includes:

- Centered AGX hero block.
- One-liner: `Mission control for parallel coding agents on your Mac.`
- Badge row for release, license, Homebrew, platform, Linux source builds, Go,
  Desktop, Discord, and CLI.
- Desktop app and Discord control as first-class features.
- CLI positioned as a companion for scripting, logs, service management, and
  tmux attach.
- High-level architecture diagram:
  `AGX Core -> Local Runtime -> Desktop / Discord / CLI`.
- Links to install and contribution guides.

Tracked docs added or updated:

- `docs/INSTALL.md`
- `docs/CONTRIBUTING.md`
- `docs/RELEASE_STATUS.md`

Ignored local-only runbooks may exist under `internal_docs/`. That directory is
ignored by git and should not be relied on for public release instructions.

## Packaging Implemented

The macOS packaging flow is implemented with:

- `scripts/package-macos.sh`
- `make package-macos`

The package command builds:

- `dist/AGX-darwin-arm64.dmg`
- `dist/agx-darwin-arm64.tar.gz`

Final checksum generation is a separate release step so macOS and Linux
artifacts can share one complete `dist/checksums.txt`.

The Desktop app bundle layout is:

```text
AGX.app/
  Contents/
    Info.plist
    MacOS/
      AGXDesktop
      agx
```

`Info.plist` uses `CFBundleExecutable=AGXDesktop`.

The Desktop runtime start/install path now prefers the bundled CLI sibling at
`AGX.app/Contents/MacOS/agx`, then falls back to `agx` on `PATH`. This allows
Desktop users to start or install the runtime without separately installing the
CLI first.

Related code/test changes:

- `internal/desktop/app.go`
- `internal/desktop/app_test.go`

Verification completed for `VERSION=v0.1.0-rc.1 make package-macos`:

- CLI version showed `agx version v0.1.0-rc.1`.
- CLI and Desktop binaries were arm64 Mach-O executables.
- DMG mounted successfully.
- DMG contained `AGX.app` and `Applications -> /Applications`.
- `shasum -a 256 -c dist/checksums.txt` passed.

## Published Preview Release

GitHub prerelease:

```text
https://github.com/nashory/agx/releases/tag/v0.1.0-rc.1
```

Assets:

```text
AGX-darwin-arm64.dmg
sha256 2963e6cfcc88a4f091b407c73729b25ea4b32834ec5fb275a547b679336ce96b

agx-darwin-arm64.tar.gz
sha256 3f8f5f2be48ddb91f0de55b186d5b7eea07b4e1ec9107ad4bb4e21e75e8d26fc

checksums.txt
```

This release is a prerelease and is not codesigned or notarized yet. For local
preview testing, Gatekeeper quarantine can be removed with:

```bash
xattr -dr com.apple.quarantine /Applications/AGX.app
```

## Homebrew Tap State

Homebrew tap repo:

```text
nashory/homebrew-tap
```

Tap name:

```bash
brew tap nashory/tap
```

Current tap commit:

```text
3c034f1 Fix AGX tap audit issues
```

Tap files:

```text
Formula/agx.rb
Casks/agx.rb
```

Formula:

- Installs CLI from `agx-darwin-arm64.tar.gz`.
- Depends on `git` and `tmux`.
- Guards the prebuilt binary path to macOS arm64.

Cask:

- Installs Desktop app from `AGX-darwin-arm64.dmg`.
- Requires Apple Silicon.
- Uses macOS `ventura` or newer as the current cask boundary.
- Includes a preview-build quarantine caveat.

Audit status:

```bash
brew audit --formula --strict nashory/tap/agx
brew audit --cask --strict nashory/tap/agx
```

Both audits passed after tap commit `3c034f1`.

## Current Homebrew Installation Blocker

Actual Homebrew install testing currently fails while `nashory/agx` is private:

```bash
brew install --formula nashory/tap/agx
brew install --cask nashory/tap/agx
```

Both fail at the release asset download step with HTTP 404 because Homebrew uses
normal unauthenticated asset URLs for the formula and cask.

This is expected while the AGX repo is private. The tap itself is valid.

To complete the normal public install test:

1. Make `nashory/agx` public.
2. Run `brew update`.
3. Run:

   ```bash
   brew install --cask nashory/tap/agx
   brew install --formula nashory/tap/agx
   ```

4. Verify:

   ```bash
   agx --version
   agx doctor
   open /Applications/AGX.app
   ```

## Security Review Status

Before publishing the preview release, the current tree and git history were
scanned with pattern searches for common secret formats, including:

- Discord bot token patterns.
- GitHub token patterns.
- OpenAI-style API key patterns.
- AWS access key patterns.
- Slack token patterns.
- Private key headers.
- Generic token, secret, and API-key names.

No real secrets were found in the inspected content. Matches were placeholders
or test data, for example:

- `$DISCORD_BOT_TOKEN`
- `MY_API_KEY = "..."`
- dummy test strings such as `"token"` or `"abcd-secret-token"`

No dedicated secret scanner was installed in the environment at the time of the
scan. Recommended follow-up before the repo is made public:

```bash
gitleaks detect --source . --verbose
```

or:

```bash
trufflehog git file://. --only-verified
```

Keep these paths untracked:

- `dist/`
- `bin/`
- `.gopath/`
- `desktop/frontend/dist/`
- `desktop/frontend/node_modules/`
- `internal_docs/`

Do not commit:

- `.env` files.
- local AGX config files containing tokens.
- SQLite databases.
- Apple signing certificates.
- provisioning profiles.
- private keys.
- release credentials.

## Release Workflow Notes

For the next RC:

```bash
git fetch origin main
git rebase origin/main
make test
VERSION=v0.1.0-rc.2 make package-macos
make release-checksums
shasum -a 256 -c dist/checksums.txt
gh release create v0.1.0-rc.2 \
  dist/AGX-darwin-arm64.dmg \
  dist/agx-darwin-arm64.tar.gz \
  dist/checksums.txt \
  --prerelease \
  --title "AGX v0.1.0-rc.2" \
  --notes "Public preview release candidate."
```

Then update the Homebrew tap formula and cask URLs/checksums to the new tag and
run:

```bash
brew update
brew audit --formula --strict nashory/tap/agx
brew audit --cask --strict nashory/tap/agx
```

Prefer publishing a new RC over mutating release assets after users may have
downloaded them.
