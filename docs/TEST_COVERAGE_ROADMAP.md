# Test Coverage Roadmap

This plan defines the test work needed to protect AGX's core behavior before
larger feature or UI changes. It prioritizes user-visible workflows and
failure paths over raw coverage percentage.

## Design Goals

The test suite should make AGX safe to change in three dimensions:

- **Behavior safety**: critical user workflows keep working across refactors.
- **Contract safety**: Desktop, CLI, runtime, and Discord agree on request,
  response, and error semantics.
- **Failure safety**: cleanup, timeout, permission, and external-service errors
  are visible and recoverable instead of silently passing.

The target state is not "100% coverage." The target state is that a maintainer
can confidently change runtime, Desktop, Discord, or CLI code and get fast,
specific failures when a core invariant breaks.

## Current Baseline

Measured with:

```bash
go test ./... -cover
go test ./... -coverprofile=/tmp/agx-cover.out
go tool cover -func=/tmp/agx-cover.out
```

Current package coverage highlights:

| Area | Current signal | Main gap |
| --- | ---: | --- |
| `desktop/frontend` | smoke-only | No component, DOM, or state-transition tests. |
| `desktop` | 0.0% | Wails desktop entrypoint has no direct tests. |
| `cmd/agx` | 18.0% | Runtime-backed CLI command parsing and output are mostly untested. |
| `internal/desktop` | 42.8% | Desktop API wrappers and runtime bridge paths have many 0% functions. |
| `internal/runtime` | 46.5% | Runtime HTTP handlers need more endpoint and error-contract tests. |
| `internal/discord` | 54.4% | Discord bot/channel orchestration has many untested edge paths. |
| `internal/session` | 54.4% | Lifecycle and recovery behavior needs more failure coverage. |

The frontend currently runs `desktop/frontend/scripts/workflow-smoke.mjs`,
which checks source strings. That is useful as a guardrail, but it does not
prove button behavior, form validation, dialog state, or API error rendering.

## Test Taxonomy

Use these test categories consistently so each layer owns the right assertions:

| Test type | Owner | What it should prove | What it should avoid |
| --- | --- | --- | --- |
| Pure unit | Go packages, `appLogic.ts` | Deterministic mapping, validation, DTO shaping, formatting. | Filesystem, network, process state. |
| Component unit | Frontend feature modules | UI state transitions, disabled states, dialogs, visible errors. | Real Wails runtime or real tmux. |
| Handler contract | `internal/runtime` | HTTP status, typed error payloads, persisted side effects. | Real Discord network calls. |
| Bridge unit | `internal/desktop`, CLI | Translation between UI/CLI calls and runtime client contracts. | Retesting runtime handler internals. |
| Fake integration | Discord/session/worktree | Multi-step orchestration with fake clients/filesystems/processes. | Flaky sleeps and real external services. |
| Release smoke | `make release-verify` | The built artifact and critical command paths still work together. | Detailed branch coverage. |

When a bug crosses layers, add the narrowest tests at each boundary instead of
one broad brittle test. For example, a Discord task sync failure should have:

- a runtime handler contract test for the error response,
- a Desktop bridge test for propagating the error,
- a frontend component test for rendering the retry/error state,
- and a Discord fake-client test for the original sync failure.

## Non-Negotiable Core Behaviors

These flows must be protected before they are refactored further:

- Project-mode task conflicts return a visible, actionable error.
- Worktree task delete/kill cleanup failures are logged and surfaced.
- Discord disconnect does not silently reuse stale secrets.
- Discord task hard sync is retryable and reports timeout/failure clearly.
- Default agent is `codex` unless configured otherwise, and settings changes
  persist through the runtime API.
- Desktop-created tasks choose the requested workspace mode and task interface.
- Runtime restart keeps projects/tasks and recovers stale runtime-owned state.
- CLI, Desktop, and Discord use the same semantic runtime error contracts.
- CLI flags and Desktop controls cannot silently drift in defaults.
- Release artifacts must not include local config, databases, tokens, or AGX
  runtime state.

## Test Harness Rules

Tests should be fast, deterministic, and local-only.

- Prefer fake clients over real Discord, real agent CLIs, or real long-running
  runtime processes.
- Use real temporary directories for worktree and filesystem behavior where the
  filesystem is the subject under test.
- Use fake clocks or explicit timeout knobs for timeout behavior.
- Avoid arbitrary sleeps. If a wait is necessary, use a named helper with a
  short deadline and clear failure message.
- Do not assert on private local paths except through `t.TempDir()` or an
  explicit fixture.
- Do not require a logged-in Discord account, installed agent CLI, tmux server,
  launchd service, or systemd service for unit tests.
- Keep external integration tests opt-in behind explicit environment variables
  if they are ever added.
- Every new fake should model only the behavior required by tests. Avoid
  building a second implementation of Discord, tmux, or the runtime API.

## Required Test Seams

Some high-risk paths need small dependency seams before they can be tested well.
Add these seams incrementally and keep production behavior unchanged.

| Area | Seam to introduce | Purpose |
| --- | --- | --- |
| Frontend | Wails API mock factory | Component tests can assert calls and injected failures. |
| Runtime API | Handler test service builder | Tests can create projects/tasks and call endpoints directly. |
| Desktop backend | Runtime client interface or adapter | Wails methods can be tested without a real runtime daemon. |
| CLI | Runtime client factory injection | Cobra commands can be tested without sockets. |
| Discord bot | Minimal Discord session interface | Channel/message operations can be faked deterministically. |
| Worktree/session | Cleanup and tmux fake hooks | Failure paths can be forced without chmod hacks or real tmux. |

Do not introduce broad abstractions only for tests. Each seam must be tied to a
specific phase and deleted or kept only if it clarifies production code.

## Fixture Strategy

Use a small set of shared fixtures to keep tests readable:

- `testProject`: project with ID, path, default agent, access state, and
  language data.
- `testTask`: local task with status, workspace mode, agent, and optional
  session fields.
- `testDiscordTask`: Discord-owned task with sync state and channel metadata.
- `testRuntimeError`: typed conflict, validation, timeout, cleanup, and sync
  errors.
- `testTranscript`: user, assistant, tool trace, status, and system messages.

Keep fixtures close to the package that owns the data shape. Shared fixtures are
allowed only when two packages intentionally share a public contract.

## Coverage Policy

Do not chase line coverage blindly. Each new test should do at least one of:

- Lock down a user-visible workflow.
- Lock down a lifecycle or cleanup invariant.
- Lock down a structured error contract.
- Lock down a migration or persisted state compatibility rule.
- Lock down a security-sensitive config or secret handling rule.

Use coverage as a regression signal after those behaviors are represented.

Coverage should be read together with skipped paths:

- Generated, platform-specific, or `main()` wrapper code can stay low coverage.
- Core contracts, cleanup behavior, and sync behavior should have explicit
  scenario coverage even if package coverage is already high.
- A new public method in runtime, Desktop backend, CLI, or Discord code should
  come with at least one success test and one failure test unless it is pure
  delegation already covered by a contract test.

## P0: Test Harness and Frontend Unit Foundation

Goal: make frontend behavior testable instead of relying on string smoke tests.

Why first:

- Desktop UI has the highest untested surface area.
- Recent work split large views into feature modules; that creates natural
  seams for tests.
- Without a real frontend test runner, UI regressions remain invisible until
  manual use.

Work items:

- Add a frontend test runner such as Vitest with React Testing Library and
  jsdom.
- Keep `workflow-smoke.mjs` as a release smoke guard, but make `npm test` run
  real unit tests plus the smoke checks.
- Add a small Wails API mock helper for `window.go.main.App` calls.
- Add tests for shared pure logic in `appLogic.ts`.
- Add initial component tests for Settings, Discord, Projects, and Monitor
  views.

Acceptance criteria:

- `npm --prefix desktop/frontend test` runs DOM/component tests.
- `npm --prefix desktop/frontend run build` remains unchanged.
- CI and `make release-verify` still run frontend tests.
- Frontend tests can mock successful and failed API calls without a runtime.
- The source-string smoke checks either remain as a separate script or are
  explicitly called from the new test command.
- Tests run reliably on local macOS and GitHub Linux runners.

Initial test targets:

- `humanizeErrorMessage` maps project-mode conflict to actionable copy.
- Settings default agent select calls `UpdateDefaultAgent`.
- Discord disconnect/connect form requires a token after disconnect.
- Project add modal shows validation errors from API failures.
- Monitor empty and populated states render expected task status rows.

Implementation notes:

- Start with `appLogic.test.ts`; it has no DOM dependency and proves the test
  runner is wired correctly.
- Add `test/setup.ts` to install Wails globals, `ResizeObserver`, and any DOM
  APIs needed by components.
- Avoid rendering full `App.tsx` initially. Test feature views directly with
  mocked props and mocked API calls.
- Keep xterm-heavy session views out of P0 unless a stable DOM shim is added.

## P1: Runtime API Error and Lifecycle Contract Tests

Goal: make runtime API behavior impossible to change silently.

Why second:

- Runtime is the source of truth for Desktop, CLI, and Discord.
- The most important failures are HTTP/API contract failures, not just internal
  function failures.
- These tests protect later Desktop and CLI tests from chasing unstable APIs.

Work items:

- Add table-driven tests for task endpoints in `internal/runtime`.
- Expand handler tests for project, config, Discord, and agent endpoints.
- Assert status code, error code, retryability, user-safe message, and metadata.
- Cover cleanup partial-success behavior for delete and stop/kill flows.
- Add tests for invalid workspace mode, missing project/task, invalid agent,
  and duplicate project-mode task creation.

Acceptance criteria:

- Project-mode conflict always returns HTTP 409, `conflict`, retryable=true,
  and includes the active task ID.
- Validation failures do not report retryable=true.
- Delete task cleanup failure keeps the user-visible warning/error contract.
- Discord task hard sync failures preserve task state for retry.
- Runtime API handler tests fail if response shape drifts.
- Handler tests can create a runtime test service without binding a real socket.
- Runtime tests do not require tmux, Discord, or an installed agent CLI unless
  explicitly marked as smoke/integration.

Initial test targets:

- `POST /v1/tasks` with active project-mode task.
- `DELETE /v1/tasks/{id}` when worktree cleanup fails.
- `POST /v1/discord/tasks/{id}/sync` for non-Discord, missing, timeout, and
  failed-sync cases.
- `PATCH /v1/config` default agent update and invalid agent name.
- `GET /v1/status` includes runtime recovery fields.

Implementation notes:

- Prefer `httptest` against the runtime handler/router over calling handler
  internals directly.
- Use the same typed error struct expected by clients in assertions.
- Add regression tests next to the endpoint file that owns the behavior.
- If a test needs to force cleanup failure, introduce a narrow cleanup hook
  rather than depending on OS permission quirks.

## P2: Desktop Backend API Bridge Tests

Goal: protect the Wails-facing backend that Desktop calls directly.

Why third:

- `internal/desktop` currently has many 0% functions in high-risk paths.
- Desktop API wrappers translate runtime behavior into frontend-visible data.
- Regressions here cause silent UI failures even when runtime itself is correct.

Work items:

- Add fake runtime client coverage for Desktop runtime-backed methods.
- Add tests for direct-mode fallbacks only where direct mode remains supported.
- Cover Discord connect/disconnect/status/sync behavior.
- Cover task creation variants: normal, no-prompt, Discord, structured Codex,
  structured Claude.
- Cover log stream start/stop forwarding and event names.
- Cover project candidate discovery and validation failure reporting.

Acceptance criteria:

- `DiscordDisconnect` clears stored token state and next connect requires a
  token unless runtime reports a reusable token.
- `DiscordTaskSync` returns clear errors and does not swallow runtime failures.
- `CreateDiscordTask` sets Discord task interface and workspace mode correctly.
- `CreateTaskNoPrompt` does not accidentally run a prompt path.
- Runtime event forwarding emits stable event names consumed by frontend.
- Desktop backend tests cover both success and runtime-client failure for each
  high-risk Wails method.
- Tests assert returned DTOs, not only absence of error.

Initial test targets:

- `CreateDiscordTask` with project/worktree modes.
- `DiscordDisconnect` followed by connect without token.
- `RuntimeStart`, `RuntimeStop`, and `RuntimeStatus` error translation.
- `StartLogStream` duplicate stream handling and stop cleanup.
- `ListProjectCandidates` excludes already registered projects.

Implementation notes:

- Introduce a small runtime client interface only for methods Desktop actually
  calls.
- Keep existing direct-mode tests separate so runtime-backed behavior remains
  clear.
- Prefer testing Wails-facing methods instead of private helper functions when
  the public method can be made deterministic with a fake client.

## P3: CLI Runtime Command Tests

Goal: make CLI parsing, output, and runtime error handling stable.

Why fourth:

- `cmd/agx` is the lowest Go coverage package outside the desktop entrypoint.
- CLI behavior is public API for users and scripts.
- Runtime-backed commands currently have many 0% builder and resolver functions.

Work items:

- Add fake runtime client tests around command constructors.
- Assert command args, flags, output formatting, and error text.
- Cover project/task resolution by ID, name, and path.
- Cover workspace mode and all-mighty flags for task creation.
- Cover Discord chat commands and attachment commands.

Acceptance criteria:

- `agx task create` validates workspace mode and passes mode to runtime client.
- `agx task create --discord` uses Discord task creation path.
- Conflict errors show actionable text and preserve the runtime error code.
- `agx project list`, `agx task list`, and `agx ps` produce stable output.
- `agx chat disconnect` does not imply token reuse after disconnect.
- CLI tests assert both stdout/stderr and returned errors.
- Command tests do not require an AGX runtime socket.

Initial test targets:

- `newRuntimeClientTaskCreateCmd`.
- `newRuntimeClientChatConnectCmd`.
- `newRuntimeClientChatDisconnectCmd`.
- `resolveRuntimeProject` and `resolveRuntimeTask`.
- `fmtRuntimeTask` status, agent, workspace, and Discord fields.

Implementation notes:

- Build command tests around `cmd.SetArgs`, `cmd.SetOut`, and `cmd.SetErr`.
- Add a fake runtime client with recorded calls so flag parsing can be asserted.
- Keep golden output small; prefer table assertions for fields that are likely
  to change intentionally.

## P4: Discord Sync and Bot Orchestration Tests

Goal: lock down Discord channel creation, retry, and message routing.

Why fifth:

- Discord bugs are highly visible and often involve partial external state.
- Existing tests cover some renderer/command behavior, but bot orchestration has
  many 0% functions.
- This phase should use fake Discord clients instead of real network calls.

Work items:

- Add fake Discord session/client coverage for bot channel operations.
- Test sync idempotency and duplicate channel prevention.
- Test hard sync retry after timeout or partial failure.
- Test user message routing into task input or structured agent stream.
- Test progress message update/clear behavior.
- Test managed channel reset protection.

Acceptance criteria:

- Creating a Discord task creates or reuses exactly one managed task channel.
- Hard sync after a failed initial sync can recover and create the channel.
- Timeout failures are persisted as failed sync state and are retryable.
- Messages from unauthorized users are ignored or rejected consistently.
- Progress message updates are coalesced and cleared without leaking stale UI.
- Tests can simulate Discord API latency, 404, permission failure, and duplicate
  channel state without real network calls.

Initial test targets:

- `EnsureCategory`, `EnsureTextChannel`, and `UpdateChannelTopic`.
- `ResetManagedChannels` with mixed AGX/non-AGX channels.
- `SendInteractivePrompt` component payload.
- `UpdateProgressMessage` and `ClearProgressMessage`.
- Discord task message routing to runtime/task bridge.

Implementation notes:

- Define fake Discord operations in terms of channel/message records and errors.
- Include tests for idempotent re-entry: calling sync twice for the same task
  should not create duplicate channels.
- Treat external API timeout behavior as a contract: state must remain
  retryable and user-visible.

## P5: Session, Worktree, and Recovery Failure Tests

Goal: make destructive and cleanup-heavy behavior safe to change.

Why sixth:

- Session/worktree code already has moderate coverage, but missing failure
  combinations can leave user-visible residue.
- This area protects data safety and cleanup correctness.
- It should follow runtime/Desktop API tests so surface contracts already exist.

Work items:

- Add worktree cleanup failure tests that assert logs and returned warnings.
- Add session stop/kill tests for tmux failure and missing session cases.
- Add runtime restart recovery tests for stale sessions and orphan worktrees.
- Add tests for project-mode task constraints across restart.
- Add tests for cleanup when task database state changes before filesystem
  cleanup completes.

Acceptance criteria:

- Worktree cleanup failure never appears as a fully successful delete.
- Missing tmux sessions are handled idempotently.
- Runtime restart marks stale active tasks offline.
- Runtime-owned orphan worktrees are cleaned or reported with context.
- Project-mode active task constraints survive restart.
- Cleanup tests assert both persisted task state and filesystem/session side
  effects.

Initial test targets:

- `Manager.DeleteTask` partial cleanup failure.
- `Manager.KillTask` with tmux failure.
- Runtime recovery summary fields.
- Worktree remove permission failure.
- Orphan worktree cleanup skip rules for non-AGX directories.

Implementation notes:

- Test with real temporary Git repositories where Git/worktree semantics are
  required.
- Use fake tmux controllers for process/session failure modes.
- Keep destructive cleanup tests restricted to `t.TempDir()` roots.

## P6: Release Gate and Coverage Budgets

Goal: keep coverage from regressing after the tests are added.

Why last:

- Coverage gates are useful only after critical tests exist.
- Early hard thresholds can block useful refactors without protecting behavior.
- This phase turns the new tests into a durable release contract.

Work items:

- Add per-package coverage reporting to CI logs.
- Add a soft warning threshold first, then a hard threshold for key packages.
- Keep `make release-verify` as the full local release gate.
- Add a short testing ownership section to `docs/DEVELOPMENT.md`.
- Document how to run frontend unit tests, smoke tests, and release tests.

Suggested initial thresholds after P0-P5:

| Area | Target |
| --- | ---: |
| `internal/runtime` | 70%+ |
| `internal/desktop` | 65%+ |
| `internal/discord` | 70%+ |
| `cmd/agx` | 55%+ |
| Frontend critical workflows | Required tests, no line threshold initially. |

Acceptance criteria:

- CI prints package coverage for every push.
- Release verification fails if critical workflow tests fail.
- Coverage gates block large regressions in runtime, desktop backend, and
  Discord packages.
- Frontend tests cover workflows before a line threshold is introduced.
- Coverage thresholds are introduced as warnings for at least one successful CI
  cycle before becoming hard failures.
- CI output includes enough detail to identify the package that regressed.

## Phase Dependencies

The phases are ordered to avoid brittle tests and excessive mocking:

| Phase | Depends on | Unlocks |
| --- | --- | --- |
| P0 | Current frontend feature split | Real frontend workflow tests and safer UI refactors. |
| P1 | Existing runtime API test helpers | Stable contracts for Desktop and CLI tests. |
| P2 | P1 error contracts | Desktop bridge tests that assert typed runtime failures. |
| P3 | P1 error contracts, optional P2 client seam | Stable public CLI behavior. |
| P4 | P1 sync contracts, fake Discord seam | Reliable Discord retry and orchestration tests. |
| P5 | P1/P2 lifecycle contracts | Safe destructive cleanup and restart recovery tests. |
| P6 | P0-P5 critical tests | Coverage budgets that protect behavior instead of noise. |

P0 and P1 can start in parallel if the frontend test runner is kept independent
from runtime API refactors. P2 should not hard-code runtime error strings before
P1 stabilizes the typed error payloads.

## Test Ownership Map

This map defines where new tests should land. Prefer adding tests next to the
code that owns the behavior.

| Behavior | Primary test location | Secondary test location |
| --- | --- | --- |
| Frontend error copy and UI state | `desktop/frontend/src/**/*.test.tsx` | `desktop/frontend/src/appLogic.test.ts` |
| Wails API mock behavior | `desktop/frontend/src/test/*` | `internal/desktop/*_test.go` |
| Runtime HTTP contracts | `internal/runtime/*_test.go` | `cmd/agx/*_test.go` for CLI translation |
| Desktop backend runtime bridge | `internal/desktop/*_test.go` | Frontend component tests for display |
| CLI flags and output | `cmd/agx/*_test.go` | Runtime handler tests for API contract |
| Discord channel and message orchestration | `internal/discord/*_test.go` | `internal/runtime/*discord*_test.go` |
| Session lifecycle | `internal/session/*_test.go` | `internal/runtime/*_test.go` for API behavior |
| Worktree cleanup | `internal/worktree/*_test.go` | `internal/session/*_test.go` and runtime delete tests |
| Database compatibility | `internal/db/*_test.go` | `docs/RELEASE.md` migration notes |
| Release artifact safety | `scripts/*_test.go` | CI release verify job |

## Phase Output Matrix

Each phase should produce concrete artifacts, not just more coverage.

| Phase | Required output |
| --- | --- |
| P0 | Frontend test runner, Wails API mock helper, first component tests, smoke script still enforced. |
| P1 | Runtime handler contract test helpers and table-driven endpoint/error tests. |
| P2 | Desktop runtime-client fake and Wails-facing method tests for critical workflows. |
| P3 | CLI fake runtime client and command tests for task, project, chat, and status flows. |
| P4 | Fake Discord client/session and idempotent sync/channel/message tests. |
| P5 | Cleanup/recovery failure tests using fake tmux hooks and real temp worktrees. |
| P6 | CI coverage report, warning thresholds, hard thresholds after stable CI cycles. |

## Minimum Scenario Set

Before calling the roadmap complete, these scenarios must have automated tests:

| Scenario | Frontend | Desktop backend | Runtime | CLI | Discord/session |
| --- | --- | --- | --- | --- | --- |
| Project-mode conflict | Required | Optional | Required | Required | Optional |
| Discord disconnect token reuse prevention | Required | Required | Required | Optional | Optional |
| Discord task hard sync timeout and retry | Required | Required | Required | Optional | Required |
| Worktree cleanup partial failure | Required for display | Required | Required | Optional | Required |
| Default agent set to Codex and updateable | Required | Required | Required | Required | Not needed |
| Task workspace mode selection | Required | Required | Required | Required | Required for cleanup |
| Runtime restart recovery | Optional | Optional | Required | Optional | Required |
| Release artifact secret/state scan | Not needed | Not needed | Not needed | Not needed | Script tests required |

## Risk Matrix

| Risk | Impact | Mitigation |
| --- | --- | --- |
| Frontend tests become brittle after CSS/layout changes. | Slows UI work. | Query by role, label, visible text, and state, not class names. |
| Fakes drift from production clients. | False confidence. | Keep fakes minimal and assert public contracts at runtime handler boundaries. |
| Coverage gates land too early. | Blocks refactors without improving quality. | Start with reporting, then warnings, then hard gates after critical tests exist. |
| Tests rely on local machine state. | CI flakes. | Use `t.TempDir()`, fake clients, and no real Discord/tmux/agent requirements. |
| Broad test-only interfaces complicate production code. | Lower maintainability. | Add narrow seams tied to one phase and review whether they remain useful. |
| Release verification becomes too slow. | Developers skip it. | Keep unit tests fast; reserve real process/runtime checks for release smoke. |

## Definition of Done

A phase is done only when all of these are true:

- New tests fail against the previously broken or unprotected behavior.
- New tests pass locally and in GitHub Actions.
- `make release-verify` passes.
- Any new fake, helper, or dependency seam is documented in the relevant test
  file or package.
- No test requires private credentials, local runtime state, or a specific user
  path.
- The phase updates this document or `docs/DEVELOPMENT.md` if it changes the
  standard test command.

## Do Not Do

Avoid these shortcuts:

- Do not replace behavior tests with snapshots of entire rendered pages.
- Do not add sleeps to hide race conditions.
- Do not use real Discord tokens in CI.
- Do not add hard coverage thresholds before P0-P5 critical tests exist.
- Do not mock the function under test; mock its external boundary.
- Do not add large interfaces that mirror entire packages just to satisfy one
  test.

## Implementation Order

Recommended commit sequence:

1. Add frontend test runner and one small appLogic test.
2. Add Settings and Discord frontend component tests.
3. Add Runtime task endpoint contract tests.
4. Add Desktop backend tests for Discord/task bridge methods.
5. Add CLI task/chat command tests.
6. Add Discord bot orchestration fake-client tests.
7. Add session/worktree cleanup failure tests.
8. Add CI coverage reporting and soft thresholds.

Each commit should run:

```bash
npm --prefix desktop/frontend test
npm --prefix desktop/frontend run build
go test ./...
make release-verify
```

After CI coverage reporting lands, also verify the GitHub Actions result for
the pushed commit.
