# Discord Voice Message STT Design

This document defines how AGX should handle Discord voice messages sent to
AGX-managed task channels.

## Problem

Discord mobile voice messages arrive as attachments, typically named
`voice-message.ogg` and encoded as Ogg/Opus. AGX can persist attachments, but a
raw audio file path is not enough for the current text-first agent flow. The
agent should receive the spoken content as text without requiring cloud STT or
manual transcription.

## Goals

- Accept Discord voice-message attachments without rejecting the whole message.
- Treat local voice STT as an optional capability, never as an AGX core
  dependency.
- Transcribe voice messages locally before sending the user prompt to the agent
  when STT is enabled and available.
- Keep the original audio file as an attachment for audit/debugging.
- Avoid cloud speech APIs and avoid sending audio outside the user's machine.
- Make transcription failures visible without dropping accompanying text.
- Keep Discord gateway handling responsive and idempotent.
- Keep the implementation testable with a fake transcriber.

## Non-Goals

- Do not build a general audio player or transcript editor in the first pass.
- Do not bundle large Whisper models into AGX releases initially.
- Do not require every user to configure STT before Discord text/image flows
  work.
- Do not support arbitrary audio/video formats in the first pass.
- Do not attempt speaker diarization or timestamp-rich transcript rendering
  initially.

## User Experience

Voice STT has three user-facing modes:

```text
disabled: never run STT; store voice attachments and show a clear notice
auto:     run STT only when the local environment is ready
enabled:  require STT for voice messages and surface setup errors explicitly
```

Default mode should be `auto`. AGX must remain fully usable when Whisper is not
installed.

When a user sends a Discord voice message:

```text
User sent 1 voice message.

Voice transcript:
"Create a new Discord project task for the main branch and check the logs."

Attachments:
- voice-message.ogg audio/ogg 184233 bytes
  saved: /Users/alice/.config/agx/attachments/task-123/msg-456/voice-message.ogg
  source: https://cdn.discordapp.com/attachments/...
```

The agent receives the transcript as the primary user intent. The original
audio remains available through the attachment summary.

If STT fails but the message has text or other accepted attachments, AGX should
still deliver the message and include a warning:

```text
Voice transcription failed:
- voice-message.ogg: whisper command failed: ...
```

If the message contains only voice attachments and all transcription attempts
fail, AGX should return a Discord-visible error asking the user to retry or send
text. The audio can remain persisted so the failure is diagnosable, but AGX
should not send an empty/ambiguous prompt to the agent.

If voice STT is disabled or unavailable in `auto` mode, voice-only messages
should not make AGX look broken. The Discord response should explain the
situation directly:

```text
AGX received your voice message, but local voice transcription is not available.
Enable local STT in AGX Desktop settings, or send the message as text.
```

## Runtime Flow

The voice path should extend the existing Discord attachment flow:

```text
Discord MessageCreate
  -> collect text and attachments
  -> runtime reserves Discord message ID
  -> runtime downloads accepted attachments
  -> runtime detects audio/ogg voice attachments
  -> runtime transcribes voice attachments locally
  -> runtime builds prompt with:
       original text
       voice transcript block
       attachment metadata
       transcription warnings, if any
  -> runtime delivers prompt to task
  -> runtime appends transcript entry
  -> runtime marks Discord message delivered
```

The Discord bot layer should not run Whisper. It should keep passing structured
`IncomingTaskMessage` values into the runtime. The runtime already owns
attachment storage, message idempotency, transcript writes, and prompt
construction; STT belongs beside those steps.

## Configuration Model

Local STT configuration should be explicit and optional:

```text
discord.voice_stt.mode          disabled | auto | enabled
discord.voice_stt.ffmpeg_path   optional path or command name
discord.voice_stt.whisper_path  optional path or command name
discord.voice_stt.model_path    optional model path
discord.voice_stt.language      auto | ko | en | ...
discord.voice_stt.timeout       duration, default 60s
```

Mode semantics:

- `disabled`: Do not run STT. Persist voice attachments if the attachment type
  is accepted, but do not deliver voice-only prompts to the agent.
- `auto`: Run STT only if `ffmpeg`, Whisper, and the model are discoverable.
  Missing dependencies are a graceful unavailable state, not a startup error.
- `enabled`: The user explicitly wants STT. Missing dependencies should produce
  a clear Discord/Desktop error for voice messages, but still must not prevent
  AGX startup or non-voice task flows.

Desktop settings should expose this as a small "Voice transcription" section:

- Mode selector: Disabled, Auto, Enabled.
- `ffmpeg` path.
- Whisper binary path.
- Model path.
- Language selector.
- `Setup` button that prepares the default local Whisper model under the AGX
  config directory and saves the resolved local configuration. The default
  model is `ggml-large-v3-turbo.bin`.
- `Test STT` button that validates dependencies without sending a task prompt.
- Status line: Ready, Disabled, Model missing, Whisper binary missing,
  `ffmpeg` missing, or Last test failed.

CLI setup support uses the same local setup path:

```text
agx voice-stt setup
```

CLI/doctor validation support should come later:

```text
agx doctor voice-stt
```

That command should check binary paths, model readability, a tiny conversion
smoke test if possible, and print setup guidance.

## Local Whisper Backend

Use a runtime-owned transcriber interface:

```go
type voiceTranscriber interface {
  Transcribe(ctx context.Context, inputPath string) (VoiceTranscript, error)
}

type VoiceTranscript struct {
  Text     string
  Engine   string
  Model    string
  Language string
}
```

Initial implementation should shell out to local tools instead of linking a
large native library into AGX:

1. Convert Discord Ogg/Opus to a temporary 16 kHz mono WAV with `ffmpeg`.
2. Run a configured local Whisper command against the WAV.
3. Read plain text output.
4. Delete temporary conversion/output files.

Recommended command support:

```text
ffmpeg -y -i <input.ogg> -ar 16000 -ac 1 <tmp.wav>
whisper-cli -m <model.bin> -f <tmp.wav> -otxt -of <tmp-output-prefix>
```

AGX should not assume a fixed binary path. The backend should resolve command
paths from config first, then from `PATH` only in `auto` or `enabled` mode.

Do not bundle a Whisper model in releases. Model download/setup should be a
guided opt-in flow shared by macOS and Windows; the runtime should also work
with an existing local `whisper.cpp` installation.

## Prompt Construction

Extend `buildDiscordAttachmentPrompt` or split it into a richer prompt builder
that receives prepared attachments plus transcription results.

Ordering rules:

1. User text first.
2. Voice transcript block second.
3. Transcription warnings third.
4. Attachment metadata last.

For multiple voice messages, preserve Discord attachment order:

```text
Voice transcripts:
1. voice-message.ogg
   "First transcript..."
2. voice-message-2.ogg
   "Second transcript..."
```

If a voice attachment has an empty transcript, treat it as a transcription
failure and include a warning instead of silently passing an empty block.

## Persistence And Idempotency

Do not re-transcribe the same Discord attachment on gateway redelivery or retry
if a successful transcription already exists.

Recommended first schema:

```sql
CREATE TABLE task_attachment_transcriptions (
  id TEXT PRIMARY KEY,
  attachment_id TEXT NOT NULL UNIQUE,
  status TEXT NOT NULL CHECK(status IN ('succeeded', 'failed')),
  engine TEXT,
  model TEXT,
  language TEXT,
  text TEXT,
  error TEXT,
  created_at TIMESTAMP NOT NULL,
  updated_at TIMESTAMP NOT NULL,
  FOREIGN KEY(attachment_id) REFERENCES task_attachments(id) ON DELETE CASCADE
);
```

Why a separate table:

- Keeps attachment metadata generic.
- Lets cleanup cascade with attachment deletion.
- Lets Desktop later show transcript status without parsing prompt text.
- Allows failed transcription records to suppress expensive immediate retries
  unless the user explicitly retries/syncs.

For the first implementation, failed transcriptions may be retried on the next
explicit Discord hard sync or task sync action, but not repeatedly inside the
same gateway delivery loop.

## Timeouts And Resource Limits

Voice transcription must be bounded:

```text
Max voice attachment size: reuse current per-file attachment limit initially
Max voice duration: best effort from ffprobe later; not required for MVP
Transcription timeout: 60 seconds default
Concurrent transcriptions per task: 1
Global transcription concurrency: 1 initially
```

The task lock currently serializes attachment preparation for a task. Reuse that
for MVP correctness. If voice transcription becomes too slow, move STT work to a
bounded global worker queue while keeping message delivery ordered per task.

## Failure Handling

Failure classes:

- Missing `ffmpeg`: local STT unavailable.
- Missing Whisper binary/model: local STT unavailable.
- Conversion failure: unsupported/corrupt audio.
- Whisper timeout: voice message too long or machine too slow.
- Empty transcript: no speech detected or transcription failed silently.

User-visible behavior:

- Text plus failed voice: deliver text and include warning.
- Image plus failed voice: deliver image metadata and include warning.
- Voice-only and mode `disabled`: return Discord notice and do not deliver an
  empty prompt.
- Voice-only and mode `auto` with unavailable STT: return Discord notice and do
  not deliver an empty prompt.
- Voice-only and mode `enabled` with unavailable/failed STT: return Discord
  error with setup guidance.
- Voice-only with failed transcription: return Discord error and do not deliver
  an empty prompt.

AGX logs should include task ID, Discord message ID, attachment ID, filename,
duration if known, backend, elapsed time, and failure reason. Do not log full
transcript text at info/error level by default.

## Security And Privacy

- Run STT locally only.
- Do not send audio to cloud services.
- Do not execute or trust attachment filenames.
- Continue validating Discord CDN hosts and attachment sizes before download.
- Store audio under the existing attachment root with task cleanup/pruning.
- Store transcript text in the local AGX DB because it becomes part of the task
  transcript and agent prompt.

## Implementation Plan

### Phase 1: Accept And Persist Voice Audio

- Allow Ogg attachments via byte sniffing (`OggS` -> `audio/ogg`).
- Add downloader tests for Discord voice messages.
- Update attachment design docs.

### Phase 2: Optional STT Configuration

- Add `voice_stt.mode` with `auto` as the default.
- Add config fields for ffmpeg, Whisper binary, model path, language, and
  timeout.
- Add Desktop settings controls and validation status.
- Ensure missing STT dependencies never fail runtime startup.

### Phase 3: Transcriber Abstraction

- Add `voiceTranscriber` interface and no-op/unavailable implementation.
- Make the unavailable implementation mode-aware so `disabled`, `auto`, and
  `enabled` produce different user-facing notices.
- Add unit tests with a fake transcriber.

### Phase 4: Local Whisper CLI Backend

- Implement Ogg-to-WAV conversion with `ffmpeg`.
- Implement `whisper-cli` invocation with timeout.
- Parse `.txt` output.
- Clean temp files.
- Add tests around command construction, timeout, missing binary, and empty
  transcript.

### Phase 5: Prompt Integration

- Extend Discord attachment preparation to produce voice transcription results.
- Insert voice transcript blocks into the prompt before attachment metadata.
- Fail voice-only messages when no transcript is available.
- Persist transcript warnings in task transcript and Discord responses.

### Phase 6: Persistence And Retry

- Add `task_attachment_transcriptions` migration and store methods.
- Reuse successful transcription rows on duplicate Discord events.
- Record failed rows with error summaries.
- Add explicit retry path through hard sync or a later `retranscribe` action.

### Phase 7: Observability And Desktop

- Add AGX logs for STT start/success/failure with safe metadata.
- Show voice transcript status in Discord task transcript UI later.
- Add `agx doctor` checks for local STT dependencies.

## Design Review

### What Looks Solid

- Keeping STT inside the runtime is the right ownership boundary. The Discord
  bot remains a transport adapter, while the runtime owns persistence,
  idempotency, prompt construction, and transcript records.
- Making STT optional avoids turning Whisper, `ffmpeg`, model files, package
  signing, and platform-specific binary issues into AGX install blockers.
- Persisting the original audio is important. It makes retries, diagnostics,
  and future transcript improvements possible.
- A separate transcription table is cleaner than adding voice-only fields to
  `task_attachments`.

### Main Risks

- Synchronous STT can make Discord message delivery feel slow. MVP can run it
  inline under timeout, but the design should leave room for a bounded worker
  queue if voice usage grows.
- `auto` mode can be confusing if AGX silently skips transcription. The UX must
  always send a clear Discord notice for voice-only messages when STT is
  unavailable.
- Whisper backend fragmentation is real. `whisper.cpp`, Python Whisper, and
  platform packages have different flags and output behavior. The first
  implementation should support one backend well, preferably `whisper.cpp`
  `whisper-cli`, before adding templates.
- Storing transcript text in the DB is unavoidable because it becomes user
  input, but it increases local sensitive-data footprint. This should be
  documented alongside existing transcript privacy notes.

### Required Guardrails

- AGX startup must never validate or require Whisper.
- Non-voice Discord messages must not be affected by STT configuration errors.
- Voice-only messages must never deliver only `saved: ...voice-message.ogg` as
  the prompt unless the user explicitly asks for raw attachment forwarding
  later.
- All external command execution must use argument arrays, not shell strings.
- Temporary converted audio files must be created under AGX-owned temp dirs and
  removed on success, failure, and timeout.
- Logs must include safe metadata and error summaries, not full transcript text
  at info/error level.

## Test Plan

- Ogg attachment is accepted and persisted.
- Voice-only message with successful fake STT delivers transcript prompt.
- Text plus voice delivers text and transcript.
- Multiple voice attachments preserve order.
- Failed voice plus text delivers text with warning.
- Failed voice-only message returns an error and does not call the agent.
- Duplicate Discord message reuses persisted transcription.
- Missing Whisper config produces a clear Discord-visible error.
- Attachment cleanup removes audio and transcription rows.
- Runtime reset removes audio and transcription state.

## Open Questions

- Should AGX ship a helper command such as `agx doctor voice-stt` or
  `agx setup voice-stt`?
- Which Whisper backend should be the blessed default: `whisper.cpp`
  `whisper-cli`, OpenAI `whisper` Python CLI, or both through command templates?
- Should Desktop expose voice transcript text separately from the raw prompt?
- Should a task-level "retry STT" button be added for failed voice messages?
