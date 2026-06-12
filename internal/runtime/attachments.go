package runtime

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
)

const attachmentsDir = "attachments"

func attachmentRoot(paths Paths) string {
	return filepath.Join(paths.ConfigDir, attachmentsDir)
}

func taskAttachmentDir(root, taskID string) (string, error) {
	taskID = sanitizePathSegment(taskID)
	if taskID == "" {
		return "", fmt.Errorf("task id is required")
	}
	return filepath.Join(root, "task-"+taskID), nil
}

func messageAttachmentDir(root, taskID, discordMessageID string) (string, error) {
	taskDir, err := taskAttachmentDir(root, taskID)
	if err != nil {
		return "", err
	}
	discordMessageID = sanitizePathSegment(discordMessageID)
	if discordMessageID == "" {
		return "", fmt.Errorf("discord message id is required")
	}
	return filepath.Join(taskDir, "msg-"+discordMessageID), nil
}

func attachmentPath(root, taskID, discordMessageID, filename string) (string, error) {
	dir, err := messageAttachmentDir(root, taskID, discordMessageID)
	if err != nil {
		return "", err
	}
	filename = sanitizeAttachmentFilename(filename)
	if filename == "" {
		return "", fmt.Errorf("filename is required")
	}
	path := filepath.Join(dir, filename)
	if !isPathWithin(root, path) {
		return "", fmt.Errorf("attachment path escapes root")
	}
	return path, nil
}

func sanitizeAttachmentFilename(filename string) string {
	filename = strings.TrimSpace(filepath.Base(filename))
	if filename == "" || filename == "." || filename == string(filepath.Separator) {
		filename = "attachment"
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range filename {
		if r == '.' || r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	out := strings.Trim(b.String(), "._-")
	if out == "" {
		out = "attachment"
	}
	if len(out) > 120 {
		ext := filepath.Ext(out)
		base := strings.TrimSuffix(out, ext)
		if len(ext) > 16 {
			ext = ""
		}
		maxBase := 120 - len(ext)
		if maxBase < 1 {
			maxBase = 120
			ext = ""
		}
		if len(base) > maxBase {
			base = base[:maxBase]
		}
		out = strings.Trim(base, "._-") + ext
	}
	if out == "" {
		out = "attachment"
	}
	return out
}

func sanitizePathSegment(value string) string {
	value = strings.TrimSpace(value)
	var b strings.Builder
	for _, r := range value {
		if r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isPathWithin(root, path string) bool {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(rootAbs, pathAbs)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
