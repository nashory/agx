# Discord Sync Latency Design

This document defines the plan for making Discord task-channel sync fast and
predictable after AGX task creation.

## Problem

Creating an AGX task is already fast, but Discord channel sync can lag badly
when a background full sync is running.

Observed runtime evidence:

```text
task_create request: 2ms
structured_discord_task_start: 518ms
discord_task_sync_*: discord sync is already running
discord_soft_sync_background: context deadline exceeded
```

The user-visible result is that Desktop shows a new Discord task quickly, but
the matching Discord channel appears late or not at all until a manual sync
works.

## Goals

- Create the Discord channel for a newly-created task within 3 seconds under
  normal Discord API conditions.
- Keep AGX task creation fast. Discord sync must not block the runtime task API
  beyond a short foreground attempt.
- Prioritize single-task channel creation over full background reconciliation.
- Avoid duplicate Discord channels when retries, restarts, or gateway redelivery
  happen.
- Keep full soft sync and hard sync available for repair, but make them lower
  priority than new task-channel creation.
- Make sync latency diagnosable from AGX logs with per-step elapsed times.
- Keep behavior robust when Discord rate limits, times out, or returns partial
  success.

## Non-Goals

- Do not make task creation wait for a full Discord guild reconciliation.
- Do not remove hard sync. It remains the explicit repair tool.
- Do not rely on Discord channel creation being instant. AGX should expose
  pending, retrying, and failed states.
- Do not rebuild the entire Discord integration in one large rewrite.

## Current Bottlenecks

### Global Sync Lock

`Bridge` uses one `syncMu` for soft sync, hard sync, task sync, and delete sync.
When `SoftSync` is running, `SyncTaskChannel` returns
`discord sync is already running` immediately.

This is the main reason new task channels wait behind unrelated maintenance
work.

### Full Soft Sync Is Too Heavy

`SyncActiveTasksWithCleanup` walks all projects, all mirrored tasks, mappings,
cleanup paths, and command permissions. On the current local database there are:

```text
projects: 13
tasks: 26
live Discord tasks: 26
discord mappings: 44
```

That makes soft sync a maintenance operation, not a latency-sensitive task
creation path.

### Single-Task Sync Still Performs Full-Guild Work

`SyncTaskChannel` does more than the minimum required for a new task:

```text
EnsureControlChannel
EnsureCategory
EnsureTextChannel
ConfigureCommandPermissions
RefreshTaskStreams
```

The ensure calls currently use guild-wide channel lookup. Command permission
updates can also add Discord REST latency and rate-limit risk.

### Observability Is Too Coarse

Logs show that a sync timed out or conflicted, but they do not show which
Discord REST step was slow:

- guild channel list
- category lookup/create
- task channel create
- topic/name update
- command permission update
- stream refresh

Without this detail, tuning is slower than it should be.

## Target Architecture

### Sync State Vocabulary

AGX already persists Discord task sync status as:

```text
pending
synced
failed
```

Keep those values as the database compatibility layer for the first latency
work. Add richer runtime and UI meaning through existing timestamps, attempts,
channel IDs, retry fields, and derived DTO labels instead of immediately
renaming the stored enum.

Derived UI labels:

```text
pending + active scheduler job: creating channel
pending + retry_after set: retry scheduled
pending + no active job: sync queued
synced + channel id: channel ready
failed + retryable error: sync failed, retry available
failed + non-retryable error: sync failed, manual action required
```

If a later migration adds stored states such as `running` or `retrying`, it
must:

- backfill existing `pending` rows safely
- keep older Desktop builds from crashing on unknown states
- update frontend labels and API tests in the same phase
- preserve existing `discord_channel_id`, attempts, and last error fields

### Priority Sync Scheduler

Replace ad hoc sync goroutines and one global `syncMu` conflict path with a
small runtime-owned scheduler.

Queue types:

```text
high: task-create channel sync, task-delete channel delete
normal: manual task sync from Desktop
low: background soft sync, permission refresh, cleanup
exclusive: hard sync
```

Rules:

- High-priority task sync should not be rejected because low-priority soft sync
  is running.
- Soft sync should yield between projects/tasks when high-priority work is
  queued.
- Hard sync remains exclusive and may block other sync work, but status should
  clearly show that hard sync is running.
- Duplicate work for the same task should be coalesced.
- A failed task sync should be retryable without requiring a full soft sync.

Scheduler status must expose the owner of active work:

```text
sync_id
kind
priority
task_id, when applicable
started_at
elapsed_ms
current_step
```

That status is required for useful `sync is already running` diagnostics. A
blocked or queued task sync should say which operation is holding the sync lane.

The scheduler should start with two lanes:

```text
task lane: high/normal task create, update, delete, and manual task sync
maintenance lane: soft sync, cleanup, permission refresh
```

Hard sync is exclusive and pauses both lanes. Soft sync must not hold the task
lane while it walks projects or performs cleanup.

The first implementation may keep one worker goroutine per lane. It does not
need a general-purpose job framework.

### Fast Task-Channel Path

Add a narrow fast path for newly-created Discord tasks:

```text
load task and project
resolve cached control channel mapping
resolve cached project category mapping
create or find task text channel
upsert task channel mapping
mark task sync success
start/refresh that task stream
enqueue permission refresh in low-priority queue
```

The fast path should avoid:

- full project scan
- full task scan
- orphan cleanup
- command permission updates
- hard sync side effects

If the project category mapping is missing, the fast path may create or ensure
that one category. It should not repair unrelated projects.

The fast path must not use the same blocking lock as full soft sync. If it still
shares `Bridge.syncMu`, it will not fix the observed latency issue.

Minimum lookup policy:

1. Prefer existing DB mapping.
2. If the mapped channel exists, update only that channel.
3. If the mapped channel is missing, search by deterministic task channel name
   and topic containing the full task ID.
4. If no existing channel is found, create one channel.
5. Persist the mapping before reporting the task channel as ready.

The topic already includes the full task ID. That should be treated as the
recovery key when a Discord channel exists but the DB mapping is missing.

### Partial Success Recovery

Discord channel creation and local DB mapping persistence are not atomic. AGX
must explicitly handle partial success.

Important cases:

```text
Discord channel created, DB mapping write succeeds:
  mark synced

Discord channel created, DB mapping write fails:
  log partial success with channel id
  record failed sync state with channel id if possible
  retry by searching for task ID in channel topic before creating another channel

DB mapping exists, Discord channel missing:
  clear or replace stale mapping only after replacement succeeds
  create/find one replacement channel
  update mapping

Discord create times out:
  do not assume failure
  retry first by finding a channel with matching task ID/topic
  create only if no matching channel exists

Channel delete succeeds, DB mapping delete fails:
  retry mapping delete
  do not recreate the deleted channel during cleanup
```

Acceptance criteria for all sync implementations:

- A retry after ambiguous Discord timeout does not create duplicate task
  channels.
- A retry after DB mapping failure recovers a previously-created channel.
- A stale mapping is replaced only when AGX has a valid replacement channel or a
  confirmed delete.
- Partial success logs include the Discord channel ID when known.

### Discord Channel Cache

Reduce repeated guild-wide channel scans by caching channel snapshots and known
mapping lookups.

Cache inputs:

- `discord_mappings`
- Discord `GuildChannels` snapshot with a short TTL
- channels created during the current runtime process

Cache invalidation:

- update cache after successful create/delete/update
- refresh from Discord on not found, permission error, or hard sync
- expire snapshot after a short interval, for example 15-30 seconds

The database mapping remains the source of truth. The cache is only a latency
optimization.

The minimum cache work belongs in the fast-path phase, not only the later cache
phase:

- avoid guild-wide lookup when a valid DB mapping exists
- remember channels created during the current runtime process
- update cache after successful create/delete/update

The later cache phase can add snapshot TTLs and broader soft-sync optimization.

### Deferred Permission Refresh

Move command permission updates out of the task creation critical path.

New behavior:

- Task channel creation succeeds once the channel exists and mapping is stored.
- Permission refresh is queued as low-priority work and debounced.
- Discord command handlers still validate allowed users and task ownership in
  AGX, so permission refresh lag should not be the only safety boundary.
- If permission refresh fails, AGX logs it and reports sync status, but does
  not undo the task channel.

Permission refresh has its own degraded state. A task channel can be ready while
command permissions are stale.

Derived states:

```text
channel ready, permissions current
channel ready, permission refresh queued
channel ready, permission refresh failed
```

Security rule:

- Discord slash-command permissions are a convenience and UX boundary.
- AGX command handlers must continue to enforce allowlisted user IDs and task
  channel ownership for every command.
- A stale permission refresh must not grant execution authority by itself.

### Durable Sync State

Use the existing task sync state as the product contract and extend it where
needed.

Each Discord task sync should record:

- state: pending, synced, failed, plus derived running/retrying labels
- started/completed timestamps
- attempt count
- last error code and safe message
- target Discord channel ID when known
- operation kind: create, update, delete, manual, repair

This lets Desktop show "Discord channel pending" instead of looking silently
stuck.

Near-term persistence should use the current table where possible:

```text
status: pending | synced | failed
attempts
discord_channel_id
last_success_at
last_failure_at
last_error
retry_after
updated_at
```

If extra fields are needed, add them through an additive migration:

```text
last_error_code
last_operation
last_sync_id
last_known_channel_id
permission_status
permission_last_error
```

Do not block the latency fix on a large schema migration unless tests prove the
existing fields cannot represent the necessary states.

## Logging Requirements

Every Discord sync operation should log one top-level operation and step-level
timings.

Top-level fields:

```text
operation
sync_id
task
project
guild
channel
priority
kind
status
elapsed_ms
error_code
error
```

Step examples:

```text
operation="discord_task_sync_step" step="load_task" elapsed_ms=1
operation="discord_task_sync_step" step="ensure_category" elapsed_ms=180
operation="discord_task_sync_step" step="create_channel" elapsed_ms=620
operation="discord_task_sync_step" step="store_mapping" elapsed_ms=3
operation="discord_permission_refresh" status="queued"
```

Logs must make these cases easy to distinguish:

- sync queued
- sync skipped as duplicate
- sync yielded to higher-priority work
- Discord API timeout
- Discord rate limit
- permission error
- mapping exists but channel is missing
- channel created but DB mapping failed
- sync blocked by hard sync with active `sync_id`
- sync queued behind another task job
- permission refresh failed after channel readiness

## User Experience

Desktop task cards and task detail should reflect Discord sync state:

```text
Discord: creating channel
Discord: channel ready
Discord: retrying sync
Discord: sync failed
```

Manual "Sync with Discord" should enqueue or run a normal-priority task sync. If
hard sync or another exclusive operation is running, the UI should say that
instead of failing silently.

Discord channel creation should be optimistic:

- create task immediately in Desktop
- show pending sync state
- create Discord channel as high-priority background work
- update task card/detail when channel mapping appears
- surface retryable failure with a manual sync action

The UI should distinguish channel readiness from permission refresh readiness.
The task card should prioritize the channel state because that is what users
notice first.

## Phased Plan

### P0: Instrument Current Sync Path

Goal: identify exactly which Discord REST calls dominate latency.

Work items:

- Add step-level logging around `SyncTaskChannel`.
- Add step-level logging around `SyncActiveTasksWithCleanup`.
- Log queue/conflict decisions in `syncDiscordAsync`,
  `syncDiscordTaskAsync`, and manual task sync.
- Include task/project/channel IDs, priority, elapsed time, and error code.
- Include active sync owner fields when work is blocked or queued.
- Define task-to-channel latency measurement points.
- Add tests for logging wrappers where practical.

Acceptance criteria:

- A slow task sync log shows which step consumed the time.
- A blocked task sync log identifies the running sync kind.
- AGX logs are enough to explain why a channel did not appear within 3 seconds.
- Logs can measure task-to-channel latency from task row creation to persisted
  Discord channel mapping.

### P1: Task-Lane Scheduler and Fast Task-Channel Sync

Goal: make newly-created task channels avoid full soft-sync work and avoid
being blocked by background soft sync.

Work items:

- Add a minimal two-lane scheduler: task lane and maintenance lane.
- Route task-create sync through the task lane.
- Keep soft sync on the maintenance lane.
- Add a fast sync method focused only on one task channel.
- Skip command permission refresh in the fast path.
- Reuse existing project/category mappings before guild-wide lookup.
- Create only the missing task channel and update only that mapping.
- Refresh only the new task stream after success.
- Add minimum channel cache behavior for newly-created/mapped channels.
- Add partial-success recovery for create succeeded but mapping failed.
- Keep existing `SyncTaskChannel` as the safer repair path during transition.

Acceptance criteria:

- New task channel creation does not scan every project/task.
- New task channel creation does not run command permission updates inline.
- Duplicate calls for the same task do not create duplicate channels.
- New task sync is not rejected with `sync is already running` because a soft
  sync is active.
- A retry after ambiguous Discord timeout checks for an existing task channel
  before creating another one.

### P2: Scheduler Completion and Soft-Sync Yielding

Goal: complete scheduler behavior for coalescing, manual sync, soft sync, and
hard sync.

Work items:

- Introduce a sync job type with priority, kind, task ID, operation, and dedupe
  key if P1 used a narrower internal type.
- Replace remaining direct background soft-sync goroutines with scheduler
  submissions.
- Make soft sync yield or defer when high-priority task sync is queued.
- Coalesce duplicate soft sync requests.
- Coalesce duplicate task sync requests by task ID.
- Keep hard sync exclusive with clear status.
- Expose active and queued scheduler status through runtime status.

Acceptance criteria:

- Multiple task changes during one soft sync produce bounded queued work.
- Hard sync status clearly explains why task sync is waiting.
- Manual task sync can report queued, running, blocked by hard sync, failed, or
  completed.
- Soft sync cannot starve forever under repeated task sync jobs.

### P3: Deferred Permission Refresh

Goal: remove command permission updates from latency-sensitive paths.

Work items:

- Add a debounced low-priority permission refresh job.
- Queue permission refresh after task channel create/delete.
- Keep command handlers' AGX-side authorization checks as the immediate safety
  boundary.
- Log permission refresh failures separately from channel sync failures.
- Persist or derive permission degraded state for Desktop and logs.
- Add tests for debounce and retry behavior.

Acceptance criteria:

- Task channel sync can succeed even if permission refresh fails.
- Permission refresh does not run more than once for a burst of task changes.
- Desktop/Discord status shows permission refresh failures without hiding
  channel readiness.
- Slash command handlers still reject unauthorized users even when Discord
  command permissions are stale.

### P4: Channel Snapshot Cache

Goal: optimize broader sync paths by reducing repeated `GuildChannels` calls.

Work items:

- Add a short-lived guild channel snapshot cache inside the Discord bot/sync
  layer.
- Update cache on create, update, and delete success.
- Invalidate cache on not-found and permission errors.
- Prefer DB mappings before scanning Discord channels.
- Add tests with fake clients to verify fewer guild list calls.

Acceptance criteria:

- Single-task sync avoids repeated guild-wide channel scans when mappings are
  valid.
- Cache invalidation handles stale mappings and deleted Discord channels.
- Full soft sync can still force a fresh snapshot when needed.
- Soft sync makes at most one fresh guild channel list call per snapshot window
  unless invalidation is required.

### P5: Durable Retry and UI State

Goal: make sync state visible and automatically recoverable.

Work items:

- Extend sync-state persistence if current fields are insufficient.
- Record enough metadata to derive running, success, failure, retrying, and
  retry eligibility without breaking the stored `pending | synced | failed`
  compatibility layer.
- Add bounded retry with backoff for transient Discord errors.
- Surface sync state in Desktop task cards and task detail.
- Make manual "Sync with Discord" clear failed/retry state after success.
- Add frontend compatibility handling for unknown future sync statuses.

Acceptance criteria:

- A timed-out channel create leaves a visible retryable state.
- Restarting AGX can resume or retry pending task syncs.
- Desktop does not require opening AGX logs to know sync failed.
- Existing `pending | synced | failed` rows migrate or render correctly.

## Test Plan

Backend tests:

- Fast task sync creates only the requested task channel.
- Fast task sync reuses existing project/category mappings.
- Fast task sync handles stale task mapping by recreating one channel.
- Fast task sync recovers a channel created before DB mapping failed.
- Fast task sync does not duplicate channels after ambiguous Discord timeout.
- Permission refresh is deferred and debounced.
- Scheduler prioritizes task sync over soft sync.
- Scheduler coalesces duplicate task sync jobs.
- Soft sync yields when high-priority work is queued.
- Hard sync remains exclusive.
- Retry state is persisted and cleared after success.
- Existing `discord_task_sync_state` rows remain readable after migrations.
- Unauthorized Discord commands are rejected even if command permissions are
  stale.

Frontend tests:

- Task card shows pending, ready, retrying, and failed Discord sync states.
- Task card distinguishes channel ready from permission refresh failed.
- Manual task sync reports queued/running/conflict states clearly.
- Bulk task changes do not spam the UI with duplicate sync errors.
- Unknown future sync statuses do not crash rendering.

Manual verification:

- Create a Discord task with many existing sessions.
- Confirm task API returns quickly.
- Confirm Discord channel appears within 3 seconds under normal network
  conditions.
- Trigger soft sync while creating tasks and confirm task channels still appear.
- Simulate Discord timeout and confirm retry/error state is visible.
- Simulate channel-created/mapping-failed and confirm retry reuses the existing
  channel.
- Confirm slash commands still reject unauthorized users while permission
  refresh is queued or failed.

## Rollout Strategy

Implement incrementally:

1. Ship logging first so current bottlenecks are measurable.
2. Add the task-lane scheduler and fast task sync together so fast sync is not
   blocked by soft sync.
3. Complete scheduler coalescing, soft-sync yielding, and hard-sync status.
4. Defer permission refresh and expose degraded permission state.
5. Add broader channel cache and durable retry UI.

Each phase should be independently shippable and covered by tests before moving
to the next phase.

## Risks

- Prioritizing task sync over soft sync can delay cleanup of stale channels.
  This is acceptable because stale cleanup is maintenance, while new channel
  creation is user-facing.
- Deferring command permission refresh can briefly leave slash command
  permissions stale. AGX-side authorization must remain the real enforcement
  layer.
- Caching Discord channel snapshots can hide external Discord changes briefly.
  Short TTLs and invalidation on errors keep this bounded.
- Scheduler bugs could starve soft sync. Add tests for fairness and bounded
  pending work.
- Partial success between Discord and SQLite can create duplicate channels if
  retries do not search by task ID before creating.
- Existing Desktop builds may not understand new persisted sync status values.
  Prefer derived states first and migrate stored enums only with compatibility
  tests.

## Success Metrics

- P50 task-to-channel latency: under 1 second.
- P95 task-to-channel latency: under 3 seconds under normal Discord API
  conditions.
- `discord sync is already running` should not appear for task creation sync
  unless hard sync is running.
- Discord sync timeout logs include the slow step.
- Manual task sync succeeds after a transient failure without hard sync.

Latency measurement:

```text
start: task row created for a Discord task
ready: Discord task channel ID is persisted in discord_task_sync_state and
       discord_mappings
display: Desktop receives task update showing channel-ready state
```

The primary product metric is `ready - start`. `display - start` is a secondary
Desktop propagation metric.
