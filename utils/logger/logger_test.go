package logger

import (
	"bytes"
	"strings"
	"testing"
)

func TestNewLoggerFromGlobalUsesLatestGlobalConfig(t *testing.T) {
	previousLevel := GetGlobalLogLevel()
	previousWriter := getGlobalFileWriter()
	t.Cleanup(func() {
		SetGlobalLogLevel(previousLevel)
		SetGlobalFileWriter(previousWriter)
	})

	var before bytes.Buffer
	SetGlobalLogLevel("INFO")
	SetGlobalFileWriter(&before)

	logger := NewLoggerFromGlobal("global-test")

	var after bytes.Buffer
	SetGlobalLogLevel("DEBUG")
	SetGlobalFileWriter(&after)

	logger.Debugf("debug line")

	if before.Len() != 0 {
		t.Fatalf("expected original writer to stay unused, got %q", before.String())
	}
	if !strings.Contains(after.String(), "debug line") {
		t.Fatalf("expected updated writer to receive log line, got %q", after.String())
	}
}

func TestNewLoggerFromGlobalDoesNotDuplicateWrites(t *testing.T) {
	previousLevel := GetGlobalLogLevel()
	previousWriter := getGlobalFileWriter()
	t.Cleanup(func() {
		SetGlobalLogLevel(previousLevel)
		SetGlobalFileWriter(previousWriter)
	})

	var buf bytes.Buffer
	SetGlobalLogLevel("INFO")
	SetGlobalFileWriter(&buf)

	logger := NewLoggerFromGlobal("global-test")
	logger.Infof("single line")

	if count := strings.Count(buf.String(), "single line"); count != 1 {
		t.Fatalf("expected one log line, got %d in %q", count, buf.String())
	}
}
