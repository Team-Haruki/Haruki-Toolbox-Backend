package avatar

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
	"github.com/google/uuid"
)

const pendingDirectory = ".pending-avatar-deletions"

func isOwnedFilename(name string) bool {
	if filepath.Base(name) != name {
		return false
	}
	ext := filepath.Ext(name)
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
	default:
		return false
	}
	stem := strings.TrimSuffix(name, ext)
	id, err := uuid.Parse(stem)
	return err == nil && id.String() == stem
}

// QueueDeletion records cleanup before the account transaction commits. An
// existing database reference keeps the file safe if the transaction rolls back.
func QueueDeletion(directory, filename string) (bool, error) {
	if directory == "" || !isOwnedFilename(filename) {
		return false, nil
	}
	root, err := os.OpenRoot(directory)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer root.Close()
	if _, err := root.Lstat(filename); errors.Is(err, fs.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if err := root.Mkdir(pendingDirectory, 0700); err != nil && !errors.Is(err, fs.ErrExist) {
		return false, err
	}
	marker, err := root.OpenFile(filepath.Join(pendingDirectory, filename), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil && !errors.Is(err, fs.ErrExist) {
		return false, err
	}
	if marker != nil {
		syncErr := marker.Sync()
		if err := errors.Join(syncErr, marker.Close()); err != nil {
			return false, err
		}
	}
	// Sync directory entries so a restart after the database commit still
	// leaves the cleanup task on the avatar volume.
	for _, path := range []string{pendingDirectory, "."} {
		dir, err := root.Open(path)
		if err != nil {
			return false, err
		}
		syncErr := dir.Sync()
		if err := errors.Join(syncErr, dir.Close()); err != nil {
			return false, err
		}
	}
	return true, nil
}

func finishDeletion(ctx context.Context, db *postgresql.Client, root *os.Root, filename string) error {
	if !isOwnedFilename(filename) {
		return nil
	}
	referenced, err := db.User.Query().Where(user.AvatarPathEQ(filename)).Exist(ctx)
	if err != nil || referenced {
		return err
	}
	if err := root.Remove(filename); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	err = root.Remove(filepath.Join(pendingDirectory, filename))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

func FinishDeletion(ctx context.Context, db *postgresql.Client, directory, filename string) error {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	return finishDeletion(ctx, db, root, filename)
}

func sweepDeletions(ctx context.Context, db *postgresql.Client, directory string, logger *harukiLogger.Logger) error {
	root, err := os.OpenRoot(directory)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()
	list, err := root.Open(pendingDirectory)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer list.Close()
	for ctx.Err() == nil {
		entries, err := list.ReadDir(64)
		for _, entry := range entries {
			if entry.Type().IsRegular() {
				if err := finishDeletion(ctx, db, root, entry.Name()); err != nil && ctx.Err() == nil {
					logger.Warnf("Pending avatar cleanup will retry: %v", err)
				}
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
	return ctx.Err()
}

func StartCleanup(ctx context.Context, db *postgresql.Client, directory string, logger *harukiLogger.Logger) func() {
	if directory == "" {
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for ctx.Err() == nil {
			if err := sweepDeletions(ctx, db, directory, logger); err != nil && ctx.Err() == nil {
				logger.Warnf("Unable to scan pending avatar deletions: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return func() { <-done }
}
