# Lifecycle Failure Audit

This audit tracks lifecycle and cleanup failures that must not disappear
silently. It is intentionally scoped to task/session/worktree/Discord cleanup
paths. General best-effort transcript writes, response body closes, and test
cleanup helpers are outside this P0 audit unless they affect user-visible task
lifecycle behavior.

## Returned to Callers

These failures are part of the user-facing operation result.

- Task deletion cleanup failures return `session.TaskCleanupError` after the
  task row has been removed.
- Runtime task delete returns an error response when worktree or tmux cleanup
  fails after the local delete succeeds.
- Discord `/kill` returns a partial-success warning when task cleanup fails
  after the task row is removed.
- Legacy task startup rollback returns the primary startup error plus cleanup
  failures for generated worktrees, task rows, and partial tmux windows.
- Legacy task restart rollback returns the primary restart error plus cleanup
  failures and restores the previous runtime state when possible.
- Structured runtime Discord task rollback returns the primary agent startup
  error plus Discord channel, worktree, and task row cleanup failures.
- Desktop direct-mode structured task rollback returns the primary failure plus
  generated worktree and task row cleanup failures.

## Logged

These failures happen in detached background work where the original caller is
no longer waiting. They must be visible in AGX logs.

- Runtime Discord startup failures.
- Background runtime Discord soft-sync failures.
- Foreground Discord task sync failures that queue a background retry.
- Background Discord task sync retry failures.
- Asynchronous Discord task channel cleanup failures after task deletion.
- Desktop background Discord soft-sync failures.
- Desktop Discord task cleanup sync failures.

## Intentionally Best Effort

These failures are safe to ignore because they clean temporary helper resources
or close handles after the main result is already known.

- Temporary command script deletion in `internal/session/script`.
- Temporary status file deletion in `internal/session/script`.
- Temporary partial download deletion in attachment download cleanup.
- File or process handle closes where no user-visible state depends on the
  close result.
- Test-only cleanup in `t.Cleanup`.

## Deferred to P1b

These paths need context ownership and cancellation cleanup, so they belong with
the P1b context/readiness work rather than P0.

- Detached `context.Background()` calls used by runtime and Discord background
  workers.
- Agent interrupt calls used as best-effort cancellation while replacing or
  stopping structured agent turns.
- Polling sleeps used for tmux readiness and runtime startup readiness.

## Rule for New Code

New task lifecycle code should choose exactly one of these outcomes for every
cleanup error:

- return it to the caller;
- log it with task/project/operation context;
- classify it as intentionally best effort in this document;
- move it into a named follow-up phase with a reason.

