package avatar

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
	_ "github.com/mattn/go-sqlite3"
)

func TestAvatarCleanupRetainsOwnedFilesAndRetriesPendingWork(t *testing.T) {
	db := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer db.Close()
	ctx := t.Context()
	directory := t.TempDir()
	const filename = "378aa0e6-6c12-4e96-bb6a-7c144eb83e83.png"
	file := filepath.Join(directory, filename)
	marker := filepath.Join(directory, pendingDirectory, filename)
	// Force a recoverable filesystem error without depending on test UID.
	if err := os.Mkdir(file, 0700); err != nil {
		t.Fatal(err)
	}
	placeholder := filepath.Join(file, "fixture")
	if err := os.WriteFile(placeholder, nil, 0600); err != nil {
		t.Fatal(err)
	}
	db.User.Create().SetID("owner").SetName("owner").SetEmail("owner@example.test").SetAvatarPath(filename).ExecX(ctx)
	if queued, err := QueueDeletion(directory, filename); err != nil || !queued {
		t.Fatalf("queue returned %t, %v", queued, err)
	}
	if queued, err := QueueDeletion(directory, filename); err != nil || !queued {
		t.Fatalf("queue retry returned %t, %v", queued, err)
	}
	if err := FinishDeletion(ctx, db, directory, filename); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("marker missing while the account still exists: %v", err)
	}
	if _, err := os.Stat(placeholder); err != nil {
		t.Fatalf("file was changed while it still had an owner: %v", err)
	}

	db.User.DeleteOneID("owner").ExecX(ctx)
	if err := FinishDeletion(ctx, db, directory, filename); err == nil {
		t.Fatal("deletion unexpectedly removed a nonempty path")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("retry marker was lost: %v", err)
	}
	if err := os.Remove(placeholder); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	workerCtx, cancel := context.WithCancel(ctx)
	wait := StartCleanup(workerCtx, db, directory, harukiLogger.NewLoggerFromGlobal("AvatarTest"))
	defer func() { cancel(); wait() }()
	deadline := time.Now().Add(time.Second)
	for {
		_, err := os.Stat(marker)
		if errors.Is(err, fs.ErrNotExist) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cleanup worker did not consume the persisted marker")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(file); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("avatar remained after retry: %v", err)
	}
	if err := FinishDeletion(ctx, db, directory, filename); err != nil {
		t.Fatalf("retry of an already removed avatar failed: %v", err)
	}
	if err := FinishDeletion(ctx, db, directory, "../unmanaged.png"); err != nil {
		t.Fatalf("unmanaged path was not ignored: %v", err)
	}
}

func TestAvatarQueueIgnoresNonOwnedAndMissingPaths(t *testing.T) {
	directory := t.TempDir()
	for _, filename := range []string{"../outside.png", "a.png", "93bc2c21-aaca-4987-b917-d70b68eb2e00.txt", "93bc2c21-aaca-4987-b917-d70b68eb2e00.png"} {
		if queued, err := QueueDeletion(directory, filename); err != nil || queued {
			t.Fatalf("unmanaged or missing path %q was queued: %t, %v", filename, queued, err)
		}
	}
	if queued, err := QueueDeletion("", "93bc2c21-aaca-4987-b917-d70b68eb2e00.png"); err != nil || queued {
		t.Fatalf("missing storage unexpectedly queued: %t, %v", queued, err)
	}
	StartCleanup(t.Context(), nil, "", nil)()
}

func TestAvatarCleanupSurvivesMissingStorageAndRejectsBrokenQueue(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	logger := harukiLogger.NewLoggerFromGlobal("AvatarTest")
	const filename = "663263f3-66e1-43b7-a23d-b84ae24ca5ad.png"
	missing := filepath.Join(dir, "missing")
	if queued, err := QueueDeletion(missing, filename); err != nil || queued {
		t.Fatalf("missing avatar storage returned %t, %v", queued, err)
	}
	if err := sweepDeletions(ctx, nil, missing, logger); err != nil {
		t.Fatal(err)
	}
	if err := sweepDeletions(ctx, nil, dir, logger); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, filename)
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if queued, err := QueueDeletion(file, filename); err == nil || queued {
		t.Fatalf("non-directory storage returned %t, %v", queued, err)
	}
	if err := FinishDeletion(ctx, nil, file, filename); err == nil {
		t.Fatal("non-directory storage produced no cleanup error")
	}
	if err := sweepDeletions(ctx, nil, file, logger); err == nil {
		t.Fatal("invalid storage was silently skipped")
	}
	if err := os.WriteFile(filepath.Join(dir, pendingDirectory), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if queued, err := QueueDeletion(dir, filename); err == nil || queued {
		t.Fatalf("invalid queue returned %t, %v", queued, err)
	}
	if err := sweepDeletions(ctx, nil, dir, logger); err == nil {
		t.Fatal("an unreadable queue was silently skipped")
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("avatar was lost when cleanup could not be queued: %v", err)
	}
}

func TestAvatarSweepPreservesFilesWhenOwnershipCannotBeChecked(t *testing.T) {
	db := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	ctx := t.Context()
	directory := t.TempDir()
	const filename = "77d7c719-438d-48ac-9b35-55355cfed603.png"
	file := filepath.Join(directory, filename)
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if queued, err := QueueDeletion(directory, filename); err != nil || !queued {
		t.Fatalf("queue returned %t, %v", queued, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := sweepDeletions(ctx, db, directory, harukiLogger.NewLoggerFromGlobal("AvatarTest")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{file, filepath.Join(directory, pendingDirectory, filename)} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("cleanup removed %q during database outage: %v", path, err)
		}
	}
}
