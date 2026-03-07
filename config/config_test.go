package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindConfigPath(t *testing.T) {
	t.Run("finds file in current directory", func(t *testing.T) {
		tmp := t.TempDir()
		filename := "cfg.yaml"
		target := filepath.Join(tmp, filename)
		if err := os.WriteFile(target, []byte("x"), 0600); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}

		withWorkingDir(t, tmp, func() {
			got := findConfigPath(filename)
			if !sameExistingPath(got, target) {
				t.Fatalf("findConfigPath returned %q, want %q", got, target)
			}
		})
	})

	t.Run("finds file in parent directory", func(t *testing.T) {
		tmp := t.TempDir()
		filename := "cfg.yaml"
		target := filepath.Join(tmp, filename)
		if err := os.WriteFile(target, []byte("x"), 0600); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}

		nested := filepath.Join(tmp, "a", "b")
		if err := os.MkdirAll(nested, 0755); err != nil {
			t.Fatalf("MkdirAll failed: %v", err)
		}

		withWorkingDir(t, nested, func() {
			got := findConfigPath(filename)
			if !sameExistingPath(got, target) {
				t.Fatalf("findConfigPath returned %q, want %q", got, target)
			}
		})
	})

	t.Run("returns filename when not found", func(t *testing.T) {
		tmp := t.TempDir()
		withWorkingDir(t, tmp, func() {
			got := findConfigPath("missing.yaml")
			if got != "missing.yaml" {
				t.Fatalf("findConfigPath returned %q, want %q", got, "missing.yaml")
			}
		})
	})
}

func withWorkingDir(t *testing.T, dir string, fn func()) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir(%s) failed: %v", dir, err)
	}
	defer func() {
		if chdirErr := os.Chdir(wd); chdirErr != nil {
			t.Fatalf("failed to restore working directory: %v", chdirErr)
		}
	}()
	fn()
}

func sameExistingPath(a, b string) bool {
	aEval, aErr := filepath.EvalSymlinks(a)
	bEval, bErr := filepath.EvalSymlinks(b)
	if aErr == nil && bErr == nil {
		return aEval == bEval
	}
	return filepath.Clean(a) == filepath.Clean(b)
}
