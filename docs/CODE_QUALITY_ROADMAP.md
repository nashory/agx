# Code Quality Roadmap

This roadmap defines the work needed to move AGX from a solid product-code
baseline to an 85+ quality score. The target is not cosmetic cleanup. The
focus is reducing regression risk, improving failure visibility, and making
large features easier to change safely.

## Current Baseline

AGX has strong backend domain separation and useful test coverage around
runtime, session, worktree, Discord, and database behavior. The main quality
risks are concentrated in a few areas:

- Large files that combine unrelated responsibilities.
- Inconsistent error handling for asynchronous cleanup and sync paths.
- Limited structured logging and operation-level observability.
- Weak frontend test coverage.
- Some duplicated behavior between Desktop, CLI, runtime, and Discord control
  surfaces.

## Target Quality Bar

AGX should be considered an 85+ codebase when the following are true:

- Core runtime and Discord failures are visible to users or logs with enough
  context to diagnose them.
- Cleanup, sync, and lifecycle errors are modeled explicitly instead of being
  silently ignored.
- Frontend views are split into focused components with tests covering the
  most important user flows.
- Runtime API handlers, DTOs, and client calls are grouped by domain.
- Changes to task lifecycle, workspace behavior, and Discord sync can be made
  without editing multiple unrelated large files.
- Release and development docs remain accurate as the implementation changes.

## Phase 1: Failure Visibility and Recovery

Goal: eliminate silent failures in task lifecycle, worktree cleanup, and
Discord sync paths.

Work items:

- Define a small set of typed lifecycle errors for cleanup, sync, permission,
  timeout, and conflict cases.
- Return partial-success cleanup failures to Desktop, CLI, and Discord callers.
- Audit ignored errors in runtime, session, desktop, and Discord packages.
- Add operation-scoped log fields for task ID, project ID, workspace mode,
  Discord channel ID, and operation name.
- Add tests for delete, kill, sync, and disconnect behavior when cleanup or
  remote sync fails after local state changes.

Acceptance criteria:

- Deleting a task never hides worktree cleanup failure.
- Discord sync timeout includes task ID, project ID, channel/thread target, and
  elapsed time in logs.
- User-facing operations report actionable errors instead of only writing to
  internal logs.
- Ignored errors are documented as intentionally safe or replaced with handling.

Expected score impact:

- Error handling / recovery: 73 -> 82
- Observability / operability: 65 -> 75
- Worktree / session lifecycle: 80 -> 84

## Phase 2: Runtime API and Service Decomposition

Goal: make runtime behavior easier to reason about by splitting large files by
domain.

Work items:

- Split runtime HTTP handlers into task, project, config, Discord, logs, and
  health files.
- Move request and response DTOs into domain-specific files.
- Keep route registration centralized, but keep handler implementation local to
  each domain.
- Extract shared response helpers for JSON, validation errors, conflicts,
  timeout errors, and partial-success warnings.
- Add focused tests next to each handler group.

Acceptance criteria:

- `internal/runtime/api.go` is reduced to routing and shared API plumbing.
- Task lifecycle endpoints can be reviewed without reading Discord or config
  endpoint code.
- Handler tests assert both status codes and structured error payloads.
- CLI and Desktop client behavior remains backward compatible.

Expected score impact:

- Architecture / modular boundaries: 82 -> 86
- Maintainability: 72 -> 78
- Backend correctness / domain logic: 84 -> 86

## Phase 3: Desktop Frontend Decomposition

Goal: reduce the risk of UI regressions by splitting the Desktop UI into
focused components and testable state modules.

Work items:

- Split `desktop/frontend/src/main.tsx` by feature area:
  - shell/navigation
  - project list and project detail
  - task cards and task detail
  - settings
  - Discord connection and sync controls
  - action/error dialogs
- Move API calls into typed client modules.
- Move shared UI state transitions into small hooks or reducers.
- Split `styles.css` into base, layout, component, and feature styles.
- Add frontend tests for critical workflows.

Acceptance criteria:

- The main entry file only wires providers, top-level state, and root render.
- Task creation failure, project-mode conflict display, default agent setting,
  Discord disconnect/reconnect, and task hard sync are covered by tests.
- Styling changes for one feature do not require scanning the entire stylesheet.
- Frontend build remains clean with no new runtime warnings.

Expected score impact:

- Frontend architecture: 62 -> 76
- UX feedback / state display: 72 -> 78
- Maintainability: 78 -> 82 after Phase 2

## Phase 4: Discord Sync Hardening

Goal: make Discord task channels reliable under timeout, restart, retry, and
partial state conditions.

Work items:

- Make sync operations idempotent by task ID and Discord target.
- Track sync attempts with state, timestamps, error messages, and retry count.
- Add explicit timeout policy for channel creation, stream refresh, and command
  acknowledgement.
- Prevent duplicate sync work for the same task while still allowing a hard
  sync retry after a failed attempt.
- Add integration-style tests using fake Discord clients for slow, failed, and
  partially successful operations.

Acceptance criteria:

- A Desktop-created Discord task can be hard-synced from the task detail view
  after an initial sync failure.
- Timeout failures leave enough state for a later retry to succeed.
- Duplicate channel creation is prevented.
- Discord command replies clearly distinguish conflict, timeout, permission,
  and validation errors.

Expected score impact:

- Discord integration robustness: 76 -> 84
- Observability / operability: 75 -> 80

## Phase 5: Shared Contracts and Client Consistency

Goal: reduce drift between Desktop, CLI, runtime, and Discord behavior.

Work items:

- Centralize API error payload types and user-facing error codes.
- Reuse the same task creation validation rules across all control surfaces.
- Add contract tests for runtime client behavior.
- Document which operations are runtime-owned and which are UI-only.
- Align Desktop direct mode and runtime-backed mode or remove duplicate paths
  where runtime ownership is now required.

Acceptance criteria:

- A conflict from task creation has the same semantic error code in Desktop,
  CLI, and Discord.
- Settings changes use one runtime config contract.
- Direct Desktop behavior cannot drift from runtime behavior for task lifecycle
  operations.

Expected score impact:

- Architecture / modular boundaries: 86 -> 88
- UX feedback / state display: 78 -> 82
- Maintainability: 82 -> 85

## Phase 6: Release-Grade Verification

Goal: make quality measurable before releases.

Work items:

- Add a documented pre-release verification checklist.
- Add frontend workflow tests to CI or the local release gate.
- Add smoke tests for runtime start, task creation, task deletion, Discord sync
  failure handling, and config update.
- Track package-level test ownership in the development guide.
- Keep README and release docs in sync with actual supported platforms and
  installation paths.

Acceptance criteria:

- A maintainer can run one documented command set before tagging a release.
- Release checks cover backend tests, frontend build/tests, formatting, and
  smoke checks.
- Known platform limitations are documented before release assets are published.

Expected score impact:

- Test coverage: 78 -> 85
- Documentation: 86 -> 88
- Overall code quality: 77 -> 85+

## Suggested Execution Order

1. Finish failure visibility work already started around task cleanup.
2. Split runtime API handlers before adding more endpoint behavior.
3. Split Desktop frontend by feature and add the first workflow tests.
4. Harden Discord sync retry, timeout, and idempotency behavior.
5. Normalize shared error contracts across control surfaces.
6. Add release verification as a required maintainer workflow.

## Tracking Metrics

Use these metrics to decide whether the roadmap is actually improving the
codebase:

- Largest source file size decreases over time.
- New lifecycle failures have typed errors and tests.
- User-facing operations have structured error payloads.
- Discord sync failures include task/project/channel context in logs.
- Frontend critical flows have automated tests.
- Release checklist can be completed without private local knowledge.

