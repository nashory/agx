package discord

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nashory/agx/internal/agentstream"
)

type recordingSemanticSender struct {
	messages []string
	progress []string
	cleared  int
}

func (s *recordingSemanticSender) SendMessage(ctx context.Context, channelID, content string) error {
	s.messages = append(s.messages, content)
	return nil
}

func (s *recordingSemanticSender) UpdateProgressMessage(ctx context.Context, channelID, content string) error {
	s.progress = append(s.progress, content)
	return nil
}

func (s *recordingSemanticSender) ClearProgressMessage(ctx context.Context, channelID string) error {
	s.cleared++
	return nil
}

type failingProgressSender struct {
	recordingSemanticSender
	progressErr error
}

func (s *failingProgressSender) UpdateProgressMessage(ctx context.Context, channelID, content string) error {
	s.progress = append(s.progress, content)
	return s.progressErr
}

type flakySemanticSender struct {
	recordingSemanticSender
	failures int
	err      error
}

func (s *flakySemanticSender) SendMessage(ctx context.Context, channelID, content string) error {
	if s.failures > 0 {
		s.failures--
		return s.err
	}
	return s.recordingSemanticSender.SendMessage(ctx, channelID, content)
}

func TestSemanticRendererRendersProgress(t *testing.T) {
	renderer := NewSemanticRenderer()
	actions := renderer.Render(agentstream.Event{Kind: agentstream.EventTurnStarted})
	if len(actions) != 1 {
		t.Fatalf("len(actions) = %d, want 1", len(actions))
	}
	if actions[0].Kind != RenderUpdateProgress || !strings.Contains(actions[0].Content, "Thinking") {
		t.Fatalf("action = %#v, want progress thinking action", actions[0])
	}
}

func TestSemanticRendererRendersSummarizedError(t *testing.T) {
	renderer := NewSemanticRenderer()
	actions := renderer.Render(agentstream.Event{
		Kind:  agentstream.EventError,
		Error: "detailed failure: connection refused while starting the agent",
	})
	if len(actions) < 2 || actions[0].Kind != RenderClearProgress {
		t.Fatalf("actions = %#v, want progress clear then error message", actions)
	}
	send := actions[1]
	if send.Kind != RenderSend || !send.HighPriority {
		t.Fatalf("action = %#v, want high-priority send", send)
	}
	if !strings.Contains(send.Content, "AGX agent error: detailed failure: connection refused while starting the agent") {
		t.Fatalf("content dropped the error summary: %q", send.Content)
	}
	if !strings.Contains(send.Content, "AGX Desktop task transcript") || strings.Contains(send.Content, "`/logs`") {
		t.Fatalf("content = %q, want task transcript guidance without logs guidance", send.Content)
	}
}

func TestSemanticRendererIncludesDiagnosticIDForErrors(t *testing.T) {
	renderer := NewSemanticRenderer()
	actions := renderer.Render(agentstream.Event{
		Kind:         agentstream.EventError,
		Error:        "content was flagged",
		DiagnosticID: "err_20260810_145300_ab12cd34",
	})
	if len(actions) != 2 {
		t.Fatalf("len(actions) = %d, want clear + message", len(actions))
	}
	content := actions[1].Content
	if !strings.Contains(content, "Diagnostic ID: `err_20260810_145300_ab12cd34`") {
		t.Fatalf("content = %q, want diagnostic id", content)
	}
}

func TestSemanticRendererRendersReconnectingErrorAsProgress(t *testing.T) {
	renderer := NewSemanticRenderer()
	actions := renderer.Render(agentstream.Event{
		Kind: agentstream.EventError,
		Error: strings.Join([]string{
			"Reconnecting... 3/5",
			"",
			"Recent codex output:",
			"ERROR opentelemetry_sdk: Bad Gateway",
		}, "\n"),
	})
	if len(actions) != 1 {
		t.Fatalf("len(actions) = %d, want progress update only", len(actions))
	}
	action := actions[0]
	if action.Kind != RenderUpdateProgress || action.HighPriority {
		t.Fatalf("action = %#v, want non-priority progress update", action)
	}
	if !strings.Contains(action.Content, "Reconnecting... 3/5") {
		t.Fatalf("content = %q, want reconnect summary", action.Content)
	}
	if strings.Contains(action.Content, "Recent codex output") {
		t.Fatalf("content includes raw recent output: %q", action.Content)
	}
}

func TestSemanticRendererRendersEmptyErrorWithFallback(t *testing.T) {
	renderer := NewSemanticRenderer()
	actions := renderer.Render(agentstream.Event{Kind: agentstream.EventError, Error: "   "})
	if len(actions) != 2 {
		t.Fatalf("len(actions) = %d, want clear + message", len(actions))
	}
	if !strings.Contains(actions[1].Content, "did not include details") {
		t.Fatalf("content = %q, want empty-error fallback", actions[1].Content)
	}
}

func TestSemanticRendererRendersLongErrorAsSingleSummary(t *testing.T) {
	renderer := NewSemanticRenderer()
	actions := renderer.Render(agentstream.Event{
		Kind:  agentstream.EventError,
		Error: strings.Repeat("stacktrace line\n", 400),
	})
	sends := 0
	for _, action := range actions {
		if action.Kind != RenderSend {
			continue
		}
		sends++
	}
	if sends != 1 {
		t.Fatalf("error sends = %d, want one summarized message", sends)
	}
	if strings.Contains(actions[1].Content, "stacktrace line\nstacktrace line") {
		t.Fatalf("content includes raw multiline error: %q", actions[1].Content)
	}
}

func TestSemanticRendererStripsANSIEscapesFromErrorSummary(t *testing.T) {
	renderer := NewSemanticRenderer()
	actions := renderer.Render(agentstream.Event{
		Kind:  agentstream.EventError,
		Error: "\x1b[31mERROR\x1b[0m codex_core::tools::router: apply_patch verification failed\nfull details",
	})
	if len(actions) != 2 {
		t.Fatalf("len(actions) = %d, want clear + message", len(actions))
	}
	content := actions[1].Content
	if strings.Contains(content, "\x1b[") {
		t.Fatalf("content includes ANSI escape: %q", content)
	}
	if !strings.Contains(content, "tool error: apply_patch verification failed") {
		t.Fatalf("content = %q, want summarized tool error", content)
	}
	if strings.Contains(content, "codex_core::tools::router") {
		t.Fatalf("content includes raw tool detail: %q", content)
	}
}

func TestSemanticRendererSummarizesTelemetryErrors(t *testing.T) {
	renderer := NewSemanticRenderer()
	actions := renderer.Render(agentstream.Event{
		Kind:  agentstream.EventError,
		Error: "\x1b[31mERROR\x1b[0m opentelemetry_sdk: name=\"BatchLogProcessor.ExportError\" error=\"Bad Gateway\"",
	})
	if len(actions) != 2 {
		t.Fatalf("len(actions) = %d, want clear + message", len(actions))
	}
	content := actions[1].Content
	if !strings.Contains(content, "telemetry export error") {
		t.Fatalf("content = %q, want telemetry summary", content)
	}
	if strings.Contains(content, "Bad Gateway") {
		t.Fatalf("content includes raw telemetry detail: %q", content)
	}
}

func TestSemanticRendererChunksAssistantMessage(t *testing.T) {
	renderer := SemanticRenderer{MaxMessageBytes: 80}
	actions := renderer.Render(agentstream.Event{
		Kind: agentstream.EventAssistantMessage,
		Text: strings.Repeat("hello world\n", 20),
	})
	if len(actions) < 2 {
		t.Fatalf("len(actions) = %d, want multiple chunks", len(actions))
	}
	for _, action := range actions {
		if action.Kind != RenderSend {
			t.Fatalf("action kind = %s, want send", action.Kind)
		}
		if len(action.Content) > 80 {
			t.Fatalf("chunk length = %d, want <= 80: %q", len(action.Content), action.Content)
		}
	}
}

func TestSemanticRendererKeepsCodeFenceValidAcrossChunks(t *testing.T) {
	renderer := SemanticRenderer{MaxMessageBytes: 80}
	actions := renderer.Render(agentstream.Event{
		Kind: agentstream.EventAssistantMessage,
		Text: "```go\n" + strings.Repeat("fmt.Println(\"hello\")\n", 10) + "```",
	})
	if len(actions) < 2 {
		t.Fatalf("len(actions) = %d, want multiple chunks", len(actions))
	}
	for _, action := range actions {
		if strings.Count(action.Content, "```")%2 != 0 {
			t.Fatalf("chunk has unbalanced code fence: %q", action.Content)
		}
	}
}

func TestSemanticRendererWrapsMarkdownTablesInCodeFence(t *testing.T) {
	renderer := NewSemanticRenderer()
	actions := renderer.Render(agentstream.Event{
		Kind: agentstream.EventAssistantMessage,
		Text: strings.Join([]string{
			"Difficulty breakdown:",
			"",
			"| Criterion | Score | Notes |",
			"|---|---|---|",
			"| Ingredients | 3/3 | all present |",
			"| Risk | 2/3 | manageable |",
			"",
			"Done.",
		}, "\n"),
	})
	if len(actions) != 1 {
		t.Fatalf("len(actions) = %d, want 1", len(actions))
	}
	content := actions[0].Content
	if !strings.Contains(content, "```text\n| Criterion | Score | Notes |") || !strings.Contains(content, "| Risk | 2/3 | manageable |\n```") {
		t.Fatalf("content = %q, want table wrapped in text code fence", content)
	}
}

func TestSemanticRendererDoesNotWrapTablesInsideCodeFence(t *testing.T) {
	renderer := NewSemanticRenderer()
	actions := renderer.Render(agentstream.Event{
		Kind: agentstream.EventAssistantMessage,
		Text: "```markdown\n| A | B |\n|---|---|\n| 1 | 2 |\n```",
	})
	if len(actions) != 1 {
		t.Fatalf("len(actions) = %d, want 1", len(actions))
	}
	if strings.Contains(actions[0].Content, "```text") {
		t.Fatalf("content = %q, should not wrap table already inside code fence", actions[0].Content)
	}
}

func TestSemanticRendererRendersUnsupportedAgent(t *testing.T) {
	renderer := NewSemanticRenderer()
	action := renderer.Unsupported(agentstream.TaskSummary{Agent: "gemini"})
	if action.Kind != RenderSend || !action.HighPriority {
		t.Fatalf("action = %#v, want high-priority send", action)
	}
	if !strings.Contains(action.Content, "gemini") || !strings.Contains(action.Content, "AGX Desktop") || strings.Contains(action.Content, "/logs") {
		t.Fatalf("content = %q, want agent and desktop guidance without logs guidance", action.Content)
	}
}

func TestSemanticRendererRendersApproval(t *testing.T) {
	renderer := NewSemanticRenderer()
	actions := renderer.Render(agentstream.Event{
		TaskID: "task-1",
		Kind:   agentstream.EventApprovalRequested,
		Approval: &agentstream.ApprovalEvent{
			Prompt:  "Run command?",
			Command: "gh auth status",
			Options: []agentstream.ApprovalOption{
				{ID: "yes", Label: "Allow once"},
				{ID: "no", Label: "Deny"},
			},
		},
	})
	if len(actions) != 1 || !actions[0].HighPriority {
		t.Fatalf("actions = %#v, want high-priority approval", actions)
	}
	if actions[0].Prompt == nil || actions[0].Prompt.TaskID != "task-1" || len(actions[0].Prompt.Options) != 2 {
		t.Fatalf("prompt = %#v, want interactive approval prompt", actions[0].Prompt)
	}
	for _, expected := range []string{"Run command?", "gh auth status", "Allow once", "Deny"} {
		if !strings.Contains(actions[0].Content, expected) {
			t.Fatalf("content = %q, missing %q", actions[0].Content, expected)
		}
	}
}

func TestSemanticRendererRendersQuestionAsInteractivePrompt(t *testing.T) {
	renderer := NewSemanticRenderer()
	actions := renderer.Render(agentstream.Event{
		TaskID: "task-1",
		Kind:   agentstream.EventQuestionRequested,
		Question: &agentstream.QuestionEvent{
			Prompt: "Pick one",
			Options: []agentstream.QuestionOption{
				{ID: "a", Label: "Option A"},
				{ID: "b", Label: "Option B"},
			},
		},
	})
	if len(actions) != 1 || !actions[0].HighPriority {
		t.Fatalf("actions = %#v, want high-priority question", actions)
	}
	if actions[0].Prompt == nil || actions[0].Prompt.Kind != "question" || len(actions[0].Prompt.Options) != 2 {
		t.Fatalf("prompt = %#v, want interactive question prompt", actions[0].Prompt)
	}
}

func TestSemanticRendererRendersCommandOutputAsProgress(t *testing.T) {
	renderer := NewSemanticRenderer()
	actions := renderer.Render(agentstream.Event{
		Kind:    agentstream.EventCommandOutputDelta,
		Command: &agentstream.CommandEvent{Stdout: "building..."},
	})
	if len(actions) != 1 {
		t.Fatalf("len(actions) = %d, want 1", len(actions))
	}
	if actions[0].Kind != RenderUpdateProgress || strings.Contains(actions[0].Content, "completed") {
		t.Fatalf("action = %#v, want progress output without completion", actions[0])
	}
}

func TestSemanticRendererRendersThinkingTextAsProgress(t *testing.T) {
	renderer := NewSemanticRenderer()
	actions := renderer.Render(agentstream.Event{
		Kind: agentstream.EventThinkingDelta,
		Text: "Use Read tools to inspect the renderer.",
	})
	if len(actions) != 1 {
		t.Fatalf("len(actions) = %d, want 1", len(actions))
	}
	if actions[0].Kind != RenderUpdateProgress || !strings.Contains(actions[0].Content, "Use Read tools") {
		t.Fatalf("action = %#v, want progress with thinking text", actions[0])
	}
}

func TestSemanticRendererRendersCommandStartAsProgress(t *testing.T) {
	renderer := NewSemanticRenderer()
	actions := renderer.Render(agentstream.Event{
		Kind:    agentstream.EventCommandStarted,
		Command: &agentstream.CommandEvent{Command: "git status --short"},
	})
	if len(actions) != 1 {
		t.Fatalf("len(actions) = %d, want 1", len(actions))
	}
	if actions[0].Kind != RenderUpdateProgress || !strings.Contains(actions[0].Content, "git status --short") {
		t.Fatalf("action[0] = %#v, want progress with command detail", actions[0])
	}
}

func TestSemanticRendererCompactsRunningProgressPreview(t *testing.T) {
	renderer := NewSemanticRenderer()
	actions := renderer.Render(agentstream.Event{
		Kind: agentstream.EventCommandOutputDelta,
		Command: &agentstream.CommandEvent{Stdout: strings.Join([]string{
			"line 1",
			"line 2",
			"line 3 should be hidden",
			"line 4 should be hidden",
			"line 5 should be hidden",
		}, "\n")},
	})
	if len(actions) != 1 {
		t.Fatalf("len(actions) = %d, want 1", len(actions))
	}
	lines := strings.Split(actions[0].Content, "\n")
	if len(lines) != 4 {
		t.Fatalf("content = %q, want label plus 3 preview lines", actions[0].Content)
	}
	if !strings.Contains(actions[0].Content, "line 1") || !strings.Contains(actions[0].Content, "line 2") {
		t.Fatalf("content = %q, want first progress lines", actions[0].Content)
	}
	if strings.Contains(actions[0].Content, "line 3 should be hidden") || !strings.HasSuffix(actions[0].Content, "...") {
		t.Fatalf("content = %q, want remaining progress collapsed", actions[0].Content)
	}
}

func TestSemanticRendererTruncatesLongProgressLine(t *testing.T) {
	renderer := NewSemanticRenderer()
	longPath := "/example/project/" + strings.Repeat("very-long-directory-name/", 20) + "reward.txt"
	actions := renderer.Render(agentstream.Event{
		Kind:    agentstream.EventCommandOutputDelta,
		Command: &agentstream.CommandEvent{Stdout: "-> " + longPath},
	})
	if len(actions) != 1 {
		t.Fatalf("len(actions) = %d, want 1", len(actions))
	}
	lines := strings.Split(actions[0].Content, "\n")
	if len(lines) != 2 {
		t.Fatalf("content = %q, want label plus one preview line", actions[0].Content)
	}
	if !strings.HasSuffix(lines[1], "...") {
		t.Fatalf("content = %q, want long line truncated with ellipsis", actions[0].Content)
	}
	if len([]rune(lines[1])) > progressPreviewMaxLineRunes {
		t.Fatalf("preview line length = %d, want <= %d", len([]rune(lines[1])), progressPreviewMaxLineRunes)
	}
}

func TestSemanticRendererRendersToolStartAsProgress(t *testing.T) {
	renderer := NewSemanticRenderer()
	actions := renderer.Render(agentstream.Event{
		Kind: agentstream.EventToolStarted,
		Tool: &agentstream.ToolEvent{Name: "Read", Input: `{"file_path":"/example/project/README.md"}`},
	})
	if len(actions) != 1 {
		t.Fatalf("len(actions) = %d, want 1", len(actions))
	}
	if actions[0].Kind != RenderUpdateProgress || !strings.Contains(actions[0].Content, "Read") || !strings.Contains(actions[0].Content, "README.md") {
		t.Fatalf("action[0] = %#v, want progress with tool name and file", actions[0])
	}
}

func TestSemanticRendererRendersFriendlyMuseToolProgress(t *testing.T) {
	renderer := NewSemanticRenderer()
	actions := renderer.Render(agentstream.Event{
		Agent: "muse",
		Kind:  agentstream.EventToolStarted,
		Tool:  &agentstream.ToolEvent{Name: "bash", Input: "Inspect workspace files\nls -la"},
	})
	if len(actions) != 1 || actions[0].Kind != RenderUpdateProgress {
		t.Fatalf("actions = %#v, want one Muse progress update", actions)
	}
	if !strings.Contains(actions[0].Content, "Inspecting workspace files") {
		t.Fatalf("content = %q, want natural progress description", actions[0].Content)
	}
	if strings.Contains(actions[0].Content, "ls -la") || strings.Contains(actions[0].Content, "`bash`") {
		t.Fatalf("content = %q, should hide raw Muse command details", actions[0].Content)
	}
}

func TestSemanticRendererRendersFriendlyLegacyMuseToolProgress(t *testing.T) {
	renderer := NewSemanticRenderer()
	actions := renderer.Render(agentstream.Event{
		Agent: "muse",
		Kind:  agentstream.EventToolStarted,
		Tool:  &agentstream.ToolEvent{Name: "Muse Code", Input: "Ran command · Read new file and parent commit · ✓ · 0.7s · ctrl+o"},
	})
	if len(actions) != 1 || !strings.Contains(actions[0].Content, "Reading new file and parent commit") {
		t.Fatalf("actions = %#v, want cleaned legacy Muse progress", actions)
	}
	if strings.Contains(actions[0].Content, "ctrl+o") || strings.Contains(actions[0].Content, "✓") {
		t.Fatalf("content = %q, should hide terminal UI decorations", actions[0].Content)
	}
}

func TestSemanticRendererNormalizesMuseProgressDescriptions(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "Check git/hg state\ngit status --short", want: "Reviewing repository status"},
		{input: "Check git history detail\ngit log -5", want: "Reviewing recent git history"},
		{input: "Locate provenance skill\nfind ~/.llms", want: "Locating provenance instructions"},
	}
	for _, test := range tests {
		actions := NewSemanticRenderer().Render(agentstream.Event{
			Agent: "muse",
			Kind:  agentstream.EventToolStarted,
			Tool:  &agentstream.ToolEvent{Name: "bash", Input: test.input},
		})
		if len(actions) != 1 || !strings.Contains(actions[0].Content, test.want) {
			t.Fatalf("input %q actions = %#v, want %q", test.input, actions, test.want)
		}
	}
}

func TestSemanticRendererRendersFailedCommandAsProgress(t *testing.T) {
	renderer := NewSemanticRenderer()
	exitCode := 1
	actions := renderer.Render(agentstream.Event{
		Kind:    agentstream.EventCommandCompleted,
		Command: &agentstream.CommandEvent{ExitCode: &exitCode, Stderr: "permission denied"},
	})
	if len(actions) != 1 || actions[0].Kind != RenderUpdateProgress {
		t.Fatalf("actions = %#v, want one progress action for failed command", actions)
	}
	if !strings.Contains(actions[0].Content, "permission denied") || !strings.Contains(actions[0].Content, "exit code 1") {
		t.Fatalf("content = %q, want failure detail", actions[0].Content)
	}
}

func TestSemanticRendererRendersSuccessfulCommandAsProgress(t *testing.T) {
	renderer := NewSemanticRenderer()
	exitCode := 0
	actions := renderer.Render(agentstream.Event{
		Kind:    agentstream.EventCommandCompleted,
		Command: &agentstream.CommandEvent{ExitCode: &exitCode, Stdout: "ok"},
	})
	if len(actions) != 1 || actions[0].Kind != RenderUpdateProgress {
		t.Fatalf("actions = %#v, want one progress action for successful command", actions)
	}
	if !strings.Contains(actions[0].Content, "ok") {
		t.Fatalf("content = %q, want output included", actions[0].Content)
	}
}

func TestSemanticRendererDoesNotRenderSuccessfulMuseCommandAsDone(t *testing.T) {
	renderer := NewSemanticRenderer()
	exitCode := 0
	actions := renderer.Render(agentstream.Event{
		Agent:   "muse",
		Kind:    agentstream.EventCommandCompleted,
		Command: &agentstream.CommandEvent{Command: "Check repository status", ExitCode: &exitCode, Stdout: "clean"},
	})
	if len(actions) != 0 {
		t.Fatalf("actions = %#v, want Muse success to keep the current progress message", actions)
	}
}

func TestSemanticRendererRendersFriendlyMuseCommandFailure(t *testing.T) {
	renderer := NewSemanticRenderer()
	exitCode := 1
	actions := renderer.Render(agentstream.Event{
		Agent:   "muse",
		Kind:    agentstream.EventCommandCompleted,
		Command: &agentstream.CommandEvent{Command: "Check repository status", ExitCode: &exitCode, Stderr: "permission denied\nraw trace"},
	})
	if len(actions) != 1 || actions[0].Kind != RenderUpdateProgress {
		t.Fatalf("actions = %#v, want one Muse failure progress update", actions)
	}
	if !strings.Contains(actions[0].Content, "Could not complete: Check repository status") || !strings.Contains(actions[0].Content, "permission denied") {
		t.Fatalf("content = %q, want concise friendly failure", actions[0].Content)
	}
	if strings.Contains(actions[0].Content, "raw trace") {
		t.Fatalf("content = %q, should not include raw multiline failure output", actions[0].Content)
	}
}

func TestSemanticForwarderFlushesAssistantDeltasOnTurnCompleted(t *testing.T) {
	sender := &recordingSemanticSender{}
	forwarder := NewSemanticEventForwarder(sender)
	events := make(chan agentstream.Event, 4)
	events <- agentstream.Event{TaskID: "task-1", TurnID: "turn-1", Kind: agentstream.EventTurnStarted}
	events <- agentstream.Event{TaskID: "task-1", TurnID: "turn-1", Kind: agentstream.EventAssistantDelta, Text: "hello "}
	events <- agentstream.Event{TaskID: "task-1", TurnID: "turn-1", Kind: agentstream.EventAssistantDelta, Text: "world"}
	events <- agentstream.Event{TaskID: "task-1", TurnID: "turn-1", Kind: agentstream.EventTurnCompleted}
	close(events)

	if err := forwarder.Forward(context.Background(), "channel-1", events); err != nil {
		t.Fatal(err)
	}
	if len(sender.messages) != 1 || sender.messages[0] != "hello world" {
		t.Fatalf("messages = %#v, want flushed assistant delta", sender.messages)
	}
	if len(sender.progress) != 1 || !strings.Contains(sender.progress[0], "Thinking") {
		t.Fatalf("progress = %#v, want only initial thinking progress for short draft", sender.progress)
	}
	if sender.cleared != 1 {
		t.Fatalf("cleared = %d, want 1", sender.cleared)
	}
}

func TestSemanticForwarderBatchesCharacterDeltas(t *testing.T) {
	sender := &recordingSemanticSender{}
	forwarder := NewSemanticEventForwarder(sender)
	events := make(chan agentstream.Event, 128)
	events <- agentstream.Event{TaskID: "task-1", TurnID: "turn-1", Kind: agentstream.EventTurnStarted}
	text := "이번에는 로컬 main과 origin/main이 새 커밋 f0c7487 기준으로 같습니다."
	for _, r := range text {
		events <- agentstream.Event{TaskID: "task-1", TurnID: "turn-1", Kind: agentstream.EventAssistantDelta, Text: string(r)}
	}
	events <- agentstream.Event{TaskID: "task-1", TurnID: "turn-1", Kind: agentstream.EventTurnCompleted}
	close(events)

	if err := forwarder.Forward(context.Background(), "channel-1", events); err != nil {
		t.Fatal(err)
	}
	if len(sender.messages) != 1 || sender.messages[0] != text {
		t.Fatalf("messages = %#v, want final batched text", sender.messages)
	}
	if len(sender.progress) > 3 {
		t.Fatalf("progress updates = %d, want batched updates instead of per-character edits: %#v", len(sender.progress), sender.progress)
	}
	if len(sender.progress) < 2 || !strings.Contains(sender.progress[len(sender.progress)-1], "같습니다") {
		t.Fatalf("progress = %#v, want final sentence progress before completion", sender.progress)
	}
}

func TestSemanticForwarderFlushesBufferedAssistantProgress(t *testing.T) {
	forwarder := NewSemanticEventForwarder(&recordingSemanticSender{})
	forwarder.render(agentstream.Event{TaskID: "task-1", TurnID: "turn-1", Kind: agentstream.EventTurnStarted})
	forwarder.render(agentstream.Event{TaskID: "task-1", TurnID: "turn-1", Kind: agentstream.EventAssistantDelta, Text: "short progress"})

	actions := forwarder.flushAllAssistantProgress()
	if len(actions) != 1 || actions[0].Kind != RenderUpdateProgress || !strings.Contains(actions[0].Content, "short progress") {
		t.Fatalf("actions = %#v, want buffered progress update", actions)
	}
	if again := forwarder.flushAllAssistantProgress(); len(again) != 0 {
		t.Fatalf("second flush actions = %#v, want no duplicate update", again)
	}
}

func TestSemanticForwarderAssistantProgressUsesRecentPreview(t *testing.T) {
	forwarder := NewSemanticEventForwarder(&recordingSemanticSender{})
	forwarder.render(agentstream.Event{TaskID: "task-1", TurnID: "turn-1", Kind: agentstream.EventTurnStarted})
	actions := forwarder.render(agentstream.Event{
		TaskID: "task-1",
		TurnID: "turn-1",
		Kind:   agentstream.EventAssistantDelta,
		Text: strings.Join([]string{
			"old status line",
			"middle status line",
			"recent status line",
			"latest status line",
		}, "\n"),
	})
	if len(actions) != 1 {
		t.Fatalf("actions = %#v, want one assistant progress update", actions)
	}
	content := actions[0].Content
	lines := strings.Split(content, "\n")
	if len(lines) != 4 {
		t.Fatalf("content = %q, want label plus compact recent preview", content)
	}
	if strings.Contains(content, "old status line") || !strings.Contains(content, "recent status line") || !strings.Contains(content, "latest status line") {
		t.Fatalf("content = %q, want recent assistant lines only", content)
	}
	if lines[1] != "..." {
		t.Fatalf("content = %q, want omitted prefix marker", content)
	}
}

func TestSemanticForwarderIgnoresProgressUpdateErrors(t *testing.T) {
	previousDelay := semanticProgressRetryDelay
	semanticProgressRetryDelay = time.Millisecond
	t.Cleanup(func() { semanticProgressRetryDelay = previousDelay })
	sender := &failingProgressSender{progressErr: context.DeadlineExceeded}
	forwarder := NewSemanticEventForwarder(sender)
	events := make(chan agentstream.Event, 4)
	events <- agentstream.Event{TaskID: "task-1", TurnID: "turn-1", Kind: agentstream.EventTurnStarted}
	events <- agentstream.Event{TaskID: "task-1", TurnID: "turn-1", Kind: agentstream.EventAssistantDelta, Text: "draft."}
	events <- agentstream.Event{TaskID: "task-1", TurnID: "turn-1", Kind: agentstream.EventAssistantMessage, Text: "final"}
	events <- agentstream.Event{TaskID: "task-1", TurnID: "turn-1", Kind: agentstream.EventTurnCompleted}
	close(events)

	if err := forwarder.Forward(context.Background(), "channel-1", events); err != nil {
		t.Fatal(err)
	}
	if len(sender.messages) != 1 || sender.messages[0] != "final" {
		t.Fatalf("messages = %#v, want final message despite progress failure", sender.messages)
	}
	if len(sender.progress) < 2 {
		t.Fatalf("progress attempts = %d, want transient progress updates retried", len(sender.progress))
	}
}

func TestSemanticForwarderRetriesAssistantSend(t *testing.T) {
	previousDelay := semanticSendRetryDelay
	semanticSendRetryDelay = time.Millisecond
	t.Cleanup(func() { semanticSendRetryDelay = previousDelay })

	sender := &flakySemanticSender{failures: 1, err: context.DeadlineExceeded}
	forwarder := NewSemanticEventForwarder(sender)
	events := make(chan agentstream.Event, 2)
	events <- agentstream.Event{TaskID: "task-1", TurnID: "turn-1", Kind: agentstream.EventAssistantMessage, Text: "final"}
	events <- agentstream.Event{TaskID: "task-1", TurnID: "turn-1", Kind: agentstream.EventTurnCompleted}
	close(events)

	if err := forwarder.Forward(context.Background(), "channel-1", events); err != nil {
		t.Fatal(err)
	}
	if len(sender.messages) != 1 || sender.messages[0] != "final" {
		t.Fatalf("messages = %#v, want final assistant message after retry", sender.messages)
	}
}

func TestSemanticForwarderReturnsAfterSendRetriesExhausted(t *testing.T) {
	previousDelay := semanticSendRetryDelay
	semanticSendRetryDelay = time.Millisecond
	t.Cleanup(func() { semanticSendRetryDelay = previousDelay })

	sendErr := errors.New("discord timeout")
	sender := &flakySemanticSender{failures: semanticSendRetryAttempts, err: sendErr}
	forwarder := NewSemanticEventForwarder(sender)
	events := make(chan agentstream.Event, 1)
	events <- agentstream.Event{TaskID: "task-1", TurnID: "turn-1", Kind: agentstream.EventAssistantMessage, Text: "final"}
	close(events)

	if err := forwarder.Forward(context.Background(), "channel-1", events); !errors.Is(err, sendErr) {
		t.Fatalf("Forward() error = %v, want %v", err, sendErr)
	}
	if len(sender.messages) != 0 {
		t.Fatalf("messages = %#v, want no successful sends", sender.messages)
	}
}

func TestSemanticForwarderFlushesAssistantBeforeToolProgress(t *testing.T) {
	sender := &recordingSemanticSender{}
	forwarder := NewSemanticEventForwarder(sender)
	events := make(chan agentstream.Event, 4)
	events <- agentstream.Event{TaskID: "task-1", TurnID: "turn-1", Kind: agentstream.EventTurnStarted}
	events <- agentstream.Event{TaskID: "task-1", TurnID: "turn-1", Kind: agentstream.EventAssistantDelta, Text: "I found the likely issue"}
	events <- agentstream.Event{TaskID: "task-1", TurnID: "turn-1", Kind: agentstream.EventToolStarted, Tool: &agentstream.ToolEvent{Name: "Read", Input: `{"file_path":"internal/discord/bot.go"}`}}
	close(events)

	if err := forwarder.Forward(context.Background(), "channel-1", events); err != nil {
		t.Fatal(err)
	}
	if len(sender.progress) < 3 {
		t.Fatalf("progress = %#v, want thinking, assistant flush, and tool progress", sender.progress)
	}
	if !strings.Contains(sender.progress[1], "I found the likely issue") {
		t.Fatalf("progress = %#v, want assistant text flushed before tool progress", sender.progress)
	}
	if !strings.Contains(sender.progress[2], "Read") {
		t.Fatalf("progress = %#v, want tool progress after assistant flush", sender.progress)
	}
}

func TestSemanticForwarderDoesNotDuplicateFinalAssistantMessage(t *testing.T) {
	sender := &recordingSemanticSender{}
	forwarder := NewSemanticEventForwarder(sender)
	events := make(chan agentstream.Event, 4)
	events <- agentstream.Event{TaskID: "task-1", TurnID: "turn-1", Kind: agentstream.EventTurnStarted}
	events <- agentstream.Event{TaskID: "task-1", TurnID: "turn-1", Kind: agentstream.EventAssistantDelta, Text: "draft"}
	events <- agentstream.Event{TaskID: "task-1", TurnID: "turn-1", Kind: agentstream.EventAssistantMessage, Text: "final"}
	events <- agentstream.Event{TaskID: "task-1", TurnID: "turn-1", Kind: agentstream.EventTurnCompleted}
	close(events)

	if err := forwarder.Forward(context.Background(), "channel-1", events); err != nil {
		t.Fatal(err)
	}
	if len(sender.messages) != 1 || sender.messages[0] != "final" {
		t.Fatalf("messages = %#v, want final assistant message only", sender.messages)
	}
}
