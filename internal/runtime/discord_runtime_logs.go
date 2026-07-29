package runtime

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
)

const maxRuntimeLogTailBytes int64 = 256 * 1024

func (s discordCommandService) RuntimeLogs(ctx context.Context, lines int) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	stdoutPath, stderrPath := RuntimeLogPaths()
	var b strings.Builder
	fmt.Fprintf(&b, "AGX runtime logs (last %d lines per file)\n\n", lines)
	appendRuntimeLogSection(&b, "runtime.err.log", stderrPath, lines)
	b.WriteString("\n\n")
	appendRuntimeLogSection(&b, "runtime.log", stdoutPath, lines)
	return b.String(), nil
}

func appendRuntimeLogSection(b *strings.Builder, name, path string, lines int) {
	fmt.Fprintf(b, "== %s (%s) ==\n", name, path)
	tail, err := tailRuntimeLogFile(path, lines)
	if err != nil {
		fmt.Fprintf(b, "(%s)\n", err)
		return
	}
	if strings.TrimSpace(tail) == "" {
		b.WriteString("(empty)\n")
		return
	}
	b.WriteString(tail)
	if !strings.HasSuffix(tail, "\n") {
		b.WriteByte('\n')
	}
}

func tailRuntimeLogFile(path string, lines int) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("not found")
		}
		return "", err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if info.Size() == 0 {
		return "", nil
	}

	offset := info.Size() - maxRuntimeLogTailBytes
	if offset < 0 {
		offset = 0
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return "", err
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return "", err
	}
	text := string(data)
	if offset > 0 {
		text = trimPartialFirstLine(text)
	}
	return lastRuntimeLogLines(text, lines), nil
}

func trimPartialFirstLine(text string) string {
	if index := strings.IndexByte(text, '\n'); index >= 0 {
		return text[index+1:]
	}
	return ""
}

func lastRuntimeLogLines(text string, lines int) string {
	text = strings.TrimRight(text, "\n")
	if text == "" || lines <= 0 {
		return ""
	}
	parts := strings.Split(text, "\n")
	if len(parts) <= lines {
		return strings.Join(parts, "\n")
	}
	return strings.Join(parts[len(parts)-lines:], "\n")
}
