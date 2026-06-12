package discord

import (
	"context"
	"strings"
	"testing"

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
	if !strings.Contains(action.Content, "gemini") || !strings.Contains(action.Content, "/logs") {
		t.Fatalf("content = %q, want agent and logs guidance", action.Content)
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
