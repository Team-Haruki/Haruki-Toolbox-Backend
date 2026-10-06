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

func TestLoggerRedactsCredentials(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := NewLogger("redact-test", "DEBUG", &buf)
	logger.Errorf("API returned non-200 status for %s: %d", "https://game.example/api/inherit/user/FAKEinheritID0004?isExecuteInherit=False", 403)
	logger.Warnf("Hydra proxy request failed: %v", `Get "http://hydra:4444/oauth2/sessions/logout?id_token_hint=FAKEtoken0004": EOF`)

	out := buf.String()
	for _, secret := range []string{"FAKEinheritID0004", "FAKEtoken0004"} {
		if strings.Contains(out, secret) {
			t.Fatalf("log output leaked %q: %q", secret, out)
		}
	}
	if !strings.Contains(out, "/inherit/user/<redacted>?isExecuteInherit=False: 403") {
		t.Fatalf("unexpected log output: %q", out)
	}
}
