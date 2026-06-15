# Code Quality Roadmap

This roadmap defines the work needed to move AGX from a solid product-code
baseline to an 85+ quality score. It is ordered by practical implementation
priority: fix user-visible failure paths first, then extract shared contracts,
then decompose large modules once behavior is covered by tests.

## Current Baseline

AGX has strong backend domain separation and useful test coverage around
runtime, session, worktree, Discord, and database behavior. The main quality
risks are concentrated in these areas:

- Large files combine unrelated responsibilities.
- Asynchronous cleanup and sync errors are not handled consistently.
- Runtime logs are not structured enough to diagnose failures quickly.
- Frontend coverage is mostly build-level, not workflow-level.
- Desktop, CLI, runtime, and Discord can drift in validation and error display.
- Config, secret, and state migration behavior needs a stronger release bar.

## Target Quality Bar

AGX should be considered an 85+ codebase when the following are true:

- Core runtime, Discord, worktree, and config failures are visible to users or
  logs with enough context to diagnose them.
- Cleanup, sync, lifecycle, timeout, permission, and conflict errors use shared
  typed contracts instead of ad hoc strings.
- Frontend views are split into focused components with tests covering the most
  important user flows.
- Runtime API handlers, DTOs, and client calls are grouped by domain.
- Task lifecycle, workspace behavior, and Discord sync can be changed without
  editing multiple unrelated large files.
- Release checks cover backend, frontend, config compatibility, and packaging
  assumptions.

## Prioritization Model

Phases are ordered by three factors:

- **User impact**: whether the work prevents confusing failures or data loss.
- **Implementation leverage**: whether the work makes later refactors safer.
- **Implementation ease**: whether the work can be done incrementally without a
  large rewrite.

| Phase | Priority | Ease | Main reason |
| --- | --- | --- | --- |
| P0 | Critical | Medium | Stop hiding lifecycle and cleanup failures. |
| P1a | Critical | Medium | Create shared error and logging contracts before more features. |
| P1b | High | Medium | Fix context ownership and flaky readiness patterns incrementally. |
| P2 | High | Medium | Make Discord sync debuggable, retryable, and migration-safe. |
| P3 | High | Medium | Split runtime API after behavior contracts and smoke tests exist. |
| P4 | High | Hard | Split the Desktop frontend after critical workflows are covered. |
| P5 | Medium | Medium | Add full release-grade verification and migration checks. |

## P0: Lifecycle Failure Visibility

Goal: eliminate silent failures in task deletion, task kill, worktree cleanup,
and local session cleanup.

Why first:

- This directly addresses user-visible confusion.
- It is smaller than a broad refactor.
- It creates the first explicit partial-success pattern for later phases.

Work items:

- Keep `TaskCleanupError` or an equivalent typed partial-success error.
- Return partial-success cleanup failures to Desktop, CLI, and Discord callers.
- Audit ignored errors in runtime, session, desktop, Discord, and agent event
  paths.
- Categorize ignored errors as safe, logged-only, user-visible, or retryable.
- Add focused tests for delete, kill, and cleanup failure after local state
  changes.

Acceptance criteria:

- Deleting a task never hides worktree cleanup failure.
- Killing a task through Discord reports partial cleanup failure in the channel.
- Desktop/API callers receive a structured error or warning when cleanup fails.
- Every ignored cleanup error has a documented reason or a test.

Expected score impact:

- Error handling / recovery: 73 -> 79
- Worktree / session lifecycle: 80 -> 84
- Observability / operability: 65 -> 70

## P1a: Shared Errors and Operation Logging

Goal: establish the error and logging contracts that later runtime, Discord,
CLI, and frontend changes should use.

Why second:

- It prevents each control surface from inventing different error behavior.
- It makes the runtime API split safer.
- It improves Discord timeout diagnosis before deeper sync changes.
- It is smaller and safer than changing context ownership at the same time.

Work items:

- Define shared error codes for conflict, validation, permission, timeout,
  cleanup failure, sync failure, and partial success.
- Standardize JSON error payloads for runtime API responses.
- Return error code, safe user detail, developer detail, retryability, and
  structured metadata separately.
- Let Desktop, CLI, and Discord translate shared error codes into their own
  surface-specific user messages.
- Add operation-scoped logging fields: task ID, project ID, workspace mode,
  operation name, Discord guild/channel/thread ID, elapsed time, and error
  code.
- Add a lightweight release artifact secret scan that checks generated packages
  for local config, tokens, `.agx` state, databases, and private runbooks.

Acceptance criteria:

- A task creation conflict has the same semantic code in Desktop, CLI, and
  Discord.
- Discord sync timeout logs include task/project/channel context and elapsed
  time.
- Runtime handlers can return typed user-safe errors without duplicating JSON
  formatting logic.
- Surface-specific messages remain free to differ while preserving the same
  error code and metadata.
- A local artifact scan can be run before public release packaging.

Expected score impact:

- Error handling / recovery: 79 -> 82
- Observability / operability: 70 -> 77
- Security / secret handling: 82 -> 84
- Maintainability: 72 -> 75

## P1b: Context Ownership and Readiness Cleanup

Goal: reduce lifecycle leaks and flaky timing by making long-running operations
explicitly owned and cancellable.

Why after P1a:

- Error and logging contracts should exist before changing lifecycle behavior.
- Context and readiness changes can be reviewed incrementally by package.
- This work is useful but should not block Discord sync hardening entirely.

Work items:

- Review `context.Background()` usage in long-running runtime, Desktop, Discord,
  and agent event paths.
- Pass task, request, service, or bridge cancellation contexts where appropriate.
- Keep detached contexts only where the operation must survive the caller, and
  document that ownership explicitly.
- Replace polling sleeps with explicit readiness or timeout helpers where the
  current behavior is flaky.
- Add tests for cancellation of runtime startup, Discord sync, task streams, and
  agent event forwarding where feasible.

Acceptance criteria:

- Long-running background operations have an owner context or a documented
  reason for using a detached context.
- Polling sleeps that gate readiness have named timeout helpers or tests.
- Cancellation does not leave duplicate Discord sync workers or orphaned task
  streams.

Expected score impact:

- Error handling / recovery: 82 -> 83
- Observability / operability: 77 -> 78
- Maintainability: 75 -> 76

## P2: Discord Sync Hardening

Goal: make Discord task channels reliable under timeout, restart, retry, and
partial state conditions.

Why third:

- Discord failures are highly visible and currently difficult to diagnose.
- P1 gives this work the logging and error contract it needs.
- The implementation can be incremental around the existing syncer and bridge.

Work items:

- Make sync operations idempotent by task ID and Discord target.
- Track sync attempts with state, timestamps, error messages, and retry count.
- Add a durable sync-state schema for attempts, last success, last failure,
  target channel/thread IDs, and retry eligibility.
- Add a migration and backfill path for existing task/channel mappings.
- Add explicit timeout policy for channel creation, stream refresh, command
  acknowledgement, and hard sync.
- Prevent duplicate sync work for the same task while still allowing hard sync
  retry after a failed attempt.
- Add integration-style tests using fake Discord clients for slow, failed,
  duplicate, restart, and partially successful operations.
- Confirm disconnect clears local secret state and does not accidentally reuse
  stale credentials.

Acceptance criteria:

- A Desktop-created Discord task can be hard-synced from the task detail view
  after an initial sync failure.
- Timeout failures leave enough state for a later retry to succeed.
- Duplicate channel creation is prevented.
- Existing Discord task mappings are preserved or safely backfilled after the
  sync-state migration.
- Failed migrations are reported clearly and do not silently drop channel
  mappings.
- Discord command replies clearly distinguish conflict, timeout, permission,
  validation, and sync errors.
- Disconnect/reconnect behavior is covered for token, guild ID, and allowed
  user IDs.

Expected score impact:

- Discord integration robustness: 76 -> 84
- Security / secret handling: 82 -> 84
- Observability / operability: 78 -> 82

## P3: Runtime API and Service Decomposition

Goal: make runtime behavior easier to review by splitting large files by
domain after the shared contracts exist.

Why fourth:

- Runtime API is central, but refactoring it before P1 risks moving duplicated
  behavior around instead of simplifying it.
- P0-P2 add tests and contracts that protect this larger change.
- Minimal frontend smoke tests should exist before runtime endpoints are moved.

Work items:

- Add smoke-level frontend workflow tests before moving runtime handlers:
  task creation conflict dialog, default agent setting, and Discord hard sync
  button behavior.
- Split runtime HTTP handlers into task, project, config, Discord, logs, health,
  and agent event files.
- Move request and response DTOs into domain-specific files.
- Keep route registration centralized, but keep handler implementation local to
  each domain.
- Extract shared response helpers for JSON, validation errors, conflicts,
  timeout errors, and partial-success warnings.
- Add handler tests next to each handler group.
- Document runtime-owned operations versus UI-only operations.

Acceptance criteria:

- `internal/runtime/api.go` is reduced to routing and shared API plumbing.
- Task lifecycle endpoints can be reviewed without reading Discord or config
  endpoint code.
- Handler tests assert status codes, structured error payloads, and important
  side effects.
- Smoke-level frontend workflow tests pass against the refactored runtime
  client behavior.
- CLI and Desktop client behavior remains backward compatible.

Expected score impact:

- Architecture / modular boundaries: 82 -> 87
- Backend correctness / domain logic: 84 -> 86
- Maintainability: 76 -> 81

## P4: Desktop Frontend Decomposition and Workflow Tests

Goal: reduce UI regression risk by splitting the Desktop UI into focused
components and covering critical workflows.

Why fifth:

- It is one of the largest maintainability wins, but it is also the highest
  churn phase.
- P1 and P3 should define API contracts before the frontend is heavily split.
- P3 should already provide smoke tests for the highest-risk user flows.

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
- Add workflow tests for critical Desktop behavior.
- Add basic accessibility checks for dialogs, form validation, focus movement,
  disabled states, and error announcements.

Acceptance criteria:

- The main entry file only wires providers, top-level state, and root render.
- Task creation failure, project-mode conflict display, default agent setting,
  Discord disconnect/reconnect, and task hard sync are covered by tests.
- Styling changes for one feature do not require scanning the entire
  stylesheet.
- Error dialogs are keyboard reachable and expose useful accessible labels.
- Frontend build and workflow tests pass without runtime warnings.

Expected score impact:

- Frontend architecture: 62 -> 76
- UX feedback / state display: 72 -> 80
- Test coverage: 78 -> 83
- Maintainability: 81 -> 85

## P5: Release-Grade Verification, Security, and Migration Checks

Goal: make quality measurable before releases and prevent local-only behavior
from leaking into public builds.

Why last:

- The strongest release gate depends on the tests and contracts added in
  earlier phases.
- Some lightweight checks can be added earlier, but the full gate should come
  after P0-P4.

Work items:

- Add a documented pre-release verification checklist.
- Add frontend workflow tests to CI or the local release gate.
- Add smoke tests for runtime start, task creation, task deletion, Discord sync
  failure handling, config update, and service restart recovery.
- Add config and database compatibility checks for newly introduced settings.
- Expand the P1a artifact secret scan into a required release gate.
- Track package-level test ownership in the development guide.
- Keep README and release docs in sync with actual supported platforms and
  installation paths.

Acceptance criteria:

- A maintainer can run one documented command set before tagging a release.
- Release checks cover backend tests, frontend build/tests, formatting, smoke
  checks, and packaging assumptions.
- Config and database changes have a compatibility or migration note.
- Known platform limitations are documented before release assets are
  published.
- Public artifacts can be inspected for accidental secret or local-state
  inclusion.

Expected score impact:

- Test coverage: 83 -> 85
- Documentation: 86 -> 88
- Security / secret handling: 84 -> 86
- Overall code quality: 77 -> 85+

## Parallelizable Work

Some work can happen while the main phases are in progress:

- Documentation updates can be made alongside every phase.
- Frontend component extraction can start after P1 if it avoids changing API
  contracts.
- Release checklist drafts can start immediately, then become mandatory after
  P4.
- Logging helpers can be introduced in P1 and adopted gradually by later
  phases.
- Artifact secret scanning should start in P1a as a lightweight local check and
  become mandatory in P5.

## Do Not Do Yet

Avoid these changes until the earlier phases provide enough coverage:

- Do not rewrite the Desktop frontend in a new framework.
- Do not replace the runtime API shape before shared error contracts exist.
- Do not add another Discord sync mode until current sync retries and timeouts
  are deterministic.
- Do not split large files mechanically without preserving behavior with tests.

## Tracking Metrics

Use these metrics to decide whether the roadmap is actually improving the
codebase:

- Largest source file size decreases over time.
- New lifecycle failures have typed errors and tests.
- User-facing operations have structured error payloads.
- Discord sync failures include task/project/channel context in logs.
- Critical Desktop workflows have automated tests.
- Context ownership is clear for long-running background operations.
- Discord sync-state schema migrations preserve existing mappings.
- Release artifact scans fail on local config, tokens, `.agx` state, databases,
  or private runbooks.
- Release checklist can be completed without private local knowledge.
