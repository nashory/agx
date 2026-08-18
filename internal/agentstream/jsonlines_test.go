package agentstream

import (
	"bytes"
	"strings"
	"testing"
)

func TestReadJSONLinesHandlesLargeAndUnterminatedLines(t *testing.T) {
	large := strings.Repeat("x", 5*1024*1024)
	input := large + "\nlast"
	var lines [][]byte

	err := ReadJSONLines(strings.NewReader(input), func(line []byte) error {
		lines = append(lines, bytes.Clone(line))
		return nil
	})
	if err != nil {
		t.Fatalf("read JSON lines: %v", err)
	}
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	if got := string(lines[0]); got != large+"\n" {
		t.Fatalf("large line length = %d, want %d", len(got), len(large)+1)
	}
	if got := string(lines[1]); got != "last" {
		t.Fatalf("final line = %q, want last", got)
	}
}
