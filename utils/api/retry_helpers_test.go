package api

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRetryOperationEventuallySucceeds(t *testing.T) {
	t.Parallel()

	attempts := 0
	err := RetryOperation(context.Background(), 3, 0, func() error {
		attempts++
		if attempts < 3 {
			return errors.New("temporary")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RetryOperation returned error: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}

func TestRetryOperationContextCanceled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := RetryOperation(ctx, 3, 10*time.Millisecond, func() error {
		return errors.New("temporary")
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled error, got %v", err)
	}
}
