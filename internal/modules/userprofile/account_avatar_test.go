package userprofile

import (
	harukiAPIHelper "haruki-suite/utils/api"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildAvatarFilePathSanitizesName(t *testing.T) {
	baseDir := t.TempDir()
	got := buildAvatarFilePath(baseDir, "../nested/../../avatar.png")
	want := filepath.Join(baseDir, "avatar.png")
	if got != want {
		t.Fatalf("buildAvatarFilePath(...) = %q, want %q", got, want)
	}
}

func TestRemoveAvatarFileIfExists(t *testing.T) {
	baseDir := t.TempDir()
	filePath := filepath.Join(baseDir, "avatar.png")
	if err := os.WriteFile(filePath, []byte("avatar"), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	if err := removeAvatarFileIfExists(filePath); err != nil {
		t.Fatalf("removeAvatarFileIfExists(existing) returned error: %v", err)
	}
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Fatalf("file should be removed, stat error=%v", err)
	}

	if err := removeAvatarFileIfExists(filePath); err != nil {
		t.Fatalf("removeAvatarFileIfExists(missing) returned error: %v", err)
	}
}

func TestHasProfileUpdatePayload(t *testing.T) {
	t.Run("empty payload", func(t *testing.T) {
		if hasProfileUpdatePayload(harukiAPIHelper.UpdateProfilePayload{}) {
			t.Fatalf("expected empty payload to be rejected")
		}
	})

	t.Run("avatar payload", func(t *testing.T) {
		avatar := "Zm9v"
		if !hasProfileUpdatePayload(harukiAPIHelper.UpdateProfilePayload{AvatarBase64: &avatar}) {
			t.Fatalf("expected avatar payload to be accepted")
		}
	})
}
