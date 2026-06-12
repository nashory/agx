package runtime

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestAttachmentPathStaysUnderRoot(t *testing.T) {
	root := t.TempDir()
	path, err := attachmentPath(root, "../task", "../../msg", "../../screen shot.png")
	if err != nil {
		t.Fatal(err)
	}
	if !isPathWithin(root, path) {
		t.Fatalf("path %q escaped root %q", path, root)
	}
	if strings.Contains(path, "..") {
		t.Fatalf("path %q contains traversal", path)
	}
	if filepath.Base(path) != "screen_shot.png" {
		t.Fatalf("filename = %q, want screen_shot.png", filepath.Base(path))
	}
}

func TestSanitizeAttachmentFilename(t *testing.T) {
	tests := map[string]string{
		"":                         "attachment",
		"../../secret.png":         "secret.png",
		" screen shot 1.PNG ":      "screen_shot_1.PNG",
		"<>:?*|":                   "attachment",
		"report.final.v1.webp":     "report.final.v1.webp",
		"hello/../../evil.exe.png": "evil.exe.png",
	}
	for input, want := range tests {
		if got := sanitizeAttachmentFilename(input); got != want {
			t.Fatalf("sanitizeAttachmentFilename(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSanitizeAttachmentFilenameKeepsExtensionWhenTruncated(t *testing.T) {
	input := strings.Repeat("a", 200) + ".png"
	got := sanitizeAttachmentFilename(input)
	if len(got) > 120 {
		t.Fatalf("length = %d, want <= 120", len(got))
	}
	if !strings.HasSuffix(got, ".png") {
		t.Fatalf("filename = %q, want .png suffix", got)
	}
}
