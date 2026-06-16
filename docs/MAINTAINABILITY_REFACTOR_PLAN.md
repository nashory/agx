# Maintainability Refactor Plan

This plan covers the next code-quality push after the test coverage roadmap:

1. Split the large Desktop `App.tsx` surface into smaller feature owners.
2. Reduce duplicated CLI fake runtime clients and command test setup.
3. Add deterministic Discord bot event-handler tests around real handler
   behavior, not just router helpers.

The goal is to raise maintainability without changing user-visible behavior.
Every phase should land with tests and CI green before the next phase starts.

## Current Problems

### Desktop `App.tsx`

`desktop/frontend/src/App.tsx` is over 2,400 lines and owns too many concerns:

- global app navigation and selected entity state,
- project/task creation workflows,
- Discord task detail rendering,
- terminal/log streaming state,
- runtime metadata/event refresh,
- action error modal state,
- API side effects and optimistic refresh logic.

The existing feature views are a good start, but `App.tsx` still contains large
workflow components and event handlers that are hard to test directly.

### CLI Tests

`cmd/agx/main_test.go` now has useful fake-runtime coverage, but the fake client
is growing in the same file as unrelated command tests. This makes future CLI
tests harder to read and encourages copy/paste fakes.

The production command seams are acceptable: small interfaces let tests avoid a
runtime socket. The problem is test organization, not production behavior.

### Discord Bot Handler Tests

`internal/discord` has strong command-router and syncer tests, but the bot event
handlers in `bot.go` still have limited direct coverage. The highest-risk gaps
are:

- interaction responses are deferred with the correct ephemeral behavior,
- unauthorized plain messages do not reach task services,
- duplicate Discord messages are dropped before invoking router/service work,
- component choice handlers disable selected buttons and report errors,
- progress message update/clear behavior remains stable around pending timers.

## Design Principles

- Do not refactor and change behavior in the same commit.
- Move code behind existing public behavior first, then add or adjust tests.
- Prefer feature-local modules over broad generic abstractions.
- Keep production interfaces narrow. Test helpers can be richer than production
  interfaces, but they should live in test files.
- Preserve current Wails bindings and API method names.
- Keep every phase shippable: frontend tests, Go tests in CI, release verify,
  and Docker smoke must pass.

## Phase 1: Split Desktop `App.tsx` by Workflow

Goal: reduce `App.tsx` to orchestration and route-level state while feature
workflows own their rendering and local behavior.

### Target Modules

Create or extend feature modules in this order:

| Module | Move from `App.tsx` | Notes |
| --- | --- | --- |
| `features/tasks/TaskCreateModal.tsx` | task create modal, workspace mode, Discord/local mode, create error state | Reuse existing API calls through props first. |
| `features/tasks/TaskDetailView.tsx` | non-Discord task detail shell if currently embedded | Avoid xterm-heavy tests initially. |
| `features/discord/DiscordTaskDetail.tsx` | Discord task detail view and `Sync with Discord` action | Test sync success/failure with mocked API. |
| `features/runtime/RuntimeEventBridge.ts` or hook | runtime event subscription/update scheduling | Keep no DOM rendering here. |
| `features/errors/ActionErrorModal.tsx` | action error modal presentation | Pure component, easy to test. |

### Implementation Order

Split this phase into smaller commits so review and rollback stay easy:

1. **Phase 1A**: Extract `ActionErrorModal` as a pure component.
   - Target: remove the global action error dialog markup from `App.tsx`.
   - Test: close behavior and optional primary action behavior.
2. **Phase 1B**: Extract the Discord task sync header/action surface before the
   full detail view.
   - Target: isolate `Sync with Discord`, sync status display, and error/log
     callbacks.
   - Reason: the full Discord task detail currently also owns transcript,
     scrolling, file panel, preview, and task refresh behavior. Moving it as one
     block would reduce `App.tsx` size but just relocate the complexity.
3. **Phase 1C**: Extract `DiscordTaskDetail` once the sync action surface is
   testable.
4. **Phase 1D**: Extract task creation modal into `features/tasks`.
5. **Phase 1E**: Move small pure helpers near the feature that owns them.
6. Only after extraction, simplify `App.tsx` state names and prop plumbing.

### Acceptance Criteria

- `App.tsx` drops materially in size, with staged targets:
  - under 2,450 lines after Phase 1A,
  - under 2,150 lines after Phase 1C,
  - under 1,900 lines after Phase 1D,
  - under 1,800 lines after helper cleanup.
- No Wails API signature changes.
- Existing UI text and class names are preserved unless a test proves a better
  user-facing behavior is required.
- Add component tests for:
  - action error modal close/primary action behavior,
  - Discord task sync success and failure,
  - task create workspace-mode and Discord/local mode selection.

## Phase 2: Consolidate CLI Test Fakes

Goal: make new CLI command tests cheap to add without inflating
`cmd/agx/main_test.go`.

### Target Structure

Add a CLI test helper file:

```text
cmd/agx/runtime_client_test.go
```

It should contain:

- `fakeRuntimeClient` with small optional function hooks,
- helpers such as `executeCommand(cmd, args...) (stdout string, stderr string, err error)`,
- reusable project/task fixtures,
- assertion helpers for output snippets where useful.

Move runtime command tests out of `main_test.go` by domain:

```text
cmd/agx/runtime_task_test.go
cmd/agx/runtime_chat_test.go
cmd/agx/runtime_project_test.go
```

### Rules

- Do not build a full fake runtime server.
- Keep default fake methods explicit and boring: return configured slices or
  clear sentinel errors.
- When a fake method records input, store exactly the fields asserted by tests.
- Use small interfaces already accepted by command constructors.

### Acceptance Criteria

- `main_test.go` stops being the dumping ground for every runtime fake method.
- Existing runtime command tests move into domain-specific files as part of the
  fake-client cleanup.
- Runtime command tests remain socket-free.
- Existing CLI tests are preserved.
- Add at least one project command test after the fake is moved:
  - `project list` stable output with default agent,
  - `project delete` resolves name and calls full project ID,
  - or `project config --agent` passes through to runtime.

## Phase 3: Discord Bot Event Handler Test Seam

Goal: test `Bot.Add*Handler` behavior without a real Discord connection.

### Problem

`Bot` currently stores a concrete `*discordgo.Session`. Router tests cover the
domain behavior, but handler tests need to assert Discord-specific side effects:

- `InteractionRespond` flags,
- `InteractionResponseEdit` content,
- `ChannelMessageSend` fallback errors,
- reactions and duplicate message filtering,
- message/component edit payloads.

### Preferred Seam

Do not replace the full `Bot.session` field in one step. `Bot` currently uses
`*discordgo.Session` for lifecycle, command registration, guild/channel sync,
message sending, progress updates, and event handlers. A single replacement
interface would either become too large or force a broad refactor.

Instead, start with handler-specific seams:

1. Keep `Bot.session *discordgo.Session` for lifecycle and channel-management
   behavior.
2. Extract handler bodies into small methods that accept a narrow handler
   session interface.
3. Register those methods from `AddCommandHandler`, `AddPlainMessageHandler`,
   `AddComponentHandler`, and `AddReactionHandler`.
4. Test the extracted handler methods with fake sessions.

This gives direct coverage for Discord event behavior without changing the
public `Bot` construction path.

Example shape:

```go
type interactionSession interface {
    Channel(channelID string, options ...discordgo.RequestOption) (*discordgo.Channel, error)
    InteractionRespond(*discordgo.Interaction, *discordgo.InteractionResponse, ...discordgo.RequestOption) error
    InteractionResponseEdit(*discordgo.Interaction, *discordgo.WebhookEdit, ...discordgo.RequestOption) (*discordgo.Message, error)
}

type messageSession interface {
    ChannelMessageSend(string, string, ...discordgo.RequestOption) (*discordgo.Message, error)
    ChannelMessageSendComplex(string, *discordgo.MessageSend, ...discordgo.RequestOption) (*discordgo.Message, error)
    ChannelMessageEdit(string, string, string, ...discordgo.RequestOption) (*discordgo.Message, error)
    ChannelMessageEditComplex(*discordgo.MessageEdit, ...discordgo.RequestOption) (*discordgo.Message, error)
    ChannelMessageDelete(string, string, ...discordgo.RequestOption) error
    MessageReactionAdd(string, string, string, ...discordgo.RequestOption) error
}
```

If progress tests need timer control later, introduce `progressSession` and a
small clock/timer seam separately. Do not add timer abstractions before a test
needs them.

### Test Strategy

Add a fake session that records handlers and method calls. Tests should trigger
registered handlers directly with `discordgo.InteractionCreate`,
`discordgo.MessageCreate`, or `discordgo.MessageReactionAdd` events.

Initial tests:

- Slash command from unauthorized user responds ephemerally and does not call
  task service.
- Plain task message from unauthorized user sends a rejection message and does
  not call `SendTaskMessage`.
- Duplicate plain message ID is ignored on the second delivery.
- Component choice with a valid selected option edits the original message to
  disable buttons and sends the choice to the task.
- Component choice service failure sends `AGX choice failed: ...`.
- `UpdateProgressMessage` creates one progress message and coalesces duplicate
  content without sending extra edits.

### Acceptance Criteria

- No real Discord network calls in tests.
- No sleeps except bounded timer tests; prefer direct helper calls where
  possible.
- Existing `NewBot` behavior remains unchanged.
- Bot handler tests assert both Discord response payloads and command-service
  calls.

## Phase 4: Verification and CI

Every commit in this refactor sequence should run:

```bash
npm --prefix desktop/frontend test
npm --prefix desktop/frontend run build
go test ./...
make release-verify
```

Local Go execution may be blocked by endpoint policy on some machines. In that
case, record the local `signal: killed` result and rely on GitHub Actions for
Go verification.

CI must stay green after each phase:

- Unit Tests on Ubuntu and macOS.
- Frontend test/build.
- Release Verify.
- Linux package smoke.
- Docker smoke.

## Phase Boundaries

Recommended commit sequence:

1. Extract pure Desktop error modal component and tests.
2. Extract Discord task detail component and tests.
3. Extract task create modal/component and tests.
4. Move CLI fake runtime helper out of `main_test.go`.
5. Add project/chat/task command tests using the shared fake.
6. Add Discord bot session seam.
7. Add bot event handler tests.

Avoid bundling all Desktop extraction in one large commit. The review risk is
lower if each commit moves one component and preserves behavior.

## Definition of Done

- `App.tsx` is smaller and mostly route/orchestration code.
- CLI command tests share one helper fake instead of copy/paste method sets.
- Discord bot handler behavior is covered with fake session tests.
- Latest GitHub Actions run is green.
- The refactor does not alter runtime API contracts, Wails bindings, or release
  artifact layout.
