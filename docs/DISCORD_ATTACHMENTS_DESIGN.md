# Discord Attachment Handling Design

This document defines how AGX should handle images and other Discord message
attachments sent to AGX-managed task channels.

## Problem

Today the Discord plain-message path forwards only `MessageCreate.Content` to
the AGX task. Discord attachments live in `MessageCreate.Attachments`, and AGX
does not currently read, persist, or describe them.

User-visible consequence:

- Image plus text: only the text reaches the agent.
- Image only: the message may be ignored because the content is empty.
- Agents may reply that they cannot see the screenshot, even though the user
  attached one in Discord.

## Goals

- Preserve attached screenshots and files in runtime-owned local state.
- Pass attachment context to agents in a form that is stable and reproducible.
- Keep task transcripts useful by recording attachment metadata.
- Bound disk usage with explicit retention and pruning.
- Avoid relying on Discord CDN URLs as the only source of truth.
- Keep the first implementation compatible with all current agents.
- Leave room for agent-specific multimodal support later.

## Non-Goals

- Do not build a full file-management UI in the first pass.
- Do not assume every agent CLI can consume remote image URLs.
- Do not expose attachment files over an AGX HTTP server initially.
- Do not keep deleted task attachments indefinitely.
- Do not accept unbounded file sizes or arbitrary content types.

## Recommended Approach

AGX should download Discord attachments into runtime-owned task storage and pass
both the local path and source URL to the agent.

Example prompt fragment appended to the user message:

```text
Attachments:
- screenshot.png image/png 184233 bytes
  saved: /Users/alice/.config/agx/attachments/task-123/msg-456/screenshot.png
  source: https://cdn.discordapp.com/attachments/...
```

The local path is the primary reference. The source URL is diagnostic metadata
and a fallback for agents or humans that can use it.

The Discord package should not send raw prompt strings once attachments are
involved. It should pass a structured message into the runtime command service:

```go
type IncomingTaskMessage struct {
	Text             string
	DiscordMessageID string
	Attachments      []IncomingAttachment
}

type IncomingAttachment struct {
	DiscordAttachmentID string
	Filename            string
	ContentType         string
	SizeBytes           int64
	URL                 string
}
```

The runtime service owns downloading, persistence, metadata writes, transcript
updates, and prompt construction. Keeping these steps in the runtime avoids
duplicating storage policy in the Discord bot layer and keeps Desktop, CLI, TUI,
and future APIs aligned around the same attachment lifecycle.

## Why Not URL-Only

Passing only Discord CDN URLs is tempting because it is quick, but it is not a
robust product behavior:

- Discord CDN URLs can depend on Discord access rules, redirects, and future
  token/expiry behavior.
- Agent CLIs differ in whether they fetch and interpret remote images.
- A transcript replayed later may not have access to the original URL.
- AGX cannot enforce size, MIME, or retention policy if it never owns the file.
- Desktop, TUI, and future transcript viewers need a stable local artifact.

URL-only forwarding can be used as a short-lived debug fallback, but it should
not be the main design.

## Storage Layout

Runtime-owned attachments should live under `DefaultPaths().ConfigDir`:

```text
~/.config/agx/attachments/
  task-<task-id>/
    msg-<discord-message-id>/
      <sanitized-filename>
```

Example:

```text
~/.config/agx/attachments/
  task-3cfaa.../
    msg-1249999999999999999/
      screenshot.png
```

Rules:

- Use task ID and Discord message ID directories to avoid filename collisions.
- Sanitize filenames and preserve a safe extension where possible.
- If multiple attachments share a filename, append a stable suffix.
- Store files with user-only permissions where the platform supports it.
- Never write outside the attachment root, even if Discord sends a hostile
  filename.

## Metadata

Add persisted attachment metadata so cleanup, transcript rendering, and future
UI surfaces do not need to scan the filesystem.

Suggested table:

```sql
CREATE TABLE task_attachments (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL,
  discord_message_id TEXT NOT NULL,
  discord_attachment_id TEXT NOT NULL,
  filename TEXT NOT NULL,
  content_type TEXT,
  size_bytes INTEGER NOT NULL,
  local_path TEXT NOT NULL,
  source_url TEXT,
  sha256 TEXT,
  created_at TIMESTAMP NOT NULL,
  FOREIGN KEY(task_id) REFERENCES tasks(id) ON DELETE CASCADE,
  UNIQUE(task_id, discord_message_id, discord_attachment_id)
);

CREATE INDEX task_attachments_task_id_idx ON task_attachments(task_id);
CREATE INDEX task_attachments_created_at_idx ON task_attachments(created_at);
CREATE INDEX task_attachments_discord_msg_idx ON task_attachments(task_id, discord_message_id);
```

The DB row should be created only after the file is downloaded, size-checked,
and fsynced or closed successfully.

Discord can redeliver gateway events, and AGX can be restarted while a message
is being processed. Attachment ingestion must therefore be idempotent:

- If `(task_id, discord_message_id, discord_attachment_id)` already exists,
  reuse the existing metadata and do not redownload the file.
- If a file exists without a metadata row, treat it as an incomplete write and
  remove it during startup orphan cleanup.
- If metadata exists but the file is missing, report the attachment as
  unavailable and do not claim that the agent received it.

Plain Discord messages also need durable idempotency, not only attachments.
The runtime should reserve a Discord message before delivering it to an agent:

```sql
CREATE TABLE discord_processed_messages (
  task_id TEXT NOT NULL,
  discord_message_id TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('processing', 'delivered', 'failed')),
  error TEXT,
  created_at TIMESTAMP NOT NULL,
  updated_at TIMESTAMP NOT NULL,
  PRIMARY KEY(task_id, discord_message_id),
  FOREIGN KEY(task_id) REFERENCES tasks(id) ON DELETE CASCADE
);
```

The delivery flow should be:

```text
insert (task_id, discord_message_id, processing)
  -> if the row already exists, return without delivering a duplicate prompt
  -> prepare accepted attachments
  -> deliver the combined prompt to the task
  -> append transcript metadata
  -> mark the message delivered
```

If delivery fails before the agent receives the prompt, AGX may delete the
processing row so Discord redelivery can retry. If the failure point is
ambiguous, mark the row `failed` and avoid automatic duplicate delivery.

## Attachment Types

Initial supported types:

```text
image/png
image/jpeg
image/webp
image/gif
```

Initial rejected types:

- executables
- archives
- files with unknown or empty content type unless extension and sniffing agree
- files larger than the configured limit

AGX can add PDF/text support later, but image support should ship first because
that is the user problem being solved.

## Size Limits

Recommended defaults:

```text
Max file size:       20 MB
Max attachments/msg: 5
Max bytes/task:      100 MB
```

When a limit is exceeded:

- Skip the offending attachment.
- Send a Discord response explaining which attachment was skipped and why.
- Still forward the text and accepted attachments to the task.

These values should be constants initially and config fields later if users need
to tune them.

The per-task byte limit is a task-level invariant. Concurrent Discord messages
for the same task must not each check a stale total and then all persist files.
The first implementation may serialize attachment preparation with the same
task lock used for message delivery. A later implementation can reserve bytes
in SQLite before downloading and then either commit or release the reservation.

## Download Flow

Plain Discord message flow:

```text
Discord MessageCreate
  -> ignore bot messages
  -> resolve AGX task from channel
  -> reserve the Discord message ID for this task
  -> collect message content
  -> collect attachments
  -> validate attachment count and metadata
  -> pass structured IncomingTaskMessage to runtime command service
  -> runtime validates task state
  -> runtime downloads accepted attachments through a bounded HTTP client
  -> persist files under attachment root
  -> insert attachment metadata rows
  -> append attachment summary to user prompt
  -> send combined prompt to runtime task service
  -> append transcript entry with user text and attachment summary
```

The attachment download should happen before the prompt is sent. If download
fails, AGX should not pretend the agent received the image.

The runtime should process each attachment with a temp-file flow:

```text
create temp file under the target message directory
  -> stream response with max-byte guard
  -> sniff content type from initial bytes
  -> fsync or close successfully
  -> atomic rename into the final sanitized path
  -> insert metadata row
```

If metadata insertion fails, remove the final file. If prompt delivery fails
after attachments were persisted, keep the files and metadata because the user
message can be retried and the transcript/error response should remain
diagnosable.

## Prompt Construction

If the user sends text and attachments:

```text
<user text>

Attachments:
- screenshot.png image/png 184233 bytes
  saved: /.../attachments/task-.../msg-.../screenshot.png
  source: https://cdn.discordapp.com/...
```

If the user sends attachments only:

```text
User sent 1 attachment.

Attachments:
- screenshot.png image/png 184233 bytes
  saved: /.../attachments/task-.../msg-.../screenshot.png
  source: https://cdn.discordapp.com/...
```

The attachment summary should be deterministic and compact. Do not paste binary
content or base64 into the prompt.

## Agent Compatibility

The first version should use local paths because that is the most universal
handoff:

- Claude Code: local file paths are more reliable than remote URLs for images.
- Gemini CLI: local paths are the natural integration point for image input.
- Codex and other CLIs: support varies; local files keep the data available for
  adapter-specific handling.

Future work can add agent-specific multimodal adapters:

```text
agent adapter
  -> accepts attachment metadata
  -> builds agent-specific argv/stdin/tool input
```

Until then, the prompt should include both local path and source URL so the
agent has the best available context.

## Retention and Cleanup

Attachment storage must have a lifecycle.

Default retention policy:

- Active and waiting tasks: keep attachments.
- Offline tasks: keep attachments while the task exists; prune them with the
  same age policy as completed tasks once they are older than the retention
  window.
- Deleted tasks: delete attachments immediately.
- Completed tasks: keep attachments for 7 days, then prune.
- Runtime reset: delete the entire attachment root.
- Startup cleanup: remove orphan attachment directories with no matching task or
  metadata row.

Add a prune command:

```bash
agx attachment prune --older-than 7d
agx attachment prune --task <task-id>
agx attachment list --task <task-id>
```

If a separate top-level command feels too large for the first pass, begin with:

```bash
agx runtime prune-attachments --older-than 7d
```

Until AGX stores explicit task completion timestamps, retention age should be
computed from `task_attachments.created_at`. If AGX later adds a task status
transition timestamp, prune eligibility should move to that timestamp so recent
attachments on long-running completed/offline tasks are not removed too early.

## Reset Behavior

`agx runtime reset --confirm` should remove:

```text
~/.config/agx/attachments
```

`--include-config` is not required because attachments are runtime state, not
configuration.

## Doctor Output

`agx doctor` should report attachment storage health:

```text
attachments: 42 files, 18.4 MB
attachment retention: completed tasks older than 7d pruneable
```

If the attachment root is not writable:

```text
attachments: unavailable (<error>)
```

## Security

Required safeguards:

- Use a bounded HTTP client with timeout.
- Download only from known Discord media hosts such as `cdn.discordapp.com` and
  `media.discordapp.net`.
- Reject redirects that leave the allowed Discord media host set.
- Enforce max bytes before and during download.
- Sniff content type from bytes, not only Discord metadata.
- Never execute attachment files.
- Never unpack archives in the first version.
- Sanitize filenames.
- Keep local files under the runtime config directory.
- Avoid logging full signed URLs at info level.
- Store source URLs for diagnostics, but mask or omit query tokens if Discord
  starts using signed URLs.

## Failure Behavior

If some attachments fail and some succeed:

- Forward accepted attachments.
- Tell the user which attachments failed.

If all attachments fail and there is no text:

- Do not send an empty prompt to the task.
- Reply in Discord with a clear error.

If the task is no longer live:

- Preserve existing inactive-task guidance.
- Do not download attachments unless the task message will be accepted.

If the runtime downloads attachments but fails to record metadata:

- Delete the downloaded files.
- Return a Discord-visible error.
- Do not send the prompt to the agent.

If the runtime records metadata but the agent message send fails:

- Keep the files and rows.
- Return the existing not-live or send-failure guidance.
- Allow a later retry to reuse the idempotent attachment rows.

## Implementation Plan

### Phase 1: Data and Storage

- Add attachment root path helper to `internal/runtime`.
- Add DB migration and store methods for task attachments.
- Add filename sanitization and attachment path construction helpers.
- Add idempotent lookup by task ID, Discord message ID, and Discord attachment
  ID.
- Include attachments in runtime reset cleanup.

### Phase 2: Discord Ingestion

- Extend plain Discord message handling to inspect `m.Attachments`.
- Replace the plain-message service string call with structured
  `IncomingTaskMessage` input.
- Add a runtime-owned attachment downloader with host, size, redirect, and
  content-type validation.
- Persist accepted files and metadata.
- Build the prompt attachment summary.
- Support attachment-only messages.
- Add unit tests around accepted, rejected, and failed attachments.

### Phase 3: Cleanup

- Delete task attachments when a task is deleted.
- Add completed-task retention pruning.
- Add orphan directory cleanup on runtime startup.
- Add CLI prune/list commands or a runtime subcommand.
- Add doctor disk-usage reporting.

### Phase 4: Agent-Specific Enhancements

- Introduce an agent attachment input abstraction.
- Add better multimodal handling for agents that support image path input.
- Keep the prompt summary as fallback behavior.

## Test Plan

Unit tests:

- Message with text and one image produces prompt with attachment summary.
- Image-only message is accepted and forwarded.
- Oversized attachment is rejected with a Discord response.
- Unsupported content type is rejected.
- Redirect to a non-Discord host is rejected.
- Filename traversal attempts stay under the attachment root.
- Duplicate Discord gateway event reuses existing attachment metadata.
- Metadata insert failure removes downloaded files and does not send the prompt.
- Agent send failure keeps persisted attachments for retry.
- Task deletion removes attachment rows and files.
- Runtime reset removes attachment root.
- Offline task attachments follow the completed-task retention policy.
- Prune removes completed-task attachments older than retention.
- Orphan cleanup removes unreferenced directories.

Integration smoke:

- Fake Discord message with image attachment reaches a fake structured agent as
  a local file path.
- Docker mode can save attachments under `/home/agx/.config/agx/attachments`.

Manual test:

1. Start AGX runtime with Discord enabled.
2. Create a Discord task.
3. Send a screenshot plus text in the task channel.
4. Verify the agent receives the local attachment path.
5. Verify the file exists under `~/.config/agx/attachments`.
6. Delete the task and verify attachment files are removed.

## Open Questions

- Should completed-task retention be fixed at 7 days or configurable from the
  first release?
- Should Desktop expose attachment previews immediately, or only after the core
  storage path is stable?
- Should non-image files be supported in the same framework after image support
  lands?
- Should source URLs be stored indefinitely, or only until the local file is
  downloaded?
