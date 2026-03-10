package bootstrap

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenMainLogWriterStdout(t *testing.T) {
	writer, cleanup, err := openMainLogWriter("")
	if err != nil {
		t.Fatalf("openMainLogWriter returned error: %v", err)
	}
	if writer == nil {
		t.Fatalf("writer is nil")
	}
	if err := cleanup(); err != nil {
		t.Fatalf("cleanup returned error: %v", err)
	}
}

func TestOpenMainLogWriterFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.log")
	writer, cleanup, err := openMainLogWriter(path)
	if err != nil {
		t.Fatalf("openMainLogWriter returned error: %v", err)
	}
	defer func() {
		if closeErr := cleanup(); closeErr != nil {
			t.Fatalf("cleanup returned error: %v", closeErr)
		}
	}()

	if writer == nil {
		t.Fatalf("writer is nil")
	}
	if _, err := io.WriteString(writer, "hello\n"); err != nil {
		t.Fatalf("WriteString returned error: %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if string(content) == "" {
		t.Fatalf("log file content is empty")
	}
}
