package runtime

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nashory/agx/internal/db"
)

const defaultAttachmentRetention = 7 * 24 * time.Hour

type AttachmentStats struct {
	Root  string
	Files int
	Bytes int64
}

type AttachmentPruneOptions struct {
	OlderThan time.Duration
	TaskID    string
}

type AttachmentPruneResult struct {
	FilesDeleted       int
	BytesDeleted       int64
	AttachmentsDeleted int
	DirsDeleted        int
}

func AttachmentStorageStats(paths Paths) (AttachmentStats, error) {
	root := attachmentRoot(paths)
	stats := AttachmentStats{Root: root}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			stats.Files++
			stats.Bytes += info.Size()
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return stats, nil
	}
	return stats, err
}

func PruneAttachments(opts AttachmentPruneOptions) (AttachmentPruneResult, error) {
	store, err := db.Open()
	if err != nil {
		return AttachmentPruneResult{}, err
	}
	defer store.Close()
	paths := DefaultPaths()
	if strings.TrimSpace(opts.TaskID) != "" {
		return pruneTaskAttachments(paths, store, strings.TrimSpace(opts.TaskID))
	}
	olderThan := opts.OlderThan
	if olderThan <= 0 {
		olderThan = defaultAttachmentRetention
	}
	return pruneOldAttachments(paths, store, time.Now().UTC().Add(-olderThan))
}

func (s *Service) cleanupOrphanAttachments() error {
	return cleanupOrphanAttachmentDirs(s.paths, s.store)
}

func (s *Service) removeTaskAttachmentFiles(taskID string) error {
	dir, err := taskAttachmentDir(attachmentRoot(s.paths), taskID)
	if err != nil {
		return err
	}
	return removeAllIfExists(dir)
}

func cleanupOrphanAttachmentDirs(paths Paths, store *db.Store) error {
	root := attachmentRoot(paths)
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	metadata, err := store.ListAllTaskAttachments()
	if err != nil {
		return err
	}
	knownFiles := map[string]bool{}
	for _, attachment := range metadata {
		knownFiles[attachment.LocalPath] = true
		if _, err := os.Stat(attachment.LocalPath); errors.Is(err, os.ErrNotExist) {
			_ = store.DeleteTaskAttachment(attachment.ID)
		} else if err != nil {
			return err
		}
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "task-") {
			continue
		}
		taskID := strings.TrimPrefix(entry.Name(), "task-")
		if _, err := store.GetTask(taskID); errors.Is(err, db.ErrTaskNotFound) {
			if err := os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
				return err
			}
			continue
		} else if err != nil {
			return err
		}
		taskDir := filepath.Join(root, entry.Name())
		if err := removeFilesWithoutMetadata(taskDir, knownFiles); err != nil {
			return err
		}
	}
	return nil
}

func pruneTaskAttachments(paths Paths, store *db.Store, taskID string) (AttachmentPruneResult, error) {
	attachments, err := store.ListTaskAttachments(taskID)
	if err != nil {
		return AttachmentPruneResult{}, err
	}
	var result AttachmentPruneResult
	for _, attachment := range attachments {
		result.BytesDeleted += attachment.SizeBytes
		if err := store.DeleteTaskAttachment(attachment.ID); err != nil && !errors.Is(err, db.ErrTaskAttachmentNotFound) {
			return result, err
		}
		result.AttachmentsDeleted++
	}
	dir, err := taskAttachmentDir(attachmentRoot(paths), taskID)
	if err != nil {
		return result, err
	}
	if removed, err := removeAllCount(dir); err != nil {
		return result, err
	} else {
		result.FilesDeleted += removed.files
		result.DirsDeleted += removed.dirs
	}
	return result, nil
}

func pruneOldAttachments(paths Paths, store *db.Store, cutoff time.Time) (AttachmentPruneResult, error) {
	attachments, err := store.ListPrunableTaskAttachments(cutoff)
	if err != nil {
		return AttachmentPruneResult{}, err
	}
	var result AttachmentPruneResult
	root := attachmentRoot(paths)
	for _, attachment := range attachments {
		if !isPathWithin(root, attachment.LocalPath) {
			return result, fmt.Errorf("attachment path escapes root: %s", attachment.LocalPath)
		}
		if err := os.Remove(attachment.LocalPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return result, fmt.Errorf("remove attachment %s: %w", attachment.LocalPath, err)
		}
		result.FilesDeleted++
		result.BytesDeleted += attachment.SizeBytes
		if err := store.DeleteTaskAttachment(attachment.ID); err != nil && !errors.Is(err, db.ErrTaskAttachmentNotFound) {
			return result, err
		}
		result.AttachmentsDeleted++
		removeEmptyParents(root, filepath.Dir(attachment.LocalPath))
	}
	return result, nil
}

func removeFilesWithoutMetadata(root string, knownFiles map[string]bool) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if knownFiles[path] {
			return nil
		}
		return os.Remove(path)
	})
}

type removeCount struct {
	files int
	dirs  int
}

func removeAllCount(path string) (removeCount, error) {
	var count removeCount
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return count, nil
	} else if err != nil {
		return count, err
	}
	if err := filepath.WalkDir(path, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			count.dirs++
		} else {
			count.files++
		}
		return nil
	}); err != nil {
		return count, err
	}
	return count, os.RemoveAll(path)
}

func removeAllIfExists(path string) error {
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return os.RemoveAll(path)
}

func removeEmptyParents(root, dir string) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return
	}
	for {
		dirAbs, err := filepath.Abs(dir)
		if err != nil || dirAbs == rootAbs || !isPathWithin(rootAbs, dirAbs) {
			return
		}
		if err := os.Remove(dirAbs); err != nil {
			return
		}
		dir = filepath.Dir(dirAbs)
	}
}
