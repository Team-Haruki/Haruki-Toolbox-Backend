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
