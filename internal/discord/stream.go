package discord

import (
	"context"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	maxDiscordMessage     = 2000
	streamCodeBlockBudget = 1800
)

type MessageSender interface {
	SendMessage(ctx context.Context, channelID, content string) error
}

type InteractivePromptSender interface {
	SendInteractivePrompt(ctx context.Context, channelID string, prompt InteractivePrompt) error
}

type InteractivePrompt struct {
	TaskID  string
	Kind    string
	Content string
	Options []InteractiveOption
}

type InteractiveOption struct {
	ID    string
	Label string
}

func FormatLogOutputMessage(output string) string {
	output = CleanTerminalOutput(output)
	if output == "" {
		return codeBlock("(no output captured)")
	}
	chunks := splitOutput(output, streamCodeBlockBudget)
	chunk := strings.TrimSpace(chunks[0])
	if len(chunks) > 1 {
		chunk = truncateUTF8(chunk, streamCodeBlockBudget-90) + "\n\n... log output truncated. Request fewer lines for a smaller response."
	}
	return codeBlock(chunk)
}

var (
	ansiPattern          = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	oscPattern           = regexp.MustCompile(`\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)
	escapeSequence       = regexp.MustCompile(`\x1b[PX^_].*?\x1b\\|\x1b[()][A-Za-z0-9]|\x1b[=>78]`)
	controlPattern       = regexp.MustCompile(`[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]`)
	blankLinePattern     = regexp.MustCompile(`\n{3,}`)
	claudeBoxLinePattern = regexp.MustCompile(`^[\s╭╮╯╰─│┌┐└┘├┤┬┴┼]+$`)
	csiTailPattern       = regexp.MustCompile(`^\??[0-9;:]*[$?]?[A-Za-z~]$`)
	tokenStatusPattern   = regexp.MustCompile(`^\(?\d+s\b.*\btokens?\)?$`)
	loneNoisePattern     = regexp.MustCompile(`^[*+.\-←›>]+$`)
	loneNumberPattern    = regexp.MustCompile(`^\d{1,3}$`)
)

func stripANSI(value string) string {
	value = oscPattern.ReplaceAllString(value, "")
	value = ansiPattern.ReplaceAllString(value, "")
	value = escapeSequence.ReplaceAllString(value, "")
	return controlPattern.ReplaceAllString(value, "")
}

func CleanTerminalOutput(output string) string {
	output = strings.ReplaceAll(output, "\r\n", "\n")
	output = strings.ReplaceAll(output, "\r", "\n")
	output = stripANSI(output)
	lines := strings.Split(output, "\n")
	cleaned := make([]string, 0, len(lines))
	for index, line := range lines {
		line = strings.TrimRight(line, " \t")
		trimmed := strings.TrimSpace(line)
		if isEchoBeforePrompt(lines, index, trimmed) || isTerminalNoiseLine(trimmed) {
			continue
		}
		cleaned = append(cleaned, line)
	}
	output = strings.TrimSpace(strings.Join(cleaned, "\n"))
	output = blankLinePattern.ReplaceAllString(output, "\n\n")
	return output
}

func isEchoBeforePrompt(lines []string, index int, line string) bool {
	if line == "" || index+1 >= len(lines) {
		return false
	}
	next := strings.TrimSpace(lines[index+1])
	if strings.HasPrefix(next, "›") || strings.HasPrefix(next, ">") {
		prompt := strings.TrimSpace(strings.TrimLeft(next, "›> "))
		return prompt != "" && prompt == line
	}
	return false
}

func isTerminalNoiseLine(line string) bool {
	if line == "" {
		return false
	}
	lower := strings.ToLower(line)
	compact := compactTerminalLine(lower)
	if claudeBoxLinePattern.MatchString(line) {
		return true
	}
	if csiTailPattern.MatchString(line) || tokenStatusPattern.MatchString(lower) || loneNoisePattern.MatchString(line) || loneNumberPattern.MatchString(line) {
		return true
	}
	if strings.Contains(compact, "tokens") && strings.Contains(compact, "s") {
		return true
	}
	if strings.HasPrefix(line, "›") || strings.HasPrefix(line, ">") {
		return true
	}
	noiseFragments := []string{
		"claude code enterprise",
		"claude code v",
		"using ai gateway",
		"--dangerously-disable-", // osx/win/linux sandbox-disable warning banner
		"dangerously-skip-permissions",
		"start using avocado",
		"sessionstart:startup hook error",
		"sessionstart:startup says:",
		"discovered 10 skills",
		"resume this session with:",
		"accept edits on",
		"esc to interrupt",
		"ctrl+o to expand",
		"running stop hooks",
		"for agents",
		"noodling",
		"boondoggling",
		"baked for",
		"cooked for",
		"churned for",
		"crushed for",
	}
	for _, fragment := range noiseFragments {
		if strings.Contains(lower, fragment) {
			return true
		}
	}
	compactFragments := []string{
		"accepteditson",
		"esctointerrupt",
		"ctrloexpand",
		"runningstophooks",
	}
	for _, fragment := range compactFragments {
		if strings.Contains(compact, fragment) {
			return true
		}
	}
	if strings.HasPrefix(strings.TrimSpace(line), "claude --resume ") {
		return true
	}
	return false
}

func compactTerminalLine(line string) string {
	var builder strings.Builder
	for _, r := range line {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

func splitOutput(text string, max int) []string {
	if len(text) <= max {
		return []string{text}
	}
	var chunks []string
	remaining := strings.TrimSpace(text)
	for len(remaining) > max {
		splitAt := bestSplitIndex(remaining, max)
		chunks = append(chunks, strings.TrimSpace(remaining[:splitAt]))
		remaining = strings.TrimSpace(remaining[splitAt:])
	}
	if remaining != "" {
		chunks = append(chunks, remaining)
	}
	return chunks
}

func bestSplitIndex(text string, max int) int {
	if len(text) <= max {
		return len(text)
	}
	min := max / 2
	if idx := strings.LastIndex(text[:max], "\n\n"); idx >= min {
		return idx + 2
	}
	if idx := strings.LastIndex(text[:max], "\n"); idx >= min {
		return idx + 1
	}
	if idx := strings.LastIndex(text[:max], " "); idx >= min {
		return idx + 1
	}
	return previousRuneBoundary(text, max)
}

func previousRuneBoundary(text string, index int) int {
	if index >= len(text) {
		return len(text)
	}
	for index > 0 && !utf8.RuneStart(text[index]) {
		index--
	}
	if index == 0 {
		_, size := utf8.DecodeRuneInString(text)
		return size
	}
	return index
}

func truncateUTF8(text string, max int) string {
	if len(text) <= max {
		return text
	}
	if max <= 0 {
		return ""
	}
	return text[:previousRuneBoundary(text, max)]
}
