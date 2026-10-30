package sekai

import (
	"context"
	"errors"
	"testing"
	"time"

	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
)

// The defaults are the pacing the inherit flow has always used. Changing them
// is an owner decision (anti-cheat risk), so this test pins them.
func TestDefaultInheritPacingIsUnchanged(t *testing.T) {
	t.Parallel()
	want := InheritPacing{
		AfterInheritCheck: 1 * time.Second,
		BeforeLogin:       2 * time.Second,
		SuiteFollowup:     1 * time.Second,
	}
	if got := DefaultInheritPacing(); got != want {
		t.Fatalf("DefaultInheritPacing() = %+v, want %+v", got, want)
	}
	for _, cfg := range []harukiConfig.SekaiInheritPacingConfig{
		{},
		{AfterInheritCheckMS: -1, BeforeLoginMS: -5, SuiteFollowupMS: 0},
		harukiConfig.DefaultSekaiInheritPacingConfig(),
	} {
		if got := InheritPacingFromConfig(cfg); got != want {
			t.Fatalf("InheritPacingFromConfig(%+v) = %+v, want defaults %+v", cfg, got, want)
		}
	}
	session, err := NewInheritSession(InheritSessionConfig{Server: ServerEN})
	if err != nil {
		t.Fatal(err)
	}
	if session.pacing != want {
		t.Fatalf("session without Pacing uses %+v, want defaults", session.pacing)
	}
}

func TestInheritPacingFromConfigConvertsMilliseconds(t *testing.T) {
	t.Parallel()
	got := InheritPacingFromConfig(harukiConfig.SekaiInheritPacingConfig{
		AfterInheritCheckMS: 300, BeforeLoginMS: 450, SuiteFollowupMS: 1,
	})
	want := InheritPacing{AfterInheritCheck: 300 * time.Millisecond, BeforeLogin: 450 * time.Millisecond, SuiteFollowup: time.Millisecond}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestSleepContextWaitsTheFullDuration(t *testing.T) {
	t.Parallel()
	start := time.Now()
	if err := sleepContext(context.Background(), 20*time.Millisecond); err != nil {
		t.Fatalf("sleepContext = %v", err)
	}
	if elapsed := time.Since(start); elapsed < 20*time.Millisecond {
		t.Fatalf("returned after %s, before the 20ms pause", elapsed)
	}
}

func TestSleepContextStopsWhenCancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(10*time.Millisecond, cancel)
	start := time.Now()
	err := sleepContext(ctx, time.Hour)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("sleepContext = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("cancellation took %s", elapsed)
	}
}

func TestSleepContextStopsAtDeadline(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := sleepContext(ctx, time.Hour); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("sleepContext = %v, want context.DeadlineExceeded", err)
	}
}

func TestSleepContextZeroDurationReportsContextState(t *testing.T) {
	t.Parallel()
	if err := sleepContext(context.Background(), 0); err != nil {
		t.Fatalf("live context: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepContext(ctx, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context: %v", err)
	}
}

func TestRetryBackoffIsUnchanged(t *testing.T) {
	t.Parallel()
	for attempt, want := range map[int]time.Duration{1: 500 * time.Millisecond, 2: time.Second, 3: 1500 * time.Millisecond} {
		if got := retryBackoff(attempt); got != want {
			t.Fatalf("retryBackoff(%d) = %s, want %s", attempt, got, want)
		}
	}
}
