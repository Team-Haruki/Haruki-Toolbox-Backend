package userprofile

import (
	harukiAPIHelper "haruki-suite/utils/api"
	"os"
	"path/filepath"
	"strings"
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

func TestNormalizeProfileName(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		normalized, err := normalizeProfileName(nil)
		if err != nil {
			t.Fatalf("normalizeProfileName(nil) error: %v", err)
		}
		if normalized != nil {
			t.Fatalf("expected nil normalized name")
		}
	})

	t.Run("trim", func(t *testing.T) {
		raw := "  Alice  "
		normalized, err := normalizeProfileName(&raw)
		if err != nil {
			t.Fatalf("normalizeProfileName(trim) error: %v", err)
		}
		if normalized == nil || *normalized != "Alice" {
			t.Fatalf("normalized = %#v, want Alice", normalized)
		}
	})

	t.Run("invalid empty", func(t *testing.T) {
		raw := "   "
		if _, err := normalizeProfileName(&raw); err == nil {
			t.Fatalf("expected empty name to fail")
		}
	})

	t.Run("invalid too long", func(t *testing.T) {
		raw := strings.Repeat("a", 51)
		if _, err := normalizeProfileName(&raw); err == nil {
			t.Fatalf("expected too long name to fail")
		}
	})

	t.Run("unicode within limit", func(t *testing.T) {
		raw := strings.Repeat("你", 50)
		normalized, err := normalizeProfileName(&raw)
		if err != nil {
			t.Fatalf("normalizeProfileName(unicode within limit) error: %v", err)
		}
		if normalized == nil || *normalized != raw {
			t.Fatalf("normalized = %#v, want %q", normalized, raw)
		}
	})

	t.Run("unicode too long", func(t *testing.T) {
		raw := strings.Repeat("你", 51)
		if _, err := normalizeProfileName(&raw); err == nil {
			t.Fatalf("expected unicode name exceeding rune limit to fail")
		}
	})
}

func TestHasProfileUpdatePayload(t *testing.T) {
	t.Run("empty payload", func(t *testing.T) {
		if hasProfileUpdatePayload(harukiAPIHelper.UpdateProfilePayload{}) {
			t.Fatalf("expected empty payload to be rejected")
		}
	})

	t.Run("name payload", func(t *testing.T) {
		name := "Alice"
		if !hasProfileUpdatePayload(harukiAPIHelper.UpdateProfilePayload{Name: &name}) {
			t.Fatalf("expected name payload to be accepted")
		}
	})

	t.Run("avatar payload", func(t *testing.T) {
		avatar := "Zm9v"
		if !hasProfileUpdatePayload(harukiAPIHelper.UpdateProfilePayload{AvatarBase64: &avatar}) {
			t.Fatalf("expected avatar payload to be accepted")
		}
	})
}
